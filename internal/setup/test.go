// SPDX-License-Identifier: GPL-3.0-or-later

package setup

import (
	"context"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

// TestConnection instantiates the named provider with cfg and runs its
// TestConnection method. Errors flow through wrapped (provider sentinels
// preserved); a nil error means the credentials reached the backend and
// got a non-error response.
func TestConnection(ctx context.Context, name string, cfg config.ProviderConfig) error {
	p, err := provider.New(name, cfg)
	if err != nil {
		return err
	}
	return p.TestConnection(ctx)
}
