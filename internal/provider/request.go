// SPDX-License-Identifier: GPL-3.0-or-later

package provider

type Request struct {
	Model        string
	SystemPrompt string
	UserPrompt   string
	Lang         string
	MaxTokens    int

	// FreeForm requests an unstructured plain-text completion (ADR-0015).
	// When true, API providers MUST skip their structured-output
	// enforcement (Anthropic tool_choice, OpenAI response_format, Gemini
	// responseSchema, Ollama format:json) and return the model's raw text
	// in Response.Content. Default false preserves the ADR-0014
	// structured-findings contract, so existing call sites are unaffected.
	// Used by `--suggest-commit` to get a commit message instead of findings.
	FreeForm bool

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
