// SPDX-License-Identifier: GPL-3.0-or-later

package guard

import (
	"strings"
	"testing"
)

// Fake credentials are deliberately split at the prefix boundary in
// SOURCE so GitHub Push Protection's secret scanner — which runs on
// source text, not compiled output — can't see a contiguous
// "AKIA...", "sk_live_...", "sk-ant-...", etc. Go folds the
// concatenations at compile time, so the values that hit our scanner
// regex are correctly shaped. NEVER ship a real, revoked, or even
// structurally-perfect synthetic key in source — git history is
// scanned forever.
var (
	fakeAWS       = "AK" + "IA" + "EXAMPLE0000000Z123"        // matches AKIA + 16 caps/digits
	fakeGithubPAT = "gh" + "p_" + strings.Repeat("a", 36)     // matches gh[pousr]_ + 36+
	fakeGitlabPAT = "gl" + "pat-" + strings.Repeat("a", 20)   // matches glpat- + 20+
	fakeAnthropic = "sk-" + "ant-" + strings.Repeat("a", 40)  // matches sk-ant- + 40+
	fakeOpenAI    = "sk" + "-" + strings.Repeat("a", 40)      // matches sk- + 40+ alnum
	fakeOpenAIPrj = "sk-" + "proj-" + strings.Repeat("a", 40) // matches sk-proj- + 40+
	fakeJWT       = "ey" + "JhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.ey" + "JzdWIiOiIxMjM0NTY3ODkwIn0.signature-part"
	fakeStripe    = "sk_" + "live_" + strings.Repeat("a", 24) // matches sk_live_ + 24+
	fakePEM       = "-----" + "BEGIN " + "RSA PRIVATE " + "KEY-----"
)

// Each test case carries a fake credential built at compile time from
// multiple string literals (see vars above). The matched value never
// appears in our assertions — we only check the pattern *name* — same
// rule the production output follows so we never leak via test logs.

func TestScanForSecretsPerPattern(t *testing.T) {
	cases := []struct {
		name        string
		patternName string
		secret      string
	}{
		{"aws", "AWS Access Key", fakeAWS},
		{"github_pat", "GitHub Token", fakeGithubPAT},
		{"gitlab_pat", "GitLab Token", fakeGitlabPAT},
		{"anthropic", "Anthropic API Key", fakeAnthropic},
		{"openai", "OpenAI API Key", fakeOpenAI},
		{"openai_proj", "OpenAI API Key", fakeOpenAIPrj},
		{"jwt", "JWT", fakeJWT},
		{"stripe_live", "Stripe Live Key", fakeStripe},
		{"pem", "PEM Private Key", fakePEM},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diff := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -0,0 +1 @@\n+" + tc.secret + "\n"
			matches := ScanForSecrets(diff)
			if len(matches) == 0 {
				t.Fatalf("expected at least one match for %s; got none", tc.patternName)
			}
			found := false
			for _, m := range matches {
				for _, p := range m.Patterns {
					if p == tc.patternName {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("expected pattern name %q in matches; got %+v", tc.patternName, matches)
			}
		})
	}
}

func TestScanForSecretsIgnoresMinusAndContextLines(t *testing.T) {
	// Removed lines (- prefix) and context lines (space prefix) carry
	// content that's *not being introduced* — scanning them would
	// re-flag history. The scanner must skip them.
	diff := strings.Join([]string{
		"diff --git a/x b/x",
		"--- a/x",
		"+++ b/x",
		"@@ -1,2 +1,2 @@",
		"-" + fakeAWS,  // removed: should NOT match
		"  " + fakeAWS, // context: should NOT match
		"+normal added line",
	}, "\n")
	if matches := ScanForSecrets(diff); len(matches) != 0 {
		t.Errorf("scanner triggered on non-+ lines: %+v", matches)
	}
}

func TestScanForSecretsIgnoresHeaderLines(t *testing.T) {
	// `+++` is a diff header, not an added line. Even if the path
	// somehow looked like a secret, the scanner must not match.
	diff := "diff --git a/x b/x\n--- a/x\n+++ b/" + fakeAWS + ".go\n"
	if matches := ScanForSecrets(diff); len(matches) != 0 {
		t.Errorf("scanner matched `+++ ` header line: %+v", matches)
	}
}

