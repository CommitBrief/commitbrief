// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/git"
)

// The mock provider's canned FreeForm reply (see provider/mock) — what a
// summary emits when the active provider is the mock.
const mockFreeForm = "feat(store): add user lookup by name"

func TestSummaryStagedEmitsPlainText(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("summary"); err != nil {
		t.Fatalf("summary: %v", err)
	}
	if out := e.out.String(); !strings.Contains(out, mockFreeForm) {
		t.Errorf("summary output missing the provider digest; got:\n%s", out)
	}
}

func TestSummaryRangeUsesCommits(t *testing.T) {
	e := newCLIEnv(t)
	// Promote the staged fixture change to a real commit so HEAD~1..HEAD is a
	// non-empty range with a commit manifest to ingest.
	gitOut(t, e.repoRoot, "commit", "-q", "-m", "feat: validate empty user")

	if err := e.run("summary", "HEAD~1", "HEAD"); err != nil {
		t.Fatalf("summary range: %v", err)
	}
	if out := e.out.String(); !strings.Contains(out, mockFreeForm) {
		t.Errorf("summary range output missing the provider digest; got:\n%s", out)
	}
}

func TestSummaryNoChanges(t *testing.T) {
	e := newCLIEnv(t)
	gitOut(t, e.repoRoot, "reset", "-q") // unstage → nothing staged to summarize

	if err := e.run("summary"); err != nil {
		t.Fatalf("summary with no staged changes should be a clean no-op, got: %v", err)
	}
	if out := e.out.String(); strings.Contains(out, mockFreeForm) {
		t.Errorf("no changes should not emit a digest; got:\n%s", out)
	}
}

func TestSummaryRejectsJSON(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("summary", "--json"); err == nil {
		t.Fatal("summary with --json must error")
	}
}

func TestSummaryRejectsMarkdown(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("summary", "--markdown"); err == nil {
		t.Fatal("summary with --markdown must error")
	}
}

func TestSummaryRejectsReviewFlags(t *testing.T) {
	for _, flag := range [][]string{
		{"--fail-on", "high"},
		{"--min-severity", "high"},
		{"--suggest-commit"},
	} {
		e := newCLIEnv(t)
		args := append([]string{"summary"}, flag...)
		if err := e.run(args...); err == nil {
			t.Errorf("summary %v must error (no findings to act on)", flag)
		}
	}
}

// --with-context only works with a CLI-backed provider; the mock is an
// API-shaped provider, so summary must reject it before any call.
func TestSummaryWithContextRequiresCLI(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("summary", "--with-context")
	if err == nil {
		t.Fatal("summary --with-context on a non-CLI provider must error")
	}
	if !strings.Contains(err.Error(), "--with-context") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDeriveLogRange(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
		ok   bool
	}{
		{"three-dot", []string{"main...develop"}, []string{"main..develop"}, true},
		{"two-dot", []string{"main..develop"}, []string{"main..develop"}, true},
		{"two refs", []string{"HEAD~3", "HEAD"}, []string{"HEAD~3..HEAD"}, true},
		{"single ref", []string{"HEAD"}, nil, false},
		{"single hash", []string{"a1b2c3d"}, nil, false},
		{"empty", nil, nil, false},
		{"flag present", []string{"--stat", "main..develop"}, nil, false},
		{"pathspec sep", []string{"main", "--", "x.go"}, nil, false},
		{"three args", []string{"a", "b", "c"}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := deriveLogRange(tc.args)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("range = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestFormatManifest(t *testing.T) {
	if got := formatManifest(nil); got != "" {
		t.Fatalf("empty manifest should be the empty string, got %q", got)
	}
	got := formatManifest([]git.CommitMeta{
		{Short: "a1b2c3d", Subject: "fix: rounding", Body: "why", Files: []string{"x.go"}},
	})
	for _, want := range []string{"a1b2c3d", "fix: rounding", "why", "files: x.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest missing %q\n%s", want, got)
		}
	}
}

func TestFormatManifestFilesTruncates(t *testing.T) {
	files := make([]string, maxManifestFilesPerCommit+5)
	for i := range files {
		files[i] = fmt.Sprintf("file%d.go", i)
	}
	got := formatManifestFiles(files)
	if !strings.Contains(got, "+5 more") {
		t.Errorf("expected a '+5 more' tail, got: %s", got)
	}
	if strings.Contains(got, fmt.Sprintf("file%d.go", maxManifestFilesPerCommit)) {
		t.Errorf("file past the cap should be elided, got: %s", got)
	}
}
