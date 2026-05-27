// SPDX-License-Identifier: GPL-3.0-or-later

package compress

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

// fakeProvider is a stripped-down provider.Provider used only by this
// package's tests. The mock provider in internal/provider/mock would do
// fine, but importing it here pulls in a dependency we don't need.
type fakeProvider struct {
	response provider.Response
	err      error
	gotReq   provider.Request
}

func (f *fakeProvider) Name() string                         { return "fake" }
func (f *fakeProvider) DefaultModel() string                 { return "fake-1" }
func (f *fakeProvider) ContextWindow(string) int             { return 100_000 }
func (f *fakeProvider) EstimateTokens(s string) int          { return len(s) / 4 }
func (f *fakeProvider) Pricing(string) provider.Pricing      { return provider.Pricing{} }
func (f *fakeProvider) TestConnection(context.Context) error { return nil }
func (f *fakeProvider) Review(_ context.Context, req provider.Request) (provider.Response, error) {
	f.gotReq = req
	if f.err != nil {
		return provider.Response{}, f.err
	}
	return f.response, nil
}

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"":           LevelBalanced,
		"balanced":   LevelBalanced,
		"BALANCED":   LevelBalanced,
		" light ":    LevelLight,
		"aggressive": LevelAggressive,
	}
	for in, want := range cases {
		got, err := ParseLevel(in)
		if err != nil {
			t.Errorf("ParseLevel(%q) err = %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParseLevel("nuclear"); err == nil {
		t.Error("ParseLevel should reject unknown levels")
	}
}

func TestLevelString(t *testing.T) {
	if LevelLight.String() != "light" || LevelBalanced.String() != "balanced" || LevelAggressive.String() != "aggressive" {
		t.Error("Level.String() round-trip broken")
	}
}

func TestEmbeddedPromptsNonEmpty(t *testing.T) {
	for _, l := range []Level{LevelLight, LevelBalanced, LevelAggressive} {
		p := systemPrompt(l)
		if len(p) < 200 {
			t.Errorf("systemPrompt(%v) suspiciously short: %d chars", l, len(p))
		}
		if !strings.Contains(p, "<user_rules>") {
			t.Errorf("systemPrompt(%v) missing prompt-injection guard <user_rules>", l)
		}
	}
}

func TestRunHappyPath(t *testing.T) {
	original := "# Rules\n\n" + strings.Repeat("This is a long redundant paragraph that needs trimming. ", 30) + "\n"
	compressed := "# Rules\n\nTrimmed.\n"
	p := &fakeProvider{response: provider.Response{Content: compressed, Usage: provider.Usage{InputTokens: 500, OutputTokens: 50}}}

	res, err := Run(context.Background(), p, Request{Original: original})
	if err != nil {
		t.Fatal(err)
	}
	if res.Aborted {
		t.Errorf("expected non-aborted; reason=%s", res.AbortReason)
	}
	if res.CompressedContent != compressed {
		t.Errorf("CompressedContent = %q, want %q", res.CompressedContent, compressed)
	}
	if res.OriginalChars != len(original) || res.CompressedChars != len(compressed) {
		t.Errorf("char counts wrong: %d / %d", res.OriginalChars, res.CompressedChars)
	}
	percent, delta := res.Savings()
	if percent <= 0 || delta <= 0 {
		t.Errorf("Savings = (%f%%, %d), want positive", percent, delta)
	}
}

func TestRunAbortsWhenLarger(t *testing.T) {
	original := "short"
	bloated := original + " plus a lot more text that makes it bigger"
	p := &fakeProvider{response: provider.Response{Content: bloated}}

	res, _ := Run(context.Background(), p, Request{Original: original})
	if !res.Aborted {
		t.Error("expected Aborted=true when output is larger than input")
	}
	if !strings.Contains(res.AbortReason, "not smaller") {
		t.Errorf("AbortReason = %q", res.AbortReason)
	}
}

func TestRunWrapsRulesInUserRulesBlock(t *testing.T) {
	p := &fakeProvider{response: provider.Response{Content: "short"}}
	_, _ = Run(context.Background(), p, Request{Original: "x"})
	if !strings.Contains(p.gotReq.UserPrompt, "<user_rules>") || !strings.Contains(p.gotReq.UserPrompt, "</user_rules>") {
		t.Errorf("UserPrompt missing <user_rules> wrap: %q", p.gotReq.UserPrompt)
	}
}

func TestRunUsesCorrectSystemPrompt(t *testing.T) {
	p := &fakeProvider{response: provider.Response{Content: "ok"}}
	_, _ = Run(context.Background(), p, Request{Original: "long content that is at least longer than the response above", Level: LevelAggressive})
	if !strings.Contains(p.gotReq.SystemPrompt, "aggressive") && !strings.Contains(p.gotReq.SystemPrompt, "Goal: maximum size") {
		t.Errorf("aggressive system prompt not selected")
	}
}

func TestRunEmptyOriginalErrors(t *testing.T) {
	p := &fakeProvider{}
	if _, err := Run(context.Background(), p, Request{Original: ""}); err == nil {
		t.Error("empty original should error")
	}
}

func TestRunNilProviderErrors(t *testing.T) {
	if _, err := Run(context.Background(), nil, Request{Original: "x"}); err == nil {
		t.Error("nil provider should error")
	}
}

func TestPostProcessStripsPreamble(t *testing.T) {
	cases := map[string]string{
		"Here is the compressed file:\n# Rules\n":          "# Rules\n",
		"Sure, here is the compressed version:\n# Rules\n": "# Rules\n",
		"# Rules\nNo preamble at all\n":                    "# Rules\nNo preamble at all\n",
		"```markdown\n# Rules\nWrapped\n```":               "# Rules\nWrapped\n",
		"```\n# Rules\nUnnamed fence\n```":                 "# Rules\nUnnamed fence\n",
	}
	for in, want := range cases {
		got := postProcess(in)
		if got != want {
			t.Errorf("postProcess input:\n%q\ngot:\n%q\nwant:\n%q", in, got, want)
		}
	}
}

func TestSavingsWithZeroOriginal(t *testing.T) {
	r := Result{OriginalTokens: 0, CompressedTokens: 0}
	if pct, d := r.Savings(); pct != 0 || d != 0 {
		t.Errorf("Savings with zero original = (%f, %d), want (0, 0)", pct, d)
	}
}

func TestBackupTimestampPortable(t *testing.T) {
	ts := BackupTimestamp(time.Date(2026, 5, 26, 14, 30, 0, 0, time.UTC))
	if ts != "2026-05-26T14-30-00Z" {
		t.Errorf("BackupTimestamp = %q, want 2026-05-26T14-30-00Z (no colons for Windows)", ts)
	}
	if strings.Contains(ts, ":") {
		t.Error("Backup timestamp must not contain colons (Windows-incompatible)")
	}
}

func TestApplyWritesBackupAndCompressed(t *testing.T) {
	repo := t.TempDir()
	original := "# Original\n\nLots of content here.\n"
	compressed := "# Compressed\n"

	if err := os.WriteFile(filepath.Join(repo, "COMMITBRIEF.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	r := Result{OriginalContent: original, CompressedContent: compressed}
	ts := BackupTimestamp(time.Date(2026, 5, 26, 14, 30, 0, 0, time.UTC))
	rulesPath, backupPath, err := Apply(repo, r, "", ts)
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != compressed {
		t.Errorf("rulesPath content = %q, want %q", got, compressed)
	}

	bgot, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(bgot) != original {
		t.Errorf("backup content = %q, want %q", bgot, original)
	}
	if !strings.Contains(filepath.ToSlash(backupPath), ".commitbrief/backups") {
		t.Errorf("backupPath = %q, expected to be under .commitbrief/backups/", backupPath)
	}
	if !strings.Contains(backupPath, ts) {
		t.Errorf("backupPath = %q, expected to contain timestamp %s", backupPath, ts)
	}
}

func TestApplyRefusesAborted(t *testing.T) {
	r := Result{Aborted: true, AbortReason: "test"}
	_, _, err := Apply(t.TempDir(), r, "", "ts")
	if err == nil || !strings.Contains(err.Error(), "test") {
		t.Errorf("Apply on aborted result: err = %v", err)
	}
}

func TestApplyRequiresRepoRoot(t *testing.T) {
	if _, _, err := Apply("", Result{CompressedContent: "x", OriginalContent: "y"}, "", "ts"); err == nil {
		t.Error("Apply with empty repoRoot should error")
	}
}
