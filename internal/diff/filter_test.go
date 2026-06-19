// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"reflect"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/git"
)

func paths(d Diff) []string {
	out := make([]string, 0, len(d.Files))
	for _, f := range d.Files {
		out = append(out, f.Path)
	}
	return out
}

// mustKeep runs KeepPaths and fails the test on an unexpected error.
// Use it everywhere the patterns are known-valid so the call sites stay
// readable; the error path has its own dedicated test.
func mustKeep(t *testing.T, d Diff, files, dirs []string) Diff {
	t.Helper()
	got, err := KeepPaths(d, files, dirs)
	if err != nil {
		t.Fatalf("KeepPaths(%v, %v) unexpected error: %v", files, dirs, err)
	}
	return got
}

func sample() Diff {
	return Diff{
		Files: []FileDiff{
			{Path: "app/Http/Controllers/API.php"},
			{Path: "app/Models/User.go"},
			{Path: "routes/web.php"},
			{Path: "database/seeder/UserSeeder.php"},
			{Path: "database/seeder/RoleSeeder.php"},
			{Path: "tests/unit_test.go"},
			{Path: "renamed.go", OldPath: "old.go"},
		},
	}
}

func TestKeepPaths_NoFiltersReturnsInput(t *testing.T) {
	d := sample()
	if got := mustKeep(t, d, nil, nil); !reflect.DeepEqual(got, d) {
		t.Errorf("KeepPaths with no filters should be identity; got %v", got)
	}
	if got := mustKeep(t, d, []string{}, []string{}); !reflect.DeepEqual(got, d) {
		t.Errorf("KeepPaths with empty slices should be identity")
	}
}

func TestKeepPaths_FileAllowlist(t *testing.T) {
	d := sample()
	got := mustKeep(t, d, []string{"routes/web.php", "app/Models/User.go"}, nil)
	want := []string{"app/Models/User.go", "routes/web.php"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("files allowlist = %v, want %v", paths(got), want)
	}
}

func TestKeepPaths_DirAllowlistPrefixMatch(t *testing.T) {
	d := sample()
	got := mustKeep(t, d, nil, []string{"database/seeder"})
	want := []string{"database/seeder/UserSeeder.php", "database/seeder/RoleSeeder.php"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("dir allowlist = %v, want %v", paths(got), want)
	}
}

func TestKeepPaths_DirAllowlistDoesNotMatchSibling(t *testing.T) {
	// "database/seed" must NOT match "database/seeder/*" — the matcher
	// appends a trailing "/" so prefix is strict on path-segment
	// boundaries. Regression guard against substring matching.
	d := Diff{Files: []FileDiff{
		{Path: "database/seeder/file.php"},
		{Path: "database/seedother/file.php"},
	}}
	got := mustKeep(t, d, nil, []string{"database/seeder"})
	want := []string{"database/seeder/file.php"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("dir prefix match = %v, want %v (no substring leakage)",
			paths(got), want)
	}
}

func TestKeepPaths_FileAndDirUnion(t *testing.T) {
	d := sample()
	got := mustKeep(t, d, []string{"routes/web.php"}, []string{"app/Models"})
	want := []string{"app/Models/User.go", "routes/web.php"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("union of file+dir = %v, want %v", paths(got), want)
	}
}

func TestKeepPaths_TrailingSlashOnDirNormalized(t *testing.T) {
	d := sample()
	a := mustKeep(t, d, nil, []string{"database/seeder"})
	b := mustKeep(t, d, nil, []string{"database/seeder/"})
	if !reflect.DeepEqual(paths(a), paths(b)) {
		t.Errorf("trailing slash should not change result\nwithout: %v\nwith:    %v",
			paths(a), paths(b))
	}
}

func TestKeepPaths_OldPathConsidered(t *testing.T) {
	// A renamed file should be picked up by either the new path or the
	// pre-rename path. Lets the user say "review changes to the file
	// formerly known as old.go" even if it's been renamed in the diff.
	d := Diff{Files: []FileDiff{
		{Path: "renamed.go", OldPath: "old.go"},
	}}
	if got := mustKeep(t, d, []string{"old.go"}, nil); len(got.Files) != 1 {
		t.Errorf("file allowlist on OldPath should match; got %d files", len(got.Files))
	}
	if got := mustKeep(t, d, []string{"renamed.go"}, nil); len(got.Files) != 1 {
		t.Errorf("file allowlist on Path should match; got %d files", len(got.Files))
	}
}

