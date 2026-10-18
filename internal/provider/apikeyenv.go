// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"fmt"
	"os"

	"github.com/CommitBrief/commitbrief/internal/config"
)

// UsesAPIKey reports whether the generic api_key_env / api_key overrides
// mean anything for this provider. The *-cli providers authenticate through
// their own binary's login, so for them both fields stay inert — exactly as
// api_key already is today.
func UsesAPIKey(name string) bool { return !IsCLI(name) }

// IsCLI reports whether the named provider is CLI-backed (Kind == KindCLI).
// A provider with no registered metadata is treated as not CLI.
func IsCLI(name string) bool {
	m, ok := MetadataFor(name)
	return ok && m.Kind == KindCLI
}

// ResolveAPIKeyEnv returns the provider block with api_key_env applied:
// when api_key_env is set, the credential comes from that environment
// variable and wins over both the provider's fixed env var (ANTHROPIC_API_KEY
// and friends, already folded into APIKey by config.ApplyEnv) and api_key.
// When the field is set but the named variable is empty, it is an error
// rather than a silent fallback — a pinned variable that is missing is
// almost always a typo, and falling back would send a different credential
// than the one the user asked for. A block without api_key_env is returned
// unchanged, so every existing config behaves byte for byte as before.
//
// It lives here (not in internal/cli) so that review, providers and doctor
// all apply the exact same strict rule.
func ResolveAPIKeyEnv(name string, pc config.ProviderConfig) (config.ProviderConfig, error) {
	if pc.APIKeyEnv == "" || !UsesAPIKey(name) {
		return pc, nil
	}
	v := os.Getenv(pc.APIKeyEnv)
	if v == "" {
		return pc, fmt.Errorf("providers.%s.api_key_env is set to %q, but that environment variable is empty or unset", name, pc.APIKeyEnv)
	}
	pc.APIKey = v
	return pc, nil
}
