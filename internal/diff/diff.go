// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import "github.com/CommitBrief/commitbrief/internal/git"

type Diff struct {
	Files  []FileDiff
	Origin git.Origin
	Args   map[string]string

	// Memoized aggregates populated at construction time (Parse,
	// Filter, KeepPaths). All read-only after that — Diff is a
	// value type and never mutated post-construction. Cached here
	// because review.go calls these once per pipeline stage and the
	// O(N) traversal over hunks/lines isn't free on large diffs.
	addedLines   int
	deletedLines int
}

type Mode int

const (
	ModeModified Mode = iota
	ModeAdded
	ModeDeleted
	ModeRenamed
	ModeCopied
)

func (m Mode) String() string {
	switch m {
	case ModeAdded:
		return "added"
	case ModeDeleted:
		return "deleted"
	case ModeRenamed:
		return "renamed"
	case ModeCopied:
		return "copied"
	default:
		return "modified"
	}
}

type FileDiff struct {
	Path    string
	OldPath string
	Mode    Mode
	Hunks   []Hunk
	Binary  bool

	// PathParts is Path pre-split on `/`, populated once during
	// parseDiffHeader. The ignore-matcher backend takes `[]string`
	// parts; without this cache every Match call would re-split.
	// On a 500-file diff with two filter layers that's 1000+
	// redundant allocations.
	PathParts []string
	// OldPathParts mirrors PathParts for renamed/deleted entries.
	OldPathParts []string
}

type Hunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Header   string
	Lines    []HunkLine
}

type LineKind int

const (
	LineContext LineKind = iota
	LineAdd
	LineDel
)

type HunkLine struct {
	Kind LineKind
	Text string
}

func (d Diff) Empty() bool { return len(d.Files) == 0 }

func (d Diff) FileCount() int { return len(d.Files) }

// AddedLines is the count of `+` lines across all hunks in all files.
// O(1) — the value is computed once in Parse / Filter / KeepPaths and
// memoized in the struct.
func (d Diff) AddedLines() int { return d.addedLines }

// DeletedLines is the count of `-` lines across all hunks in all
// files. Memoized like AddedLines.
func (d Diff) DeletedLines() int { return d.deletedLines }

// countLineKinds walks the file/hunk/line tree once and returns the
// add/del totals. Construction helpers (Parse, Filter, KeepPaths)
// call this to populate the memo fields without duplicating the
// counting loop.
func countLineKinds(files []FileDiff) (added, deleted int) {
	for _, f := range files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				switch l.Kind {
				case LineAdd:
					added++
				case LineDel:
					deleted++
				}
			}
		}
	}
	return added, deleted
}
