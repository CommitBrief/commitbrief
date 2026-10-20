// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/cache"
)

// sarifCategory returns runs[0].automationDetails.id of a SARIF document.
func sarifCategory(t *testing.T, data []byte) string {
	t.Helper()
	var doc struct {
		Runs []struct {
			AutomationDetails struct {
				ID string `json:"id"`
			} `json:"automationDetails"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || len(doc.Runs) != 1 {
		t.Fatalf("not a single-run SARIF document: %v\n%s", err, data)
	}
	return doc.Runs[0].AutomationDetails.ID
}

// resultMessages returns the message text of every result, in order.
func resultMessages(results []map[string]any) []string {
	var out []string
	for _, r := range results {
		msg, _ := r["message"].(map[string]any)
		text, _ := msg["text"].(string)
		out = append(out, text)
	}
	return out
}

// guardSarifFiles writes a policy and a schema-v1 review into a temp dir and
// returns the policy path, the review path and the SARIF output path.
func guardSarifFiles(t *testing.T, policyBody, reviewBody string) (policyPath, reviewPath, sarifPath string) {
	t.Helper()
	dir := t.TempDir()
	policyPath = filepath.Join(dir, "policy.yml")
	reviewPath = filepath.Join(dir, "review.json")
	sarifPath = filepath.Join(dir, "guard.sarif")
	writeFile(t, policyPath, policyBody)
	writeFile(t, reviewPath, reviewBody)
	return policyPath, reviewPath, sarifPath
}

func TestGuardSarifBlockedWritesOnlyOffendingFindings(t *testing.T) {
	e := newCLIEnv(t)
	pol, review, out := guardSarifFiles(t, guardPolicy, blockReviewJSON)

	err := e.run("guard", "--policy", pol, "--from-json", review, "--sarif", "-o", out)
	if err == nil {
		t.Fatal("a blocked verdict must still exit non-zero")
	}
	data, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatalf("SARIF must be written even when blocked: %v", rerr)
	}
	if got := sarifCategory(t, data); got != "commitbrief/guard/" {
		t.Errorf("category = %q, want commitbrief/guard/", got)
	}
	// critical cap 0 breached by t1; medium cap 1 breached by t2 and t3.
	if n := len(sarifResults(t, data)); n != 3 {
		t.Errorf("results = %d, want 3", n)
	}
	if !strings.Contains(e.errOut.String(), "BLOCKED") {
		t.Errorf("verdict should be on stderr; stderr:\n%s", e.errOut.String())
	}
	if strings.Contains(e.out.String(), "BLOCKED") {
		t.Errorf("verdict must not pollute stdout; stdout:\n%s", e.out.String())
	}
}

func TestGuardSarifOnlyViolatingSeverityIsWritten(t *testing.T) {
	// Only the critical cap is breached; the two mediums are within a cap of 2.
	e := newCLIEnv(t)
	pol, review, out := guardSarifFiles(t,
		"version: 1\nthresholds:\n  critical: 0\n  medium: 2\n", blockReviewJSON)

	if err := e.run("guard", "--policy", pol, "--from-json", review, "--sarif", "-o", out); err == nil {
		t.Fatal("expected blocked")
	}
	data, _ := os.ReadFile(out)
	results := sarifResults(t, data)
	if len(results) != 1 {
		t.Fatalf("results = %d, want only the critical finding\n%s", len(results), data)
	}
	if msg := strings.Join(resultMessages(results), "\n"); !strings.Contains(msg, "t1") {
		t.Errorf("the critical finding (t1) should be the one written:\n%s", msg)
	}
}

func TestGuardSarifPassingPolicyWritesEmptyResults(t *testing.T) {
	e := newCLIEnv(t)
	pol, review, out := guardSarifFiles(t, guardPolicy, passReviewJSON)

	if err := e.run("guard", "--policy", pol, "--from-json", review, "--sarif", "-o", out); err != nil {
		t.Fatalf("passing policy must exit 0: %v\nstderr:\n%s", err, e.errOut.String())
	}
	data, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatalf("a passing policy must still write the file: %v", rerr)
	}
	if n := len(sarifResults(t, data)); n != 0 {
		t.Errorf("results = %d, want 0 (the medium finding is within its cap)", n)
	}
	if got := sarifCategory(t, data); got != "commitbrief/guard/" {
		t.Errorf("category = %q", got)
	}
	if !strings.Contains(e.errOut.String(), "PASS") {
		t.Errorf("verdict should be on stderr:\n%s", e.errOut.String())
	}
}

func TestGuardSarifDenyWritesOnlyDeniedFindings(t *testing.T) {
	e := newCLIEnv(t)
	pol, review, out := guardSarifFiles(t, "version: 1\nrules:\n  deny: [sec.injection]\n", ruleReviewJSON)

	if err := e.run("guard", "--policy", pol, "--from-json", review, "--sarif", "-o", out); err == nil {
		t.Fatal("a denied rule must block")
	}
	data, _ := os.ReadFile(out)
	results := sarifResults(t, data)
	if len(results) != 1 || results[0]["ruleId"] != "sec.injection" {
		t.Fatalf("want exactly the denied sec.injection finding, got %v", results)
	}
}

func TestGuardSarifNeverWritesWaivedFindings(t *testing.T) {
	// high cap 0 would be breached by the two style.naming findings, but both
	// are waived; the low sec.injection finding is within no cap and so is not
	// an offender either.
	e := newCLIEnv(t)
	pol, review, out := guardSarifFiles(t,
		"version: 1\nthresholds:\n  high: 0\nrules:\n  allow: [style.naming]\n", ruleReviewJSON)

	if err := e.run("guard", "--policy", pol, "--from-json", review, "--sarif", "-o", out); err != nil {
		t.Fatalf("waived findings must not block: %v\nstderr:\n%s", err, e.errOut.String())
	}
	data, _ := os.ReadFile(out)
	if n := len(sarifResults(t, data)); n != 0 {
		t.Errorf("waived findings leaked into SARIF: %d results\n%s", n, data)
	}
}

func TestGuardSarifToStdoutKeepsVerdictOffStdout(t *testing.T) {
	e := newCLIEnv(t)
	pol, review, _ := guardSarifFiles(t, guardPolicy, blockReviewJSON)

	if err := e.run("guard", "--policy", pol, "--from-json", review, "--sarif"); err == nil {
		t.Fatal("expected blocked")
	}
	// stdout must be exactly one JSON document, so a CI redirect is valid SARIF.
	if n := len(sarifResults(t, e.out.Bytes())); n != 3 {
		t.Errorf("results = %d, want 3", n)
	}
	if !strings.Contains(e.errOut.String(), "BLOCKED") {
		t.Errorf("verdict should be on stderr:\n%s", e.errOut.String())
	}
}

func TestGuardSarifQuietSilencesHumanVerdict(t *testing.T) {
	e := newCLIEnv(t)
	pol, review, out := guardSarifFiles(t, guardPolicy, passReviewJSON)

	if err := e.run("guard", "--policy", pol, "--from-json", review, "--sarif", "-o", out, "--quiet"); err != nil {
		t.Fatalf("guard: %v", err)
	}
	if strings.Contains(e.errOut.String(), "PASS") || strings.Contains(e.out.String(), "PASS") {
		t.Errorf("--quiet must silence the human verdict; stdout=%q stderr=%q", e.out.String(), e.errOut.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("--quiet must not suppress the SARIF file: %v", err)
	}
}

func TestGuardSarifRejectsJSONBeforeAnyWork(t *testing.T) {
	e := newCLIEnv(t)
	setSarifFake(t, sarifOneFinding)
	pol, _, out := guardSarifFiles(t, guardPolicy, passReviewJSON)

	err := e.run("guard", "--policy", pol, "--provider", "sarif-fake", "--no-cache", "--sarif", "--json", "-o", out)
	if err == nil {
		t.Fatal("--sarif with --json must be refused")
	}
	if sarifCallCount() != 0 {
		t.Errorf("provider was called %d time(s); the conflict must fail first", sarifCallCount())
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("no SARIF file may be written on a flag conflict")
	}
}

func TestGuardSarifFromJSONDegradedFailsClosed(t *testing.T) {
	degraded := map[string]string{
		"degrade_reason": `{"schema":1,"findings":null,"content":"free text","meta":{"degrade_reason":"unparsable"}}`,
		"raw_content":    `{"schema":1,"content":"the model answered in prose","meta":{}}`,
	}
	for name, review := range degraded {
		t.Run(name, func(t *testing.T) {
			e := newCLIEnv(t)
			pol, reviewPath, out := guardSarifFiles(t, guardPolicy, review)

			if err := e.run("guard", "--policy", pol, "--from-json", reviewPath, "--sarif", "-o", out); err == nil {
				t.Fatal("a degraded review must not produce a SARIF success")
			}
			if _, statErr := os.Stat(out); statErr == nil {
				t.Error("no SARIF file may be written for a degraded review (an empty results[] would close every alert)")
			}
		})
	}
}

func TestGuardSarifRunModeWritesGuardCategory(t *testing.T) {
	e := newCLIEnv(t)
	setSarifFake(t, sarifOneFinding)
	pol, _, out := guardSarifFiles(t, "version: 1\nthresholds:\n  high: 0\n", passReviewJSON)

	err := e.run("guard", "--policy", pol, "--provider", "sarif-fake", "--no-cache", "--sarif", "-o", out)
	if err == nil {
		t.Fatalf("a high finding with cap 0 must block\nstderr:\n%s", e.errOut.String())
	}
	data, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatalf("SARIF not written: %v", rerr)
	}
	if got := sarifCategory(t, data); got != "commitbrief/guard/" {
		t.Errorf("category = %q, want commitbrief/guard/", got)
	}
	results := sarifResults(t, data)
	if len(results) != 1 || results[0]["ruleId"] != "sec.injection" {
		t.Errorf("results = %v", results)
	}
}

func TestGuardSarifRunModeDegradedFailsClosed(t *testing.T) {
	e := newCLIEnv(t)
	setSarifFake(t, "I could not produce structured output, sorry.")
	pol, _, out := guardSarifFiles(t, guardPolicy, passReviewJSON)

	if err := e.run("guard", "--policy", pol, "--provider", "sarif-fake", "--no-cache", "--sarif", "-o", out); err == nil {
		t.Fatal("a degraded run-mode review must fail the command")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("no SARIF file may be written for a degraded review")
	}
}

// A degraded live review is cached as a markdown-fallback entry. The replay
// carries meta.degrade_reason (ADR-0031 §Update 2026-10-20) and the seam's
// degrade probe is the second line of defence: either way the run must fail
// closed, because an offending subset written under commitbrief/guard/ would
// close every open LLM-sourced alert in that category.
func TestGuardSarifRunModeDegradedCacheReplayWithFlakyFailsClosed(t *testing.T) {
	e := newCLIEnv(t)
	setSarifFake(t, "I could not produce structured output, sorry.")
	stageSecretIntoRepo(t, e.repoRoot, "worker_test.go", "package app\n\nimport \"time\"\n\nfunc TestJob() {\n\ttime.Sleep(2 * time.Second)\n}\n")
	pol, _, out := guardSarifFiles(t, guardPolicy, passReviewJSON)
	args := []string{"guard", "--policy", pol, "--provider", "sarif-fake", "--sarif", "-o", out}

	// First run: live degrade (cache miss, entry stored).
	if err := e.run(args...); err == nil {
		t.Fatal("the live degraded review must fail the command")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatal("no SARIF file may be written for a live degraded review")
	}
	liveCalls := sarifCallCount() // the live degrade path retries once
	if liveCalls == 0 {
		t.Fatal("the first run must reach the provider")
	}

	// Second run: cache replay of the degraded entry, flaky findings present.
	if err := e.run(args...); err == nil {
		t.Fatalf("a degraded cache replay must fail the command\nstderr:\n%s", e.errOut.String())
	}
	if got := sarifCallCount(); got != liveCalls {
		t.Fatalf("provider calls after the second run = %d, want %d (the replay must come from the cache)", got, liveCalls)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("no SARIF file may be written for a degraded cache replay")
	}
}

// The cache replay of a degraded response reports the same meta.degrade_reason
// the live call did (ADR-0031 §Update 2026-10-20), so the JSON document alone
// is enough for `guard --from-json` to fail closed -- including when static
// flaky findings sit next to the empty content.
func TestGuardSarifFromJSONCacheReplayDegradedFailsClosed(t *testing.T) {
	e := newCLIEnv(t)
	setSarifFake(t, "I could not produce structured output, sorry.")
	stageSecretIntoRepo(t, e.repoRoot, "worker_test.go", "package app\n\nimport \"time\"\n\nfunc TestJob() {\n\ttime.Sleep(2 * time.Second)\n}\n")

	reviewDoc := func(wantCached bool) string {
		e.out.Reset()
		if err := e.run("--provider", "sarif-fake", "--json"); err != nil {
			t.Fatalf("review --json: %v\nstderr:\n%s", err, e.errOut.String())
		}
		var doc struct {
			Meta struct {
				Cached        bool   `json:"cached"`
				DegradeReason string `json:"degrade_reason"`
			} `json:"meta"`
			Findings []map[string]any `json:"findings"`
		}
		if err := json.Unmarshal(e.out.Bytes(), &doc); err != nil {
			t.Fatalf("review --json output is not JSON: %v\n%s", err, e.out.String())
		}
		if doc.Meta.Cached != wantCached {
			t.Fatalf("meta.cached = %v, want %v", doc.Meta.Cached, wantCached)
		}
		if doc.Meta.DegradeReason != "prose" {
			t.Fatalf("meta.degrade_reason = %q, want \"prose\" (cached=%v)", doc.Meta.DegradeReason, wantCached)
		}
		if len(doc.Findings) == 0 {
			t.Fatal("the flaky pre-pass should have left findings next to the empty content")
		}
		return e.out.String()
	}
	reviewDoc(false)          // live degrade, entry stored
	replay := reviewDoc(true) // cache hit

	dir := t.TempDir()
	pol := filepath.Join(dir, "policy.yml")
	reviewPath := filepath.Join(dir, "review.json")
	out := filepath.Join(dir, "guard.sarif")
	if err := os.WriteFile(pol, []byte(guardPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reviewPath, []byte(replay), 0o600); err != nil {
		t.Fatal(err)
	}
	e.errOut.Reset()
	err := e.run("guard", "--policy", pol, "--from-json", reviewPath, "--sarif", "-o", out)
	if err == nil {
		t.Fatal("a degraded cache replay fed through --from-json must fail the command")
	}
	if !strings.Contains(err.Error(), "could not be parsed") {
		t.Errorf("want the sarif.degraded error, got: %v", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("no SARIF file may be written for a degraded cache replay")
	}
}

func TestCachedDegradeReason(t *testing.T) {
	cases := []struct {
		name string
		in   cache.Result
		want string
	}{
		{"clean json", cache.Result{Format: cache.FormatJSON, Content: `{"findings":[]}`}, ""},
		{"legacy empty format, clean", cache.Result{Content: `{"findings":[]}`}, ""},
		{"plain text is never a degrade", cache.Result{Format: cache.FormatPlainText, Content: "free text"}, ""},
		{"stored reason wins", cache.Result{Format: cache.FormatMarkdownFallback, Content: "{", DegradeReason: "retry-error"}, "retry-error"},
		{"legacy fallback entry derives prose", cache.Result{Format: cache.FormatMarkdownFallback, Content: "I could not do it."}, "prose"},
		{"json entry that no longer parses", cache.Result{Format: cache.FormatJSON, Content: "not json"}, "prose"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cachedDegradeReason(tc.in); got != tc.want {
				t.Errorf("cachedDegradeReason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGuardWithoutSarifIsUnchanged(t *testing.T) {
	// The verdict stays on stdout when --sarif is absent.
	e := newCLIEnv(t)
	pol, review, _ := guardSarifFiles(t, guardPolicy, passReviewJSON)

	if err := e.run("guard", "--policy", pol, "--from-json", review); err != nil {
		t.Fatalf("guard: %v", err)
	}
	if !strings.Contains(e.out.String(), "PASS") {
		t.Errorf("verdict missing from stdout:\n%s", e.out.String())
	}
}
