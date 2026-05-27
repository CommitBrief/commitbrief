// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"fmt"
	"sort"
	"sync"

	"github.com/CommitBrief/commitbrief/internal/config"
)

type Factory func(cfg config.ProviderConfig) (Provider, error)

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Factory)
)

// Register installs a provider factory under the given name. Intended to be
// called from package init() functions of provider subpackages. Panics on
// duplicate registration to surface programmer errors at startup rather
// than silently overriding.
func Register(name string, factory Factory) {
	if name == "" {
		panic("provider: Register called with empty name")
	}
	if factory == nil {
		panic("provider: Register called with nil factory for " + name)
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic("provider: duplicate registration for " + name)
	}
	registry[name] = factory
}

func New(name string, cfg config.ProviderConfig) (Provider, error) {
	registryMu.RLock()
	factory, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q (known: %v)", ErrUnknownProvider, name, Names())
	}
	return factory(cfg)
}

func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// resetForTest is a test helper that drops all registrations. Exposed via
// reset_test.go (build-tagged); production code never calls it.
func resetForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[string]Factory)
}