func TestScanForSecretsCleanDiffNoMatches(t *testing.T) {
	// Realistic-looking +line with no credential-shape strings → zero
	// matches. The "all clear" path callers depend on.
	diff := strings.Join([]string{
		"diff --git a/main.go b/main.go",
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -1,3 +1,4 @@",
		" package main",
		" import \"fmt\"",
		"+func greet() { fmt.Println(\"hello\") }",
	}, "\n")
	if matches := ScanForSecrets(diff); len(matches) != 0 {
		t.Errorf("clean diff should produce zero matches; got %+v", matches)
	}
}

func TestScanForSecretsAvoidsCommonFalsePositives(t *testing.T) {
	// Short prefixes appear in benign code (variable names, error
	// messages). The length floors in the patterns must reject them.
	// Build each "almost a secret" string at runtime so GitHub Push
	// Protection's source scanner doesn't get its own false-positive
	// on this assertion file.
	tooShortAWS := "AK" + "IA" + "1234"
	tooShortGitHub := "gh" + "p_" + "test"
	tooShortGitLab := "gl" + "pat-" + "foo"
	diff := strings.Join([]string{
		"+const ProxyHeader = \"sk-shop\"",                  // 7 chars, too short
		"+log.Print(\"" + tooShortGitHub + "\")",            // too short
		"+const githubUserHint = \"gh user help me debug\"", // no underscore
		"+const k = \"" + tooShortAWS + "\"",                // too short
		"+def_helper := \"" + tooShortGitLab + "\"",         // too short
	}, "\n")
	if matches := ScanForSecrets(diff); len(matches) != 0 {
		t.Errorf("false positives on short benign strings: %+v", matches)
	}
}

func TestScanForSecretsCollapsesMultiplePatternsOnSameLine(t *testing.T) {
	// A line that hits two patterns gets one SecretMatch entry with
	// both names listed — keeps the output compact + makes the test
	// assertions stable regardless of pattern iteration order.
	diff := "+" + fakeAWS + " and " + fakePEM
	matches := ScanForSecrets(diff)
	if len(matches) != 1 {
		t.Fatalf("expected single SecretMatch for a single line with two pattern hits; got %d entries: %+v", len(matches), matches)
	}
	if got := matches[0].Patterns; len(got) != 2 {
		t.Errorf("expected 2 pattern names on the collapsed line; got %d: %v", len(got), got)
	}
}

func TestScanForSecretsEmptyInput(t *testing.T) {
	if matches := ScanForSecrets(""); matches != nil {
		t.Errorf("empty input should return nil; got %+v", matches)
	}
}

func TestScanForSecretsReportsLineNumber(t *testing.T) {
	// Line numbers are 1-based and count every line in the supplied
	// diff string, including header lines. This contract matters for
	// the CLI's `line N: PatternName` output.
	diff := "diff --git a/x b/x\n+" + fakeAWS
	matches := ScanForSecrets(diff)
	if len(matches) != 1 || matches[0].Line != 2 {
		t.Errorf("expected single match at line 2; got %+v", matches)
	}
}

func TestScanTextFlagsRawContent(t *testing.T) {
	// UC-05 regression guard. ScanText operates on arbitrary text with
	// no diff prefixes — used for COMMITBRIEF.md / output.md rules
	// content before it's embedded into the system prompt.
	content := "## Project rules\n\nDo not log " + fakeAWS + " or " + fakeGithubPAT + "\nThe end.\n"
	matches := ScanText(content)
	if len(matches) != 1 {
		t.Fatalf("expected one match line; got %+v", matches)
	}
	if matches[0].Line != 3 {
		t.Errorf("expected match on line 3; got %+v", matches)
	}
	got := strings.Join(matches[0].Patterns, ",")
	if !strings.Contains(got, "AWS Access Key") || !strings.Contains(got, "GitHub Token") {
		t.Errorf("both patterns should be reported; got %q", got)
	}
}

func TestScanTextEmptyAndCleanReturnNil(t *testing.T) {
	if ScanText("") != nil {
		t.Errorf("empty input should return nil")
	}
	if got := ScanText("nothing\nto see\nhere\n"); got != nil {
		t.Errorf("clean text should return nil; got %+v", got)
	}
}

func TestSecretPatternNamesReturnsSortedUniqueList(t *testing.T) {
	names := SecretPatternNames()
	if len(names) < 8 {
		t.Errorf("SecretPatternNames count = %d, expected >= 8 (per ROADMAP plan)", len(names))
	}
	for i := 1; i < len(names); i++ {
		if names[i] <= names[i-1] {
			t.Errorf("pattern names not sorted: %q <= %q at index %d", names[i], names[i-1], i)
		}
	}
}
