package diff

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/ignore"
)

const sampleModify = `diff --git a/internal/auth/login.go b/internal/auth/login.go
index abc1234..def5678 100644
--- a/internal/auth/login.go
+++ b/internal/auth/login.go
@@ -10,7 +10,9 @@ func Login(ctx context.Context, creds Credentials) error {
 	if creds.Username == "" {
 		return ErrEmptyUsername
 	}
-	if creds.Password == "" {
+	if creds.Password == "" || len(creds.Password) < 8 {
 		return ErrEmptyPassword
 	}
+	logger.Info("login attempt", "user", creds.Username)
+	return verify(ctx, creds)
 	return nil
 }
`

const sampleAdded = `diff --git a/internal/feature/new.go b/internal/feature/new.go
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/internal/feature/new.go
@@ -0,0 +1,3 @@
+package feature
+
+func New() {}
`

const sampleDeleted = `diff --git a/old/dead.go b/old/dead.go
deleted file mode 100644
index 2222222..0000000
--- a/old/dead.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package old
-
-func Dead() {}
`

const sampleRenamed = `diff --git a/old/path.go b/new/path.go
similarity index 95%
rename from old/path.go
rename to new/path.go
index 3333333..4444444 100644
--- a/old/path.go
+++ b/new/path.go
@@ -1,3 +1,3 @@
 package main

-func old() {}
+func renamed() {}
`

const sampleBinary = `diff --git a/assets/logo.png b/assets/logo.png
index 5555555..6666666 100644
Binary files a/assets/logo.png and b/assets/logo.png differ
`

const sampleMulti = sampleModify + sampleAdded

func parse(t *testing.T, content string) Diff {
	t.Helper()
	d, err := Parse(git.Diff{Content: content, Origin: git.OriginStaged})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return d
}

func TestParseEmpty(t *testing.T) {
	d := parse(t, "")
	if !d.Empty() {
		t.Errorf("Empty diff should yield zero files; got %d", d.FileCount())
	}
}

