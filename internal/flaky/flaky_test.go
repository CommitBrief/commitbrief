// SPDX-License-Identifier: GPL-3.0-or-later

package flaky

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/render"
)

func TestIsTestFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"internal/worker/job_test.go", true},
		{"tests/test_login.py", true},
		{"app/login_test.py", true},
		{"src/components/Button.test.tsx", true},
		{"e2e/login.spec.ts", true},
		{"spec/models/user_spec.rb", true},
		{"src/test/java/com/acme/JobTest.java", true},
		{"Service.Tests.cs", true},
		{"tests/Feature/LoginTest.php", true},
		{"__tests__/util.js", true},
		{"internal/worker/job.go", false},
		{"src/components/Button.tsx", false},
		{"app/models/user.rb", false},
		{"README.md", false},
	}
	for _, c := range cases {
		if got := isTestFile(c.path); got != c.want {
			t.Errorf("isTestFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestDetectLang(t *testing.T) {
	cases := map[string]string{
		"a_test.go":       "go",
		"a.test.ts":       "ts",
		"a.test.jsx":      "js",
		"test_a.py":       "python",
		"AThingTest.java": "java",
		"user_spec.rb":    "ruby",
		"LoginTest.php":   "php",
		"Svc.Tests.cs":    "csharp",
		"Makefile":        "",
	}
	for path, want := range cases {
		if got := detectLang(path); got != want {
			t.Errorf("detectLang(%q) = %q, want %q", path, got, want)
		}
	}
}

// loadCatalog loads the English catalog so finding text resolves through the
// same path production uses; a missing key would surface as the raw key.
func loadCatalog(t *testing.T) *i18n.Catalog {
	t.Helper()
	cat, err := i18n.Load("en")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	return cat
}

func detect(t *testing.T, cat *i18n.Catalog, raw string) []render.Finding {
	t.Helper()
	parsed, err := diff.Parse(git.Diff{Content: raw})
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	return New(cat).Detect(parsed)
}

// assertResolved checks that the catalog text fields are populated (not the
// raw key) and that the finding satisfies the render.Finding invariants the
// JSON/markdown renderers rely on.
func assertResolved(t *testing.T, f render.Finding) {
	t.Helper()
	if !f.Severity.IsValid() {
		t.Errorf("invalid severity %q", f.Severity)
	}
	if f.Title == "" || strings.HasPrefix(f.Title, "flaky.") {
		t.Errorf("title not resolved: %q", f.Title)
	}
	if f.Description == "" || f.Suggestion == "" {
		t.Errorf("missing description/suggestion: %+v", f)
	}
	if !strings.HasPrefix(f.Snippet, "+") {
		t.Errorf("snippet should keep the diff prefix: %q", f.Snippet)
	}
}

func TestDetect_GoLineNumbersAndRules(t *testing.T) {
	cat := loadCatalog(t)
	// Cursor starts at NewStart=10. Context advances, deleted does not:
	//   10  setup()        (context)
	//   --  old()          (deleted, no advance)
	//   11  time.Sleep(..) (added)  -> hard-sleep @ 11
	//   12  rand.Intn(..)  (added)  -> unseeded-random @ 12
	//   13  assert(x)      (context)
	raw := `diff --git a/worker/job_test.go b/worker/job_test.go
--- a/worker/job_test.go
+++ b/worker/job_test.go
@@ -10,4 +10,5 @@ func TestJob(t *testing.T) {
 	setup()
-	old()
+	time.Sleep(2 * time.Second)
+	x := rand.Intn(100)
 	assert(x)
`
	got := detect(t, cat, raw)
	if len(got) != 2 {
		t.Fatalf("len(findings) = %d, want 2: %+v", len(got), got)
	}
	for _, f := range got {
		assertResolved(t, f)
		if f.File != "worker/job_test.go" {
			t.Errorf("file = %q", f.File)
		}
		if f.Language != "go" {
			t.Errorf("language = %q, want go", f.Language)
		}
	}
	sleepF, randF := got[0], got[1]
	if sleepF.Line != 11 || sleepF.Severity != render.SeverityMedium {
		t.Errorf("hard-sleep: line=%d sev=%s, want 11/medium", sleepF.Line, sleepF.Severity)
	}
	if !strings.Contains(strings.ToLower(sleepF.Title), "sleep") {
		t.Errorf("hard-sleep title = %q", sleepF.Title)
	}
	if randF.Line != 12 || randF.Severity != render.SeverityLow {
		t.Errorf("unseeded-random: line=%d sev=%s, want 12/low", randF.Line, randF.Severity)
	}
	if !strings.Contains(strings.ToLower(randF.Title), "random") {
		t.Errorf("unseeded-random title = %q", randF.Title)
	}
}

func TestDetect_NonTestFileIgnored(t *testing.T) {
	cat := loadCatalog(t)
	raw := `diff --git a/worker/job.go b/worker/job.go
--- a/worker/job.go
+++ b/worker/job.go
@@ -1,1 +1,2 @@
 package worker
+	time.Sleep(1 * time.Second)
`
	if got := detect(t, cat, raw); len(got) != 0 {
		t.Fatalf("non-test file should yield no findings, got %d: %+v", len(got), got)
	}
}

func TestDetect_CypressFixedWaitPrecision(t *testing.T) {
	cat := loadCatalog(t)
	// cy.wait(<number>) is flaky; cy.wait('@alias') is the correct pattern
	// and must NOT be flagged.
	raw := `diff --git a/e2e/login.spec.ts b/e2e/login.spec.ts
--- a/e2e/login.spec.ts
+++ b/e2e/login.spec.ts
@@ -5,1 +5,3 @@
 it('logs in', () => {
+  cy.wait(2000)
+  cy.wait('@loginRequest')
`
	got := detect(t, cat, raw)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 finding (numeric cy.wait only), got %d: %+v", len(got), got)
	}
	if got[0].Line != 6 {
		t.Errorf("cy.wait line = %d, want 6", got[0].Line)
	}
	assertResolved(t, got[0])
}

func TestDetect_DeletedAndBinaryFilesSkipped(t *testing.T) {
	cat := loadCatalog(t)
	raw := `diff --git a/foo_test.go b/foo_test.go
deleted file mode 100644
--- a/foo_test.go
+++ /dev/null
@@ -1,2 +0,0 @@
-	time.Sleep(5)
-	old()
`
	if got := detect(t, cat, raw); len(got) != 0 {
		t.Fatalf("deleted file should be skipped, got %d: %+v", len(got), got)
	}
}
