// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const blockReviewJSON = `{"schema":1,"findings":[
{"severity":"critical","file":"a.go","line":1,"title":"t1","description":"d","suggestion":"s"},
{"severity":"medium","file":"b.go","line":2,"title":"t2","description":"d","suggestion":"s"},
{"severity":"medium","file":"c.go","line":3,"title":"t3","description":"d","suggestion":"s"}],"meta":{}}`

const passReviewJSON = `{"schema":1,"findings":[
{"severity":"medium","file":"b.go","line":2,"title":"t","description":"d","suggestion":"s"}],"meta":{}}`

const guardPolicy = "version: 1\nthresholds:\n  critical: 0\n  high: 0\n  medium: 1\ntotal: 5\n"

// runGuardConsume drives `guard --from-json` against a written policy + review,
// in a clean global-flag scope, returning the captured stdout and the error.
func runGuardConsume(t *testing.T, jsonMode bool, policyBody, reviewBody string) (string, error) {
	t.Helper()
	// runGuard resolves config via resolveContext (for app.Catalog, among
	// other things), so this needs the same env sandboxing newCLIEnv gives
	// full integration tests — without it, the real ~/.commitbrief/config.yml
	// (e.g. output.lang: tr) decides which language the verdict renders in.
	isolateHomeEnv(t)
	// resolveContext(false) also best-effort-resolves a REPO root from cwd;
	// without this, that's this very repo's own checkout, so a repo-level
	// .commitbrief/config.yml here (e.g. from `setup --local`) would leak in
	// exactly the same way (item 9).
	chdirIsolated(t)
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.yml")
	reviewPath := filepath.Join(dir, "review.json")
	if err := os.WriteFile(policyPath, []byte(policyBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reviewPath, []byte(reviewBody), 0o644); err != nil {
		t.Fatal(err)
	}

	saved := global
	global = globalFlags{json: jsonMode}
	defer func() { global = saved }()

	cmd := newGuardCmd()
	// Standalone (no root parent), so silence cobra's own usage/error dump on a
	// RunE error — in production the root sets these; here we want stdout to be
	// only the verdict the renderer wrote, so the JSON-mode test can parse it.
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"--policy", policyPath, "--from-json", reviewPath})
	err := cmd.Execute()
	return outBuf.String(), err
}

func TestGuardConsumeBlocks(t *testing.T) {
	out, err := runGuardConsume(t, false, guardPolicy, blockReviewJSON)
	if err == nil {
		t.Fatal("expected a non-nil error (blocked) for CI exit 1")
	}
	if !strings.Contains(out, "BLOCKED") {
		t.Errorf("output missing BLOCKED verdict:\n%s", out)
	}
	if !strings.Contains(out, "critical") || !strings.Contains(out, "medium") {
		t.Errorf("output missing the breached severities:\n%s", out)
	}
}

func TestGuardConsumePasses(t *testing.T) {
	out, err := runGuardConsume(t, false, guardPolicy, passReviewJSON)
	if err != nil {
		t.Fatalf("expected pass (nil error), got %v", err)
	}
	if !strings.Contains(out, "PASS") {
		t.Errorf("output missing PASS verdict:\n%s", out)
	}
}

func TestGuardJSONVerdict(t *testing.T) {
	out, err := runGuardConsume(t, true, guardPolicy, blockReviewJSON)
	if err == nil {
		t.Fatal("expected blocked error")
	}
	var v struct {
		Passed     bool `json:"passed"`
		Total      int  `json:"total"`
		Violations []struct {
			Severity string `json:"severity"`
		} `json:"violations"`
	}
	if jerr := json.Unmarshal([]byte(out), &v); jerr != nil {
		t.Fatalf("verdict is not valid JSON: %v\n%s", jerr, out)
	}
	if v.Passed {
		t.Error("verdict.passed = true, want false")
	}
	if v.Total != 3 {
		t.Errorf("verdict.total = %d, want 3", v.Total)
	}
	if len(v.Violations) != 2 {
		t.Errorf("want 2 violations, got %d", len(v.Violations))
	}
}

func TestGuardMissingPolicy(t *testing.T) {
	// Same hermeticity requirement as runGuardConsume: runGuard resolves
	// config before it ever looks at --policy.
	isolateHomeEnv(t)
	chdirIsolated(t)
	dir := t.TempDir()
	reviewPath := filepath.Join(dir, "review.json")
	if err := os.WriteFile(reviewPath, []byte(passReviewJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := global
	global = globalFlags{}
	defer func() { global = saved }()

	cmd := newGuardCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--policy", filepath.Join(dir, "nope.yml"), "--from-json", reviewPath})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for a missing policy file (gate is opt-in but must not silently pass)")
	}
}

func TestGuardBadReviewJSON(t *testing.T) {
	if _, err := runGuardConsume(t, false, guardPolicy, "not json"); err == nil {
		t.Fatal("expected an error for unparseable review JSON")
	}
}

func TestGuardCommandRegistered(t *testing.T) {
	cmd := newGuardCmd()
	if cmd.Use != "guard" {
		t.Errorf("Use = %q, want guard", cmd.Use)
	}
	if err := cmd.Args(cmd, []string{"unexpected"}); err == nil {
		t.Error("guard should reject positional args (NoArgs)")
	}
}

const ruleReviewJSON = `{"schema":1,"findings":[
{"severity":"high","rule_id":"style.naming","file":"a.go","line":1,"title":"t1","description":"d","suggestion":"s"},
{"severity":"high","rule_id":"style.naming","file":"b.go","line":2,"title":"t2","description":"d","suggestion":"s"},
{"severity":"low","rule_id":"sec.injection","file":"c.go","line":3,"title":"t3","description":"d","suggestion":"s"}],"meta":{}}`

type guardVerdictDoc struct {
	Passed     bool           `json:"passed"`
	Total      int            `json:"total"`
	Waived     *int           `json:"waived"`
	Counts     map[string]int `json:"counts"`
	Violations []struct {
		Severity string `json:"severity"`
		Actual   int    `json:"actual"`
	} `json:"violations"`
}

func TestGuardJSONReportsWaived(t *testing.T) {
	// high cap is 0; both high findings are style.naming and waived by allow,
	// so the gate passes and the verdict reports waived=2.
	policy := "version: 1\nthresholds:\n  high: 0\nrules:\n  allow: [style.naming]\n"
	out, err := runGuardConsume(t, true, policy, ruleReviewJSON)
	if err != nil {
		t.Fatalf("expected pass once waived, got %v\n%s", err, out)
	}
	var v guardVerdictDoc
	if jerr := json.Unmarshal([]byte(out), &v); jerr != nil {
		t.Fatalf("verdict is not valid JSON: %v\n%s", jerr, out)
	}
	if !v.Passed || v.Waived == nil || *v.Waived != 2 {
		t.Fatalf("verdict = %+v (waived=%v), want passed with waived=2", v, v.Waived)
	}
	if v.Total != 1 || v.Counts["high"] != 0 {
		t.Errorf("waived findings must not count: total=%d counts=%v", v.Total, v.Counts)
	}
}

func TestGuardJSONWaivedAlwaysPresent(t *testing.T) {
	// A policy without `rules` still emits waived (0): the field is additive
	// and stable, never conditionally absent.
	out, err := runGuardConsume(t, true, guardPolicy, passReviewJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var v guardVerdictDoc
	if jerr := json.Unmarshal([]byte(out), &v); jerr != nil {
		t.Fatal(jerr)
	}
	if v.Waived == nil || *v.Waived != 0 {
		t.Fatalf("waived = %v, want present and 0", v.Waived)
	}
}

func TestGuardDenyBlocksOnSingleFinding(t *testing.T) {
	policy := "version: 1\nrules:\n  deny: [sec.injection]\n"
	out, err := runGuardConsume(t, true, policy, ruleReviewJSON)
	if err == nil {
		t.Fatal("expected blocked error for a denied rule")
	}
	var v guardVerdictDoc
	if jerr := json.Unmarshal([]byte(out), &v); jerr != nil {
		t.Fatal(jerr)
	}
	if v.Passed || len(v.Violations) != 1 || v.Violations[0].Severity != "rule:sec.injection" || v.Violations[0].Actual != 1 {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestGuardHumanOutputShowsRuleViolationAndWaived(t *testing.T) {
	policy := "version: 1\nrules:\n  deny: [sec.injection]\n  allow: [style.naming]\n"
	out, err := runGuardConsume(t, false, policy, ruleReviewJSON)
	if err == nil {
		t.Fatal("expected blocked error")
	}
	if !strings.Contains(out, "sec.injection") || !strings.Contains(out, "denied") {
		t.Errorf("human output missing the denied rule line:\n%s", out)
	}
	if !strings.Contains(out, "Waived by policy rules.allow: 2 finding(s)") {
		t.Errorf("human output missing the waived line:\n%s", out)
	}
}

func TestGuardDenyWildcardReportsFindingRuleID(t *testing.T) {
	// deny: [style.*] matches both style.naming findings; each is its own
	// rule:style.naming violation, never rule:style.* (ADR-0045 §6).
	policy := "version: 1\nrules:\n  deny: [style.*]\n"
	out, err := runGuardConsume(t, true, policy, ruleReviewJSON)
	if err == nil {
		t.Fatal("expected blocked error for a denied rule")
	}
	var v guardVerdictDoc
	if jerr := json.Unmarshal([]byte(out), &v); jerr != nil {
		t.Fatal(jerr)
	}
	if v.Passed || len(v.Violations) != 2 {
		t.Fatalf("verdict = %+v", v)
	}
	for i, vio := range v.Violations {
		if vio.Severity != "rule:style.naming" || vio.Actual != 1 {
			t.Errorf("violation[%d] = %+v", i, vio)
		}
	}
}

func TestGuardHumanOutputFoldsRepeatedRuleViolations(t *testing.T) {
	// deny: [style.*] matches two style.naming findings; the human output
	// folds them into one counted line instead of two identical ones.
	policy := "version: 1\nrules:\n  deny: [style.*]\n"
	out, err := runGuardConsume(t, false, policy, ruleReviewJSON)
	if err == nil {
		t.Fatal("expected blocked error")
	}
	if n := strings.Count(out, "rule style.naming: denied by policy"); n != 1 {
		t.Errorf("want one folded style.naming line, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "(2 findings)") {
		t.Errorf("folded line missing the finding count:\n%s", out)
	}
}

// TestGuardRunModeLegacyBaselineWarning pins the run-mode warning routing: the
// guard-installed sink forwards the legacy-baseline notice to guard's own
// stderr, --quiet silences it, and the verdict on stdout is unaffected either way.
func TestGuardRunModeLegacyBaselineWarning(t *testing.T) {
	runGuard := func(t *testing.T, extra ...string) (stdout, stderr string) {
		t.Helper()
		e := newCLIEnv(t)
		e.writeBaselineFile(t, `{"version":1,"fingerprints":["0000"]}`)
		writeFile(t, filepath.Join(e.repoRoot, ".commitbrief", "policy.yml"),
			"version: 1\ntotal: 100\n")
		// --json keeps the verdict observable under --quiet (the human line is
		// suppressed by --quiet by design).
		args := append([]string{"guard", "--no-cache", "--json"}, extra...)
		if err := e.run(args...); err != nil {
			t.Fatalf("guard: %v\nstderr:\n%s", err, e.errOut.String())
		}
		return e.out.String(), e.errOut.String()
	}

	loudOut, loudErr := runGuard(t)
	if !strings.Contains(loudErr, "version 1 baseline") {
		t.Errorf("stderr missing the legacy-baseline warning:\n%s", loudErr)
	}
	if strings.Count(loudErr, "version 1 baseline") != 1 {
		t.Errorf("warning must print once per run:\n%s", loudErr)
	}

	quietOut, quietErr := runGuard(t, "--quiet")
	if strings.Contains(quietErr, "version 1 baseline") {
		t.Errorf("--quiet must silence the warning:\n%s", quietErr)
	}
	if !strings.Contains(loudOut, `"passed": true`) {
		t.Errorf("unexpected verdict:\n%s", loudOut)
	}
	if loudOut != quietOut {
		t.Errorf("--quiet must not change the verdict:\nloud:\n%s\nquiet:\n%s", loudOut, quietOut)
	}
}

// Fail closed without --sarif (ADR-0029 §Update 2026-10-25): a degraded review
// parses as "no findings", so without this gate a change the policy never
// evaluated would pass with exit 0.
const (
	degradedMetaReviewJSON = `{"schema":1,"findings":null,"content":"free text","meta":{"degrade_reason":"unparsable"}}`
	degradedRawReviewJSON  = `{"schema":1,"content":"the model answered in prose","meta":{}}`
	cleanEmptyReviewJSON   = `{"schema":1,"findings":[],"meta":{}}`
)

func TestGuardFromJSONDegradedFailsClosed(t *testing.T) {
	cases := map[string]string{
		"degrade_reason": degradedMetaReviewJSON,
		"raw_content":    degradedRawReviewJSON,
	}
	for name, review := range cases {
		for _, jsonMode := range []bool{false, true} {
			subName := name + "/plain"
			if jsonMode {
				subName = name + "/json"
			}
			t.Run(subName, func(t *testing.T) {
				out, err := runGuardConsume(t, jsonMode, guardPolicy, review)
				if err == nil {
					t.Fatal("a degraded review must block the gate (exit 1)")
				}
				if !strings.Contains(err.Error(), "policy cannot be verified") {
					t.Errorf("want the guard.degraded error, got: %v", err)
				}
				if strings.Contains(out, "PASS") {
					t.Errorf("a degraded review must never render a PASS verdict, got:\n%s", out)
				}
				if jsonMode && strings.TrimSpace(out) != "" {
					t.Errorf("--json must not write a verdict document for a degraded review, got:\n%s", out)
				}
			})
		}
	}
}

func TestGuardCleanEmptyReviewStillPasses(t *testing.T) {
	out, err := runGuardConsume(t, false, guardPolicy, cleanEmptyReviewJSON)
	if err != nil {
		t.Fatalf("a healthy review with no findings must pass: %v", err)
	}
	if !strings.Contains(out, "PASS") {
		t.Errorf("expected a PASS verdict, got:\n%s", out)
	}
}

func TestGuardRunModeDegradedFailsClosed(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		name := "plain"
		if jsonMode {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			e := newCLIEnv(t)
			setSarifFake(t, "I could not produce structured output, sorry.")
			pol, _, _ := guardSarifFiles(t, guardPolicy, passReviewJSON)
			args := []string{"guard", "--policy", pol, "--provider", "sarif-fake", "--no-cache"}
			if jsonMode {
				args = append(args, "--json")
			}
			err := e.run(args...)
			if err == nil {
				t.Fatalf("a degraded run-mode review must block the gate\nstdout:\n%s", e.out.String())
			}
			if !strings.Contains(err.Error(), "policy cannot be verified") {
				t.Errorf("want the guard.degraded error, got: %v", err)
			}
			if strings.Contains(e.out.String(), "PASS") || strings.Contains(e.out.String(), `"passed"`) {
				t.Errorf("no verdict may be written for a degraded review, got:\n%s", e.out.String())
			}
		})
	}
}

// A degraded live review is cached; the replay must block too, whether the
// pipeline signals it (run-mode) or only the document does (--from-json).
func TestGuardRunModeDegradedCacheReplayFailsClosed(t *testing.T) {
	e := newCLIEnv(t)
	setSarifFake(t, "I could not produce structured output, sorry.")
	stageSecretIntoRepo(t, e.repoRoot, "worker_test.go", "package app\n\nimport \"time\"\n\nfunc TestJob() {\n\ttime.Sleep(2 * time.Second)\n}\n")
	pol, _, _ := guardSarifFiles(t, guardPolicy, passReviewJSON)
	args := []string{"guard", "--policy", pol, "--provider", "sarif-fake"}

	if err := e.run(args...); err == nil {
		t.Fatal("the live degraded review must block the gate")
	}
	liveCalls := sarifCallCount()
	if liveCalls == 0 {
		t.Fatal("the first run must reach the provider")
	}
	e.out.Reset()
	err := e.run(args...)
	if err == nil {
		t.Fatalf("a degraded cache replay must block the gate\nstderr:\n%s", e.errOut.String())
	}
	if got := sarifCallCount(); got != liveCalls {
		t.Fatalf("provider calls after the second run = %d, want %d (the replay must come from the cache)", got, liveCalls)
	}
	if !strings.Contains(err.Error(), "policy cannot be verified") {
		t.Errorf("want the guard.degraded error, got: %v", err)
	}
	if strings.Contains(e.out.String(), "PASS") {
		t.Errorf("a degraded cache replay must not render PASS, got:\n%s", e.out.String())
	}
}
