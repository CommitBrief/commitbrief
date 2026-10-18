// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
)

func TestResolveAPIKeyEnv(t *testing.T) {
	t.Cleanup(resetMetadataForTest)
	resetMetadataForTest()
	RegisterMetadata(Metadata{Name: "fake-api", Kind: KindAPI})
	RegisterMetadata(Metadata{Name: "fake-cli", Kind: KindCLI})

	t.Setenv("PINNED_KEY", "from-env")
	t.Setenv("EMPTY_KEY", "")

	// API provider: env value beats the existing key.
	pc, err := ResolveAPIKeyEnv("fake-api", config.ProviderConfig{APIKey: "old", APIKeyEnv: "PINNED_KEY"})
	if err != nil || pc.APIKey != "from-env" {
		t.Fatalf("got (%q, %v), want from-env", pc.APIKey, err)
	}

	// Set but empty: error naming provider and variable, no fallback.
	_, err = ResolveAPIKeyEnv("fake-api", config.ProviderConfig{APIKey: "old", APIKeyEnv: "EMPTY_KEY"})
	if err == nil || !strings.Contains(err.Error(), "EMPTY_KEY") || !strings.Contains(err.Error(), "providers.fake-api.api_key_env") {
		t.Fatalf("want error naming provider and variable, got %v", err)
	}

	// CLI provider: the field is inert, even when the variable is empty and
	// the block comes back untouched. A registered KindCLI makes this
	// unconditional: dropping the KindCLI check would fail here.
	in := config.ProviderConfig{APIKey: "keep", APIKeyEnv: "EMPTY_KEY"}
	out, err := ResolveAPIKeyEnv("fake-cli", in)
	if err != nil {
		t.Fatalf("cli provider must ignore api_key_env, got %v", err)
	}
	if out.APIKey != "keep" {
		t.Errorf("cli provider key changed: %q", out.APIKey)
	}
	if !IsCLI("fake-cli") || IsCLI("fake-api") || IsCLI("unregistered") {
		t.Error("IsCLI classification wrong")
	}

	// No api_key_env: block unchanged.
	same, err := ResolveAPIKeyEnv("fake-api", config.ProviderConfig{APIKey: "k", Model: "m"})
	if err != nil || same.APIKey != "k" || same.Model != "m" {
		t.Fatalf("unset api_key_env changed the block: %+v, %v", same, err)
	}
}
