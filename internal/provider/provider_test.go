package provider

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
)

type stubProvider struct {
	name string
}

func (s *stubProvider) Name() string                                  { return s.name }
func (s *stubProvider) DefaultModel() string                          { return "stub-model" }
func (s *stubProvider) ContextWindow(string) int                      { return 1024 }
func (s *stubProvider) EstimateTokens(string) int                     { return 0 }
func (s *stubProvider) Pricing(string) Pricing                        { return Pricing{} }
func (s *stubProvider) TestConnection(context.Context) error          { return nil }
func (s *stubProvider) Review(context.Context, Request) (Response, error) {
	return Response{}, nil
}
func (s *stubProvider) ReviewStream(context.Context, Request) (<-chan Event, error) {
	return nil, nil
}

func TestRegisterAndNew(t *testing.T) {
	resetForTest()
	Register("stub", func(_ config.ProviderConfig) (Provider, error) {
		return &stubProvider{name: "stub"}, nil
	})
	p, err := New("stub", config.ProviderConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "stub" {
		t.Errorf("Name = %q, want stub", p.Name())
	}
}

func TestNewUnknownProvider(t *testing.T) {
	resetForTest()
	_, err := New("nonexistent", config.ProviderConfig{})
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("err = %v, want ErrUnknownProvider", err)
	}
}

func TestNamesSorted(t *testing.T) {
	resetForTest()
	Register("openai", func(c config.ProviderConfig) (Provider, error) {
		return &stubProvider{name: "openai"}, nil
	})
	Register("anthropic", func(c config.ProviderConfig) (Provider, error) {
		return &stubProvider{name: "anthropic"}, nil
	})
	Register("gemini", func(c config.ProviderConfig) (Provider, error) {
		return &stubProvider{name: "gemini"}, nil
	})
	got := Names()
	want := []string{"anthropic", "gemini", "openai"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("Names()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRegisterPanicsOnDuplicate(t *testing.T) {
	resetForTest()
	Register("dupe", func(_ config.ProviderConfig) (Provider, error) {
		return &stubProvider{}, nil
	})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	Register("dupe", func(_ config.ProviderConfig) (Provider, error) {
		return &stubProvider{}, nil
	})
}

func TestRegisterPanicsOnEmptyName(t *testing.T) {
	resetForTest()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on empty name")
		}
	}()
	Register("", func(_ config.ProviderConfig) (Provider, error) {
		return &stubProvider{}, nil
	})
}

func TestRegisterPanicsOnNilFactory(t *testing.T) {
	resetForTest()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil factory")
		}
	}()
	Register("nilfactory", nil)
}

func TestPricingCostBasic(t *testing.T) {
	p := Pricing{InputPer1M: 3.0, OutputPer1M: 15.0}
	u := Usage{InputTokens: 1_000_000, OutputTokens: 100_000}
	got := p.Cost(u)
	want := 3.0 + 1.5 // 3.0 for input M + 1.5 for 100k output
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("Cost = %f, want %f", got, want)
	}
}

func TestPricingCostWithCache(t *testing.T) {
	p := Pricing{
		InputPer1M:       3.0,
		OutputPer1M:      15.0,
		CachedInputPer1M: 0.30,
	}
	u := Usage{
		InputTokens:       1_000_000,
		CachedInputTokens: 800_000,
		OutputTokens:      0,
	}
	got := p.Cost(u)
	// 200k uncached * 3.0/M + 800k cached * 0.30/M
	want := 0.6 + 0.24
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("cached Cost = %f, want %f", got, want)
	}
}

func TestPricingCostCachedFallbackToUncached(t *testing.T) {
	// CachedInputPer1M == 0 means "treat cached at full price"
	p := Pricing{InputPer1M: 3.0, OutputPer1M: 15.0}
	u := Usage{InputTokens: 1_000_000, CachedInputTokens: 500_000}
	got := p.Cost(u)
	want := 3.0 // full input price regardless of cache flag
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("Cost without cache discount = %f, want %f", got, want)
	}
}

func TestPricingCostCachedExceedsInputClamped(t *testing.T) {
	// Defensive: if a provider reports CachedInputTokens > InputTokens, we
	// clamp rather than double-counting.
	p := Pricing{InputPer1M: 3.0, OutputPer1M: 15.0, CachedInputPer1M: 0.30}
	u := Usage{InputTokens: 100, CachedInputTokens: 500}
	got := p.Cost(u)
	want := float64(100) * 0.30 / 1_000_000
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("clamped Cost = %f, want %f", got, want)
	}
}

func TestEventTypeString(t *testing.T) {
	cases := map[EventType]string{
		EventDelta: "delta",
		EventUsage: "usage",
		EventDone:  "done",
		EventError: "error",
	}
	for typ, want := range cases {
		if got := typ.String(); got != want {
			t.Errorf("EventType(%d).String() = %q, want %q", typ, got, want)
		}
	}
}

func TestFactoryPropagatesError(t *testing.T) {
	resetForTest()
	want := errors.New("bad config")
	Register("broken", func(_ config.ProviderConfig) (Provider, error) {
		return nil, want
	})
	_, err := New("broken", config.ProviderConfig{})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

// Compile-time check: stubProvider implements Provider.
var _ Provider = (*stubProvider)(nil)
