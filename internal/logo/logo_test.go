// SPDX-License-Identifier: GPL-3.0-or-later

package logo

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintEmbedsSuppliedVersion(t *testing.T) {
	// The version arg must appear verbatim in the wordmark line so
	// the printed brand matches the running build. A regression here
	// (e.g. the wordmark falling back to a hardcoded literal) would
	// silently show users the wrong version on every run.
	var buf bytes.Buffer
	Print(&buf, "v9.99.99")
	out := buf.String()
	if !strings.Contains(out, "v9.99.99") {
		t.Errorf("output missing supplied version %q; got first 400 bytes:\n%s",
			"v9.99.99", truncate(out, 400))
	}
	if !strings.Contains(out, "commitbrief") {
		t.Errorf("output missing wordmark; got first 400 bytes:\n%s", truncate(out, 400))
	}
	if !strings.Contains(out, "© GNU GPL v3") {
		t.Errorf("output missing license tag; got first 400 bytes:\n%s", truncate(out, 400))
	}
}

func TestPrintPreservesIntentionalWhitespace(t *testing.T) {
	// The frame ships with one leading newline and two trailing blank
	// lines (\n + 8 rows + \n\n\n). Trimming any of these would
	// crush the vertical rhythm the design depends on.
	var buf bytes.Buffer
	Print(&buf, "dev")
	out := buf.String()
	if !strings.HasPrefix(out, "\n") {
		t.Errorf("expected output to begin with a leading newline")
	}
	if !strings.HasSuffix(out, "\n\n\n") {
		t.Errorf("expected output to end with three trailing newlines (one row terminator + two blank lines)")
	}
}

func TestPrintRendersEightRows(t *testing.T) {
	// 16 source rows collapsed via half-block characters → exactly
	// 8 terminal rows. A mismatch usually means the pixel grid was
	// resized without updating the loop bound.
	var buf bytes.Buffer
	Print(&buf, "dev")
	// Drop the leading "\n" and trailing "\n\n\n" so the remaining
	// content is exactly the 8 rows separated by 7 newlines.
	body := strings.TrimPrefix(buf.String(), "\n")
	body = strings.TrimSuffix(body, "\n\n\n")
	if got := strings.Count(body, "\n"); got != 7 {
		t.Errorf("expected 8 rows (7 newlines between them); got %d newlines", got)
	}
}

func TestPrintIncludesHomepageHyperlink(t *testing.T) {
	// OSC 8 hyperlink for the home page should be present so terminals
	// that understand the escape can make the wordmark clickable.
	var buf bytes.Buffer
	Print(&buf, "dev")
	if !strings.Contains(buf.String(), "https://commitbrief.com") {
		t.Errorf("expected homepage URL inside the OSC 8 hyperlink escape")
	}
}

func TestPrintLinksIssuesNotAuthor(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, "dev")
	out := buf.String()
	if !strings.Contains(out, "https://github.com/CommitBrief/commitbrief/issues") {
		t.Errorf("expected the Issues link in the footer")
	}
	if !strings.Contains(out, "Issues") {
		t.Errorf("expected the Issues label in the footer")
	}
	if strings.Contains(out, "muhammetsafak.com.tr") || strings.Contains(out, "Author") {
		t.Errorf("Author link should have been removed from the footer")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
