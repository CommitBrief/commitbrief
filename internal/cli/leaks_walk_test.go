// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/guard"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/leaks"
	"github.com/CommitBrief/commitbrief/internal/render"
)

// The commit walk's own ceiling is invisible to the user unless the report
// says so: "no credentials found" over a repo whose older history was never
// scanned is misleading. These tests drive the report writers directly because
// the 10000-commit ceiling cannot be reached from a CLI fixture.

func leaksWalkApp(t *testing.T, code string) *appContext {
	t.Helper()
	cat, err := i18n.Load(code)
	if err != nil {
		t.Fatal(err)
	}
	return &appContext{Catalog: cat}
}

func TestLeaksWalkTruncatedNoticeInHumanReport(t *testing.T) {
	global = globalFlags{color: "never"}
	for _, code := range []string{"en", "tr"} {
		app := leaksWalkApp(t, code)
		if !app.Catalog.Has("leaks.walk_truncated") {
			t.Fatalf("%s catalog lacks leaks.walk_truncated", code)
		}
		want := strconv.Itoa(git.WalkLimit())

		var buf bytes.Buffer
		if err := writeLeaksReport(&buf, app, leaks.Result{WalkTruncated: true}, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "⚠") || !strings.Contains(buf.String(), want) {
			t.Errorf("%s: expected the walk-truncation notice naming %s commits, got:\n%s", code, want, buf.String())
		}

		buf.Reset()
		if err := writeLeaksReport(&buf, app, leaks.Result{}, nil); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), want) {
			t.Errorf("%s: a complete walk must not print the notice, got:\n%s", code, buf.String())
		}
	}
}

func TestLeaksTruncationNoticesShownUnderQuiet(t *testing.T) {
	global = globalFlags{color: "never", quiet: true}
	t.Cleanup(func() { global = globalFlags{color: "never"} })

	app := leaksWalkApp(t, "en")
	for name, res := range map[string]leaks.Result{
		"walk":        {WalkTruncated: true},
		"max-commits": {Truncated: true},
	} {
		var buf bytes.Buffer
		if err := writeLeaksReport(&buf, app, res, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "⚠") {
			t.Errorf("%s: --quiet must not hide truncation, got:\n%s", name, buf.String())
		}
		if strings.Contains(buf.String(), "Scanned") {
			t.Errorf("%s: --quiet must still silence the coverage line, got:\n%s", name, buf.String())
		}
	}
}

func leaksWalkJSONMeta(t *testing.T, res leaks.Result) map[string]any {
	t.Helper()
	global = globalFlags{color: "never"}
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	app := leaksWalkApp(t, "en")
	if err := emitLeaksJSON(cmd, app, []render.Finding{}, res); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema int            `json:"schema"`
		Meta   map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, buf.String())
	}
	if doc.Schema != 1 {
		t.Errorf("schema = %d, want 1", doc.Schema)
	}
	return doc.Meta
}

func TestLeaksWalkTruncatedJSONMeta(t *testing.T) {
	meta := leaksWalkJSONMeta(t, leaks.Result{WalkTruncated: true})
	if meta["walk_truncated"] != true {
		t.Errorf("meta.walk_truncated = %v, want true", meta["walk_truncated"])
	}

	// Additive-optional: a complete walk keeps the v1 meta shape.
	meta = leaksWalkJSONMeta(t, leaks.Result{})
	if _, present := meta["walk_truncated"]; present {
		t.Errorf("meta.walk_truncated must be omitted when false, got %v", meta["walk_truncated"])
	}
}

// meta.truncated reports the --max-commits window, a different thing from the
// walk ceiling behind meta.walk_truncated: each flag moves independently.
func TestLeaksTruncatedJSONMeta(t *testing.T) {
	meta := leaksWalkJSONMeta(t, leaks.Result{Truncated: true})
	if meta["truncated"] != true {
		t.Errorf("meta.truncated = %v, want true", meta["truncated"])
	}
	if _, present := meta["walk_truncated"]; present {
		t.Errorf("meta.walk_truncated must stay absent when only the window cut the scan, got %v", meta["walk_truncated"])
	}

	meta = leaksWalkJSONMeta(t, leaks.Result{WalkTruncated: true})
	if _, present := meta["truncated"]; present {
		t.Errorf("meta.truncated must stay absent when only the walk ceiling was hit, got %v", meta["truncated"])
	}

	meta = leaksWalkJSONMeta(t, leaks.Result{Truncated: true, WalkTruncated: true})
	if meta["truncated"] != true || meta["walk_truncated"] != true {
		t.Errorf("both flags must be set together, got truncated=%v walk_truncated=%v",
			meta["truncated"], meta["walk_truncated"])
	}

	// Additive-optional: a complete scan keeps the v1 meta shape.
	meta = leaksWalkJSONMeta(t, leaks.Result{})
	if _, present := meta["truncated"]; present {
		t.Errorf("meta.truncated must be omitted when false, got %v", meta["truncated"])
	}
}