func TestKeepPaths_RecomputesLineAggregates(t *testing.T) {
	// AddedLines/DeletedLines are memoized at construction. After a
	// filter narrows the file set, the aggregates must reflect the
	// SURVIVING files only — not the original whole-tree counts.
	// Regression guard against forgetting to call countLineKinds in
	// new filter helpers.
	in := Diff{
		Files: []FileDiff{
			{
				Path:      "keep.go",
				PathParts: []string{"keep.go"},
				Hunks: []Hunk{{Lines: []HunkLine{
					{Kind: LineAdd, Text: "a"},
					{Kind: LineAdd, Text: "b"},
					{Kind: LineDel, Text: "x"},
				}}},
			},
			{
				Path:      "drop.go",
				PathParts: []string{"drop.go"},
				Hunks: []Hunk{{Lines: []HunkLine{
					{Kind: LineAdd, Text: "drop"},
					{Kind: LineDel, Text: "drop"},
					{Kind: LineDel, Text: "drop"},
				}}},
			},
		},
	}
	// Pre-populate memo to mimic Parse output.
	in.addedLines, in.deletedLines = countLineKinds(in.Files)
	if in.AddedLines() != 3 || in.DeletedLines() != 3 {
		t.Fatalf("test setup: AddedLines=%d DeletedLines=%d, want 3/3",
			in.AddedLines(), in.DeletedLines())
	}

	out := mustKeep(t, in, []string{"keep.go"}, nil)
	if out.AddedLines() != 2 {
		t.Errorf("post-filter AddedLines = %d, want 2 (subset)", out.AddedLines())
	}
	if out.DeletedLines() != 1 {
		t.Errorf("post-filter DeletedLines = %d, want 1 (subset)", out.DeletedLines())
	}
}

func TestKeepPaths_PreservesOriginAndArgs(t *testing.T) {
	// Origin/Args metadata is preserved across the filter so downstream
	// consumers (cache key, dry-run formatter, render meta) keep the
	// original scope context.
	d := sample()
	d.Origin = git.OriginStaged
	d.Args = map[string]string{"scope": "staged"}
	got := mustKeep(t, d, []string{"routes/web.php"}, nil)
	if got.Origin != git.OriginStaged || !reflect.DeepEqual(got.Args, map[string]string{"scope": "staged"}) {
		t.Errorf("metadata not preserved: Origin=%v Args=%v", got.Origin, got.Args)
	}
}

func TestKeepPaths_EmptyDirEntryIgnored(t *testing.T) {
	// Empty-string dir entries (e.g. someone passed --dir ""  by mistake)
	// shouldn't match every file in the repo. Treat them as no-op.
	d := sample()
	got := mustKeep(t, d, nil, []string{"", "  "})
	if len(got.Files) != 0 {
		t.Errorf("empty/whitespace dir should not match anything; got %d files", len(got.Files))
	}
}

// --- glob support (ADR-0026) -------------------------------------------------

func TestKeepPaths_GlobBasenameAnyDepth(t *testing.T) {
	// A slash-less pattern matches the basename at any depth: `*.go`
	// catches both a root file and a nested one (gitignore semantics).
	d := Diff{Files: []FileDiff{
		{Path: "main.go"},
		{Path: "app/Models/User.go"},
		{Path: "routes/web.php"},
	}}
	got := mustKeep(t, d, []string{"*.go"}, nil)
	want := []string{"main.go", "app/Models/User.go"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("*.go = %v, want %v (root + nested, .php excluded)", paths(got), want)
	}
}

func TestKeepPaths_GlobAnchoredRecursive(t *testing.T) {
	// A slash-bearing pattern is root-anchored. `internal/**/*.ts` must
	// match .ts files anywhere under internal/, but not a .ts outside it
	// nor a non-.ts inside it.
	d := Diff{Files: []FileDiff{
		{Path: "internal/a.ts"},
		{Path: "internal/cli/widgets/x.ts"},
		{Path: "internal/cli/x.go"},
		{Path: "web/y.ts"},
	}}
	got := mustKeep(t, d, []string{"internal/**/*.ts"}, nil)
	want := []string{"internal/a.ts", "internal/cli/widgets/x.ts"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("internal/**/*.ts = %v, want %v (anchored, recursive, .ts only)", paths(got), want)
	}
}

