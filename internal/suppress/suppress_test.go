// SPDX-License-Identifier: GPL-3.0-or-later

package suppress

import (
	"testing"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/render"
)

func mustParse(t *testing.T, content string) diff.Diff {
	t.Helper()
	d, err := diff.Parse(git.Diff{Content: content, Origin: git.OriginStaged})
	if err != nil {
		t.Fatalf("parse diff: %v", err)
	}
	return d
}

func finding(file string, line int, sev render.Severity) render.Finding {
	return render.Finding{
		Severity:    sev,
		File:        file,
		Line:        line,
		Title:       "t",
		Description: "d",
		Suggestion:  "s",
	}
}

// A file added with three lines; line 2 carries an inline ignore marker.
const sameLineGo = `diff --git a/a.go b/a.go
new file mode 100644
--- /dev/null
+++ b/a.go
@@ -0,0 +1,3 @@
+package a
+x := query(userInput) // commitbrief-ignore: parameterized elsewhere
+func New() {}
`

func TestParseSameLineMarker(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, sameLineGo))
	r, ok := sup[2]
	if !ok {
		t.Fatalf("expected a marker on line 2, got %v", sup)
	}
	if r.Severity != "" {
		t.Errorf("unscoped marker should have empty severity, got %q", r.Severity)
	}
	if r.Reason != "parameterized elsewhere" {
		t.Errorf("reason = %q, want %q", r.Reason, "parameterized elsewhere")
	}
}

func TestFilterSameLineSuppresses(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, sameLineGo))
	in := []render.Finding{finding("a.go", 2, render.SeverityCritical)}
	kept, n := Filter(in, sup)
	if n != 1 || len(kept) != 0 {
		t.Fatalf("same-line unscoped marker should suppress: kept=%d suppressed=%d", len(kept), n)
	}
}

// Marker on the line ABOVE the finding.
const lineAboveGo = `diff --git a/a.go b/a.go
new file mode 100644
--- /dev/null
+++ b/a.go
@@ -0,0 +1,3 @@
+package a
+// commitbrief-ignore: known, tracked in TICKET-1
+riskyCall()
`

func TestFilterLineAboveSuppresses(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, lineAboveGo))
	// Finding on line 3; marker is on line 2 (directly above).
	in := []render.Finding{finding("a.go", 3, render.SeverityHigh)}
	kept, n := Filter(in, sup)
	if n != 1 || len(kept) != 0 {
		t.Fatalf("line-above marker should suppress: kept=%d suppressed=%d", len(kept), n)
	}
}

func TestFilterLineTwoAboveDoesNotSuppress(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, lineAboveGo))
	// Finding on line 4: marker is two lines above — out of range.
	in := []render.Finding{finding("a.go", 4, render.SeverityHigh)}
	kept, n := Filter(in, sup)
	if n != 0 || len(kept) != 1 {
		t.Fatalf("marker two lines above must NOT suppress: kept=%d suppressed=%d", len(kept), n)
	}
}

// Scoped marker: [high] suppresses only high-severity findings on the line.
const scopedGo = `diff --git a/a.go b/a.go
new file mode 100644
--- /dev/null
+++ b/a.go
@@ -0,0 +1,2 @@
+package a
+danger() // commitbrief-ignore[high]: accepted risk
`

func TestScopedSeverityMatch(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, scopedGo))
	r := sup[2]
	if r.Severity != render.SeverityHigh {
		t.Fatalf("scoped severity = %q, want high", r.Severity)
	}

	// A high finding on line 2 is suppressed.
	kept, n := Filter([]render.Finding{finding("a.go", 2, render.SeverityHigh)}, sup)
	if n != 1 || len(kept) != 0 {
		t.Fatalf("scoped [high] should suppress a high finding: kept=%d suppressed=%d", len(kept), n)
	}

	// A critical finding on the SAME line is NOT suppressed (scope mismatch).
	kept, n = Filter([]render.Finding{finding("a.go", 2, render.SeverityCritical)}, sup)
	if n != 0 || len(kept) != 1 {
		t.Fatalf("scoped [high] must NOT suppress a critical finding: kept=%d suppressed=%d", len(kept), n)
	}
}

