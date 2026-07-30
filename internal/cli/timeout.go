// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

// parseTimeout maps a raw --timeout / review.timeout value to a duration.
//
// Two spellings are accepted on purpose: a Go duration ("90s", "10m",
// "1h30m") reads best in a config file, while a bare integer is what CI
// authors reach for — so "600" is taken as 600 seconds rather than
// rejected for a missing unit.
//
// "" and "0" both mean "no deadline"; the zero return is the documented
// signal for "leave every built-in provider timeout exactly as it is",
// which keeps an unset flag byte-for-byte backwards compatible. A
// negative or unparseable value is an error so a typo surfaces before
// the paid round-trip instead of silently disabling the guard.
func parseTimeout(raw string) (time.Duration, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d < 0 {
			return 0, fmt.Errorf("invalid timeout %q (must not be negative)", raw)
		}
		return d, nil
	}
	secs, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q (expected a duration like 90s, 10m, 1h30m, or a whole number of seconds)", raw)
	}
	if secs < 0 {
		return 0, fmt.Errorf("invalid timeout %q (must not be negative)", raw)
	}
	return time.Duration(secs) * time.Second, nil
}

// resolveTimeout applies the precedence chain --timeout > review.timeout
// > built-in (0 = off). An explicit `--timeout 0` therefore cancels a
// configured value for one run, which is the only way to get the built-in
// provider defaults back without editing config.
func resolveTimeout(flagVal string, cfg *config.Config) (time.Duration, error) {
	if strings.TrimSpace(flagVal) != "" {
		return parseTimeout(flagVal)
	}
	if cfg == nil {
		return 0, nil
	}
	return parseTimeout(cfg.Review.Timeout)
}

// withTimeout derives the command's working context. With no timeout
// resolved it is a plain cancel-only child (same lifetime as today); with
// one it carries the deadline that bounds the WHOLE command run — diff
// acquisition, prompt build, provider call, render, and any interactive
// confirmation in between. Callers must defer the returned cancel.
func (app *appContext) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if app == nil || app.Timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, app.Timeout)
}

// wrapTimeoutErr replaces a provider/pipeline error with a self-explaining
// timeout message when the run's own deadline is what actually fired.
// Providers report a deadline in their own dialect (a wrapped
// context.DeadlineExceeded, an SDK error string, or clireview's formatted
// "timed out after") so checking ctx is the one reliable signal. A nil
// error, or a failure with the deadline still unexpired, passes through
// untouched.
func wrapTimeoutErr(ctx context.Context, err error, cat *i18n.Catalog, d time.Duration) error {
	if err == nil || d <= 0 {
		return err
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return err
	}
	if cat == nil {
		return fmt.Errorf("timed out after %s; pass a larger --timeout (or set review.timeout) to allow more time", d)
	}
	return errors.New(cat.T("timeout.exceeded", d.String()))
}

// newProviderWithTimeout builds a provider and, when a timeout is
// resolved, hands it down to the providers that impose a hard cap of
// their own. A context deadline can only SHORTEN a run — the 5-minute
// clireview cap, ollama's http.Client timeout and the Anthropic SDK's
// 10-minute non-streaming ceiling would still fire first — so raising
// --timeout has to reach the provider itself. See provider.TimeoutSetter.
func newProviderWithTimeout(name string, cfg config.ProviderConfig, d time.Duration) (provider.Provider, error) {
	p, err := provider.New(name, cfg)
	if err != nil {
		return nil, err
	}
	if d > 0 {
		if ts, ok := p.(provider.TimeoutSetter); ok {
			ts.SetTimeout(d)
		}
	}
	return p, nil
}
