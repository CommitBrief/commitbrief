// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"bytes"
	"io"
	"strings"
	"testing"
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

