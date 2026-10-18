// SPDX-License-Identifier: GPL-3.0-or-later

// Package policy implements the declarative merge-policy gate behind the
// `commitbrief guard` command (ADR-0029). A policy is a small YAML document
// (`.commitbrief/policy.yml`) that caps how many findings of each severity a
// change may carry before it is blocked — a richer, opt-in alternative to the
// single `--fail-on=<severity>` threshold, aimed at gating high-volume
// (AI-authored) pull requests.
//
// The gate evaluates the *actionable* finding set — the findings that survive
// baseline + inline suppression (signal control, ADR-0027) — because that is
// exactly what `--json` emits and what a human reviewer sees. Rule-id-scoped
// allow/deny lists (`rules:`, ADR-0045) key on the closed rule taxonomy id each
// finding carries: `deny` blocks on any matching finding, `allow` waives the
// matching findings (they are excluded from the thresholds and `total` and
// reported as `waived`). Entries are validated against the taxonomy at load
// time, so a typo is a hard error and never silently opens the gate.
package policy

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

// Policy is the parsed `.commitbrief/policy.yml` document.
type Policy struct {
	// Version is the policy schema version. Only 1 is understood today.
	Version int `yaml:"version"`
	// Thresholds maps a severity ("critical".."info") to the maximum number
	// of findings of that severity allowed before the gate blocks. A severity
	// absent from the map — or present with a null/`~` value — is unlimited.
	Thresholds map[render.Severity]*int `yaml:"thresholds"`
	// Total is an optional cap on the total number of actionable findings.
	// nil means no overall cap.
	Total *int `yaml:"total"`
	// Rules is the optional rule-id scoped allow/deny block (ADR-0045).
	Rules RuleLists `yaml:"rules"`
}

// RuleLists is the `rules:` block. Each entry is a canonical taxonomy id
// (`sec.injection`) or a category wildcard (`sec.*`); nothing else.
type RuleLists struct {
	// Deny: any actionable finding matching an entry is a violation,
	// whatever the thresholds say.
	Deny []string `yaml:"deny"`
	// Allow: findings matching an entry are waived — excluded from the
	// thresholds and total, counted in Verdict.Waived. Deny wins over allow
	// when both match a finding.
	Allow []string `yaml:"allow"`
}

// Violation is a single breached limit.
type Violation struct {
	// Severity is the breached severity, "total" for the overall cap, or
	// "rule:<id>" for a denied rule, where <id> is the canonical rule id of
	// the offending finding (never the policy entry, so a "sec.*" entry
	// yields "rule:sec.injection"). One finding is one such violation, with
	// Allowed 0 and Actual 1 (ADR-0045 §6, ADR-0029 §Update).
	Severity string `json:"severity"`
	// Allowed is the configured maximum; Actual is the observed count.
	Allowed int `json:"allowed"`
	Actual  int `json:"actual"`
}

// Verdict is the result of evaluating findings against a Policy.
type Verdict struct {
	Passed     bool           `json:"passed"`
	Counts     map[string]int `json:"counts"`
	Total      int            `json:"total"`
	Violations []Violation    `json:"violations"`
	// Waived is the number of findings an `allow` entry exempted from the
	// thresholds and Total. Counts and Total describe only the remainder.
	Waived int `json:"waived"`
}

// Load reads and validates a policy file. A missing file is reported as a
// distinct error (errors.Is(err, ErrNotFound)) so the caller can tell
// "no policy configured" from "policy is malformed".
func Load(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, fmt.Errorf("read policy: %w", err)
	}
	return Parse(data)
}

// ErrNotFound is returned by Load when the policy file does not exist.
var ErrNotFound = errors.New("policy file not found")

// Parse decodes + validates a policy document. Unknown keys are rejected so a
// typo (e.g. "threshold:") is a hard error, never a silent no-op.
func Parse(data []byte) (*Policy, error) {
	var p Policy
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	if p.Version != 1 {
		return nil, fmt.Errorf("parse policy: unsupported version %d (want 1)", p.Version)
	}
	for sev, max := range p.Thresholds {
		if !sev.IsValid() {
			return nil, fmt.Errorf("parse policy: unknown severity %q in thresholds", sev)
		}
		if max != nil && *max < 0 {
			return nil, fmt.Errorf("parse policy: threshold for %q must be >= 0", sev)
		}
	}
	if p.Total != nil && *p.Total < 0 {
		return nil, errors.New("parse policy: total must be >= 0")
	}
	if err := p.Rules.validate(); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	return &p, nil
}

