// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/i18n"
)

// ---------- parseTimeout ----------

func TestParseTimeoutAcceptedValues(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"0s", 0},
		{"90s", 90 * time.Second},
		{"10m", 10 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"600", 600 * time.Second}, // bare integer = seconds (CI ergonomics)
		{"  5m  ", 5 * time.Minute},
		{"1500ms", 1500 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseTimeout(tc.in)
			if err != nil {
				t.Fatalf("parseTimeout(%q) returned error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("parseTimeout(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseTimeoutRejectsBadValues(t *testing.T) {
	bad := []string{"abc", "-5s", "-600", "5 minutes", "m10", "1.5.2"}
	for _, in := range bad {
		t.Run(in, func(t *testing.T) {
			if _, err := parseTimeout(in); err == nil {
				t.Fatalf("parseTimeout(%q) accepted an invalid value", in)
			}
		})
	}
}

// ---------- resolveTimeout ----------

func TestResolveTimeoutPrecedence(t *testing.T) {
	cfgWith := func(v string) *config.Config {
		c := config.Default()
		c.Review.Timeout = v
		return c
	}
	cases := []struct {
		name string
		flag string
		cfg  *config.Config
		want time.Duration
	}{
		{"nothing set", "", config.Default(), 0},
		{"config only", "", cfgWith("10m"), 10 * time.Minute},
		{"flag only", "45s", config.Default(), 45 * time.Second},
		{"flag beats config", "2m", cfgWith("10m"), 2 * time.Minute},
		{"explicit zero cancels config", "0", cfgWith("10m"), 0},
		{"nil config", "30s", nil, 30 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveTimeout(tc.flag, tc.cfg)
			if err != nil {
				t.Fatalf("resolveTimeout: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolveTimeout(%q) = %v, want %v", tc.flag, got, tc.want)
			}
		})
	}
}

func TestResolveTimeoutSurfacesBadConfigValue(t *testing.T) {
	cfg := config.Default()
	cfg.Review.Timeout = "ten minutes"
	if _, err := resolveTimeout("", cfg); err == nil {
		t.Fatal("want error for an unparseable review.timeout, got nil")
	}
}

// ---------- withTimeout ----------

func TestWithTimeoutSetsDeadlineOnlyWhenConfigured(t *testing.T) {
	off := &appContext{}
	ctx, cancel := off.withTimeout(context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Error("unset timeout must not attach a deadline")
	}

	on := &appContext{Timeout: time.Minute}
	ctx2, cancel2 := on.withTimeout(context.Background())
	defer cancel2()
	deadline, ok := ctx2.Deadline()
	if !ok {
		t.Fatal("configured timeout must attach a deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Minute {
		t.Errorf("deadline is %v away, want (0, 1m]", remaining)
	}
}

// ---------- wrapTimeoutErr ----------

func TestWrapTimeoutErr(t *testing.T) {
	cat, err := i18n.Load("en")
	if err != nil {
		t.Fatal(err)
	}
	providerErr := errors.New("provider anthropic: connection reset")

	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	t.Run("nil error passes through", func(t *testing.T) {
		if got := wrapTimeoutErr(expired, nil, cat, time.Minute); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("live context passes through", func(t *testing.T) {
		got := wrapTimeoutErr(context.Background(), providerErr, cat, time.Minute)
		if got != providerErr {
			t.Errorf("got %v, want the original error untouched", got)
		}
	})

	t.Run("no timeout configured passes through", func(t *testing.T) {
		got := wrapTimeoutErr(expired, providerErr, cat, 0)
		if got != providerErr {
			t.Errorf("got %v, want the original error untouched", got)
		}
	})

	t.Run("expired deadline is rewritten", func(t *testing.T) {
		got := wrapTimeoutErr(expired, providerErr, cat, 90*time.Second)
		if got == nil {
			t.Fatal("got nil, want a timeout error")
		}
		if !strings.Contains(got.Error(), "1m30s") {
			t.Errorf("message %q should name the configured duration", got.Error())
		}
		if !strings.Contains(got.Error(), "--timeout") {
			t.Errorf("message %q should point at --timeout", got.Error())
		}
	})

	t.Run("nil catalog still explains itself", func(t *testing.T) {
		got := wrapTimeoutErr(expired, providerErr, nil, 30*time.Second)
		if got == nil || !strings.Contains(got.Error(), "30s") {
			t.Errorf("got %v, want a message naming 30s", got)
		}
	})
}

// ---------- newProviderWithTimeout ----------

func TestNewProviderWithTimeoutRejectsUnknownProvider(t *testing.T) {
	if _, err := newProviderWithTimeout("nope-not-a-provider", config.ProviderConfig{}, time.Minute); err == nil {
		t.Fatal("want error for an unregistered provider, got nil")
	}
}
