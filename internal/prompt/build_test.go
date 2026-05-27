// SPDX-License-Identifier: GPL-3.0-or-later

package prompt

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/lang"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

func TestBuildSystemContainsRulesAndContract(t *testing.T) {
	rulesLoaded := rules.Loaded{
		Content: "Review for security issues.",
		Source:  rules.SourceFile,
		Hash:    "abc",
	}
	langRes := lang.Resolution{Code: "tr", Name: "Türkçe", Source: lang.SourceRepoConfig}

	p := Build(rulesLoaded, langRes, "diff body")

	wantTags := []string{
		"<project_rules>", "</project_rules>",
		"<severity_rubric>", "</severity_rubric>",
		"<response_format>", "</response_format>",
	}
	for _, tag := range wantTags {
		if !strings.Contains(p.System, tag) {
			t.Errorf("system missing %q; got:\n%s", tag, p.System)
		}
	}
	if !strings.Contains(p.System, "Review for security issues.") {
		t.Errorf("system missing rules content; got:\n%s", p.System)
	}
	if strings.Contains(p.System, "<output_format>") {
		t.Errorf("system should not include <output_format> after ADR-0014; got:\n%s", p.System)
	}
	for _, sev := range []string{"critical", "high", "medium", "low", "info"} {
		if !strings.Contains(p.System, sev) {
			t.Errorf("severity rubric missing level %q\n%s", sev, p.System)
		}
	}
	if !strings.Contains(p.System, `"findings"`) {
		t.Errorf("response_format missing JSON schema sentinel; got:\n%s", p.System)
	}
	if !strings.Contains(p.System, "Respond in Türkçe (ISO tr)") {
		t.Errorf("system missing lang directive; got:\n%s", p.System)
	}
	if !strings.Contains(p.System, "immutable") {
		t.Errorf("prompt-injection guard line missing; got:\n%s", p.System)
	}
}

func TestBuildUserContainsDiff(t *testing.T) {
	r := rules.Loaded{Content: "rules"}
	langRes := lang.Resolution{Code: "en", Name: "English"}

	p := Build(r, langRes, "--- a/foo.go\n+++ b/foo.go")

	if !strings.Contains(p.User, "--- a/foo.go") {
		t.Errorf("user missing diff content; got:\n%s", p.User)
	}
	if !strings.Contains(p.User, "```diff") {
		t.Errorf("user missing diff fence; got:\n%s", p.User)
	}
	if !strings.HasPrefix(p.User, "Diff to review:") {
		t.Errorf("user should start with 'Diff to review:'; got:\n%s", p.User)
	}
}

func TestBuildEmptyDiff(t *testing.T) {
	p := Build(rules.Default(), lang.Resolution{Code: "en", Name: "English"}, "")
	if !strings.Contains(p.User, "```diff") {
		t.Error("empty diff should still produce fenced user prompt")
	}
}

func TestEstimatedTokensReasonable(t *testing.T) {
	p := Build(rules.Default(), lang.Resolution{Code: "en", Name: "English"}, "small diff")
	got := p.EstimatedTokens()
	// default.md (expanded in v0.9.0 with the optimization +
	// adversarial-security depth) + severity rubric + JSON contract
	// (now with `suggestion`) + lang directive + guard ≈ 2500–3000
	// tokens. We don't pin an exact number — just assert order of
	// magnitude. Floor catches accidental empty-prompt regressions;
	// ceiling catches accidental prompt-bloat regressions.
	if got < 1500 || got > 4000 {
		t.Errorf("EstimatedTokens = %d, expected ~2500-3000", got)
	}
}

func TestExceedsContext(t *testing.T) {
	p := Build(rules.Default(), lang.Resolution{Code: "en", Name: "English"}, "diff")
	if p.ExceedsContext(200_000) {
		t.Error("default rules + contract should not exceed 200K context")
	}
	if !p.ExceedsContext(10) {
		t.Error("tiny context window should fail check")
	}
	// Zero context window means "no limit" (provider didn't report)
	if p.ExceedsContext(0) {
		t.Error("zero context window should be treated as no limit, not as zero capacity")
	}
}
