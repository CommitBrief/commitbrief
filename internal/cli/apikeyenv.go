// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

// applyAPIKeyEnvLenient folds api_key_env into every provider block of the
// merged config where the variable is actually set, and leaves the rest
// alone. It never fails: it only makes the merged view (providers list,
// doctor) show the credential that will really be used. The strict check
// that turns "set but empty" into an error lives in provider.ResolveAPIKeyEnv
// and runs where a provider is actually built.
func applyAPIKeyEnvLenient(cfg *config.Config) {
	for name, pc := range cfg.Providers {
		if pc.APIKeyEnv == "" || !provider.UsesAPIKey(name) {
			continue
		}
		if v := os.Getenv(pc.APIKeyEnv); v != "" {
			pc.APIKey = v
			cfg.Providers[name] = pc
		}
	}
}
