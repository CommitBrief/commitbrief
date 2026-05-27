// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadPromptLineConsumesOneLinePerCall(t *testing.T) {
	// UC-21 regression guard. The shared *bufio.Reader created at the
	// top of runReview must return one line per ReadString('\n') call
	// — three sequential prompts (guard → secret → cost) reading from
	// the same buffer must each get their own answer instead of one
	// scanner gobbling everything via lookahead.
	r := bufio.NewReader(strings.NewReader("yes\nno\ny\n"))

	answers := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		ans, err := readPromptLine(r)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		answers = append(answers, ans)
	}
	if got, want := strings.Join(answers, ","), "yes,no,y"; got != want {
		t.Errorf("answers = %q, want %q (one line per prompt)", got, want)
	}
}

func TestReadPromptLineHandlesNoTrailingNewline(t *testing.T) {
	// A user pressing Ctrl-D after typing "y" (no newline) should
	// still surface "y" as the answer, not "".
	r := bufio.NewReader(strings.NewReader("y"))
	got, err := readPromptLine(r)
	if err != nil {
		t.Fatal(err)
	}
	if got != "y" {
		t.Errorf("answer = %q, want %q", got, "y")
	}
}

func TestReadPromptLineNormalisesCaseAndWhitespace(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("  YES  \n"))
	got, _ := readPromptLine(r)
	if got != "yes" {
		t.Errorf("answer = %q, want lowercased+trimmed yes", got)
	}
}

func TestReadPromptLineEOFReturnsEmpty(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	got, err := readPromptLine(r)
	if err != nil {
		t.Errorf("EOF should return (\"\", nil), got err=%v", err)
	}
	if got != "" {
		t.Errorf("EOF should return empty string; got %q", got)
	}
}
