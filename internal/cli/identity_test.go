// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/guard"
	"github.com/CommitBrief/commitbrief/internal/leaks"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

const identityDiff = `diff --git a/svc.go b/svc.go
--- a/svc.go
+++ b/svc.go
@@ -42,7 +42,7 @@ func (s *Server) Handle(ctx context.Context, req Request) (int, error) {
 	v27 := 27
 	v28 := 28
 	v29 := 29
-	query := "SELECT * FROM t WHERE id=" + req.ID
+	query := "SELECT * FROM t WHERE id=" + req.ID + " LIMIT 1"
 	return run(query)
 }
 
`

// identityDiffShifted is identityDiff with an unrelated line added above the
// hunk, so every line number below it moves down by one.
const identityDiffShifted = `diff --git a/svc.go b/svc.go
--- a/svc.go
+++ b/svc.go
@@ -5,6 +5,7 @@ import "fmt"
 // helper
 func Alpha(a int) int {
+	_ = a
 	return a
 }
 
@@ -42,7 +43,7 @@ func (s *Server) Handle(ctx context.Context, req Request) (int, error) {
 	v27 := 27
 	v28 := 28
 	v29 := 29
-	query := "SELECT * FROM t WHERE id=" + req.ID
+	query := "SELECT * FROM t WHERE id=" + req.ID + " LIMIT 1"
 	return run(query)
 }
 
`

func parseIdentityDiff(t *testing.T, raw string) diff.Diff {
	t.Helper()
	d, err := diff.Parse(git.Diff{Content: raw, Origin: git.OriginDiff})
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	return d
}

func baseIdentityFinding() render.Finding {
	return render.Finding{
		Severity: render.SeverityHigh,
		RuleID:   "sec.injection",
		File:     "svc.go",
		Line:     45,
		Title:    "SQL built by concatenation",
	}
}

func TestStampIdentity_NilPassesThrough(t *testing.T) {
	if got := stampIdentity(parseIdentityDiff(t, identityDiff), nil); got != nil {
		t.Errorf("nil findings must stay nil (degrade signal), got %#v", got)
	}
	if got := stampIdentity(parseIdentityDiff(t, identityDiff), []render.Finding{}); got == nil || len(got) != 0 {
		t.Errorf("empty non-nil findings must stay empty non-nil, got %#v", got)
	}
}

func TestStampIdentity_TitleAndSeverityDoNotMoveFingerprint(t *testing.T) {
	d := parseIdentityDiff(t, identityDiff)
	a := baseIdentityFinding()
	b := baseIdentityFinding()
	b.Title = "A completely different headline"
	b.Severity = render.SeverityLow
	b.Description = "reworded"
	out := stampIdentity(d, []render.Finding{a, b})
	if out[0].Fingerprint == "" {
		t.Fatal("fingerprint not stamped")
	}
	if out[0].Fingerprint != out[1].Fingerprint {
		t.Errorf("title/severity changed the fingerprint: %s vs %s", out[0].Fingerprint, out[1].Fingerprint)
	}
}

func TestStampIdentity_FileRuleSymbolMoveFingerprint(t *testing.T) {
	d := parseIdentityDiff(t, identityDiff)
	base := baseIdentityFinding()

	otherRule := base
	otherRule.RuleID = "sec.xss"
	otherFile := base
	otherFile.File = "other.go"
	// Line 3 is outside the only hunk, so the symbol is empty.
	noSymbol := base
	noSymbol.Line = 3

	out := stampIdentity(d, []render.Finding{base, otherRule, otherFile, noSymbol})
	seen := map[string]int{}
	for i, f := range out {
		if f.Fingerprint == "" {
			t.Fatalf("finding %d has no fingerprint", i)
		}
		if prev, dup := seen[f.Fingerprint]; dup {
			t.Errorf("findings %d and %d share fingerprint %s", prev, i, f.Fingerprint)
		}
		seen[f.Fingerprint] = i
	}
}

func TestStampIdentity_UnknownRuleBecomesGeneralOther(t *testing.T) {
	d := parseIdentityDiff(t, identityDiff)
	empty := baseIdentityFinding()
	empty.RuleID = ""
	unknown := baseIdentityFinding()
	unknown.RuleID = "made.up-rule"
	messy := baseIdentityFinding()
	messy.RuleID = "  SEC.Injection "

	out := stampIdentity(d, []render.Finding{empty, unknown, messy})
	if out[0].RuleID != rules.RuleOther || out[1].RuleID != rules.RuleOther {
		t.Errorf("empty/unknown should become %s, got %q / %q", rules.RuleOther, out[0].RuleID, out[1].RuleID)
	}
	if out[2].RuleID != "sec.injection" {
		t.Errorf("case/whitespace should normalise to sec.injection, got %q", out[2].RuleID)
	}
	if out[0].Fingerprint != out[1].Fingerprint {
		t.Error("empty and unknown ids collapse to the same rule, so they must share a fingerprint")
	}
}

func TestStampIdentity_StableWhenUnrelatedLineAddedAbove(t *testing.T) {
	before := stampIdentity(parseIdentityDiff(t, identityDiff), []render.Finding{baseIdentityFinding()})
	shifted := baseIdentityFinding()
	shifted.Line = 46 // the same statement, one line lower after the insertion above
	after := stampIdentity(parseIdentityDiff(t, identityDiffShifted), []render.Finding{shifted})
	if before[0].Fingerprint != after[0].Fingerprint {
		t.Errorf("an unrelated line above the hunk moved the fingerprint: %s vs %s",
			before[0].Fingerprint, after[0].Fingerprint)
	}
}

func TestLeakFindingsCarryHardcodedSecretRuleID(t *testing.T) {
	res := leaks.Result{Findings: []leaks.Finding{{File: "a.env", Line: 2, Patterns: []string{"aws-access-key"}}}}
	got := leakFindings(res, []guard.UserSecretPattern(nil))
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].RuleID != rules.RuleHardcodedSecret {
		t.Errorf("rule_id = %q, want %q", got[0].RuleID, rules.RuleHardcodedSecret)
	}
	if got[0].Fingerprint != "" {
		t.Errorf("leaks must not stamp a fingerprint, got %q", got[0].Fingerprint)
	}
	if got[0].Snippet != "" {
		t.Error("leaks must never echo the secret in Snippet")
	}
}

func TestEnableDebugLog(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	enableDebugLog(false)
	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug enabled without --verbose")
	}
	enableDebugLog(true)
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug not enabled with --verbose")
	}
}
