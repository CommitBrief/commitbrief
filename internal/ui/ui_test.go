// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

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

func TestClip(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 0, "hello"},  // 0 = no limit (width unknown)
		{"hello", -1, "hello"}, // negative = no limit
		{"hello", 10, "hello"}, // fits
		{"hello", 5, "hello"},  // exact
		{"hello world", 5, "hell…"},
		{"abcdef", 3, "ab…"},
		{"hi", 1, "…"},
	}
	for _, c := range cases {
		if got := clip(c.in, c.max); got != c.want {
			t.Errorf("clip(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := map[time.Duration]string{
		0:                         "0s",
		3 * time.Second:           "3s",
		59 * time.Second:          "59s",
		60 * time.Second:          "1:00",
		83 * time.Second:          "1:23",
		(10*60 + 5) * time.Second: "10:05",
	}
	for d, want := range cases {
		if got := formatElapsed(d); got != want {
			t.Errorf("formatElapsed(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestRedrawShowsElapsedOnActiveStage(t *testing.T) {
	// Drive redraw directly (no animation goroutine) so the assertion is
	// deterministic. An active stage older than elapsedShowAfter must carry
	// its elapsed counter; a freshly-started one must not.
	var buf bytes.Buffer
	p := &Progress{w: &buf, mode: progressAnimated, frame: 1}
	p.stages = []stage{{label: "Thinking...", state: stageActive}}
	p.activeSince = time.Now().Add(-83 * time.Second)
	p.redraw()
	if got := buf.String(); !strings.Contains(got, "Thinking...") ||
		!strings.Contains(got, "1:23") || !strings.Contains(got, stageTimerColor) {
		t.Errorf("active stage redraw should show label + elapsed 1:23; got:\n%q", got)
	}

	buf.Reset()
	p2 := &Progress{w: &buf, mode: progressAnimated, frame: 1}
	p2.stages = []stage{{label: "Searching...", state: stageActive}}
	p2.activeSince = time.Now() // just started → below elapsedShowAfter
	p2.redraw()
	if got := buf.String(); strings.Contains(got, stageTimerColor) {
		t.Errorf("just-started stage must not show a timer yet; got:\n%q", got)
	}
}

func TestColorEnabledDumbTermDemotesAuto(t *testing.T) {
	// TERM=dumb must force ColorAuto off (the animated renderer would
	// flood a terminal that can't process cursor escapes). On a non-TTY
	// buffer auto is already off, so this asserts the guard doesn't error
	// and stays off; the meaningful regression it locks is the explicit
	// override below.
	t.Setenv("TERM", "dumb")
	if ColorEnabled(&bytes.Buffer{}, ColorAuto) {
		t.Error("TERM=dumb + ColorAuto must be false")
	}
}

func TestColorEnabledAlwaysWinsOverDumbTerm(t *testing.T) {
	// An explicit --color always still wins over TERM=dumb: the user has
	// said they know their terminal. Confirms the dumb guard sits after
	// the ColorAlways short-circuit.
	t.Setenv("TERM", "dumb")
	if !ColorEnabled(&bytes.Buffer{}, ColorAlways) {
		t.Error("--color always must override TERM=dumb")
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
