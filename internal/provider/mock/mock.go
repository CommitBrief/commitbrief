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
const DefaultResponseContent = `{"findings":[{"severity":"info","file":"mock.go","line":1,"title":"mock review output","description":"Synthetic finding produced by the mock provider for tests."}]}`

type Provider struct {
	mu sync.Mutex

	NameValue        string
	DefaultModelName string
	Window           int
	PricingValue     provider.Pricing

	ResponseContent string
	ChunkCount      int
	Latency         time.Duration

	InputTokens  int
	OutputTokens int
	CachedInput  int

	// Error injection
	ReviewErr      error
	StreamErr      error
	StreamErrAfter int // 0: synchronous (returned from ReviewStream); >0: emit EventError after N delta events
	TestConnErr    error

	// Call telemetry
	ReviewCalls int
	StreamCalls int
	TestCalls   int
	LastRequest provider.Request
}

func New() *Provider {
	return &Provider{
		NameValue:        defaultName,
		DefaultModelName: "mock-model",
		Window:           100_000,
		ResponseContent:  DefaultResponseContent,
		ChunkCount:       3,
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

func (m *Provider) ReviewStream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	m.mu.Lock()
	m.StreamCalls++
	m.LastRequest = req
	streamErr := m.StreamErr
	streamErrAfter := m.StreamErrAfter
	content := m.ResponseContent
	chunks := m.ChunkCount
	usage := m.usage()
	latency := m.Latency
	m.mu.Unlock()

	if streamErr != nil && streamErrAfter == 0 {
		return nil, streamErr
	}
	if chunks <= 0 {
		chunks = 1
	}

	deltas := splitChunks(content, chunks)
	out := make(chan provider.Event, len(deltas)+2)

	go func() {
		defer close(out)
		for i, d := range deltas {
			if latency > 0 {
				select {
				case <-time.After(latency):
				case <-ctx.Done():
					out <- provider.Event{Type: provider.EventError, Err: ctx.Err()}
					return
				}
			}
			if streamErr != nil && streamErrAfter > 0 && i >= streamErrAfter {
				out <- provider.Event{Type: provider.EventError, Err: streamErr}
				return
			}
			select {
			case out <- provider.Event{Type: provider.EventDelta, Delta: d}:
			case <-ctx.Done():
				out <- provider.Event{Type: provider.EventError, Err: ctx.Err()}
				return
			}
		}
		out <- provider.Event{Type: provider.EventUsage, Usage: usage}
		out <- provider.Event{Type: provider.EventDone}
	}()
	return out, nil
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

func splitChunks(s string, n int) []string {
	if n <= 1 {
		return []string{s}
	}
	if len(s) == 0 {
		return []string{""}
	}
	if n > len(s) {
		n = len(s)
	}
	out := make([]string, 0, n)
	base := len(s) / n
	rem := len(s) % n
	pos := 0
	for i := 0; i < n; i++ {
		size := base
		if i < rem {
			size++
		}
		out = append(out, s[pos:pos+size])
		pos += size
	}
	return out
}
