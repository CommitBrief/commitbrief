// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/graph"
)

func graphFixture(matched map[string]bool) []GraphRow {
	commits := []git.CommitMeta{
		{Hash: "aaa", Short: "aaa1111", Subject: "feat: one", Author: "Alice"},
		{Hash: "bbb", Short: "bbb2222", Subject: "fix: two", Author: "Bob"},
	}
	nodes := []graph.Commit{
		{Hash: "aaa", Parents: []string{"bbb"}},
		{Hash: "bbb"},
	}
	laid := graph.Layout(nodes, matched)
	rows := make([]GraphRow, len(laid))
	for i := range laid {
		rows[i] = GraphRow{Row: laid[i], Commit: commits[i]}
	}
	return rows
}

func TestGraphEmptyWritesNothing(t *testing.T) {
	var w bytes.Buffer
	if err := Graph(&w, nil, GraphOptions{}); err != nil {
		t.Fatal(err)
	}
	if w.Len() != 0 {
		t.Fatalf("empty graph should write nothing, got %q", w.String())
	}
}

func TestGraphASCIIByDefault(t *testing.T) {
	// The zero GraphOptions is the pipe/test-buffer case: no ANSI, no
	// box-drawing. A consumer redirecting to a file must get plain text.
	var w bytes.Buffer
	if err := Graph(&w, graphFixture(nil), GraphOptions{}); err != nil {
		t.Fatal(err)
	}
	out := w.String()
	if strings.ContainsAny(out, "●○│╲╱") {
		t.Errorf("unicode glyphs leaked into the ASCII fallback:\n%s", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ANSI escapes leaked with Color=false:\n%s", out)
	}
	if !strings.Contains(out, "* aaa1111") {
		t.Errorf("expected an ASCII commit marker and short hash:\n%s", out)
	}
}

func TestGraphUnicodeGlyphs(t *testing.T) {
	var w bytes.Buffer
	if err := Graph(&w, graphFixture(nil), GraphOptions{Unicode: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "●") {
		t.Errorf("expected the unicode commit glyph:\n%s", w.String())
	}
}

func TestGraphMarksMatchedAndContextDifferently(t *testing.T) {
	var w bytes.Buffer
	opts := GraphOptions{Unicode: true, Filtered: true}
	if err := Graph(&w, graphFixture(map[string]bool{"aaa": true}), opts); err != nil {
		t.Fatal(err)
	}
	out := w.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(lines[0], "●") {
		t.Errorf("matched commit should use the filled glyph; got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "○") {
		t.Errorf("context commit should use the hollow glyph; got %q", lines[1])
	}
	// The distinction is meaningless without a key, so a filtered render
	// always explains itself.
	if !strings.Contains(out, "matches the filter") {
		t.Errorf("a filtered graph must print its legend:\n%s", out)
	}
}

func TestGraphOmitsLegendWhenUnfiltered(t *testing.T) {
	var w bytes.Buffer
	if err := Graph(&w, graphFixture(nil), GraphOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.String(), "matches the filter") {
		t.Errorf("an unfiltered graph has nothing to explain:\n%s", w.String())
	}
}

func TestGraphClipsToWidth(t *testing.T) {
	// A wrapped row would have no lane gutter on its continuation line, which
	// visually detaches it from the graph — so rows clip instead.
	var w bytes.Buffer
	if err := Graph(&w, graphFixture(nil), GraphOptions{Width: 20}); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(w.String()), "\n") {
		if len([]rune(line)) > 20 {
			t.Errorf("line exceeds the clip width: %q (%d runes)", line, len([]rune(line)))
		}
	}
}

func TestGraphZeroWidthMeansNoClipping(t *testing.T) {
	// 0 is TerminalWidth's "unknown" signal and must not be read as "clip
	// everything away".
	var w bytes.Buffer
	if err := Graph(&w, graphFixture(nil), GraphOptions{Width: 0}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "feat: one") {
		t.Errorf("width 0 must leave the row intact:\n%s", w.String())
	}
}

func TestGraphHandlesMissingSubject(t *testing.T) {
	rows := graphFixture(nil)
	rows[0].Commit.Subject = ""
	var w bytes.Buffer
	if err := Graph(&w, rows, GraphOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "(no subject)") {
		t.Errorf("an empty subject should render a placeholder, not a blank column:\n%s", w.String())
	}
}

func TestGraphRendersRefLabels(t *testing.T) {
	rows := graphFixture(nil)
	rows[0].Refs = []string{"main", "v1.2.0"}
	var w bytes.Buffer
	if err := Graph(&w, rows, GraphOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "(main, v1.2.0)") {
		t.Errorf("expected ref labels:\n%s", w.String())
	}
}

func TestBranchTopologyRendersCounts(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	branches := []git.Branch{
		{Name: "main", IsBase: true, Author: "Alice", Date: now.Add(-2 * time.Hour)},
		{Name: "feature/x", Ahead: 3, Behind: 12, Author: "Bob", Date: now.Add(-72 * time.Hour)},
	}
	var w bytes.Buffer
	if err := BranchTopology(&w, branches, GraphOptions{Now: now}); err != nil {
		t.Fatal(err)
	}
	out := w.String()
	if !strings.Contains(out, "base") {
		t.Errorf("the base branch should be labelled as such:\n%s", out)
	}
	if !strings.Contains(out, "+3") || !strings.Contains(out, "-12") {
		t.Errorf("expected ahead/behind counts:\n%s", out)
	}
	if !strings.Contains(out, "2h") || !strings.Contains(out, "3d") {
		t.Errorf("expected relative ages:\n%s", out)
	}
}

func TestBranchTopologyEmptyWritesNothing(t *testing.T) {
	var w bytes.Buffer
	if err := BranchTopology(&w, nil, GraphOptions{}); err != nil {
		t.Fatal(err)
	}
	if w.Len() != 0 {
		t.Fatalf("no branches should write nothing, got %q", w.String())
	}
}

func TestRelativeAge(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		ago  time.Duration
		want string
	}{
		{"seconds", 30 * time.Second, "now"},
		{"minutes", 42 * time.Minute, "42m"},
		{"hours", 5 * time.Hour, "5h"},
		{"days", 3 * 24 * time.Hour, "3d"},
		{"weeks", 3 * 7 * 24 * time.Hour, "3w"},
		{"years", 2 * 365 * 24 * time.Hour, "2y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RelativeAge(now.Add(-tc.ago), now); got != tc.want {
				t.Fatalf("RelativeAge(-%v) = %q, want %q", tc.ago, got, tc.want)
			}
		})
	}
}

func TestRelativeAgeFutureDateDoesNotGoNegative(t *testing.T) {
	// A skewed clock or rewritten history can date a commit in the future;
	// "-3h" would read as nonsense.
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	if got := RelativeAge(now.Add(3*time.Hour), now); got != "now" {
		t.Fatalf("future date rendered as %q, want \"now\"", got)
	}
}
