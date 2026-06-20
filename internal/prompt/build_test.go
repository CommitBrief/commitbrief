// SPDX-License-Identifier: GPL-3.0-or-later

package prompt

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/arch"
	"github.com/CommitBrief/commitbrief/internal/cache"
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

	p := Build(rulesLoaded, langRes, "diff body", "")

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

func TestBuildPlainTextContextGating(t *testing.T) {
	r := rules.Loaded{Content: "rules"}
	langRes := lang.Resolution{Code: "en", Name: "English"}

	off := BuildPlainText(r, langRes, "diff", "", false)
	if strings.Contains(off.System, "PROJECT CONTEXT ACCESS") {
		t.Error("diff-only plain-text prompt must NOT include the context section")
	}

	on := BuildPlainText(r, langRes, "diff", "", true)
	if !strings.Contains(on.System, "PROJECT CONTEXT ACCESS") {
		t.Error("context plain-text prompt must include the context section")
	}
	// The context section must keep the diff as the subject and forbid writes.
	for _, want := range []string{"ONLY the changes", "untrusted data", "Do not modify"} {
		if !strings.Contains(on.System, want) {
			t.Errorf("context section missing guard phrase %q", want)
		}
	}
}

func TestBuildUserContainsDiff(t *testing.T) {
	r := rules.Loaded{Content: "rules"}
	langRes := lang.Resolution{Code: "en", Name: "English"}

	p := Build(r, langRes, "--- a/foo.go\n+++ b/foo.go", "")

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
	p := Build(rules.Default(), lang.Resolution{Code: "en", Name: "English"}, "", "")
	if !strings.Contains(p.User, "```diff") {
		t.Error("empty diff should still produce fenced user prompt")
	}
}

func TestEstimatedTokensReasonable(t *testing.T) {
	p := Build(rules.Default(), lang.Resolution{Code: "en", Name: "English"}, "small diff", "")
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
	p := Build(rules.Default(), lang.Resolution{Code: "en", Name: "English"}, "diff", "")
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

// TestBuildArchitectureContextInjection verifies the ADR-0030 architecture
// block is wrapped in <architecture_constraints> when supplied, and that an
// empty archContext leaves the system prompt byte-identical to before (so no
// existing cache key is invalidated).
func TestBuildArchitectureContextInjection(t *testing.T) {
	r := rules.Loaded{Content: "rules"}
	langRes := lang.Resolution{Code: "en", Name: "English"}
	archBlock := "domain may not import db"

	with := Build(r, langRes, "diff", archBlock)
	without := Build(r, langRes, "diff", "")

	if !strings.Contains(with.System, "<architecture_constraints>") ||
		!strings.Contains(with.System, "</architecture_constraints>") {
		t.Errorf("architecture block not wrapped in tags; got:\n%s", with.System)
	}
	if !strings.Contains(with.System, archBlock) {
		t.Errorf("architecture content missing; got:\n%s", with.System)
	}
	if strings.Contains(without.System, "<architecture_constraints>") {
		t.Errorf("empty archContext must emit no architecture block; got:\n%s", without.System)
	}
	if with.System == without.System {
		t.Error("supplying architecture context must change the system prompt (cache-key driver)")
	}
	// The block sits between project_rules and severity_rubric.
	ai := strings.Index(with.System, "<architecture_constraints>")
	ri := strings.Index(with.System, "<severity_rubric>")
	pi := strings.Index(with.System, "</project_rules>")
	if pi >= ai || ai >= ri {
		t.Errorf("architecture block out of order (want project_rules < arch < rubric); indices p=%d a=%d r=%d", pi, ai, ri)
	}
}

// TestBuildPlainTextArchitectureContext verifies the CLI-provider prompt also
// carries the architecture block.
func TestBuildPlainTextArchitectureContext(t *testing.T) {
	r := rules.Loaded{Content: "rules"}
	langRes := lang.Resolution{Code: "en", Name: "English"}

	with := BuildPlainText(r, langRes, "diff", "domain may not import db", false)
	if !strings.Contains(with.System, "<architecture_constraints>") {
		t.Errorf("plain-text prompt missing architecture block; got:\n%s", with.System)
	}
	without := BuildPlainText(r, langRes, "diff", "", false)
	if strings.Contains(without.System, "<architecture_constraints>") {
		t.Errorf("empty archContext must emit no block in plain-text prompt; got:\n%s", without.System)
	}
}

// TestArchitectureChangesCacheKey is the end-to-end ADR-0030 invariant: a
// change to architecture.json must invalidate stale cached reviews. The arch
// block folds into the system prompt, which is a keyed cache-key field, so
// (a) adding architecture context changes the key vs. none, and (b) editing
// architecture.json (two different configs) yields two different keys. Built
// from the public Summarize → Build → cache.Compute chain, exactly as the
// review pipeline does it.
func TestArchitectureChangesCacheKey(t *testing.T) {
	r := rules.Default()
	langRes := lang.Resolution{Code: "en", Name: "English"}
	const diffText = "@@ -1 +1 @@\n+import x"

	keyFor := func(archJSON string) string {
		ctx := arch.Summarize([]byte(archJSON))
		p := Build(r, langRes, diffText, ctx)
		return cache.Compute(cache.ComputeArgs{
			Diff:         diffText,
			SystemPrompt: p.System,
			Provider:     "anthropic",
			Model:        "claude-opus-4-8",
			Lang:         "en",
		})
	}

	none := keyFor("")
	archA := keyFor(`{"layers":{"domain":["internal/domain"],"db":["internal/db"]},"rules":{"domain":[],"db":["domain"]}}`)
	archB := keyFor(`{"layers":{"domain":["internal/domain"],"db":["internal/db"]},"rules":{"domain":["db"],"db":["domain"]}}`)

	if none == archA {
		t.Error("adding architecture context must change the cache key")
	}
	if archA == archB {
		t.Error("editing architecture.json (different rules) must change the cache key")
	}

	// And it stays deterministic: same config → same key (no stale-miss churn).
	if archA != keyFor(`{"layers":{"domain":["internal/domain"],"db":["internal/db"]},"rules":{"domain":[],"db":["domain"]}}`) {
		t.Error("the same architecture.json must produce a stable cache key")
	}
}
