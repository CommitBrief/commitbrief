package render

import (
	"time"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

type Format int

const (
	FormatTerminal Format = iota
	FormatMarkdown
	FormatJSON
)

func (f Format) String() string {
	switch f {
	case FormatTerminal:
		return "terminal"
	case FormatMarkdown:
		return "markdown"
	case FormatJSON:
		return "json"
	default:
		return "unknown"
	}
}

type Payload struct {
	Content string
	Meta    Meta
	Verbose bool
}

type Meta struct {
	Provider  string
	Model     string
	Lang      string
	Usage     provider.Usage
	Cost      float64
	Latency   time.Duration
	Cached    bool
	Timestamp time.Time
	// Stats describing what went into this review. Used by the Cards
	// renderer for its pre-body status line; zero values cause the line
	// to be omitted. Markdown / JSON / verbose-footer renderers ignore
	// these fields, so adding more here is backwards-compatible.
	Files        int  // post-filter file count
	LinesAdded   int  // total `+` lines in the reviewed diff
	LinesRemoved int  // total `-` lines in the reviewed diff
	RulesLoaded  bool // a non-default COMMITBRIEF.md was loaded
}