// End to end: --max-commits smaller than the history marks the --json meta.
func TestLeaksJSONReportsMaxCommitsTruncation(t *testing.T) {
	e := newCLIEnv(t)
	for i := 0; i < 3; i++ {
		writeFile(t, filepath.Join(e.repoRoot, "extra.txt"), strings.Repeat("x\n", i+1))
		gitCmd(t, e.repoRoot, "add", "extra.txt")
		gitCmd(t, e.repoRoot, "commit", "-q", "-m", "more")
	}
	if err := e.run("leaks", "--json", "--max-commits", "2"); err != nil {
		t.Fatalf("leaks: %v", err)
	}
	type metaDoc struct {
		Meta map[string]any `json:"meta"`
	}
	var doc metaDoc
	if err := json.Unmarshal(e.out.Bytes(), &doc); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, e.out.String())
	}
	if doc.Meta["truncated"] != true {
		t.Errorf("meta.truncated = %v, want true\n%s", doc.Meta["truncated"], e.out.String())
	}
	if _, present := doc.Meta["walk_truncated"]; present {
		t.Errorf("meta.walk_truncated must be absent, got %v", doc.Meta["walk_truncated"])
	}

	e2 := newCLIEnv(t)
	if err := e2.run("leaks", "--json", "--max-commits", "50"); err != nil {
		t.Fatalf("leaks: %v", err)
	}
	var doc2 metaDoc
	if err := json.Unmarshal(e2.out.Bytes(), &doc2); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, e2.out.String())
	}
	if _, present := doc2.Meta["truncated"]; present {
		t.Errorf("a window that fits the history must not set meta.truncated, got %v", doc2.Meta["truncated"])
	}
}

// A scan failure that is not a bad user regex must not be labeled one. With
// git off PATH the worktree scan fails with git.ErrNoGitCLI, which has its own
// localized message.
func TestLeaksGitMissingIsNotReportedAsInvalidPattern(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("PATH", t.TempDir()) // an empty dir: no git, and the real one stays untouched

	err := e.run("leaks")
	if err == nil {
		t.Fatal("leaks without a git binary must fail")
	}
	cat := leaksWalkApp(t, "en").Catalog
	if !strings.Contains(err.Error(), cat.T("common.error.git_missing")) {
		t.Errorf("expected the git-missing message, got: %v", err)
	}
	if strings.Contains(err.Error(), "secret_patterns") || strings.Contains(err.Error(), "secret pattern") {
		t.Errorf("a missing git must not be blamed on secret patterns, got: %v", err)
	}
}

func TestLeaksErrorClassification(t *testing.T) {
	app := leaksWalkApp(t, "en")
	bad := []guard.UserSecretPattern{{Name: "broken", Regex: "("}}
	good := []guard.UserSecretPattern{{Name: "fine", Regex: "fine-[0-9]+"}}

	t.Run("invalid regex keeps the pattern message", func(t *testing.T) {
		_, cErr := guard.CompileUserPatterns(bad)
		if cErr == nil {
			t.Fatal("fixture regex should not compile")
		}
		got := leaksError(app, bad, cErr)
		want := app.Catalog.T("guard.secret_patterns.invalid", cErr.Error())
		if got.Error() != want {
			t.Errorf("got %q, want %q", got.Error(), want)
		}
	})
	t.Run("git sentinels are localized", func(t *testing.T) {
		for _, tc := range []struct {
			err error
			key string
		}{
			{fmt.Errorf("leaks: %w", git.ErrNoGitCLI), "common.error.git_missing"},
			{git.ErrNotARepo, "common.error.not_a_repo"},
		} {
			got := leaksError(app, good, tc.err)
			if got.Error() != app.Catalog.T(tc.key) {
				t.Errorf("got %q, want %q", got.Error(), app.Catalog.T(tc.key))
			}
		}
	})
	t.Run("other failures pass through unlabeled", func(t *testing.T) {
		cause := errors.New("git ls-files: boom")
		got := leaksError(app, good, cause)
		if !errors.Is(got, cause) || strings.Contains(got.Error(), "pattern") {
			t.Errorf("got %q, want the original error untouched", got)
		}
	})
}
