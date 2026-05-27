// SPDX-License-Identifier: GPL-3.0-or-later

package provider

type Request struct {
	Model        string
	SystemPrompt string
	UserPrompt   string
	Lang         string
	MaxTokens    int

	// ProviderOpts is an escape hatch (ADR-0001 risk #1) for features that
	// don't fit the common interface, e.g. Anthropic prompt caching or
	// OpenAI logprobs. Providers cast to their expected type; unknown values
	// are ignored.
	ProviderOpts any
}

type Response struct {
	Content string
	Model   string
	Usage   Usage
}

type Usage struct {
	InputTokens  int
	OutputTokens int

	// CachedInputTokens is the subset of InputTokens served from a provider-
	// side prompt cache (Anthropic ephemeral cache, OpenAI cached completions).
	// Zero if unknown or unsupported.
	CachedInputTokens int
}

type EventType int

const (
	EventDelta EventType = iota
	EventUsage
	EventDone
	EventError
)

func (e EventType) String() string {
	switch e {
	case EventDelta:
		return "delta"
	case EventUsage:
		return "usage"
	case EventDone:
		return "done"
	case EventError:
		return "error"
	default:
		return "unknown"
	}
}

type Event struct {
	Type  EventType
	Delta string
	Usage Usage
	Err   error
}
