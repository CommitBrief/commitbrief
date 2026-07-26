// SPDX-License-Identifier: GPL-3.0-or-later

// Package graph lays a commit list out onto terminal columns ("lanes") so a
// renderer can draw the DAG the way `git log --graph` does (ADR-0037).
//
// It is deliberately git-free and render-free: the input is an ordered commit
// list with parent hashes, the output is one Row per commit carrying the glyph
// for every lane column. That keeps the tricky part — lane bookkeeping — a pure
// function that can be tested against hand-written DAGs, and leaves colour,
// unicode-vs-ASCII, and width clipping entirely to the caller.
package graph

// Commit is the minimal shape Layout needs. It mirrors the fields of
// git.CommitMeta that matter for topology, so callers can adapt without this
// package importing internal/git (keeping it a leaf, like internal/tokens).
type Commit struct {
	Hash    string
	Parents []string
}

// Glyph is what occupies one lane column on one row.
type Glyph uint8

const (
	// GlyphEmpty is an unused column.
	GlyphEmpty Glyph = iota
	// GlyphCommit is the commit's own marker; exactly one per row.
	GlyphCommit
	// GlyphVertical is a lane passing straight through this row.
	GlyphVertical
	// GlyphFork is a lane branching out to the right (a second parent leaving
	// the commit's lane).
	GlyphFork
	// GlyphMerge is a lane folding back in to the left (a lane whose commit
	// has been reached and that now rejoins).
	GlyphMerge
)

// Row is one rendered line: the commit, its lane index, and the glyph for each
// lane column. Cells is exactly Width() wide.
type Row struct {
	Commit  Commit
	Lane    int
	Matched bool
	Cells   []Glyph
}

// Width returns how many lane columns this row occupies.
func (r Row) Width() int { return len(r.Cells) }

// Layout assigns each commit a lane and produces one Row per commit, in input
// order (which the caller is expected to have sorted newest-first, as git log
// does).
//
// The algorithm is the standard one: `lanes` holds, per column, the hash that
// column is currently waiting to draw. When a commit is reached, it takes the
// leftmost lane already waiting for it; its first parent inherits that lane and
// every additional parent claims a new one (a fork). Any *other* lane also
// waiting for this commit is released — that is a merge point, where two lines
// of development converge.
//
// matched marks which commits satisfied the caller's filter; a nil map means
// "everything matches", which is what an unfiltered run wants.
//
// Commits whose parents are outside the input set (a truncated walk, or a
// range that starts mid-history) simply close their lane — the graph shows the
// boundary rather than inventing edges to commits it was never given.
func Layout(commits []Commit, matched map[string]bool) []Row {
	if len(commits) == 0 {
		return nil
	}
	// present bounds the DAG to what we were actually given, so a parent edge
	// pointing outside the walk closes its lane instead of holding a column
	// open forever.
	present := make(map[string]struct{}, len(commits))
	for _, c := range commits {
		present[c.Hash] = struct{}{}
	}

	var lanes []string // per column: the hash that column is waiting for; "" = free
	rows := make([]Row, 0, len(commits))

	for _, c := range commits {
		lane := indexOf(lanes, c.Hash)
		if lane < 0 {
			// A commit nothing is waiting for: a branch tip, or the first
			// commit of the walk. It opens its own lane.
			lane = firstFree(lanes)
			if lane == len(lanes) {
				lanes = append(lanes, "")
			}
			lanes[lane] = c.Hash
		}

		// Every other lane waiting for this same commit is a line of
		// development converging here. Record them, then release them.
		var merging []int
		for i, waiting := range lanes {
			if i != lane && waiting == c.Hash {
				merging = append(merging, i)
				lanes[i] = ""
			}
		}

		parents := knownParents(c.Parents, present)

		// Rebind this commit's lane to its first parent, then give every
		// additional parent a lane of its own.
		var forking []int
		if len(parents) == 0 {
			lanes[lane] = "" // root commit (or a truncated boundary): lane closes
		} else {
			lanes[lane] = parents[0]
			for _, p := range parents[1:] {
				// A parent already being waited for needs no new column — the
				// two lines simply share it from here down.
				if indexOf(lanes, p) >= 0 {
					continue
				}
				f := firstFree(lanes)
				if f == len(lanes) {
					lanes = append(lanes, "")
				}
				lanes[f] = p
				forking = append(forking, f)
			}
		}

		rows = append(rows, Row{
			Commit:  c,
			Lane:    lane,
			Matched: isMatched(matched, c.Hash),
			Cells:   cellsFor(lanes, lane, merging, forking),
		})
	}

	// Pad every row to the widest one so a renderer can index columns without
	// bounds-checking each row.
	width := 0
	for _, r := range rows {
		if len(r.Cells) > width {
			width = len(r.Cells)
		}
	}
	for i := range rows {
		for len(rows[i].Cells) < width {
			rows[i].Cells = append(rows[i].Cells, GlyphEmpty)
		}
	}
	return rows
}

// cellsFor renders one row's columns from the lane table as it stands *after*
// the commit was processed. The commit's own column shows the commit marker;
// lanes that merged into it this row show a merge glyph even though they are
// already released; lanes opened by a fork show a fork glyph; everything else
// still occupied is a pass-through.
func cellsFor(lanes []string, lane int, merging, forking []int) []Glyph {
	cells := make([]Glyph, len(lanes))
	for i, waiting := range lanes {
		if waiting != "" {
			cells[i] = GlyphVertical
		}
	}
	for _, i := range merging {
		if i < len(cells) {
			cells[i] = GlyphMerge
		}
	}
	for _, i := range forking {
		if i < len(cells) {
			cells[i] = GlyphFork
		}
	}
	if lane < len(cells) {
		cells[lane] = GlyphCommit
	}
	return cells
}

// knownParents drops parent edges pointing outside the walked set, so a
// truncated history closes its lanes instead of waiting for commits that will
// never arrive. Duplicates are dropped too (a merge of a commit with itself is
// malformed, but git has produced stranger things).
func knownParents(parents []string, present map[string]struct{}) []string {
	if len(parents) == 0 {
		return nil
	}
	out := make([]string, 0, len(parents))
	seen := make(map[string]struct{}, len(parents))
	for _, p := range parents {
		if p == "" {
			continue
		}
		if _, ok := present[p]; !ok {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func indexOf(lanes []string, hash string) int {
	for i, l := range lanes {
		if l == hash {
			return i
		}
	}
	return -1
}

// firstFree returns the leftmost released column, or len(lanes) when a new one
// must be appended. Reusing released columns is what keeps the graph narrow
// instead of drifting right with every merge.
func firstFree(lanes []string) int {
	for i, l := range lanes {
		if l == "" {
			return i
		}
	}
	return len(lanes)
}

// isMatched treats a nil map as "no filter is active", so an unfiltered graph
// highlights every commit rather than none.
func isMatched(matched map[string]bool, hash string) bool {
	if matched == nil {
		return true
	}
	return matched[hash]
}
