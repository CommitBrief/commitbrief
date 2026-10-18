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
	r, ok := sup.At("a.go", 2)
	if !ok {
		t.Fatalf("expected a marker on a.go line 2, got %+v", sup)
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
	r, _ := sup.At("a.go", 2)
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
		if _, ok := sup.At("a.txt", 1); !ok {
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
	if sup.Len() != 0 {
		t.Fatalf("context-line marker must be ignored, got %+v", sup)
	}
}

func TestFilterEmptySuppressions(t *testing.T) {
	in := []render.Finding{finding("a.go", 1, render.SeverityHigh)}
	kept, n := Filter(in, Suppressions{})
	if n != 0 || len(kept) != 1 {
		t.Fatalf("empty suppressions must pass through: kept=%d suppressed=%d", len(kept), n)
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
	r, _ := sup.At("a.go", 1)
	if r.Severity != "" {
		t.Fatalf("unknown severity should fall open to unscoped, got %q", r.Severity)
	}
	kept, n := Filter([]render.Finding{finding("a.go", 1, render.SeverityCritical)}, sup)
	if n != 1 || len(kept) != 0 {
		t.Fatalf("fail-open marker should suppress: kept=%d suppressed=%d", len(kept), n)
	}
}

// twoFileGo carries a marker at line 2 of a.go only; b.go has no marker but
// its line 2 holds a risky statement.
const twoFileGo = `diff --git a/a.go b/a.go
new file mode 100644
--- /dev/null
+++ b/a.go
@@ -0,0 +1,3 @@
+package a
+x := query(userInput) // commitbrief-ignore: parameterized elsewhere
+func New() {}
diff --git a/b.go b/b.go
new file mode 100644
--- /dev/null
+++ b/b.go
@@ -0,0 +1,3 @@
+package b
+y := query(otherInput)
+func New() {}
`

// D7 regression: a marker in one file must not suppress a finding that sits on
// the same line number of a different file.
func TestMarkerDoesNotSuppressSameLineInOtherFile(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, twoFileGo))

	kept, n := Filter([]render.Finding{finding("b.go", 2, render.SeverityHigh)}, sup)
	if n != 0 || len(kept) != 1 {
		t.Fatalf("marker in a.go must NOT suppress b.go:2: kept=%d suppressed=%d", len(kept), n)
	}
	// The line-above window is per-file too: b.go:3 must not see a.go's marker.
	kept, n = Filter([]render.Finding{finding("b.go", 3, render.SeverityHigh)}, sup)
	if n != 0 || len(kept) != 1 {
		t.Fatalf("marker in a.go must NOT suppress b.go:3 via line-above: kept=%d suppressed=%d", len(kept), n)
	}

	// The same marker still suppresses the matching finding in its own file.
	kept, n = Filter([]render.Finding{finding("a.go", 2, render.SeverityHigh)}, sup)
	if n != 1 || len(kept) != 0 {
		t.Fatalf("marker in a.go must still suppress a.go:2: kept=%d suppressed=%d", len(kept), n)
	}
}

func TestSameLineNumberMarkersInTwoFilesAreIndependent(t *testing.T) {
	content := `diff --git a/a.go b/a.go
new file mode 100644
--- /dev/null
+++ b/a.go
@@ -0,0 +1,2 @@
+package a
+x() // commitbrief-ignore[high]: a
diff --git a/b.go b/b.go
new file mode 100644
--- /dev/null
+++ b/b.go
@@ -0,0 +1,2 @@
+package b
+y() // commitbrief-ignore[low]: b
`
	sup := ParseSuppressions(mustParse(t, content))
	if sup.Len() != 2 {
		t.Fatalf("both markers must be kept (distinct files), got %d", sup.Len())
	}
	ra, _ := sup.At("a.go", 2)
	rb, _ := sup.At("b.go", 2)
	if ra.Severity != render.SeverityHigh || rb.Severity != render.SeverityLow {
		t.Fatalf("markers crossed files: a=%q b=%q", ra.Severity, rb.Severity)
	}
}

func ruleMarkerDiff(token string) string {
	return "diff --git a/a.go b/a.go\nnew file mode 100644\n--- /dev/null\n+++ b/a.go\n" +
		"@@ -0,0 +1,1 @@\n+x() // commitbrief-ignore[" + token + "]: r\n"
}

func ruleFinding(rule string, sev render.Severity) render.Finding {
	f := finding("a.go", 1, sev)
	f.RuleID = rule
	return f
}

