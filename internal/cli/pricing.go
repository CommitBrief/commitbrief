// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

// resolvePricing returns the effective per-model pricing: the provider's
// built-in rate table, with any user override from
// `providers.<active>.pricing.<model>` merged on top (OQ-09). Only
// non-zero override fields apply, so a partial override (e.g. just
// output_per_1m) keeps the built-in value for the rest. Used everywhere a
// dollar figure is computed (cost preflight, verbose footer, cached cost,
// dry-run, compress savings) so the override is honored uniformly.
func resolvePricing(cfg *config.Config, prov provider.Provider, model string) provider.Pricing {
	base := prov.Pricing(model)
	if cfg == nil {
		return base
	}
	pc, ok := cfg.Providers[cfg.Provider]
	if !ok {
		return base
	}
	mp, ok := pc.Pricing[model]
	if !ok {
		return base
	}
	if mp.InputPer1M != 0 {
		base.InputPer1M = mp.InputPer1M
	}
	if mp.OutputPer1M != 0 {
		base.OutputPer1M = mp.OutputPer1M
	}
	if mp.CachedInputPer1M != 0 {
		base.CachedInputPer1M = mp.CachedInputPer1M
	}
	return base
}

// resolveContextWindow returns the effective context window for a model:
// `providers.<active>.context_window` when it is positive, otherwise the
// provider's own table. Same shape as resolvePricing — a config value
// replaces the built-in, an unset (zero) field leaves it untouched, so a
// config without the key behaves exactly as before. The override is inert
// for the *-cli providers (their window is the wrapped binary's business,
// like api_key). A non-positive result means "unknown" and ExceedsContext
// then never triggers the preflight.
func resolveContextWindow(cfg *config.Config, prov provider.Provider, model string) int {
	if cfg != nil && !provider.IsCLI(prov.Name()) {
		if pc, ok := cfg.Providers[cfg.Provider]; ok && pc.ContextWindow > 0 {
			return pc.ContextWindow
		}
	}
	return prov.ContextWindow(model)
}

// costIsUnknown reports whether a dollar figure for this run would be a
// fabrication rather than a number: the provider declares its prices only
// through user config (Metadata.PricingUserDeclared — openai-compatible)
// and the user declared none for this model. Providers with a legitimate
// zero price (ollama, the *-cli providers) are never "unknown". Note that a
// literal 0.0 price in config cannot be told apart from "unset" —
// resolvePricing treats zero as unset — so it also reads as unknown.
func costIsUnknown(cfg *config.Config, prov provider.Provider, model string) bool {
	m, ok := provider.MetadataFor(prov.Name())
	if !ok || !m.PricingUserDeclared {
		return false
	}
	pr := resolvePricing(cfg, prov, model)
	return pr.InputPer1M == 0 && pr.OutputPer1M == 0
}

// insecureRemoteBaseURL reports whether raw is a plain-http URL whose host
// is not a loopback address: the diff (source code) and any bearer token
// would cross the network unencrypted (ADR-0001 §Update 2026-10-17). It is
// a warning condition only — the endpoint is never refused.
func insecureRemoteBaseURL(raw string) (host string, insecure bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return "", false
	}
	h := u.Hostname()
	if strings.EqualFold(h, "localhost") {
		return "", false
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return "", false
	}
	return u.Host, true
}

// insecureHTTPNote returns the localized plain-http warning for the named
// provider's configured base_url, or "" when there is nothing to warn about
// (not a user-declared-endpoint provider, https, or loopback).
func insecureHTTPNote(app *appContext, name string) string {
	m, ok := provider.MetadataFor(name)
	if !ok || !m.PricingUserDeclared {
		return ""
	}
	host, bad := insecureRemoteBaseURL(app.Config.Providers[name].BaseURL)
	if !bad {
		return ""
	}
	return app.Catalog.T("provider.insecure_http_note", name, host)
}

// noteInsecureHTTP prints only the plain-http warning, once, for commands
// that send the diff to the provider but show no cost and run no token
// preflight (commit, summary). Shown even under --quiet (security warning).
func noteInsecureHTTP(cmd *cobra.Command, app *appContext, prov provider.Provider, prog *ui.Progress) {
	n := insecureHTTPNote(app, prov.Name())
	if n == "" {
		return
	}
	if prog != nil {
		prog.Pause()
		defer prog.Resume()
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), n)
}

// noteUndeclaredLimits prints, at most once per run, the stderr notes for a
// provider whose endpoint, prices and context window only the user can
// declare (openai-compatible):
//   - a plain-http, non-loopback base_url (code and token travel in the
//     clear; shown even under --quiet because it is a security warning),
//   - no declared price: the cost will read "unknown" rather than $0.0000,
//   - (tokenPreflight paths only) no declared context window: the token
//     preflight has nothing to compare against.
//
// Silent for every other provider. prog may be nil for commands that run no
// progress display. Call it exactly once per run, after the model is
// resolved.
func noteUndeclaredLimits(cmd *cobra.Command, app *appContext, prov provider.Provider, model string, prog *ui.Progress, tokenPreflight bool) {
	m, ok := provider.MetadataFor(prov.Name())
	if !ok || !m.PricingUserDeclared {
		return
	}
	var notes []string
	if n := insecureHTTPNote(app, prov.Name()); n != "" {
		notes = append(notes, n)
	}
	if !global.quiet {
		if costIsUnknown(app.Config, prov, model) {
			notes = append(notes, app.Catalog.T("cost.unknown_note", prov.Name(), model))
		}
		if tokenPreflight && app.Config.Guard.TokenPreflight && resolveContextWindow(app.Config, prov, model) <= 0 {
			notes = append(notes, app.Catalog.T("guard.tokens.window_unknown_note", prov.Name()))
		}
	}
	if len(notes) == 0 {
		return
	}
	if prog != nil {
		prog.Pause()
		defer prog.Resume()
	}
	for _, n := range notes {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), n)
	}
}
