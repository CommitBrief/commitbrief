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
}
