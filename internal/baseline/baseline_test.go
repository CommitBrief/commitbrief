// SPDX-License-Identifier: GPL-3.0-or-later

package baseline

import (
	"encoding/json"
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

// stamped returns a finding carrying an identity fingerprint, the way the
// pipeline's stampIdentity leaves it before signal control. The fingerprint is
// independent of the title (ADR-0045), which is the whole point of v2.
func stamped(file string, line int, sev render.Severity, title, fp string) render.Finding {
	f := finding(file, line, sev, title)
	f.RuleID = "general.other"
	f.Fingerprint = fp
	return f
}

func writeRaw(t *testing.T, dir, body string) {
	t.Helper()
	path := Path(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyFingerprintDeterministic(t *testing.T) {
	f := finding("a.go", 10, render.SeverityHigh, "Possible nil deref")
	if got, want := legacyFingerprint(f), legacyFingerprint(f); got != want {
		t.Fatalf("fingerprint not deterministic: %q vs %q", got, want)
	}
}

func TestLegacyFingerprintLineDriftResilient(t *testing.T) {
	// Same finding at a different line must yield the SAME fingerprint —
	// brownfield baselines must survive code shifting down/up the file.
	a := finding("a.go", 10, render.SeverityHigh, "Possible nil deref")
	b := finding("a.go", 999, render.SeverityHigh, "Possible nil deref")
	if legacyFingerprint(a) != legacyFingerprint(b) {
		t.Fatal("line drift changed the fingerprint; it must not include Line")
	}
}

func TestLegacyFingerprintTitleNormalized(t *testing.T) {
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
	want := legacyFingerprint(base)
	for _, v := range variants {
		if got := legacyFingerprint(finding("a.go", 1, render.SeverityLow, v)); got != want {
			t.Errorf("title %q normalized fingerprint = %q, want %q", v, got, want)
		}
	}
}

func TestLegacyFingerprintDistinctOnEachField(t *testing.T) {
	base := finding("a.go", 1, render.SeverityHigh, "Title")
	cases := map[string]render.Finding{
		"different file":     finding("b.go", 1, render.SeverityHigh, "Title"),
		"different severity": finding("a.go", 1, render.SeverityLow, "Title"),
		"different title":    finding("a.go", 1, render.SeverityHigh, "Other"),
	}
	fp := legacyFingerprint(base)
	for name, c := range cases {
		if legacyFingerprint(c) == fp {
			t.Errorf("%s should change the fingerprint but did not", name)
		}
	}
}

func TestLegacyFingerprintIgnoresVolatileFields(t *testing.T) {
	// Description and Snippet are LLM-volatile prose; they must NOT affect
	// the fingerprint or every re-run would churn.
	a := finding("a.go", 1, render.SeverityHigh, "Title")
	b := a
	b.Description = "completely different wording from the model this time"
	b.Snippet = "+ new line\n- old line"
	if legacyFingerprint(a) != legacyFingerprint(b) {
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
		stamped("a.go", 10, render.SeverityHigh, "One", "fp-one"),
		stamped("b.go", 20, render.SeverityLow, "Two", "fp-two"),
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
	if set.Legacy() {
		t.Fatal("a freshly written baseline must not be reported as legacy")
	}
	for _, f := range findings {
		if !set.Contains(f.Fingerprint) || !set.Matches(f) {
			t.Errorf("loaded set missing fingerprint for %s", f.Title)
		}
	}
}

func TestWriteAlwaysWritesVersion2(t *testing.T) {
	dir := t.TempDir()
	// Start from a v1 file, then re-baseline: the result must be v2.
	writeRaw(t, dir, `{"version":1,"fingerprints":["abc"]}`)
	if _, err := Write(dir, []render.Finding{stamped("a.go", 1, render.SeverityHigh, "X", "fp-x")}); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Version      int      `json:"version"`
		Fingerprints []string `json:"fingerprints"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Version != 2 || FileVersion != 2 {
		t.Fatalf("version = %d (FileVersion %d), want 2", got.Version, FileVersion)
	}
	if len(got.Fingerprints) != 1 || got.Fingerprints[0] != "fp-x" {
		t.Fatalf("fingerprints = %v, want [fp-x] (absorb, not merge)", got.Fingerprints)
	}
}

func TestWriteSkipsUnstampedFindings(t *testing.T) {
	dir := t.TempDir()
	n, err := Write(dir, []render.Finding{finding("a.go", 1, render.SeverityHigh, "No identity")})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != 0 {
		t.Fatalf("a finding without a fingerprint must not be recorded, got %d", n)
	}
}

func TestWriteDeduplicates(t *testing.T) {
	dir := t.TempDir()
	// Same fingerprint twice (different line and title) must collapse to one
	// entry.
	n, err := Write(dir, []render.Finding{
		stamped("a.go", 1, render.SeverityHigh, "Dup", "fp-dup"),
		stamped("a.go", 50, render.SeverityHigh, "Dup, reworded", "fp-dup"),
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected dedup to 1 fingerprint, got %d", n)
	}
}

func TestV1FileMatchesWithLegacyFormula(t *testing.T) {
	dir := t.TempDir()
	known := finding("a.go", 10, render.SeverityHigh, "Known")
	// The v1 file holds the title-based fingerprint; the live finding also
	// carries a (different) v2 identity fingerprint, which must NOT be used.
	writeRaw(t, dir, `{"version":1,"fingerprints":["`+legacyFingerprint(known)+`"]}`)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("load v1: %v", err)
	}
	if !set.Legacy() {
		t.Fatal("a non-empty v1 file must report Legacy()")
	}
	live := known
	live.RuleID = "general.other"
	live.Fingerprint = "v2-identity-fp"
	kept, baselined := Filter([]render.Finding{live, finding("b.go", 1, render.SeverityHigh, "Other")}, set)
	if baselined != 1 || len(kept) != 1 || kept[0].File != "b.go" {
		t.Fatalf("v1 set must match via the legacy formula: kept=%+v baselined=%d", kept, baselined)
	}
}

func TestEmptyV1FileIsNotLegacy(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, `{"version":1,"fingerprints":[]}`)
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if set.Legacy() {
		t.Fatal("an empty v1 file has nothing to migrate; no warning")
	}
}

func TestV2KeepsFindingBaselinedWhenTitleChanges(t *testing.T) {
	dir := t.TempDir()
	orig := stamped("a.go", 10, render.SeverityHigh, "Possible nil deref", "fp-stable")
	if _, err := Write(dir, []render.Finding{orig}); err != nil {
		t.Fatal(err)
	}
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The model rewords the title and the line moves; identity is unchanged.
	reworded := stamped("a.go", 400, render.SeverityHigh, "Nil pointer dereference possible", "fp-stable")
	kept, baselined := Filter([]render.Finding{reworded}, set)
	if baselined != 1 || len(kept) != 0 {
		t.Fatalf("v2 match must survive a title change: kept=%d baselined=%d", len(kept), baselined)
	}
}

func TestV2DoesNotMatchUnstampedFinding(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, []render.Finding{stamped("a.go", 1, render.SeverityHigh, "X", "fp-x")}); err != nil {
		t.Fatal(err)
	}
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if set.Matches(finding("a.go", 1, render.SeverityHigh, "X")) {
		t.Fatal("a finding with an empty fingerprint must never match a v2 set")
	}
}

func TestFilterRemovesBaselined(t *testing.T) {
	known := stamped("a.go", 10, render.SeverityHigh, "Known", "fp-known")
	fresh := stamped("b.go", 5, render.SeverityCritical, "Fresh", "fp-fresh")
	dir := t.TempDir()
	if _, err := Write(dir, []render.Finding{known}); err != nil {
		t.Fatal(err)
	}
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	kept, baselined := Filter([]render.Finding{known, fresh}, set)
	if baselined != 1 {
		t.Fatalf("baselined count = %d, want 1", baselined)
	}
	if len(kept) != 1 || kept[0].Title != "Fresh" {
		t.Fatalf("kept = %+v, want only the fresh finding", kept)
	}
}

func TestFilterEmptySetIsPassthrough(t *testing.T) {
	in := []render.Finding{stamped("a.go", 1, render.SeverityHigh, "X", "fp-x")}
	kept, baselined := Filter(in, Set{})
	if baselined != 0 || len(kept) != 1 {
		t.Fatalf("empty set must pass through: kept=%d baselined=%d", len(kept), baselined)
	}
}

func TestFilterKeptIsNonNil(t *testing.T) {
	// Even when everything is filtered, kept must be non-nil so downstream
	// nil-means-degrade is preserved.
	f := stamped("a.go", 1, render.SeverityHigh, "X", "fp-x")
	dir := t.TempDir()
	if _, err := Write(dir, []render.Finding{f}); err != nil {
		t.Fatal(err)
	}
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	kept, _ := Filter([]render.Finding{f}, set)
	if kept == nil {
		t.Fatal("kept slice must be non-nil even when fully filtered")
	}
	if len(kept) != 0 {
		t.Fatalf("kept should be empty, got %d", len(kept))
	}
}

func TestLoadCorruptFileErrors(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "{not json")
	if _, err := Load(dir); err == nil {
		t.Fatal("corrupt baseline must error, not silently unhide findings")
	}
}

func TestLoadUnsupportedVersionErrors(t *testing.T) {
	for _, body := range []string{
		`{"version":999,"fingerprints":[]}`,
		`{"version":0,"fingerprints":[]}`,
		`{"version":3,"fingerprints":[]}`,
	} {
		dir := t.TempDir()
		writeRaw(t, dir, body)
		if _, err := Load(dir); err == nil {
			t.Fatalf("unsupported version must error: %s", body)
		}
	}
}

func TestWriteAbsorbDropsRemovedFindings(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, []render.Finding{stamped("a.go", 1, render.SeverityHigh, "Old", "fp-old")}); err != nil {
		t.Fatal(err)
	}
	// Re-baseline with a different set: the old fingerprint must be gone
	// (Write is an absorb, not a merge).
	if _, err := Write(dir, []render.Finding{stamped("b.go", 1, render.SeverityLow, "New", "fp-new")}); err != nil {
		t.Fatal(err)
	}
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if set.Contains("fp-old") {
		t.Fatal("re-baseline must absorb, not merge: old fingerprint should be gone")
	}
	if !set.Contains("fp-new") {
		t.Fatal("re-baseline missing the new fingerprint")
	}
}
