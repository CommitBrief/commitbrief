// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decodeReviewJSON runs `commitbrief --json <args>` and decodes the document.
func (e *cliEnv) decodeReviewJSON(t *testing.T, args ...string) map[string]any {
	t.Helper()
	e.out.Reset()
	e.errOut.Reset()
	full := append([]string{"--json", "--no-cache"}, args...)
	if err := e.run(full...); err != nil {
		t.Fatalf("run %v: %v\nstderr: %s", full, err, e.errOut.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &doc); err != nil {
		t.Fatalf("decode json: %v\nstdout: %s", err, e.out.String())
	}
	return doc
}

func findingsLen(doc map[string]any) int {
	f, _ := doc["findings"].([]any)
	return len(f)
}

func metaInt(doc map[string]any, key string) (int, bool) {
	meta, _ := doc["meta"].(map[string]any)
	v, ok := meta[key].(float64)
	return int(v), ok
}

// TestUpdateBaselineThenFilter is the SC1 end-to-end: the first review
// surfaces the mock finding; --update-baseline absorbs it; the next review
// filters it out (findings empty, meta.baselined=1); --no-baseline restores it.
func TestUpdateBaselineThenFilter(t *testing.T) {
	e := newCLIEnv(t)

	// Baseline run: the mock provider returns one finding.
	doc := e.decodeReviewJSON(t)
	if n := findingsLen(doc); n != 1 {
		t.Fatalf("baseline run findings = %d, want 1", n)
	}
	if _, ok := metaInt(doc, "baselined"); ok {
		t.Error("meta.baselined must be omitted when nothing baselined")
	}

	// --update-baseline writes the file but does NOT filter this run.
	e.out.Reset()
	e.errOut.Reset()
	if err := e.run("--update-baseline", "--no-cache"); err != nil {
		t.Fatalf("update-baseline: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.repoRoot, ".commitbrief", "baseline.json")); err != nil {
		t.Fatalf("baseline.json not written: %v", err)
	}

	// Next review: the finding is now baselined → removed + counted.
	doc = e.decodeReviewJSON(t)
	if n := findingsLen(doc); n != 0 {
		t.Fatalf("post-baseline findings = %d, want 0", n)
	}
	if got, ok := metaInt(doc, "baselined"); !ok || got != 1 {
		t.Fatalf("meta.baselined = (%d, present=%v), want 1", got, ok)
	}

	// --no-baseline bypasses the filter for this run.
	doc = e.decodeReviewJSON(t, "--no-baseline")
	if n := findingsLen(doc); n != 1 {
		t.Fatalf("--no-baseline findings = %d, want 1", n)
	}
	if _, ok := metaInt(doc, "baselined"); ok {
		t.Error("--no-baseline must not report a baselined count")
	}
}

// TestBaselineConfigOff verifies review.baseline=false disables the filter
// even when a baseline.json exists.
func TestBaselineConfigOff(t *testing.T) {
	e := newCLIEnv(t)

	if err := e.run("--update-baseline", "--no-cache"); err != nil {
		t.Fatalf("update-baseline: %v", err)
	}
	// Set on the USER config (not --local): config set --local writes a fresh
	// repo skeleton that would reset the provider to the built-in default and
	// shadow the mock; the user-level write merges into the existing mock
	// config instead. Either path exercises the same review.baseline gate.
	e.out.Reset()
	if err := e.run("config", "set", "review.baseline", "false"); err != nil {
		t.Fatalf("config set review.baseline false: %v", err)
	}

	doc := e.decodeReviewJSON(t)
	if n := findingsLen(doc); n != 1 {
		t.Fatalf("review.baseline=false should not filter; findings = %d, want 1", n)
	}
}

// TestMissingBaselineIsNoOp verifies a review with review.baseline=true (the
// default) but no baseline.json file behaves exactly as before.
func TestMissingBaselineIsNoOp(t *testing.T) {
	e := newCLIEnv(t)
	doc := e.decodeReviewJSON(t)
	if n := findingsLen(doc); n != 1 {
		t.Fatalf("missing baseline must be a no-op; findings = %d, want 1", n)
	}
	if _, ok := metaInt(doc, "baselined"); ok {
		t.Error("no baseline file → no baselined count")
	}
}

// TestSignalControlFooterPrinted verifies the human footer lands on stderr
// after a baselined run (and not under --json's stdout).
func TestSignalControlFooterPrinted(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--update-baseline", "--no-cache"); err != nil {
		t.Fatalf("update-baseline: %v", err)
	}
	// A human (non-JSON) run should print the "N baselined" footer to stderr.
	e.out.Reset()
	e.errOut.Reset()
	if err := e.run("--no-cache"); err != nil {
		t.Fatalf("review: %v", err)
	}
	if !strings.Contains(e.errOut.String(), "baselined") {
		t.Errorf("expected a 'baselined' footer on stderr, got:\n%s", e.errOut.String())
	}
}

// TestUpdateBaselineNoBaselineMutuallyExclusive verifies the flag conflict.
func TestUpdateBaselineNoBaselineMutuallyExclusive(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("--update-baseline", "--no-baseline", "--no-cache")
	if err == nil {
		t.Fatal("--update-baseline + --no-baseline must conflict")
	}
}
