package diff

import "github.com/CommitBrief/commitbrief/internal/git"

type Diff struct {
	Files  []FileDiff
	Origin git.Origin
	Args   map[string]string
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

func (d Diff) AddedLines() int {
	var n int
	for _, f := range d.Files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Kind == LineAdd {
					n++
				}
			}
		}
	}
	return n
}

func (d Diff) DeletedLines() int {
	var n int
	for _, f := range d.Files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Kind == LineDel {
					n++
				}
			}
		}
	}
	return n
}
