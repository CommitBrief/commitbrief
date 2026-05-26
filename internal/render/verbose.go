package render

import (
	"fmt"
	"strings"
	"time"
)

const verboseRule = "─────────────────────────────────"

// VerboseFooter returns the multi-line footer appended after a review when
// --verbose is in effect. Format is stable: tokens, cost, latency, provider,
// model — each on its own line, aligned. Cached results get a `(cached)`
// marker on the tokens line so users see why a fast result was free.
func VerboseFooter(m Meta) string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(verboseRule)
	sb.WriteString("\n")
	if m.Provider != "" {
		fmt.Fprintf(&sb, "Provider:  %s\n", m.Provider)
	}
	if m.Model != "" {
		fmt.Fprintf(&sb, "Model:     %s\n", m.Model)
	}
	tokens := fmt.Sprintf("in=%d, out=%d", m.Usage.InputTokens, m.Usage.OutputTokens)
	if m.Usage.CachedInputTokens > 0 {
		tokens += fmt.Sprintf(" (cached: %d)", m.Usage.CachedInputTokens)
	}
	if m.Cached {
		tokens += " (cached)"
	}
	fmt.Fprintf(&sb, "Tokens:    %s\n", tokens)
	if m.Cost > 0 {
		fmt.Fprintf(&sb, "Cost:      $%.4f\n", m.Cost)
	}
	if m.Latency > 0 {
		fmt.Fprintf(&sb, "Latency:   %s\n", formatDuration(m.Latency))
	}
	sb.WriteString(verboseRule)
	sb.WriteString("\n")
	return sb.String()
}

func formatDuration(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return d.String()
	}
}