// validate checks every entry against the closed taxonomy and rejects an exact
// id that sits in both lists. Entries are normalised in place (trimmed,
// lowercased) and deduplicated after normalisation, so matching is a plain
// comparison and a repeated entry (or an alias of an earlier one) is harmless.
func (l *RuleLists) validate() error {
	for _, list := range []struct {
		name    string
		entries *[]string
	}{{"deny", &l.Deny}, {"allow", &l.Allow}} {
		seen := make(map[string]struct{}, len(*list.entries))
		uniq := (*list.entries)[:0:0]
		for _, e := range *list.entries {
			norm, ok := normaliseEntry(e)
			if !ok {
				return fmt.Errorf("rules.%s: unknown rule id or category %q", list.name, e)
			}
			if _, dup := seen[norm]; dup {
				continue
			}
			seen[norm] = struct{}{}
			uniq = append(uniq, norm)
		}
		*list.entries = uniq
	}
	deny := make(map[string]struct{}, len(l.Deny))
	for _, e := range l.Deny {
		deny[e] = struct{}{}
	}
	for _, e := range l.Allow {
		if _, dup := deny[e]; dup {
			return fmt.Errorf("rules: %q is in both allow and deny", e)
		}
	}
	return nil
}

// normaliseEntry validates one allow/deny entry: a canonical taxonomy id
// (aliases resolve to their canonical form) or `<category>.*` for an existing
// category. Anything else — including a bare category, a partial glob or an
// unknown id — is rejected.
func normaliseEntry(raw string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if cat, ok := strings.CutSuffix(e, ".*"); ok {
		for _, r := range rules.Taxonomy() {
			if r.Category() == cat {
				return e, true
			}
		}
		return "", false
	}
	id, fallback := rules.NormaliseRuleID(e)
	if fallback {
		return "", false
	}
	return id, true
}

// matchEntry reports whether the (already normalised) finding rule id is
// covered by a validated entry.
func matchEntry(entry, id string) bool {
	if cat, ok := strings.CutSuffix(entry, ".*"); ok {
		return strings.HasPrefix(id, cat+".")
	}
	return entry == id
}

// anyEntry reports whether any entry covers id.
func anyEntry(entries []string, id string) bool {
	for _, e := range entries {
		if matchEntry(e, id) {
			return true
		}
	}
	return false
}

// Evaluate counts the findings by severity and checks them against the policy.
// findings must be the actionable set (post baseline + suppression). Findings
// waived by rules.allow are excluded from Counts, Total and every limit and
// reported in Waived. The returned Verdict is deterministic: Counts always has
// all five severities, and Violations are ordered critical→info, then "total",
// then one "rule:<id>" per denied finding, alphabetically by id.
func (p *Policy) Evaluate(findings []render.Finding) Verdict {
	order := []render.Severity{
		render.SeverityCritical, render.SeverityHigh, render.SeverityMedium,
		render.SeverityLow, render.SeverityInfo,
	}
	counts := map[string]int{}
	for _, sev := range order {
		counts[string(sev)] = 0
	}

	// Rule lists first. A finding without a rule id (an old binary's JSON fed
	// to `guard --from-json`) counts as general.other. Deny wins over allow:
	// a denied finding is never waived, so an allow wildcard cannot shadow a
	// narrower deny entry (fail-closed).
	var denied []string // canonical rule id of each denied finding
	var kept []render.Finding
	waived := 0
	for _, f := range findings {
		id, _ := rules.NormaliseRuleID(f.RuleID)
		isDenied := anyEntry(p.Rules.Deny, id)
		if isDenied {
			denied = append(denied, id)
		}
		if !isDenied && anyEntry(p.Rules.Allow, id) {
			waived++
			continue
		}
		kept = append(kept, f)
	}
	for _, f := range kept {
		counts[string(f.Severity)]++
	}

	var violations []Violation
	for _, sev := range order {
		max, ok := p.Thresholds[sev]
		if !ok || max == nil {
			continue // unlimited
		}
		if actual := counts[string(sev)]; actual > *max {
			violations = append(violations, Violation{Severity: string(sev), Allowed: *max, Actual: actual})
		}
	}
	total := len(kept)
	if p.Total != nil && total > *p.Total {
		violations = append(violations, Violation{Severity: "total", Allowed: *p.Total, Actual: total})
	}

	// Keep severity violations in canonical order, then total, then the denied
	// rules alphabetically by rule id. The rule violations are appended after
	// the sort; sorting the ids makes their order independent of finding order.
	sort.SliceStable(violations, func(i, j int) bool {
		return severityRank(violations[i].Severity) < severityRank(violations[j].Severity)
	})
	sort.Strings(denied)
	for _, id := range denied {
		violations = append(violations, Violation{Severity: "rule:" + id, Allowed: 0, Actual: 1})
	}

	return Verdict{
		Passed:     len(violations) == 0,
		Counts:     counts,
		Total:      total,
		Violations: violations,
		Waived:     waived,
	}
}

func severityRank(s string) int {
	switch render.Severity(s) {
	case render.SeverityCritical:
		return 0
	case render.SeverityHigh:
		return 1
	case render.SeverityMedium:
		return 2
	case render.SeverityLow:
		return 3
	case render.SeverityInfo:
		return 4
	default: // "total"
		return 5
	}
}
