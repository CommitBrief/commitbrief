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
