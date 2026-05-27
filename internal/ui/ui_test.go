// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/i18n"
)

func TestParseColorMode(t *testing.T) {
	cases := map[string]ColorMode{
		"always": ColorAlways,
		"never":  ColorNever,
		"auto":   ColorAuto,
		"":       ColorAuto,
		"bogus":  ColorAuto,
	}
	for in, want := range cases {
		if got := ParseColorMode(in); got != want {
			t.Errorf("ParseColorMode(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestColorEnabledNeverWins(t *testing.T) {
	if ColorEnabled(&bytes.Buffer{}, ColorNever) {
		t.Error("ColorNever should always disable")
	}
}

func TestColorEnabledAlwaysWinsOverNonTTY(t *testing.T) {
	if !ColorEnabled(&bytes.Buffer{}, ColorAlways) {
		t.Error("ColorAlways should enable even on non-TTY")
	}
}

func TestColorEnabledAutoOffOnNonTTY(t *testing.T) {
	if ColorEnabled(&bytes.Buffer{}, ColorAuto) {
		t.Error("ColorAuto on a non-TTY writer should be false")
	}
}

func TestColorEnabledRespectsNoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if ColorEnabled(&bytes.Buffer{}, ColorAlways) {
		t.Error("NO_COLOR=1 must override ColorAlways")
	}
}

func TestColorEnabledRespectsCommitbriefNoColor(t *testing.T) {
	t.Setenv("COMMITBRIEF_NO_COLOR", "1")
	if ColorEnabled(&bytes.Buffer{}, ColorAlways) {
		t.Error("COMMITBRIEF_NO_COLOR=1 must override ColorAlways")
	}
}

func TestEnableANSINonFile(t *testing.T) {
	// Non-*os.File writers are no-op'd; should not error.
	if err := EnableANSI(&bytes.Buffer{}); err != nil {
		t.Errorf("EnableANSI on bytes.Buffer = %v, want nil", err)
	}
}

func TestAskYesNoAssumeYes(t *testing.T) {
	got, err := AskYesNo(strings.NewReader(""), io.Discard, "Continue?", AskOptions{AssumeYes: true})
	if err != nil || !got {
		t.Errorf("AssumeYes: got=%v err=%v", got, err)
	}
}

func TestAskYesNoNonInteractive(t *testing.T) {
	got, err := AskYesNo(strings.NewReader(""), io.Discard, "Continue?", AskOptions{NonInteractive: true})
	if err != nil || got {
		t.Errorf("NonInteractive: got=%v err=%v", got, err)
	}
}

func TestAskYesNoAcceptsTurkishWhenCatalogPassed(t *testing.T) {
	// UC-14 regression guard. With a TR catalog, "e" and "evet" must
	// count as affirmative. The English forms still work too — locale
	// is additive, not exclusive.
	tr, err := i18n.Load("tr")
	if err != nil {
		t.Fatalf("load tr catalog: %v", err)
	}
	cases := map[string]bool{
		"e":      true,
		"E":      true,
		"evet":   true,
		"EVET":   true,
		" evet ": true,
		"y":      true, // EN still accepted
		"yes":    true,
		"hayir":  false,
		"":       false,
	}
	for ans, want := range cases {
		t.Run(ans, func(t *testing.T) {
			got, err := AskYesNo(strings.NewReader(ans+"\n"), io.Discard, "?",
				AskOptions{Catalog: tr})
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("TR catalog, answer %q → %v, want %v", ans, got, want)
			}
		})
	}
}

func TestAskYesNoEnCatalogStillStrict(t *testing.T) {
	// With explicit EN catalog, "e" is NOT accepted — only y/yes.
	// Guards against the Turkish extension accidentally bleeding into
	// the English default behaviour.
	en, err := i18n.Load("en")
	if err != nil {
		t.Fatalf("load en catalog: %v", err)
	}
	got, err := AskYesNo(strings.NewReader("e\n"), io.Discard, "?",
		AskOptions{Catalog: en})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Errorf("EN catalog should reject 'e' as affirmative")
	}
}

func TestPromptSuffixCatalogDriven(t *testing.T) {
	en, _ := i18n.Load("en")
	tr, _ := i18n.Load("tr")
	if got := PromptSuffix(nil); got != "[y/N]" {
		t.Errorf("nil catalog → %q, want [y/N]", got)
	}
	if got := PromptSuffix(en); got != "[y/N]" {
		t.Errorf("EN catalog → %q, want [y/N]", got)
	}
	if got := PromptSuffix(tr); got != "[e/H]" {
		t.Errorf("TR catalog → %q, want [e/H]", got)
	}
}

func TestAskYesNoAnswers(t *testing.T) {
	cases := map[string]bool{
		"y":        true,
		"Y":        true,
		"yes":      true,
		"YES":      true,
		"  yes  ":  true,
		"":         false,
		"n":        false,
		"no":       false,
		"yep":      false,
		"anything": false,
	}
	for ans, want := range cases {
		t.Run(ans, func(t *testing.T) {
			got, err := AskYesNo(strings.NewReader(ans+"\n"), io.Discard, "?", AskOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("answer %q → %v, want %v", ans, got, want)
			}
		})
	}
}
