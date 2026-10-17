// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"log/slog"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

// stampIdentity gives every structured finding its ADR-0045 identity: a
// canonical rule_id and a fingerprint.
//
// It is the single call site both review exits (fresh response and cache hit)
// and the remote PR paths share, so a finding's identity cannot depend on
// which path produced it. It runs after the findings are assembled (LLM +
// deterministic flaky merge) and BEFORE signal control (baseline / inline
// suppression), which is where later phases key on the fingerprint.
//
// rule_id: trimmed, lowercased, alias-resolved; an empty or unknown value
// becomes general.other. That is deliberately not a parse error — the model
// being loose about a label must never trigger the ADR-0031 repair retry or
// degrade an otherwise valid review.
//
// fingerprint: sha256(path, rule_id, symbol) where symbol is derived from the
// parsed diff (diff.Diff.SymbolFor). It is computed here, never read from the
// model and never stored in the cache entry: a cache hit recomputes it from
// the cached findings plus the current diff.
//
// A nil slice (degrade / plain text: no findings array) passes through
// untouched so downstream code keeps treating it as a degrade.
func stampIdentity(parsed diff.Diff, findings []render.Finding) []render.Finding {
	if findings == nil {
		return nil
	}
	normalised := 0
	for i := range findings {
		f := &findings[i]
		id, fallback := rules.NormaliseRuleID(f.RuleID)
		if fallback {
			normalised++
		}
		f.RuleID = id
		f.Fingerprint = render.Fingerprint(f.File, id, parsed.SymbolFor(f.File, f.Line))
	}
	if normalised > 0 {
		slog.Debug("rule_id normalised to fallback", "count", normalised, "fallback", rules.RuleOther, "findings", len(findings))
	}
	return findings
}
