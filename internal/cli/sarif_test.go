// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/mock"

	// Linked so the acceptance command (--provider claude-cli --sarif) runs
	// against the real CLI-backed factory, which never spawns a process at
	// construction time.
	_ "github.com/CommitBrief/commitbrief/internal/provider/claude-cli"
)

// sarifFakeProvider is a controllable structured provider: the response body
// and the call counter live in package-level state because provider.Register
// takes a factory, and the factory hands out a fresh value on every lookup.
type sarifFakeProvider struct{ *mock.Provider }

// sarifFakeCLIProvider additionally satisfies provider.PlainTextEmitter, the
// marker claude-cli / gemini-cli / codex-cli carry.
type sarifFakeCLIProvider struct{ *mock.Provider }

func (sarifFakeCLIProvider) EmitsPlainText() {}

var (
	sarifRegisterOnce sync.Once
	sarifMu           sync.Mutex
	sarifResponse     string
	sarifCalls        int
)

func (p sarifFakeProvider) Review(ctx context.Context, req provider.Request) (provider.Response, error) {
	sarifMu.Lock()
	sarifCalls++
	p.ResponseContent = sarifResponse
	sarifMu.Unlock()
	return p.Provider.Review(ctx, req)
}

func (p sarifFakeCLIProvider) Review(ctx context.Context, req provider.Request) (provider.Response, error) {
	sarifMu.Lock()
	sarifCalls++
	sarifMu.Unlock()
	return p.Provider.Review(ctx, req)
}

func registerSarifFakes() {
	sarifRegisterOnce.Do(func() {
		provider.Register("sarif-fake", func(config.ProviderConfig) (provider.Provider, error) {
			return sarifFakeProvider{mock.New()}, nil
		})
		provider.Register("sarif-fake-cli", func(config.ProviderConfig) (provider.Provider, error) {
			return sarifFakeCLIProvider{mock.New()}, nil
		})
	})
}

func setSarifFake(t *testing.T, response string) {
	t.Helper()
	registerSarifFakes()
	sarifMu.Lock()
	sarifResponse = response
	sarifCalls = 0
	sarifMu.Unlock()
}

func sarifCallCount() int {
	sarifMu.Lock()
	defer sarifMu.Unlock()
	return sarifCalls
}

const sarifOneFinding = `{"findings":[{"rule_id":"sec.injection","severity":"high","file":"app.go","line":6,"title":"Unsanitised input","description":"User input reaches a query.","suggestion":"Use a parameterised query."}]}`

func TestSarifFlagWritesValidSARIF(t *testing.T) {
	e := newCLIEnv(t)
	setSarifFake(t, sarifOneFinding)
	out := filepath.Join(t.TempDir(), "review.sarif")

	if err := e.run("--provider", "sarif-fake", "--staged", "--no-cache", "--sarif", "-o", out); err != nil {
		t.Fatalf("--sarif: %v\nstderr:\n%s", err, e.errOut.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("sarif file not written: %v", err)
	}
	var log struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name string `json:"name"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID              string            `json:"ruleId"`
				Level               string            `json:"level"`
				PartialFingerprints map[string]string `json:"partialFingerprints"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, data)
	}
	if log.Version != "2.1.0" || len(log.Runs) != 1 {
		t.Fatalf("unexpected SARIF envelope: version=%q runs=%d", log.Version, len(log.Runs))
	}
	run := log.Runs[0]
	if run.Tool.Driver.Name != "CommitBrief" {
		t.Errorf("driver name = %q", run.Tool.Driver.Name)
	}
	if len(run.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(run.Results))
	}
	r := run.Results[0]
	if r.RuleID != "sec.injection" || r.Level != "error" {
		t.Errorf("result = %+v", r)
	}
	if r.PartialFingerprints["commitbrief/v1"] == "" {
		t.Errorf("partialFingerprints[commitbrief/v1] missing: %+v", r.PartialFingerprints)
	}
	if strings.Contains(e.out.String(), "Unsanitised input") {
		t.Errorf("with -o the SARIF must not also land on stdout; got:\n%s", e.out.String())
	}
}

