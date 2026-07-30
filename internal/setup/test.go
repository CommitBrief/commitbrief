// SPDX-License-Identifier: GPL-3.0-or-later

package setup

import (
	"context"
	"time"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

// TestConnection instantiates the named provider with cfg and runs its
// TestConnection method. Errors flow through wrapped (provider sentinels
// preserved); a nil error means the credentials reached the backend and
// got a non-error response.
func TestConnection(ctx context.Context, name string, cfg config.ProviderConfig) error {
	return TestConnectionTimeout(ctx, name, cfg, 0)
}

// TestConnectionTimeout is TestConnection with the caller's resolved
// --timeout / review.timeout value handed to the provider. It matters for
// the same reason it does on the review path: a probe against a slow
// ollama host or a CLI binary would otherwise die at the provider's own
// built-in cap no matter how much time the user allowed. A zero d keeps
// every built-in exactly as it is.
func TestConnectionTimeout(ctx context.Context, name string, cfg config.ProviderConfig, d time.Duration) error {
	p, err := provider.New(name, cfg)
	if err != nil {
		return err
	}
	if d > 0 {
		if ts, ok := p.(provider.TimeoutSetter); ok {
			ts.SetTimeout(d)
		}
	}
	return p.TestConnection(ctx)
}
