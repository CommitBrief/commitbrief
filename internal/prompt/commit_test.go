// SPDX-License-Identifier: GPL-3.0-or-later

package prompt

import (
	"strings"
	"testing"
)

func TestParseCommitType(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want CommitType
		ok   bool
	}{
		{"plain", CommitPlain, true},
		{"conventional", CommitConventional, true},
		{"conventional+body", CommitConventionalBody, true},
		{"gitmoji", CommitGitmoji, true},
		{"subject+body", CommitSubjectBody, true},
		{"", "", false},
		{"Plain", "", false},
		{"semantic", "", false},
	} {
		got, ok := ParseCommitType(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseCommitType(%q) = (%q,%v), want (%q,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestBuildCommitMessageSingle(t *testing.T) {
	p := BuildCommitMessage("diff body", CommitOptions{Type: CommitConventional, Count: 1})
	if !strings.Contains(p.System, "Conventional Commits") {
		t.Errorf("conventional prompt missing format rule:\n%s", p.System)
	}
	if !strings.Contains(p.System, "ONE commit message") {
		t.Errorf("single prompt should ask for one message:\n%s", p.System)
	}
	if strings.Contains(p.System, MessageDelimiter) {
		t.Errorf("single prompt must not mention the delimiter:\n%s", p.System)
	}
	if !strings.Contains(p.System, "English") {
		t.Errorf("prompt should pin English output:\n%s", p.System)
	}
	if !strings.Contains(p.User, "<diff>\ndiff body\n</diff>") {
		t.Errorf("user prompt should fence the diff: %q", p.User)
	}
}

func TestBuildCommitMessageMulti(t *testing.T) {
	p := BuildCommitMessage("d", CommitOptions{Type: CommitPlain, Count: 3})
	if !strings.Contains(p.System, "3 commit messages") {
		t.Errorf("multi prompt should ask for 3 messages:\n%s", p.System)
	}
	if !strings.Contains(p.System, MessageDelimiter) {
		t.Errorf("multi prompt must instruct the delimiter:\n%s", p.System)
	}
}

func TestBuildCommitMessageDefaultsToOne(t *testing.T) {
	// Count <= 0 must not emit the multi-message delimiter contract.
	p := BuildCommitMessage("d", CommitOptions{Type: CommitPlain, Count: 0})
	if strings.Contains(p.System, MessageDelimiter) {
		t.Errorf("count 0 should behave as single:\n%s", p.System)
	}
}

func TestParseMessagesSingle(t *testing.T) {
	got := ParseMessages("  feat: do a thing\n\nbody line\n", 1)
	if len(got) != 1 {
		t.Fatalf("want 1 message, got %d: %#v", len(got), got)
	}
	if got[0] != "feat: do a thing\n\nbody line" {
		t.Errorf("unexpected trim: %q", got[0])
	}
}

func TestParseMessagesMulti(t *testing.T) {
	raw := "msg one\n" + MessageDelimiter + "\nmsg two\n" + MessageDelimiter + "\nmsg three"
	got := ParseMessages(raw, 5)
	want := []string{"msg one", "msg two", "msg three"}
	if len(got) != len(want) {
		t.Fatalf("want %d messages, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("message[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseMessagesCapsAtN(t *testing.T) {
	raw := "a\n" + MessageDelimiter + "\nb\n" + MessageDelimiter + "\nc"
	got := ParseMessages(raw, 2)
	if len(got) != 2 {
		t.Fatalf("want cap at 2, got %d: %#v", len(got), got)
	}
}

func TestParseMessagesStripsFenceAndQuotes(t *testing.T) {
	if got := ParseMessages("```\nfeat: x\n```", 1); len(got) != 1 || got[0] != "feat: x" {
		t.Errorf("fence not stripped: %#v", got)
	}
	if got := ParseMessages(`"fix: y"`, 1); len(got) != 1 || got[0] != "fix: y" {
		t.Errorf("quotes not stripped: %#v", got)
	}
	// A whole multi-line subject+body wrapped in quotes is also unwrapped.
	if got := ParseMessages("\"feat: x\n\nbody line.\"", 1); len(got) != 1 || got[0] != "feat: x\n\nbody line." {
		t.Errorf("multi-line wrapping quotes not stripped: %#v", got)
	}
	// An unquoted body that merely contains a quote is left intact.
	if got := ParseMessages("fix: y\n\nsee the \"edge\" case.", 1); len(got) != 1 || got[0] != "fix: y\n\nsee the \"edge\" case." {
		t.Errorf("inner quotes should not be touched: %#v", got)
	}
}

func TestParseMessagesEmpty(t *testing.T) {
	if got := ParseMessages("   \n  ", 1); got != nil {
		t.Errorf("blank input should yield nil, got %#v", got)
	}
}
