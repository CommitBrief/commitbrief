// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import "context"

type Provider interface {
	Name() string

	DefaultModel() string

	ContextWindow(model string) int

	EstimateTokens(text string) int

	Pricing(model string) Pricing

	Review(ctx context.Context, req Request) (Response, error)

	TestConnection(ctx context.Context) error
}
