// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"strings"
	"testing"
)

// TestWithContextRejectsAPIProvider: --with-context (ADR-0017) is
// CLI-provider only. The test harness's default provider is a non-CLI
// (API/mock) provider, so the flag must fail fast — before any provider
// call — with the context.cli_only message rather than being silently
// ignored.
func TestWithContextRejectsAPIProvider(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("--staged", "--with-context")
	if err == nil {
		t.Fatal("--with-context with a non-CLI provider must error")
	}
	if !strings.Contains(err.Error(), "with-context") {
		t.Errorf("error should name --with-context; got: %v", err)
	}
}