func TestKeepPaths_GlobSingleLevel(t *testing.T) {
	// `app/Models/*` matches every entry directly under app/Models. Note
	// the gitignore semantic: a single `*` matches the `sub` segment, and
	// because gitignore then treats the matched directory as excluded,
	// files beneath it (app/Models/sub/Nested.go) are excluded too. So
	// `app/Models/*` keeps the whole app/Models subtree but nothing
	// outside it. (Use `app/Models` literal-dir for the same effect, or
	// `app/Models/*.go` to constrain by extension.)
	d := Diff{Files: []FileDiff{
		{Path: "app/Models/User.go"},
		{Path: "app/Models/sub/Nested.go"},
		{Path: "app/Http/Controller.go"},
	}}
	got := mustKeep(t, d, nil, []string{"app/Models/*"})
	want := []string{"app/Models/User.go", "app/Models/sub/Nested.go"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("app/Models/* = %v, want %v (whole subtree, app/Http excluded)", paths(got), want)
	}
}

func TestKeepPaths_GlobCharClass(t *testing.T) {
	// A character class `[abc]` matches one of the listed runes.
	d := Diff{Files: []FileDiff{
		{Path: "a.go"},
		{Path: "b.go"},
		{Path: "d.go"},
		{Path: "ab.go"},
	}}
	got := mustKeep(t, d, []string{"[abc]*.go"}, nil)
	// a.go, b.go, ab.go all start with one of [abc]; d.go does not.
	want := []string{"a.go", "b.go", "ab.go"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("[abc]*.go = %v, want %v", paths(got), want)
	}
}

func TestKeepPaths_LiteralAndGlobUnion(t *testing.T) {
	// Union across a literal file, a literal dir, and a glob: a file is
	// kept if it matches ANY bucket.
	d := Diff{Files: []FileDiff{
		{Path: "routes/web.php"},      // literal file
		{Path: "app/Models/User.go"},  // literal dir
		{Path: "app/Models/sub/X.go"}, // literal dir (prefix)
		{Path: "cmd/main.go"},         // glob (*.go) — but also literal-dir miss
		{Path: "docs/readme.md"},      // matches nothing
	}}
	got := mustKeep(t, d,
		[]string{"routes/web.php", "*.go"},
		[]string{"app/Models"},
	)
	want := []string{"routes/web.php", "app/Models/User.go", "app/Models/sub/X.go", "cmd/main.go"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("literal ∪ dir ∪ glob = %v, want %v", paths(got), want)
	}
}

func TestKeepPaths_GlobMatchesOldPathOnRename(t *testing.T) {
	// A glob should match against the pre-rename OldPath too, mirroring
	// the literal-allowlist behavior — review "the file that used to be
	// legacy/*.go" even after it moved.
	d := Diff{Files: []FileDiff{
		{Path: "current/renamed.txt", OldPath: "legacy/old.go"},
	}}
	if got := mustKeep(t, d, []string{"legacy/*.go"}, nil); len(got.Files) != 1 {
		t.Errorf("glob on OldPath should match renamed file; got %d files", len(got.Files))
	}
	// And the new path must not match the same glob (control).
	d2 := Diff{Files: []FileDiff{
		{Path: "current/renamed.txt"},
	}}
	if got := mustKeep(t, d2, []string{"legacy/*.go"}, nil); len(got.Files) != 0 {
		t.Errorf("glob should not match unrelated new path; got %d files", len(got.Files))
	}
}

func TestKeepPaths_GlobWindowsInputNormalized(t *testing.T) {
	// A user on Windows may pass a backslash-separated glob. After
	// filepath.ToSlash it must match the slash-normalized diff path.
	d := Diff{Files: []FileDiff{
		{Path: "app/Models/User.go"},
		{Path: "app/Http/X.go"},
	}}
	got := mustKeep(t, d, nil, []string{`app\Models\*`})
	want := []string{"app/Models/User.go"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf(`app\Models\* (Windows input) = %v, want %v`, paths(got), want)
	}
}

func TestKeepPaths_GlobUsesPathPartsCacheWhenPresent(t *testing.T) {
	// When PathParts is populated (the production parse-time path), the
	// glob matcher must use it and still match correctly.
	d := Diff{Files: []FileDiff{
		{Path: "internal/cli/root.go", PathParts: []string{"internal", "cli", "root.go"}},
	}}
	if got := mustKeep(t, d, []string{"*.go"}, nil); len(got.Files) != 1 {
		t.Errorf("glob with PathParts cache should match; got %d files", len(got.Files))
	}
}

func TestKeepPaths_InvalidGlobReturnsError(t *testing.T) {
	// An unterminated character class is rejected with an error rather
	// than silently mis-filtering (e.g. dropping every file).
	d := sample()
	_, err := KeepPaths(d, []string{"[abc.go"}, nil)
	if err == nil {
		t.Fatalf("invalid glob '[abc.go' should return an error")
	}
}
