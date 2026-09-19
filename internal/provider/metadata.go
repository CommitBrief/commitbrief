// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"fmt"
	"sort"
	"sync"
)

// Kind classifies how a provider reaches a model, which is the distinction
// users actually make when choosing one: a remote paid API, a model running
// on their own machine, or an already-installed coding CLI acting as the
// backend.
type Kind string

const (
	KindAPI   Kind = "api"
	KindLocal Kind = "local"
	KindCLI   Kind = "cli"
)

// ModelInfo is everything documentation needs to say about one model.
// Pricing is USD per 1M tokens; a zero Pricing means "free or not
// metered" (ollama, the CLI-backed providers), not "unknown".
type ModelInfo struct {
	ID            string  `json:"id"`
	Default       bool    `json:"default,omitempty"`
	ContextWindow int     `json:"context_window"`
	Pricing       Pricing `json:"pricing"`
}

// Metadata is the static, API-key-free description of a provider.
//
// It exists because the Provider interface cannot answer these questions:
// it is instance-level (ContextWindow and Pricing only answer for a model
// you already name, and there is no Models method), and New() refuses to
// build a client without an API key — so an unconfigured provider's facts
// were unreachable. Documentation needs them for every provider, including
// ones the reader has not configured. See ADR-0039.
type Metadata struct {
	Name           string      `json:"name"`
	Kind           Kind        `json:"kind"`
	DefaultModel   string      `json:"default_model"`
	APIKeyEnv      string      `json:"api_key_env,omitempty"`
	DefaultBaseURL string      `json:"default_base_url,omitempty"`
	Binary         string      `json:"binary,omitempty"`
	Models         []ModelInfo `json:"models,omitempty"`
}

var (
	metadataMu sync.RWMutex
	metadata   = make(map[string]Metadata)
)

// RegisterMetadata records a provider's static description. Call it from
// the same init() that calls Register, so the two registries cannot
// disagree about which providers exist.
//
// It panics on a bad registration for the same reason Register does:
// these are compile-time-constant program facts, and a provider that
// silently failed to describe itself would surface as a documentation
// hole rather than a crash.
func RegisterMetadata(m Metadata) {
	if m.Name == "" {
		panic("provider: RegisterMetadata with empty name")
	}
	metadataMu.Lock()
	defer metadataMu.Unlock()
	if _, dup := metadata[m.Name]; dup {
		panic(fmt.Sprintf("provider: metadata for %q registered twice", m.Name))
	}
	metadata[m.Name] = cloneMetadata(m)
}

// AllMetadata returns every registered description, sorted by name so the
// generated inventory is byte-stable across runs.
//
// It reports only what has been linked in: the registry is populated by
// the blank imports in cmd/commitbrief/main.go, so a package that calls
// this from its own tests must blank-import the providers or it will
// observe an empty registry and pass vacuously.
func AllMetadata() []Metadata {
	metadataMu.RLock()
	defer metadataMu.RUnlock()

	out := make([]Metadata, 0, len(metadata))
	for _, m := range metadata {
		out = append(out, cloneMetadata(m))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// MetadataFor looks up one provider's description.
func MetadataFor(name string) (Metadata, bool) {
	metadataMu.RLock()
	defer metadataMu.RUnlock()
	m, ok := metadata[name]
	if !ok {
		return Metadata{}, false
	}
	return cloneMetadata(m), true
}

// cloneMetadata deep-copies the Models slice. Without it a caller could
// mutate the registry's own backing array through a returned value.
func cloneMetadata(m Metadata) Metadata {
	if m.Models != nil {
		models := make([]ModelInfo, len(m.Models))
		copy(models, m.Models)
		m.Models = models
	}
	return m
}

// resetMetadataForTest clears the registry between tests.
func resetMetadataForTest() {
	metadataMu.Lock()
	defer metadataMu.Unlock()
	metadata = make(map[string]Metadata)
}
