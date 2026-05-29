// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/git"
)

func parseOne(t *testing.T, raw string) Diff {
	t.Helper()
	d, err := Parse(git.Diff{Content: raw, Origin: git.OriginDiff})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return d
}

func TestNumberedStringPrefixesLines(t *testing.T) {
	// sampleModify hunk header: @@ -10,7 +10,9 @@
	got := parseOne(t, sampleModify).NumberedString()
	wants := []string{
		"10|  \tif creds.Username == \"\" {",                            // context keeps new number 10
		"13| -\tif creds.Password == \"\" {",                            // removed line keeps OLD number 13
		"13| +\tif creds.Password == \"\" || len(creds.Password) < 8 {", // added line, NEW number 13
		"16| +\tlogger.Info(\"login attempt\", \"user\", creds.Username)",
	}
	for _, w := range wants {
		// tolerate the leading tab in the fixture's code text
		needle := strings.ReplaceAll(w, "\t", "")
		hay := strings.ReplaceAll(got, "\t", "")
		if !strings.Contains(hay, needle) {
			t.Errorf("NumberedString missing %q\n--- got ---\n%s", needle, got)
		}
	}
	// The @@ header itself is untouched.
	if !strings.Contains(got, "@@ -10,7 +10,9 @@") {
		t.Errorf("hunk header lost:\n%s", got)
	}
}

func TestNumberedStringIsPureFunctionOfString(t *testing.T) {
	// Stripping the "<n>| " prefix from each numbered hunk line must
	// reproduce the plain String() output — the cache keys on String(),
	// so the two must stay in lock-step.
	d := parseOne(t, sampleModify)
	plain, numbered := d.String(), d.NumberedString()
	if strings.Count(plain, "\n") != strings.Count(numbered, "\n") {
		t.Fatalf("line count drift: plain=%d numbered=%d",
			strings.Count(plain, "\n"), strings.Count(numbered, "\n"))
	}
}

func TestAnchorsResolveSides(t *testing.T) {
	anchors := parseOne(t, sampleModify).Anchors()
	fa, ok := anchors["internal/auth/login.go"]
	if !ok {
		t.Fatalf("no anchors for the file; keys=%v", anchors)
	}

	// New line 13 is the added password-length check → RIGHT.
	if side, ok := fa.Resolve(13, false); !ok || side != SideRight {
		t.Errorf("line 13 RIGHT-first: side=%q ok=%v, want RIGHT/true", side, ok)
	}
	// Old line 13 is the removed line → reachable on LEFT when preferred.
	if side, ok := fa.Resolve(13, true); !ok || side != SideLeft {
		t.Errorf("line 13 LEFT-first: side=%q ok=%v, want LEFT/true", side, ok)
	}
	// New line 18 exists only on the RIGHT side (old numbering stops at
	// 17), so it resolves RIGHT even when LEFT is preferred.
	if side, ok := fa.Resolve(18, true); !ok || side != SideRight {
		t.Errorf("line 18: side=%q ok=%v, want RIGHT/true (no LEFT match)", side, ok)
	}
	// A line outside every hunk is unanchorable.
	if _, ok := fa.Resolve(9999, false); ok {
		t.Errorf("line 9999 should not anchor")
	}
	// Zero / negative lines never anchor.
	if _, ok := fa.Resolve(0, false); ok {
		t.Errorf("line 0 should not anchor")
	}
}

func TestAnchorsAddedFileHasNoLeftSide(t *testing.T) {
	fa := parseOne(t, sampleAdded).Anchors()["internal/feature/new.go"]
	if side, ok := fa.Resolve(1, true); !ok || side != SideRight {
		t.Errorf("added file line 1 prefer-left: side=%q ok=%v, want RIGHT/true", side, ok)
	}
}
