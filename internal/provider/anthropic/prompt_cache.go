// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

import "github.com/anthropics/anthropic-sdk-go"

// systemPromptWithCache wraps the rules content as a single text block
// marked with `cache_control: ephemeral`. Anthropic's prompt cache reads
// charge at ~10% of the input rate; repeated reviews against the same
// rules file thus pay the full system-prompt cost only on the first
// invocation within the 5-minute TTL window.
//
// Tracks OQ-12 (open): the exact cache scope (system-only vs. system+diff)
// and TTL choice ("5m" vs "1h") will be revisited as we gather real usage.
func systemPromptWithCache(prompt string) []anthropic.TextBlockParam {
	if prompt == "" {
		return nil
	}
	return []anthropic.TextBlockParam{
		{
			Text: prompt,
			// TTL must be set explicitly: TextBlockParam.CacheControl is
			// tagged omitzero, so the zero-value struct gets dropped from
			// the request JSON and the cache marker never reaches the API.
			CacheControl: anthropic.CacheControlEphemeralParam{
				TTL: anthropic.CacheControlEphemeralTTLTTL5m,
			},
		},
	}
}