func TestUnscopedSuppressesAnySeverity(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, sameLineGo)) // unscoped marker on line 2
	for _, sev := range []render.Severity{
		render.SeverityCritical, render.SeverityHigh, render.SeverityMedium,
		render.SeverityLow, render.SeverityInfo,
	} {
		kept, n := Filter([]render.Finding{finding("a.go", 2, sev)}, sup)
		if n != 1 || len(kept) != 0 {
			t.Errorf("unscoped marker should suppress %s: kept=%d suppressed=%d", sev, len(kept), n)
		}
	}
}

// Comment-prefix variants: the token must be found regardless of // # -- /* */.
func TestCommentPrefixVariants(t *testing.T) {
	cases := map[string]string{
		"slash-slash": "+code() // commitbrief-ignore: r",
		"hash":        "+code() # commitbrief-ignore: r",
		"double-dash": "+code() -- commitbrief-ignore: r",
		"block":       "+code() /* commitbrief-ignore: r */",
		"bare":        "+commitbrief-ignore: r",
	}
	for name, line := range cases {
		content := "diff --git a/a.txt b/a.txt\n" +
			"new file mode 100644\n--- /dev/null\n+++ b/a.txt\n" +
			"@@ -0,0 +1,1 @@\n" + line + "\n"
		sup := ParseSuppressions(mustParse(t, content))
		if _, ok := sup[1]; !ok {
			t.Errorf("%s: marker not detected in %q", name, line)
		}
	}
}

func TestAddedOnlyContextLinesIgnored(t *testing.T) {
	// A marker on a CONTEXT line (unchanged, pre-existing) must NOT be read —
	// suppression must be part of the change under review.
	content := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@
 untouched() // commitbrief-ignore: this is a context line, ignore me
-old()
+new()
`
	sup := ParseSuppressions(mustParse(t, content))
	if len(sup) != 0 {
		t.Fatalf("context-line marker must be ignored, got %v", sup)
	}
}

func TestFilterEmptyAndNilSuppressions(t *testing.T) {
	in := []render.Finding{finding("a.go", 1, render.SeverityHigh)}
	kept, n := Filter(in, Suppressions{})
	if n != 0 || len(kept) != 1 {
		t.Fatalf("empty suppressions must pass through: kept=%d suppressed=%d", len(kept), n)
	}
	kept, n = Filter(in, nil)
	if n != 0 || len(kept) != 1 {
		t.Fatalf("nil suppressions must pass through: kept=%d suppressed=%d", len(kept), n)
	}
}

func TestFilterKeptNonNilWhenAllSuppressed(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, sameLineGo))
	kept, _ := Filter([]render.Finding{finding("a.go", 2, render.SeverityHigh)}, sup)
	if kept == nil {
		t.Fatal("kept must be non-nil even when all suppressed")
	}
}

func TestFindingWithoutLineNeverSuppressed(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, sameLineGo))
	kept, n := Filter([]render.Finding{finding("a.go", 0, render.SeverityHigh)}, sup)
	if n != 0 || len(kept) != 1 {
		t.Fatalf("a line-less finding must never be suppressed: kept=%d suppressed=%d", len(kept), n)
	}
}

func TestUnknownScopedSeverityFailsOpen(t *testing.T) {
	// [bogus] is not a valid severity → treated as unscoped (suppresses any).
	content := "diff --git a/a.go b/a.go\nnew file mode 100644\n--- /dev/null\n+++ b/a.go\n" +
		"@@ -0,0 +1,1 @@\n+x() // commitbrief-ignore[bogus]: r\n"
	sup := ParseSuppressions(mustParse(t, content))
	r := sup[1]
	if r.Severity != "" {
		t.Fatalf("unknown severity should fall open to unscoped, got %q", r.Severity)
	}
	kept, n := Filter([]render.Finding{finding("a.go", 1, render.SeverityCritical)}, sup)
	if n != 1 || len(kept) != 0 {
		t.Fatalf("fail-open marker should suppress: kept=%d suppressed=%d", len(kept), n)
	}
}
