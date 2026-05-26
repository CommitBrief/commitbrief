package prompt

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/lang"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

func TestBuildSystemContainsRulesOutputAndLangDirective(t *testing.T) {
	rulesLoaded := rules.Loaded{
		Content: "Review for security issues.",
		Source:  rules.SourceFile,
		Hash:    "abc",
	}
	outputLoaded := rules.Loaded{
		Content: "Severity scale: high/medium/low.",
		Source:  rules.SourceDefault,
		Hash:    "def",
	}
	langRes := lang.Resolution{Code: "tr", Name: "Türkçe", Source: lang.SourceRepoConfig}

	p := Build(rulesLoaded, outputLoaded, langRes, "diff body")

	for _, tag := range []string{"<project_rules>", "</project_rules>", "<output_format>", "</output_format>"} {
		if !strings.Contains(p.System, tag) {
			t.Errorf("system missing %q; got:\n%s", tag, p.System)
		}
	}
	if !strings.Contains(p.System, "Review for security issues.") {
		t.Errorf("system missing rules content; got:\n%s", p.System)
	}
	if !strings.Contains(p.System, "Severity scale") {
		t.Errorf("system missing output content; got:\n%s", p.System)
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
	o := rules.Loaded{Content: "out"}
	langRes := lang.Resolution{Code: "en", Name: "English"}

	p := Build(r, o, langRes, "--- a/foo.go\n+++ b/foo.go")

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
	p := Build(rules.Default(), rules.DefaultOutput(), lang.Resolution{Code: "en", Name: "English"}, "")
	if !strings.Contains(p.User, "```diff") {
		t.Error("empty diff should still produce fenced user prompt")
	}
}

func TestEstimatedTokensReasonable(t *testing.T) {
	p := Build(rules.Default(), rules.DefaultOutput(), lang.Resolution{Code: "en", Name: "English"}, "small diff")
	got := p.EstimatedTokens()
	// default.md + output.md + guard ≈ 700-1000 tokens by design. We don't
	// pin an exact number — just assert order of magnitude.
	if got < 500 || got > 2000 {
		t.Errorf("EstimatedTokens = %d, expected ~700-1000", got)
	}
}

func TestExceedsContext(t *testing.T) {
	p := Build(rules.Default(), rules.DefaultOutput(), lang.Resolution{Code: "en", Name: "English"}, "diff")
	if p.ExceedsContext(200_000) {
		t.Error("default rules + output should not exceed 200K context")
	}
	if !p.ExceedsContext(10) {
		t.Error("tiny context window should fail check")
	}
	// Zero context window means "no limit" (provider didn't report)
	if p.ExceedsContext(0) {
		t.Error("zero context window should be treated as no limit, not as zero capacity")
	}
}
