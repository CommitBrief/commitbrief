// SPDX-License-Identifier: GPL-3.0-or-later

package openaicompat

import "github.com/CommitBrief/commitbrief/internal/provider"

// Metadata describes openai-compatible without needing a credential or an
// endpoint (ADR-0039). Everything user-specific — model, base URL, prices,
// context window — is declared in config, so the static inventory carries no
// models, no default model and no fixed API-key variable
// (the key comes from providers.openai-compatible.api_key_env or api_key).
func Metadata() provider.Metadata {
	return provider.Metadata{
		Name:                Name,
		Kind:                provider.KindAPI,
		PricingUserDeclared: true,
	}
}
