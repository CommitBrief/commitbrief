// SPDX-License-Identifier: GPL-3.0-or-later

package mock

import "github.com/CommitBrief/commitbrief/internal/provider"

// Metadata describes the mock provider so the two registries stay in step:
// anything that enumerates providers sees the same set either way. See
// ADR-0039.
//
// It reads its facts off a fresh New() rather than restating them, so the
// canned name, model, window and pricing have exactly one definition. The
// mock has no supportedModels table because it serves a single synthetic
// model and bills nothing, hence the zero Pricing.
//
// main.go does not blank-import this package, so the mock never reaches the
// shipped inventory — only test binaries that import it see this entry.
func Metadata() provider.Metadata {
	p := New()
	model := p.DefaultModel()

	return provider.Metadata{
		Name:         p.Name(),
		Kind:         provider.KindAPI,
		DefaultModel: model,
		Models: []provider.ModelInfo{{
			ID:            model,
			Default:       true,
			ContextWindow: p.ContextWindow(model),
			Pricing:       p.Pricing(model),
		}},
	}
}

// Registering from init() rather than from Register() is deliberate:
// RegisterMetadata panics on a duplicate, and Register() is a test helper
// that a test binary may reasonably call after resetting the factory
// registry. init() runs exactly once per process either way.
func init() {
	provider.RegisterMetadata(Metadata())
}
