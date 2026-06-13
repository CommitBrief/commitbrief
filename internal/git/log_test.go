// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"reflect"
	"testing"
)

// rec builds one RS-prefixed, US-delimited log record the way the
// rangeCommitFormat + --name-status output looks, so the parser tests read
// like the real thing without shelling out to git.
func rec(short, subject, body, nameStatus string) string {
	return logRecordSep + short + logFieldSep + subject + logFieldSep + body + logFieldSep + nameStatus
}

func TestParseRangeCommits(t *testing.T) {
	out := rec("a1b2c3d", "fix: invoice rounding", "Round half-up so totals match.",
		"\nM\tinternal/invoice/calc.go\nA\tinternal/invoice/calc_test.go\n") +
		rec("d4e5f6a", "feat: refresh token", "", // empty body
			"\nM\tinternal/auth/token.go\n")

	got := parseRangeCommits(out)
	want := []CommitMeta{
		{
			Short:   "a1b2c3d",
			Subject: "fix: invoice rounding",
			Body:    "Round half-up so totals match.",
			Files:   []string{"internal/invoice/calc.go", "internal/invoice/calc_test.go"},
		},
		{
			Short:   "d4e5f6a",
			Subject: "feat: refresh token",
			Body:    "",
			Files:   []string{"internal/auth/token.go"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseRangeCommits mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestParseRangeCommitsEmpty(t *testing.T) {
	if got := parseRangeCommits(""); len(got) != 0 {
		t.Fatalf("empty log should yield no commits, got %#v", got)
	}
}

func TestParseRangeCommitsSkipsMalformed(t *testing.T) {
	// A record missing the subject/body fields (only a hash) is skipped, not
	// fatal — the manifest is advisory.
	out := logRecordSep + "deadbee" + // no field separators at all
		rec("a1b2c3d", "feat: real one", "", "\nM\tfile.go\n")
	got := parseRangeCommits(out)
	if len(got) != 1 || got[0].Short != "a1b2c3d" {
		t.Fatalf("expected only the well-formed record, got %#v", got)
	}
}

func TestParseNameStatusRenames(t *testing.T) {
	// Renames/copies carry two paths; the resulting (last) path is the one we
	// attribute the change to.
	block := "\nR100\told/path.go\tnew/path.go\nC75\tsrc.go\tdst.go\nM\tplain.go\n"
	got := parseNameStatus(block)
	want := []string{"new/path.go", "dst.go", "plain.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseNameStatus mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}