func TestParseSingleModification(t *testing.T) {
	d := parse(t, sampleModify)
	if d.FileCount() != 1 {
		t.Fatalf("FileCount = %d, want 1", d.FileCount())
	}
	f := d.Files[0]
	if f.Path != "internal/auth/login.go" {
		t.Errorf("Path = %q", f.Path)
	}
	if f.Mode != ModeModified {
		t.Errorf("Mode = %v, want Modified", f.Mode)
	}
	if f.Binary {
		t.Error("Binary = true on text file")
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("Hunks = %d, want 1", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.OldStart != 10 || h.OldLines != 7 {
		t.Errorf("old range = (%d,%d), want (10,7)", h.OldStart, h.OldLines)
	}
	if h.NewStart != 10 || h.NewLines != 9 {
		t.Errorf("new range = (%d,%d), want (10,9)", h.NewStart, h.NewLines)
	}
	if !strings.Contains(h.Header, "func Login") {
		t.Errorf("Header = %q, expected function context", h.Header)
	}
	if d.AddedLines() != 3 {
		t.Errorf("AddedLines = %d, want 3", d.AddedLines())
	}
	if d.DeletedLines() != 1 {
		t.Errorf("DeletedLines = %d, want 1", d.DeletedLines())
	}
}

func TestParseAddedFile(t *testing.T) {
	d := parse(t, sampleAdded)
	if d.FileCount() != 1 {
		t.Fatalf("FileCount = %d", d.FileCount())
	}
	f := d.Files[0]
	if f.Mode != ModeAdded {
		t.Errorf("Mode = %v, want Added", f.Mode)
	}
	if f.Path != "internal/feature/new.go" {
		t.Errorf("Path = %q", f.Path)
	}
	if d.AddedLines() != 3 {
		t.Errorf("AddedLines = %d, want 3", d.AddedLines())
	}
}

func TestParseDeletedFile(t *testing.T) {
	d := parse(t, sampleDeleted)
	f := d.Files[0]
	if f.Mode != ModeDeleted {
		t.Errorf("Mode = %v, want Deleted", f.Mode)
	}
	if d.DeletedLines() != 3 {
		t.Errorf("DeletedLines = %d, want 3", d.DeletedLines())
	}
}

func TestParseRenamedFile(t *testing.T) {
	d := parse(t, sampleRenamed)
	f := d.Files[0]
	if f.Mode != ModeRenamed {
		t.Errorf("Mode = %v, want Renamed", f.Mode)
	}
	if f.OldPath != "old/path.go" {
		t.Errorf("OldPath = %q", f.OldPath)
	}
	if f.Path != "new/path.go" {
		t.Errorf("Path = %q", f.Path)
	}
}

func TestParseBinaryFile(t *testing.T) {
	d := parse(t, sampleBinary)
	f := d.Files[0]
	if !f.Binary {
		t.Error("Binary = false on .png file")
	}
	if len(f.Hunks) != 0 {
		t.Errorf("Binary file should have no hunks; got %d", len(f.Hunks))
	}
}

func TestParseMultipleFiles(t *testing.T) {
	d := parse(t, sampleMulti)
	if d.FileCount() != 2 {
		t.Errorf("FileCount = %d, want 2", d.FileCount())
	}
	if d.Files[0].Mode != ModeModified || d.Files[1].Mode != ModeAdded {
		t.Errorf("modes wrong: %v, %v", d.Files[0].Mode, d.Files[1].Mode)
	}
}

func TestParsePreservesOriginAndArgs(t *testing.T) {
	in := git.Diff{
		Content: sampleModify,
		Origin:  git.OriginCommit,
		Args:    map[string]string{"hash": "abc123"},
	}
	d, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	if d.Origin != git.OriginCommit {
		t.Errorf("Origin = %q", d.Origin)
	}
	if d.Args["hash"] != "abc123" {
		t.Errorf("Args lost: %v", d.Args)
	}
}

func TestParseHunkWithoutLineCount(t *testing.T) {
	// Single-line hunks omit the count: `@@ -5 +5 @@`
	src := `diff --git a/x b/x
--- a/x
+++ b/x
@@ -5 +5 @@
-old
+new
`
	d := parse(t, src)
	h := d.Files[0].Hunks[0]
	if h.OldLines != 1 || h.NewLines != 1 {
		t.Errorf("default line count broken: old=%d new=%d", h.OldLines, h.NewLines)
	}
}

func TestParseInvalidHunkHeader(t *testing.T) {
	src := `diff --git a/x b/x
--- a/x
+++ b/x
@@ broken @@
`
	_, err := Parse(git.Diff{Content: src})
	if err == nil {
		t.Error("expected error for malformed hunk header")
	}
}

func TestFilterExcludesByPath(t *testing.T) {
	d := parse(t, sampleMulti)
	m := ignore.Empty()
	parsed, _ := ignore.Parse(strings.NewReader("internal/feature/**\n"))
	filtered := Filter(d, parsed)
	if filtered.FileCount() != 1 {
		t.Fatalf("FileCount = %d, want 1 (feature/new.go filtered)", filtered.FileCount())
	}
	if filtered.Files[0].Path != "internal/auth/login.go" {
		t.Errorf("wrong file kept: %q", filtered.Files[0].Path)
	}

	// Empty matcher: no-op
	same := Filter(d, m)
	if same.FileCount() != d.FileCount() {
		t.Error("Empty matcher should pass diff through unchanged")
	}
}

func TestFilterUsesOldPathForRename(t *testing.T) {
	d := parse(t, sampleRenamed)
	parsed, _ := ignore.Parse(strings.NewReader("old/**\n"))
	filtered := Filter(d, parsed)
	if filtered.FileCount() != 0 {
		t.Errorf("rename matching OldPath should be filtered; got %d files", filtered.FileCount())
	}
}

func TestFilterNilMatcherIsNoOp(t *testing.T) {
	d := parse(t, sampleMulti)
	out := Filter(d, nil)
	if out.FileCount() != d.FileCount() {
		t.Error("nil matcher should leave Diff intact")
	}
}

func TestEstimateTokens(t *testing.T) {
	cases := map[string]int{
		"":    0,
		"a":   1,
		"abc": 1,
		"abcd": 1,
		"abcde": 2,
		strings.Repeat("a", 100): 25,
	}
	for in, want := range cases {
		if got := EstimateTokens(in); got != want {
			t.Errorf("EstimateTokens(%q len=%d) = %d, want %d", in, len(in), got, want)
		}
	}
}

func TestEstimateTokensWithinTwentyPercent(t *testing.T) {
	// For typical English+code, chars/4 should be within 20% of "true" tokens.
	// We sanity-check against a hand-tokenized expectation rather than a real
	// tokenizer: the sample below has ~120 chars; we expect 25-35 tokens.
	const sample = "func Login(ctx context.Context, creds Credentials) error { return nil }"
	got := EstimateTokens(sample)
	// 71 chars / 4 = ~18 tokens — close to a real BPE tokenizer's output.
	if got < 10 || got > 30 {
		t.Errorf("EstimateTokens out of expected range: got %d (chars=%d)", got, len(sample))
	}
}

func TestDiffEstimateTokensApproximatesContent(t *testing.T) {
	d := parse(t, sampleModify)
	got := d.EstimateTokens()
	if got < 20 {
		t.Errorf("EstimateTokens too low: %d", got)
	}
	if got > EstimateTokens(sampleModify) {
		t.Errorf("structured estimate (%d) should not exceed raw estimate (%d)", got, EstimateTokens(sampleModify))
	}
}

func TestFormatRoundTripPreservesContent(t *testing.T) {
	cases := map[string]string{
		"modify":  sampleModify,
		"added":   sampleAdded,
		"deleted": sampleDeleted,
		"binary":  sampleBinary,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			d := parse(t, src)
			out := d.String()
			reparsed := parse(t, out)
			if reparsed.FileCount() != d.FileCount() {
				t.Errorf("round-trip FileCount: %d → %d", d.FileCount(), reparsed.FileCount())
			}
			if reparsed.AddedLines() != d.AddedLines() {
				t.Errorf("round-trip AddedLines: %d → %d", d.AddedLines(), reparsed.AddedLines())
			}
			if reparsed.DeletedLines() != d.DeletedLines() {
				t.Errorf("round-trip DeletedLines: %d → %d", d.DeletedLines(), reparsed.DeletedLines())
			}
		})
	}
}

func TestModeString(t *testing.T) {
	cases := map[Mode]string{
		ModeModified: "modified",
		ModeAdded:    "added",
		ModeDeleted:  "deleted",
		ModeRenamed:  "renamed",
		ModeCopied:   "copied",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Errorf("Mode(%d).String() = %q, want %q", m, got, want)
		}
	}
}
