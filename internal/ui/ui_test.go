// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/provider"
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

func TestDrainBasic(t *testing.T) {
	ch := make(chan provider.Event, 4)
	ch <- provider.Event{Type: provider.EventDelta, Delta: "hello "}
	ch <- provider.Event{Type: provider.EventDelta, Delta: "world"}
	ch <- provider.Event{Type: provider.EventUsage, Usage: provider.Usage{InputTokens: 5, OutputTokens: 2}}
	ch <- provider.Event{Type: provider.EventDone}
	close(ch)

	var w bytes.Buffer
	res := Drain(context.Background(), ch, &w)
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Content != "hello world" {
		t.Errorf("Content = %q", res.Content)
	}
	if res.Usage.InputTokens != 5 {
		t.Errorf("Usage = %+v", res.Usage)
	}
	if w.String() != "hello world" {
		t.Errorf("writer = %q", w.String())
	}
}

func TestDrainStopsOnError(t *testing.T) {
	ch := make(chan provider.Event, 3)
	ch <- provider.Event{Type: provider.EventDelta, Delta: "first "}
	ch <- provider.Event{Type: provider.EventError, Err: errors.New("boom")}
	ch <- provider.Event{Type: provider.EventDelta, Delta: "ignored"}
	close(ch)

	res := Drain(context.Background(), ch, io.Discard)
	if res.Err == nil {
		t.Fatal("expected err")
	}
	if !strings.Contains(res.Err.Error(), "boom") {
		t.Errorf("err = %v", res.Err)
	}
	if res.Content != "first " {
		t.Errorf("Content after error = %q, want only pre-error content", res.Content)
	}
}

func TestDrainContextCancellation(t *testing.T) {
	ch := make(chan provider.Event)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	res := Drain(ctx, ch, io.Discard)
	if !errors.Is(res.Err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", res.Err)
	}
}

func TestDrainNilWriterStillBuffers(t *testing.T) {
	ch := make(chan provider.Event, 1)
	ch <- provider.Event{Type: provider.EventDelta, Delta: "x"}
	close(ch)
	res := Drain(context.Background(), ch, nil)
	if res.Content != "x" {
		t.Error("nil writer should still buffer content")
	}
}

func TestSpinnerNonTTYIsInert(t *testing.T) {
	s := NewSpinner(&bytes.Buffer{}, "loading")
	s.Start()
	s.Update("still loading")
	s.Stop()
	// No assertions: just verifying nothing panics or hangs.
}

func TestSpinnerStopBeforeStartSafe(t *testing.T) {
	s := NewSpinner(&bytes.Buffer{}, "x")
	s.Stop() // should not deadlock / panic
}