func TestRuleScopedMarkerSuppressesOnlyThatRule(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, ruleMarkerDiff("sec.injection")))
	if w := sup.Warnings(); len(w) != 0 {
		t.Fatalf("a valid rule id must not warn, got %+v", w)
	}

	kept, n := Filter([]render.Finding{ruleFinding("sec.injection", render.SeverityHigh)}, sup)
	if n != 1 || len(kept) != 0 {
		t.Fatalf("[sec.injection] should suppress sec.injection: kept=%d suppressed=%d", len(kept), n)
	}
	// Different rule, same line, same severity: kept.
	kept, n = Filter([]render.Finding{ruleFinding("sec.xss", render.SeverityHigh)}, sup)
	if n != 0 || len(kept) != 1 {
		t.Fatalf("[sec.injection] must NOT suppress sec.xss: kept=%d suppressed=%d", len(kept), n)
	}
	// A finding with no rule id counts as general.other: kept.
	kept, n = Filter([]render.Finding{ruleFinding("", render.SeverityHigh)}, sup)
	if n != 0 || len(kept) != 1 {
		t.Fatalf("[sec.injection] must NOT suppress an unstamped finding: kept=%d suppressed=%d", len(kept), n)
	}
}

func TestCategoryWildcardMarker(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, ruleMarkerDiff("sec.*")))
	if w := sup.Warnings(); len(w) != 0 {
		t.Fatalf("a known category wildcard must not warn, got %+v", w)
	}
	for _, rule := range []string{"sec.injection", "sec.xss"} {
		kept, n := Filter([]render.Finding{ruleFinding(rule, render.SeverityLow)}, sup)
		if n != 1 || len(kept) != 0 {
			t.Errorf("[sec.*] should suppress %s: kept=%d suppressed=%d", rule, len(kept), n)
		}
	}
	kept, n := Filter([]render.Finding{ruleFinding("general.other", render.SeverityLow)}, sup)
	if n != 0 || len(kept) != 1 {
		t.Fatalf("[sec.*] must NOT suppress general.other: kept=%d suppressed=%d", len(kept), n)
	}
}

func TestSeverityScopeUnchangedAndRuleAgnostic(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, ruleMarkerDiff("high")))
	if w := sup.Warnings(); len(w) != 0 {
		t.Fatalf("[high] must not warn, got %+v", w)
	}
	for _, rule := range []string{"sec.injection", "general.other", ""} {
		kept, n := Filter([]render.Finding{ruleFinding(rule, render.SeverityHigh)}, sup)
		if n != 1 || len(kept) != 0 {
			t.Errorf("[high] should suppress a high finding of rule %q: kept=%d suppressed=%d", rule, len(kept), n)
		}
	}
	kept, n := Filter([]render.Finding{ruleFinding("sec.injection", render.SeverityCritical)}, sup)
	if n != 0 || len(kept) != 1 {
		t.Fatalf("[high] must NOT suppress a critical finding: kept=%d suppressed=%d", len(kept), n)
	}
}

func TestUnknownRuleFailsOpenWithWarning(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, ruleMarkerDiff("sec.nonsense")))
	w := sup.Warnings()
	if len(w) != 1 || !w[0].UnknownRule || w[0].Token != "sec.nonsense" || w[0].Path != "a.go" || w[0].Line != 1 {
		t.Fatalf("warnings = %+v, want one unknown-rule warning for a.go:1", w)
	}
	kept, n := Filter([]render.Finding{ruleFinding("sec.injection", render.SeverityHigh)}, sup)
	if n != 1 || len(kept) != 0 {
		t.Fatalf("an unknown rule falls open to unscoped: kept=%d suppressed=%d", len(kept), n)
	}
	// An unknown category wildcard behaves the same.
	sup = ParseSuppressions(mustParse(t, ruleMarkerDiff("nope.*")))
	if w := sup.Warnings(); len(w) != 1 || !w[0].UnknownRule {
		t.Fatalf("unknown category wildcard should warn, got %+v", w)
	}
}

func TestUnknownSeverityTokenWarns(t *testing.T) {
	sup := ParseSuppressions(mustParse(t, ruleMarkerDiff("bogus")))
	w := sup.Warnings()
	if len(w) != 1 || w[0].UnknownRule || w[0].Token != "bogus" {
		t.Fatalf("warnings = %+v, want one unknown-scope warning", w)
	}
}
