// SPDX-License-Identifier: GPL-3.0-or-later

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
	// Content is the raw provider response — under ADR-0014 a JSON string
	// matching the findings schema; on graceful degrade it may be free-form
	// markdown left over from a malformed JSON response. Cached as-is.
	Content string

	// Findings is the parsed structured response from the LLM. A non-nil
	// empty slice means "no review-worthy issues"; a nil slice signals
	// graceful degrade — the JSON parse failed and renderers must fall
	// back to rendering Content directly (Stage A behavior).
	Findings []Finding

	// OutputTemplate is the loaded OUTPUT.md template body, consumed by the
	// Markdown renderer. Empty string falls back to emitting Content
	// unchanged (also the path used during degrade).
	OutputTemplate string

	// Compact requests one-line-per-finding rendering in the Cards layout,
	// useful when a review surfaces many findings and per-finding panels
	// would dominate the terminal. Header/status/footer stay; the body
	// becomes a severity-ordered list of "[icon] SEVERITY • file:line —
	// title" lines. Other renderers (Markdown/JSON) ignore this field.
	Compact bool

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
