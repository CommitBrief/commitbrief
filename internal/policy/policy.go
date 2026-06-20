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
// allow/deny lists are intentionally out of scope: findings carry no stable
// rule identifier (render.Finding is free-form LLM output), so a name-based
// rule list would be unreliable; that waits on a finding rule-id field.
package policy

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/CommitBrief/commitbrief/internal/render"
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
}

// Violation is a single breached limit.
type Violation struct {
	// Severity is the breached severity, or "total" for the overall cap.
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
	return &p, nil
}

// Evaluate counts the findings by severity and checks them against the policy.
// findings must be the actionable set (post baseline + suppression). The
// returned Verdict is deterministic: Counts always has all five severities,
// and Violations are ordered critical→info, then "total".
func (p *Policy) Evaluate(findings []render.Finding) Verdict {
	order := []render.Severity{
		render.SeverityCritical, render.SeverityHigh, render.SeverityMedium,
		render.SeverityLow, render.SeverityInfo,
	}
	counts := map[string]int{}
	for _, sev := range order {
		counts[string(sev)] = 0
	}
	for _, f := range findings {
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
	total := len(findings)
	if p.Total != nil && total > *p.Total {
		violations = append(violations, Violation{Severity: "total", Allowed: *p.Total, Actual: total})
	}

	// Keep severity violations in canonical order; total (added last) already
	// trails them. Sort defensively in case map iteration ever reorders.
	sort.SliceStable(violations, func(i, j int) bool {
		return severityRank(violations[i].Severity) < severityRank(violations[j].Severity)
	})

	return Verdict{
		Passed:     len(violations) == 0,
		Counts:     counts,
		Total:      total,
		Violations: violations,
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
