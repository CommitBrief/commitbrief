// SPDX-License-Identifier: GPL-3.0-or-later

package mock

import (
	"context"
	"sync"
	"time"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

const defaultName = "mock"

// DefaultResponseContent is the canned JSON the mock provider returns when
// no override is set on ResponseContent. It matches the ADR-0014 JSON
// findings schema so the renderer's happy path is exercised end-to-end in
// any CLI integration test that doesn't explicitly stage its own payload.
// The single finding's title is "mock review output" — historically
// asserted by CLI tests that previously expected a plain-text body; the
// string survives the format change as a finding-title match.
const DefaultResponseContent = `{"findings":[{"severity":"info","file":"mock.go","line":1,"title":"mock review output","description":"Synthetic finding produced by the mock provider for tests.","suggestion":"This is a synthetic suggestion used only to keep the schema-validation tests passing."}]}`

type Provider struct {
	mu sync.Mutex

	NameValue        string
	DefaultModelName string
	Window           int
	PricingValue     provider.Pricing

	ResponseContent string
	Latency         time.Duration

	InputTokens  int
	OutputTokens int
	CachedInput  int

	// Error injection
	ReviewErr   error
	TestConnErr error

	// Call telemetry
	ReviewCalls int
	TestCalls   int
	LastRequest provider.Request
}

func New() *Provider {
	return &Provider{
		NameValue:        defaultName,
		DefaultModelName: "mock-model",
		Window:           100_000,
		ResponseContent:  DefaultResponseContent,
		InputTokens:      100,
		OutputTokens:     50,
	}
}

func (m *Provider) Name() string {
	if m.NameValue == "" {
		return defaultName
	}
	return m.NameValue
}

func (m *Provider) DefaultModel() string {
	if m.DefaultModelName == "" {
		return "mock-model"
	}
	return m.DefaultModelName
}

func (m *Provider) ContextWindow(string) int {
	if m.Window == 0 {
		return 100_000
	}
	return m.Window
}

func (m *Provider) EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

func (m *Provider) Pricing(string) provider.Pricing {
	return m.PricingValue
}

func (m *Provider) TestConnection(ctx context.Context) error {
	m.mu.Lock()
	m.TestCalls++
	err := m.TestConnErr
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func (m *Provider) Review(ctx context.Context, req provider.Request) (provider.Response, error) {
	m.mu.Lock()
	m.ReviewCalls++
	m.LastRequest = req
	err := m.ReviewErr
	content := m.ResponseContent
	usage := m.usage()
	model := req.Model
	if model == "" {
		model = m.DefaultModel()
	}
	latency := m.Latency
	m.mu.Unlock()

	if latency > 0 {
		select {
		case <-time.After(latency):
		case <-ctx.Done():
			return provider.Response{}, ctx.Err()
		}
	}
	if err != nil {
		return provider.Response{}, err
	}
	return provider.Response{Content: content, Model: model, Usage: usage}, nil
}

func (m *Provider) usage() provider.Usage {
	return provider.Usage{
		InputTokens:       m.InputTokens,
		OutputTokens:      m.OutputTokens,
		CachedInputTokens: m.CachedInput,
	}
}

// Register installs a fresh mock provider under the name "mock" in the
// global registry. Tests that drive the CLI through config-driven provider
// lookup (Phase 5+) call this; production code never imports this package.
func Register() {
	provider.Register(defaultName, func(_ config.ProviderConfig) (provider.Provider, error) {
		return New(), nil
	})
}
