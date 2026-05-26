package cli

import (
	"bytes"
	"sort"
	"strings"
	"testing"
)

func TestSplitThreeDot(t *testing.T) {
	cases := map[string]struct {
		target, feature string
		ok              bool
	}{
		"main...feature":     {"main", "feature", true},
		"origin/main...HEAD": {"origin/main", "HEAD", true},
		"missing-separator":  {"", "", false},
		"":                   {"", "", false},
		"a..b":               {"", "", false},
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			gotT, gotF, gotOK := splitThreeDot(input)
			if gotOK != want.ok || gotT != want.target || gotF != want.feature {
				t.Errorf("splitThreeDot(%q) = (%q, %q, %v), want (%q, %q, %v)",
					input, gotT, gotF, gotOK, want.target, want.feature, want.ok)
			}
		})
	}
}

func TestRootCommandHasSubcommands(t *testing.T) {
	root := newRootCmd()
	want := []string{"compress", "dry-run", "init", "list", "setup"}
	got := []string{}
	for _, c := range root.Commands() {
		// cobra adds `help` and `completion` automatically; filter to ours.
		if c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		got = append(got, c.Name())
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("subcommands = %v, want %v", got, want)
	}
}

func TestRootCommandHelpRuns(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help should not error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"commitbrief", "Available Commands:", "--staged", "--json"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q\n%s", want, out)
		}
	}
}

func TestBuildMatcherWithoutRepoRoot(t *testing.T) {
	m := buildMatcher("")
	if !m.Match("go.sum") {
		t.Error("builtin matcher should still catch go.sum when no repoRoot is provided")
	}
}

func TestBuildMatcherHonorsRepoIgnore(t *testing.T) {
	dir := t.TempDir()
	// Empty repo dir + no .commitbriefignore → behaves like Builtin
	m := buildMatcher(dir)
	if !m.Match("vendor/foo.go") {
		t.Error("builtin pattern lost when repoIgnore missing")
	}
}

func TestListCommandExists(t *testing.T) {
	if newListCmd() == nil {
		t.Fatal("newListCmd returned nil")
	}
}

func TestInitCommandExists(t *testing.T) {
	if newInitCmd() == nil {
		t.Fatal("newInitCmd returned nil")
	}
}
