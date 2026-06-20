// SPDX-License-Identifier: GPL-3.0-or-later

package baseline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/render"
)

func finding(file string, line int, sev render.Severity, title string) render.Finding {
	return render.Finding{
		Severity:    sev,
		File:        file,
		Line:        line,
		Title:       title,
		Description: "desc",
		Suggestion:  "fix",
	}
}

func TestFingerprintDeterministic(t *testing.T) {
	f := finding("a.go", 10, render.SeverityHigh, "Possible nil deref")
	if got, want := Fingerprint(f), Fingerprint(f); got != want {
		t.Fatalf("fingerprint not deterministic: %q vs %q", got, want)
	}
}

func TestFingerprintLineDriftResilient(t *testing.T) {
	// Same finding at a different line must yield the SAME fingerprint —
	// brownfield baselines must survive code shifting down/up the file.
	a := finding("a.go", 10, render.SeverityHigh, "Possible nil deref")
	b := finding("a.go", 999, render.SeverityHigh, "Possible nil deref")
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("line drift changed the fingerprint; it must not include Line")
	}
}

func TestFingerprintTitleNormalized(t *testing.T) {
	// Trim + collapse internal whitespace + lowercase: these all collapse
	// to one fingerprint.
	base := finding("a.go", 1, render.SeverityLow, "Possible Nil Deref")
	variants := []string{
		"  Possible Nil Deref  ", // leading/trailing
		"Possible   Nil   Deref", // collapsed internal whitespace
		"possible nil deref",     // lowercase
		"POSSIBLE NIL DEREF",
		"Possible\tNil\nDeref",
	}
	want := Fingerprint(base)
	for _, v := range variants {
		if got := Fingerprint(finding("a.go", 1, render.SeverityLow, v)); got != want {
			t.Errorf("title %q normalized fingerprint = %q, want %q", v, got, want)
		}
	}
}

func TestFingerprintDistinctOnEachField(t *testing.T) {
	base := finding("a.go", 1, render.SeverityHigh, "Title")
	cases := map[string]render.Finding{
		"different file":     finding("b.go", 1, render.SeverityHigh, "Title"),
		"different severity": finding("a.go", 1, render.SeverityLow, "Title"),
		"different title":    finding("a.go", 1, render.SeverityHigh, "Other"),
	}
	fp := Fingerprint(base)
	for name, c := range cases {
		if Fingerprint(c) == fp {
			t.Errorf("%s should change the fingerprint but did not", name)
		}
	}
}

func TestFingerprintIgnoresVolatileFields(t *testing.T) {
	// Description and Snippet are LLM-volatile prose; they must NOT affect
	// the fingerprint or every re-run would churn.
	a := finding("a.go", 1, render.SeverityHigh, "Title")
	b := a
	b.Description = "completely different wording from the model this time"
	b.Snippet = "+ new line\n- old line"
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("Description/Snippet must not affect the fingerprint")
	}
}

func TestLoadMissingFileIsEmptyNoError(t *testing.T) {
	dir := t.TempDir()
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("missing baseline should not error, got %v", err)
	}
	if set.Len() != 0 {
		t.Fatalf("missing baseline should be empty, got %d", set.Len())
	}
}

func TestWriteThenLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	findings := []render.Finding{
		finding("a.go", 10, render.SeverityHigh, "One"),
		finding("b.go", 20, render.SeverityLow, "Two"),
	}
	n, err := Write(dir, findings)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != 2 {
		t.Fatalf("write count = %d, want 2", n)
	}
	// File must live under .commitbrief/.
	if _, err := os.Stat(filepath.Join(dir, ".commitbrief", "baseline.json")); err != nil {
		t.Fatalf("baseline.json not written where expected: %v", err)
	}
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, f := range findings {
		if !set.Contains(Fingerprint(f)) {
			t.Errorf("loaded set missing fingerprint for %s", f.Title)
		}
	}
}

func TestWriteDeduplicates(t *testing.T) {
	dir := t.TempDir()
	// Same fingerprint twice (same file/severity/title, different line) must
	// collapse to one entry.
	n, err := Write(dir, []render.Finding{
		finding("a.go", 1, render.SeverityHigh, "Dup"),
		finding("a.go", 50, render.SeverityHigh, "Dup"),
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected dedup to 1 fingerprint, got %d", n)
	}
}

func TestFilterRemovesBaselined(t *testing.T) {
	known := finding("a.go", 10, render.SeverityHigh, "Known")
	fresh := finding("b.go", 5, render.SeverityCritical, "Fresh")
	set := Set{Fingerprint(known): {}}

	kept, baselined := Filter([]render.Finding{known, fresh}, set)
	if baselined != 1 {
		t.Fatalf("baselined count = %d, want 1", baselined)
	}
	if len(kept) != 1 || kept[0].Title != "Fresh" {
		t.Fatalf("kept = %+v, want only the fresh finding", kept)
	}
}

func TestFilterLineDriftStillBaselined(t *testing.T) {
	// A baselined finding that moved to a new line must STILL be filtered.
	orig := finding("a.go", 10, render.SeverityHigh, "Known")
	moved := finding("a.go", 200, render.SeverityHigh, "Known")
	set := Set{Fingerprint(orig): {}}

	kept, baselined := Filter([]render.Finding{moved}, set)
	if baselined != 1 || len(kept) != 0 {
		t.Fatalf("moved finding not baselined: kept=%d baselined=%d", len(kept), baselined)
	}
}

func TestFilterEmptySetIsPassthrough(t *testing.T) {
	in := []render.Finding{finding("a.go", 1, render.SeverityHigh, "X")}
	kept, baselined := Filter(in, Set{})
	if baselined != 0 || len(kept) != 1 {
		t.Fatalf("empty set must pass through: kept=%d baselined=%d", len(kept), baselined)
	}
	// nil set too.
	kept, baselined = Filter(in, nil)
	if baselined != 0 || len(kept) != 1 {
		t.Fatalf("nil set must pass through: kept=%d baselined=%d", len(kept), baselined)
	}
}

func TestFilterKeptIsNonNil(t *testing.T) {
	// Even when everything is filtered, kept must be non-nil so downstream
	// nil-means-degrade is preserved.
	f := finding("a.go", 1, render.SeverityHigh, "X")
	kept, _ := Filter([]render.Finding{f}, Set{Fingerprint(f): {}})
	if kept == nil {
		t.Fatal("kept slice must be non-nil even when fully filtered")
	}
	if len(kept) != 0 {
		t.Fatalf("kept should be empty, got %d", len(kept))
	}
}

func TestLoadCorruptFileErrors(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("corrupt baseline must error, not silently unhide findings")
	}
}

func TestLoadUnsupportedVersionErrors(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":999,"fingerprints":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("unsupported version must error")
	}
}

func TestWriteAbsorbDropsRemovedFindings(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, []render.Finding{finding("a.go", 1, render.SeverityHigh, "Old")}); err != nil {
		t.Fatal(err)
	}
	// Re-baseline with a different set: the old fingerprint must be gone
	// (Write is an absorb, not a merge).
	if _, err := Write(dir, []render.Finding{finding("b.go", 1, render.SeverityLow, "New")}); err != nil {
		t.Fatal(err)
	}
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if set.Contains(Fingerprint(finding("a.go", 1, render.SeverityHigh, "Old"))) {
		t.Fatal("re-baseline must absorb, not merge: old fingerprint should be gone")
	}
	if !set.Contains(Fingerprint(finding("b.go", 1, render.SeverityLow, "New"))) {
		t.Fatal("re-baseline missing the new fingerprint")
	}
}
