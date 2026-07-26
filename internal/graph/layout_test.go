// SPDX-License-Identifier: GPL-3.0-or-later

package graph

import (
	"strings"
	"testing"
)

// c builds a commit from a hash and its parents, keeping the DAG literals in
// these tests readable.
func c(hash string, parents ...string) Commit {
	return Commit{Hash: hash, Parents: parents}
}

// render draws the lane columns as text so a test failure shows the shape that
// was produced, not a slice of integers. It mirrors what the real renderer does
// but stays deliberately dumb — this package is about lane assignment, and the
// glyph-to-rune mapping lives with the renderer.
func render(rows []Row) string {
	var sb strings.Builder
	for _, r := range rows {
		for _, g := range r.Cells {
			switch g {
			case GlyphCommit:
				sb.WriteByte('*')
			case GlyphVertical:
				sb.WriteByte('|')
			case GlyphFork:
				sb.WriteByte('\\')
			case GlyphMerge:
				sb.WriteByte('/')
			default:
				sb.WriteByte(' ')
			}
		}
		sb.WriteByte(' ')
		sb.WriteString(r.Commit.Hash)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func lanes(rows []Row) []int {
	out := make([]int, len(rows))
	for i, r := range rows {
		out[i] = r.Lane
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLayoutEmpty(t *testing.T) {
	if got := Layout(nil, nil); got != nil {
		t.Fatalf("empty input should yield nil, got %#v", got)
	}
	if got := Layout([]Commit{}, nil); got != nil {
		t.Fatalf("empty slice should yield nil, got %#v", got)
	}
}

func TestLayoutLinearHistoryStaysInOneLane(t *testing.T) {
	rows := Layout([]Commit{
		c("a", "b"),
		c("b", "c"),
		c("c"),
	}, nil)

	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if !equalInts(lanes(rows), []int{0, 0, 0}) {
		t.Errorf("linear history must not widen; lanes = %v\n%s", lanes(rows), render(rows))
	}
	for i, r := range rows {
		if r.Width() != 1 {
			t.Errorf("row %d width = %d, want 1\n%s", i, r.Width(), render(rows))
		}
		if r.Cells[0] != GlyphCommit {
			t.Errorf("row %d should mark its own commit\n%s", i, render(rows))
		}
	}
}

func TestLayoutRootCommitClosesItsLane(t *testing.T) {
	// The last commit has no parents; nothing should still be pending.
	rows := Layout([]Commit{c("a", "b"), c("b")}, nil)
	last := rows[len(rows)-1]
	if last.Cells[0] != GlyphCommit {
		t.Fatalf("root commit should occupy its lane\n%s", render(rows))
	}
}

func TestLayoutForkAndMerge(t *testing.T) {
	// m is a merge of a (first parent) and b (second parent); both descend
	// from r.
	//
	//   m
	//   |\
	//   a b
	//   |/
	//   r
	rows := Layout([]Commit{
		c("m", "a", "b"),
		c("a", "r"),
		c("b", "r"),
		c("r"),
	}, nil)

	if !equalInts(lanes(rows), []int{0, 0, 1, 0}) {
		t.Fatalf("unexpected lane assignment %v\n%s", lanes(rows), render(rows))
	}
	// The merge row must open a second column for the second parent.
	if rows[0].Width() < 2 || rows[0].Cells[1] != GlyphFork {
		t.Errorf("merge commit should fork a lane for its second parent\n%s", render(rows))
	}
	// Both sides converge on r, so the second lane folds back in.
	if rows[3].Cells[1] != GlyphMerge {
		t.Errorf("converging lane should be marked as merging\n%s", render(rows))
	}
}

func TestLayoutReleasedLaneIsReused(t *testing.T) {
	// Two independent branch tips, the first of which terminates before the
	// second one appears. The freed column must be reused rather than the
	// graph drifting rightwards.
	rows := Layout([]Commit{
		c("a"),      // tip 1, root — opens and immediately closes lane 0
		c("b", "c"), // tip 2 — should reuse lane 0
		c("c"),
	}, nil)

	if !equalInts(lanes(rows), []int{0, 0, 0}) {
		t.Fatalf("freed lane should be reused; lanes = %v\n%s", lanes(rows), render(rows))
	}
	for _, r := range rows {
		if r.Width() != 1 {
			t.Fatalf("graph should stay one column wide\n%s", render(rows))
		}
	}
}

func TestLayoutOctopusMergeOpensALanePerExtraParent(t *testing.T) {
	rows := Layout([]Commit{
		c("m", "a", "b", "d"),
		c("a", "r"),
		c("b", "r"),
		c("d", "r"),
		c("r"),
	}, nil)

	if rows[0].Width() != 3 {
		t.Fatalf("a 3-parent merge needs 3 lanes, got %d\n%s", rows[0].Width(), render(rows))
	}
	if rows[0].Cells[1] != GlyphFork || rows[0].Cells[2] != GlyphFork {
		t.Errorf("both extra parents should fork\n%s", render(rows))
	}
	if !equalInts(lanes(rows), []int{0, 0, 1, 2, 0}) {
		t.Errorf("unexpected octopus lanes %v\n%s", lanes(rows), render(rows))
	}
}

func TestLayoutParentOutsideTheWalkClosesTheLane(t *testing.T) {
	// A truncated walk: `a`'s parent was never handed to Layout. The lane must
	// close at the boundary instead of staying open for a commit that will
	// never arrive (which would leave a dangling column down the whole graph).
	rows := Layout([]Commit{c("a", "missing")}, nil)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Width() != 1 || rows[0].Cells[0] != GlyphCommit {
		t.Fatalf("boundary commit should occupy exactly its own lane\n%s", render(rows))
	}
}

func TestLayoutSharedSecondParentDoesNotOpenADuplicateLane(t *testing.T) {
	// Both parents of m are already-pending or identical targets; the graph
	// must not allocate a column for a hash it is already waiting on.
	rows := Layout([]Commit{
		c("m", "a", "a"),
		c("a"),
	}, nil)

	if rows[0].Width() != 1 {
		t.Fatalf("duplicate parent should not widen the graph\n%s", render(rows))
	}
}

func TestLayoutMatchedMarking(t *testing.T) {
	commits := []Commit{c("a", "b"), c("b", "c"), c("c")}

	// A nil map means no filter is active — everything is "matched" so an
	// unfiltered graph is not drawn entirely as context.
	for i, r := range Layout(commits, nil) {
		if !r.Matched {
			t.Errorf("row %d: nil matched map should mark every commit", i)
		}
	}

	rows := Layout(commits, map[string]bool{"b": true})
	want := []bool{false, true, false}
	for i, r := range rows {
		if r.Matched != want[i] {
			t.Errorf("row %d (%s): Matched = %v, want %v", i, r.Commit.Hash, r.Matched, want[i])
		}
	}
}

func TestLayoutRowsAreUniformWidth(t *testing.T) {
	// The renderer indexes columns without bounds checks, so every row must be
	// padded to the widest.
	rows := Layout([]Commit{
		c("m", "a", "b"),
		c("a", "r"),
		c("b", "r"),
		c("r"),
	}, nil)

	width := rows[0].Width()
	for i, r := range rows {
		if r.Width() != width {
			t.Fatalf("row %d width = %d, want %d (all rows padded)\n%s",
				i, r.Width(), width, render(rows))
		}
	}
}

func TestLayoutExactlyOneCommitGlyphPerRow(t *testing.T) {
	rows := Layout([]Commit{
		c("m", "a", "b"),
		c("a", "r"),
		c("b", "r"),
		c("r"),
	}, nil)

	for i, r := range rows {
		n := 0
		for _, g := range r.Cells {
			if g == GlyphCommit {
				n++
			}
		}
		if n != 1 {
			t.Errorf("row %d has %d commit glyphs, want exactly 1\n%s", i, n, render(rows))
		}
		if r.Cells[r.Lane] != GlyphCommit {
			t.Errorf("row %d: Lane %d does not hold the commit glyph\n%s", i, r.Lane, render(rows))
		}
	}
}