func TestSarifFlagToStdout(t *testing.T) {
	e := newCLIEnv(t)
	setSarifFake(t, sarifOneFinding)

	if err := e.run("--provider", "sarif-fake", "--staged", "--no-cache", "--sarif"); err != nil {
		t.Fatalf("--sarif: %v\nstderr:\n%s", err, e.errOut.String())
	}
	var v map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &v); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v\n%s", err, e.out.String())
	}
	if v["version"] != "2.1.0" {
		t.Errorf("version = %v", v["version"])
	}
}

func TestSarifOnDiffSubcommand(t *testing.T) {
	e := newCLIEnv(t)
	setSarifFake(t, sarifOneFinding)
	gitCmd(t, e.repoRoot, "reset", "--hard", "HEAD")
	commitChange(t, e.repoRoot, "feature.go",
		"package app\n\nfunc Feature() int { return 1 }\n", "feat: add feature")
	out := filepath.Join(t.TempDir(), "diff.sarif")

	if err := e.run("diff", "HEAD~1", "--provider", "sarif-fake", "--no-cache", "--sarif", "-o", out); err != nil {
		t.Fatalf("diff --sarif: %v\nstderr:\n%s", err, e.errOut.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("sarif file not written: %v", err)
	}
	if !strings.Contains(string(data), `"version": "2.1.0"`) {
		t.Errorf("not a SARIF document:\n%s", data)
	}
}

func TestSarifRefusesCLIProviderBeforeAnyCall(t *testing.T) {
	cases := [][]string{
		{"--provider", "sarif-fake-cli"},
	}
	for _, args := range cases {
		e := newCLIEnv(t)
		setSarifFake(t, sarifOneFinding)
		out := filepath.Join(t.TempDir(), "x.sarif")

		full := append(append([]string{}, args...), "--staged", "--no-cache", "--sarif", "-o", out)
		err := e.run(full...)
		if err == nil {
			t.Fatalf("%v: expected an error for --sarif with a CLI provider", args)
		}
		if !strings.Contains(err.Error(), "--sarif needs structured findings") {
			t.Errorf("unexpected error: %v", err)
		}
		if n := sarifCallCount(); n != 0 {
			t.Errorf("provider was called %d time(s); --sarif must refuse before any call", n)
		}
		if _, statErr := os.Stat(out); statErr == nil {
			t.Errorf("no file may be written on refusal")
		}
	}
}

func TestSarifRefusesRealClaudeCLIProvider(t *testing.T) {
	// The acceptance command: --provider claude-cli --sarif x.sarif. The real
	// claude-cli factory only builds a spec, so the refusal must fire before
	// the `claude` binary is ever looked up or run.
	for _, args := range [][]string{
		{"--provider", "claude-cli"},
	} {
		e := newCLIEnv(t)
		out := filepath.Join(t.TempDir(), "x.sarif")
		full := append(append([]string{}, args...), "--staged", "--no-cache", "--sarif", "-o", out)
		err := e.run(full...)
		if err == nil || !strings.Contains(err.Error(), "--sarif needs structured findings") {
			t.Errorf("%v: expected the CLI-provider refusal; got %v", args, err)
		}
		if _, statErr := os.Stat(out); statErr == nil {
			t.Errorf("%v: no file may be written on refusal", args)
		}
	}
}

func TestSarifOutputConflicts(t *testing.T) {
	for _, extra := range [][]string{
		{"--json"}, {"--markdown"}, {"--suggest-commit"}, {"--show-prompt"}, {"--cli", "claude"},
	} {
		e := newCLIEnv(t)
		setSarifFake(t, sarifOneFinding)
		out := filepath.Join(t.TempDir(), "x.sarif")
		// --cli and --provider are mutually exclusive in cobra itself.
		args := append([]string{"--staged", "--no-cache", "--sarif", "-o", out}, extra...)
		if extra[0] != "--cli" {
			args = append(args, "--provider", "sarif-fake")
		}
		err := e.run(args...)
		if err == nil {
			t.Errorf("--sarif %v: expected an error", extra)
			continue
		}
		if !strings.Contains(err.Error(), "--sarif cannot be combined") {
			t.Errorf("--sarif %v: unexpected error: %v", extra, err)
		}
		if n := sarifCallCount(); n != 0 {
			t.Errorf("--sarif %v: provider called %d time(s)", extra, n)
		}
		if _, statErr := os.Stat(out); statErr == nil {
			t.Errorf("--sarif %v: no file may be written on a conflict", extra)
		}
	}
}

func TestSarifFailsClosedOnDegradedResponse(t *testing.T) {
	// An empty results[] tells Code Scanning every open alert is fixed, so a
	// response that never parsed into findings must not become a SARIF file.
	e := newCLIEnv(t)
	setSarifFake(t, "not actually JSON")
	out := filepath.Join(t.TempDir(), "degraded.sarif")

	err := e.run("--provider", "sarif-fake", "--staged", "--no-cache", "--sarif", "-o", out)
	if err == nil {
		t.Fatal("expected a fail-closed error on a degraded response")
	}
	if !strings.Contains(err.Error(), "--sarif refused") {
		t.Errorf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("no SARIF file may be written for a degraded response")
	}
}

func TestSarifFailsClosedOnCachedDegradedResponse(t *testing.T) {
	// First run without --sarif stores a markdown-fallback entry; the second
	// run replays it with --sarif and must refuse instead of emitting an
	// empty log.
	e := newCLIEnv(t)
	setSarifFake(t, "not actually JSON")
	if err := e.run("--provider", "sarif-fake", "--staged"); err != nil {
		t.Fatalf("priming run: %v\nstderr:\n%s", err, e.errOut.String())
	}
	global = globalFlags{color: "never"}
	reviewScope = reviewScopeFlags{}
	e.out.Reset()
	e.errOut.Reset()
	before := sarifCallCount()
	out := filepath.Join(t.TempDir(), "cached.sarif")

	err := e.run("--provider", "sarif-fake", "--staged", "--sarif", "-o", out)
	if err == nil {
		t.Fatal("expected a fail-closed error replaying a degraded cache entry")
	}
	if !strings.Contains(err.Error(), "--sarif refused") {
		t.Errorf("unexpected error: %v", err)
	}
	if sarifCallCount() != before {
		t.Errorf("replay should not call the provider")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("no SARIF file may be written for a degraded cache entry")
	}
}

func TestSarifFlagIsNotPersistent(t *testing.T) {
	root := newRootCmd()
	if root.Flags().Lookup("sarif") == nil {
		t.Error("root should carry --sarif")
	}
	diffCmd, _, err := root.Find([]string{"diff"})
	if err != nil || diffCmd.Flags().Lookup("sarif") == nil {
		t.Errorf("diff should carry --sarif (err=%v)", err)
	}
	if root.PersistentFlags().Lookup("sarif") != nil {
		t.Error("--sarif must be a local flag, not persistent")
	}
	for _, name := range []string{"guard", "leaks", "map", "mcp", "commit"} {
		sub, _, err := root.Find([]string{name})
		if err != nil || sub == root {
			continue
		}
		if sub.Flags().Lookup("sarif") != nil {
			t.Errorf("%s must not expose --sarif", name)
		}
	}
}

// sarifResults decodes a SARIF document and returns its results[].
func sarifResults(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var doc struct {
		Version string `json:"version"`
		Runs    []struct {
			Results []map[string]any `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("not a single JSON document: %v\n%s", err, data)
	}
	if doc.Version != "2.1.0" || len(doc.Runs) != 1 {
		t.Fatalf("unexpected SARIF envelope: version=%q runs=%d", doc.Version, len(doc.Runs))
	}
	if doc.Runs[0].Results == nil {
		t.Fatalf("results must be an array, not null:\n%s", data)
	}
	return doc.Runs[0].Results
}

func TestSarifEmptyDiffWritesEmptyResultsFile(t *testing.T) {
	// A diff that is empty after filtering must still produce a SARIF file:
	// the upload-sarif step of a CI flow fails on a missing path, and an empty
	// results[] is what closes stale alerts.
	e := newCLIEnv(t)
	setSarifFake(t, sarifOneFinding)
	gitCmd(t, e.repoRoot, "reset", "--hard", "HEAD")
	out := filepath.Join(t.TempDir(), "empty.sarif")
	// A stale file from a previous run must be replaced, not left in place.
	if err := os.WriteFile(out, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := e.run("--provider", "sarif-fake", "--staged", "--no-cache", "--sarif", "-o", out); err != nil {
		t.Fatalf("--sarif on an empty diff: %v\nstderr:\n%s", err, e.errOut.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("sarif file not written for an empty diff: %v", err)
	}
	if n := len(sarifResults(t, data)); n != 0 {
		t.Errorf("results = %d, want 0", n)
	}
	if n := sarifCallCount(); n != 0 {
		t.Errorf("provider called %d time(s) for an empty diff", n)
	}
}

func TestSarifEmptyDiffToStdout(t *testing.T) {
	e := newCLIEnv(t)
	setSarifFake(t, sarifOneFinding)
	gitCmd(t, e.repoRoot, "reset", "--hard", "HEAD")

	if err := e.run("--provider", "sarif-fake", "--staged", "--no-cache", "--sarif"); err != nil {
		t.Fatalf("--sarif on an empty diff: %v\nstderr:\n%s", err, e.errOut.String())
	}
	if n := len(sarifResults(t, e.out.Bytes())); n != 0 {
		t.Errorf("results = %d, want 0", n)
	}
}

func TestSarifWithFailOnWritesFileThenFails(t *testing.T) {
	// The Faz 07 flow: the SARIF file must exist even when --fail-on trips,
	// so the upload step (if: always()) still has something to upload.
	e := newCLIEnv(t)
	setSarifFake(t, sarifOneFinding)
	out := filepath.Join(t.TempDir(), "gate.sarif")

	err := e.run("--provider", "sarif-fake", "--staged", "--no-cache", "--sarif", "-o", out, "--fail-on", "high")
	if err == nil {
		t.Fatal("expected --fail-on high to fail on a high finding")
	}
	data, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatalf("sarif file must be written before --fail-on fails: %v", readErr)
	}
	if n := len(sarifResults(t, data)); n != 1 {
		t.Errorf("results = %d, want 1", n)
	}
}

func TestSarifCacheHitRendersSARIF(t *testing.T) {
	e := newCLIEnv(t)
	setSarifFake(t, sarifOneFinding)
	if err := e.run("--provider", "sarif-fake", "--staged"); err != nil {
		t.Fatalf("priming run: %v\nstderr:\n%s", err, e.errOut.String())
	}
	global = globalFlags{color: "never"}
	reviewScope = reviewScopeFlags{}
	e.out.Reset()
	e.errOut.Reset()
	before := sarifCallCount()
	out := filepath.Join(t.TempDir(), "cached.sarif")

	if err := e.run("--provider", "sarif-fake", "--staged", "--sarif", "-o", out); err != nil {
		t.Fatalf("cached --sarif: %v\nstderr:\n%s", err, e.errOut.String())
	}
	if sarifCallCount() != before {
		t.Error("replay should not call the provider")
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("sarif file not written: %v", err)
	}
	results := sarifResults(t, data)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	fp, _ := results[0]["partialFingerprints"].(map[string]any)
	if fp["commitbrief/v1"] == nil || fp["commitbrief/v1"] == "" {
		t.Errorf("cache-hit result lost its fingerprint: %v", results[0])
	}
}

func TestSarifIgnoresMinSeverity(t *testing.T) {
	// --min-severity is display-only; Code Scanning must still see the
	// finding, exactly as --json does.
	e := newCLIEnv(t)
	setSarifFake(t, sarifOneFinding)
	out := filepath.Join(t.TempDir(), "min.sarif")

	if err := e.run("--provider", "sarif-fake", "--staged", "--no-cache", "--sarif", "-o", out, "--min-severity", "critical"); err != nil {
		t.Fatalf("--sarif --min-severity: %v\nstderr:\n%s", err, e.errOut.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("sarif file not written: %v", err)
	}
	if n := len(sarifResults(t, data)); n != 1 {
		t.Errorf("results = %d, want 1 (--min-severity must not filter SARIF)", n)
	}
}

func TestSarifWithUpdateBaselineEmitsFullSet(t *testing.T) {
	// --update-baseline absorbs the current findings instead of filtering
	// this run, so the SARIF it emits carries the whole set.
	e := newCLIEnv(t)
	setSarifFake(t, sarifOneFinding)
	out := filepath.Join(t.TempDir(), "baseline.sarif")

	if err := e.run("--provider", "sarif-fake", "--staged", "--no-cache", "--sarif", "-o", out, "--update-baseline"); err != nil {
		t.Fatalf("--sarif --update-baseline: %v\nstderr:\n%s", err, e.errOut.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("sarif file not written: %v", err)
	}
	if n := len(sarifResults(t, data)); n != 1 {
		t.Errorf("results = %d, want 1", n)
	}
}
