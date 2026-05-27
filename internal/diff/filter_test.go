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
	if got := KeepPaths(d, nil, nil); !reflect.DeepEqual(got, d) {
		t.Errorf("KeepPaths with no filters should be identity; got %v", got)
	}
	if got := KeepPaths(d, []string{}, []string{}); !reflect.DeepEqual(got, d) {
		t.Errorf("KeepPaths with empty slices should be identity")
	}
}

func TestKeepPaths_FileAllowlist(t *testing.T) {
	d := sample()
	got := KeepPaths(d, []string{"routes/web.php", "app/Models/User.go"}, nil)
	want := []string{"app/Models/User.go", "routes/web.php"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("files allowlist = %v, want %v", paths(got), want)
	}
}

func TestKeepPaths_DirAllowlistPrefixMatch(t *testing.T) {
	d := sample()
	got := KeepPaths(d, nil, []string{"database/seeder"})
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
	got := KeepPaths(d, nil, []string{"database/seeder"})
	want := []string{"database/seeder/file.php"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("dir prefix match = %v, want %v (no substring leakage)",
			paths(got), want)
	}
}

func TestKeepPaths_FileAndDirUnion(t *testing.T) {
	d := sample()
	got := KeepPaths(d, []string{"routes/web.php"}, []string{"app/Models"})
	want := []string{"app/Models/User.go", "routes/web.php"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("union of file+dir = %v, want %v", paths(got), want)
	}
}

func TestKeepPaths_TrailingSlashOnDirNormalized(t *testing.T) {
	d := sample()
	a := KeepPaths(d, nil, []string{"database/seeder"})
	b := KeepPaths(d, nil, []string{"database/seeder/"})
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
	if got := KeepPaths(d, []string{"old.go"}, nil); len(got.Files) != 1 {
		t.Errorf("file allowlist on OldPath should match; got %d files", len(got.Files))
	}
	if got := KeepPaths(d, []string{"renamed.go"}, nil); len(got.Files) != 1 {
		t.Errorf("file allowlist on Path should match; got %d files", len(got.Files))
	}
}

func TestKeepPaths_PreservesOriginAndArgs(t *testing.T) {
	// Origin/Args metadata is preserved across the filter so downstream
	// consumers (cache key, dry-run formatter, render meta) keep the
	// original scope context.
	d := sample()
	d.Origin = git.OriginStaged
	d.Args = map[string]string{"scope": "staged"}
	got := KeepPaths(d, []string{"routes/web.php"}, nil)
	if got.Origin != git.OriginStaged || !reflect.DeepEqual(got.Args, map[string]string{"scope": "staged"}) {
		t.Errorf("metadata not preserved: Origin=%v Args=%v", got.Origin, got.Args)
	}
}

func TestKeepPaths_EmptyDirEntryIgnored(t *testing.T) {
	// Empty-string dir entries (e.g. someone passed --dir ""  by mistake)
	// shouldn't match every file in the repo. Treat them as no-op.
	d := sample()
	got := KeepPaths(d, nil, []string{"", "  "})
	if len(got.Files) != 0 {
		t.Errorf("empty/whitespace dir should not match anything; got %d files", len(got.Files))
	}
}
