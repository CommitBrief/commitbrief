// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/ui"
)

// UC-21 regression guard. runReview creates one *bufio.Reader at the top
// and threads it through every interactive prompt (guard → secret scan →
// token/cost preflight) via ui.Confirm. Each prompt must consume exactly
// one line from the shared buffer; a per-call bufio.Scanner inside
// AskYesNo would over-read via lookahead and swallow the answers meant
// for later prompts. These tests pin the behaviour at the ui.Confirm
// boundary the review pipeline actually uses.

func TestSharedReaderConsumesOneLinePerConfirm(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("yes\nno\ny\n"))

	got := make([]bool, 0, 3)
	for i := 0; i < 3; i++ {
		ok, err := ui.Confirm(r, io.Discard, "?", ui.AskOptions{})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		got = append(got, ok)
	}

	want := []bool{true, false, true}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("answers = %v, want %v (one line consumed per prompt)", got, want)
		}
	}
}

func TestSharedReaderHandlesNoTrailingNewline(t *testing.T) {
	// A user pressing Ctrl-D after typing "y" (no newline) should still
	// surface "y" as affirmative, not an empty/negative answer.
	r := bufio.NewReader(strings.NewReader("y"))
	ok, err := ui.Confirm(r, io.Discard, "?", ui.AskOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Errorf("no-trailing-newline 'y' should be affirmative")
	}
}

func TestSharedReaderEOFIsNegative(t *testing.T) {
	// Drained buffer (immediate EOF) must read as the default-no, not error.
	r := bufio.NewReader(strings.NewReader(""))
	ok, err := ui.Confirm(r, io.Discard, "?", ui.AskOptions{})
	if err != nil {
		t.Errorf("EOF should return (false, nil); got err=%v", err)
	}
	if ok {
		t.Errorf("EOF should be negative (default no)")
	}
}
