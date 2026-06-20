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
	// Line-scoped rules carry the matched line as a "+"-prefixed snippet;
	// file-scoped rules (over-mock) legitimately carry none.
	if f.Snippet != "" && !strings.HasPrefix(f.Snippet, "+") {
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

// findingByTitleContains returns the first finding whose title contains sub
// (case-insensitive), or a zero Finding and false.
func findingByTitleContains(got []render.Finding, sub string) (render.Finding, bool) {
	for _, f := range got {
		if strings.Contains(strings.ToLower(f.Title), strings.ToLower(sub)) {
			return f, true
		}
	}
	return render.Finding{}, false
}

func TestDetect_BrittleSelector(t *testing.T) {
	cat := loadCatalog(t)
	// Positive: an absolute-XPath Playwright locator and a CSS :nth-child are
	// brittle. Negative: a data-testid locator and a role/text query are the
	// recommended stable patterns and must NOT be flagged.
	raw := `diff --git a/e2e/checkout.spec.ts b/e2e/checkout.spec.ts
--- a/e2e/checkout.spec.ts
+++ b/e2e/checkout.spec.ts
@@ -1,1 +1,6 @@
 test('checkout', async () => {
+  await page.locator('//div[2]/button[1]').click()
+  cy.get('ul li:nth-child(3)').click()
+  await page.getByTestId('submit').click()
+  await page.getByRole('button', { name: 'Pay' }).click()
+  cy.get('[data-test=total]').should('be.visible')
`
	got := detect(t, cat, raw)
	var brittle []render.Finding
	for _, f := range got {
		if strings.Contains(strings.ToLower(f.Title), "selector") {
			brittle = append(brittle, f)
		}
	}
	if len(brittle) != 2 {
		t.Fatalf("brittle-selector: want 2 findings (absolute XPath + :nth-child), got %d: %+v", len(brittle), got)
	}
	for _, f := range brittle {
		assertResolved(t, f)
		if f.Severity != render.SeverityLow {
			t.Errorf("brittle-selector severity = %s, want low", f.Severity)
		}
	}
	if brittle[0].Line != 2 {
		t.Errorf("first brittle-selector line = %d, want 2", brittle[0].Line)
	}
}

func TestDetect_BrittleSelectorOnlyJSTS(t *testing.T) {
	cat := loadCatalog(t)
	// :nth-child inside a Go test string is not a UI selector context; the
	// rule is language-gated to js/ts and must stay silent here.
	raw := `diff --git a/css_test.go b/css_test.go
--- a/css_test.go
+++ b/css_test.go
@@ -1,1 +1,2 @@
 func TestParse(t *testing.T) {
+	got := parse("ul li:nth-child(2)")
`
	if got := detect(t, cat, raw); len(got) != 0 {
		t.Fatalf("brittle-selector must be js/ts only, got %d on a .go file: %+v", len(got), got)
	}
}

func TestDetect_TimeDependency(t *testing.T) {
	cat := loadCatalog(t)
	// Positive: time.Now() compared inside an assertion. Negative: time.Now()
	// captured into a variable for setup (no assertion on the same line) must
	// NOT be flagged — only a clock read in the assertion path is flaky.
	raw := `diff --git a/clock_test.go b/clock_test.go
--- a/clock_test.go
+++ b/clock_test.go
@@ -1,1 +1,4 @@
 func TestExpiry(t *testing.T) {
+	start := time.Now()
+	require.Equal(t, time.Now().Day(), got.Day())
+	process(start)
`
	got := detect(t, cat, raw)
	f, ok := findingByTitleContains(got, "wall clock")
	if !ok {
		t.Fatalf("time-dependency: expected a finding, got %+v", got)
	}
	assertResolved(t, f)
	if f.Line != 3 {
		t.Errorf("time-dependency line = %d, want 3 (the assertion line, not the setup capture)", f.Line)
	}
	if f.Severity != render.SeverityLow {
		t.Errorf("time-dependency severity = %s, want low", f.Severity)
	}
	// The setup capture on line 2 must not produce its own finding.
	if len(got) != 1 {
		t.Errorf("want exactly 1 finding (assertion only, not the setup capture), got %d: %+v", len(got), got)
	}
}

func TestDetect_OverMock(t *testing.T) {
	cat := loadCatalog(t)
	// Positive: six jest mock setups inside one test function crosses the
	// threshold (>5). The finding anchors at the sixth (threshold-crossing)
	// setup.
	raw := `diff --git a/svc.test.ts b/svc.test.ts
--- a/svc.test.ts
+++ b/svc.test.ts
@@ -1,1 +1,9 @@
 test('processes order', () => {
+  jest.mock('./a')
+  jest.mock('./b')
+  jest.spyOn(svc, 'c')
+  jest.spyOn(svc, 'd')
+  api.fetch.mockReturnValue(1)
+  api.save.mockResolvedValue(2)
+  const r = run()
+  expect(r).toBe(2)
`
	got := detect(t, cat, raw)
	f, ok := findingByTitleContains(got, "mocks")
	if !ok {
		t.Fatalf("over-mock: expected a finding, got %+v", got)
	}
	assertResolved(t, f)
	if f.Severity != render.SeverityLow {
		t.Errorf("over-mock severity = %s, want low", f.Severity)
	}
	if f.Snippet != "" {
		t.Errorf("over-mock is file-scoped and should carry no snippet, got %q", f.Snippet)
	}
	// Anchored at the 6th setup = added line 7 (header is new line 1).
	if f.Line != 7 {
		t.Errorf("over-mock line = %d, want 7 (the threshold-crossing setup)", f.Line)
	}
}

func TestDetect_OverMockUnderThreshold(t *testing.T) {
	cat := loadCatalog(t)
	// Exactly the threshold count (5) of legitimate stubs must NOT fire — the
	// gate only triggers above overMockThreshold to keep precision high.
	raw := `diff --git a/svc.test.ts b/svc.test.ts
--- a/svc.test.ts
+++ b/svc.test.ts
@@ -1,1 +1,8 @@
 test('processes order', () => {
+  jest.mock('./a')
+  jest.mock('./b')
+  jest.spyOn(svc, 'c')
+  jest.spyOn(svc, 'd')
+  api.fetch.mockReturnValue(1)
+  const r = run()
+  expect(r).toBe(1)
`
	for _, f := range detect(t, cat, raw) {
		if strings.Contains(strings.ToLower(f.Title), "mocks") {
			t.Fatalf("over-mock fired at the threshold (5); should only fire above it: %+v", f)
		}
	}
}

func TestDetect_OverMockScopedPerFunction(t *testing.T) {
	cat := loadCatalog(t)
	// Three mocks in one test and three in another must NOT combine into a
	// single over-threshold count: scoping is per test function.
	raw := `diff --git a/svc.test.ts b/svc.test.ts
--- a/svc.test.ts
+++ b/svc.test.ts
@@ -1,1 +1,11 @@
 describe('svc', () => {
+  test('a', () => {
+    jest.mock('./a')
+    jest.mock('./b')
+    jest.spyOn(svc, 'c')
+  })
+  test('b', () => {
+    jest.mock('./d')
+    jest.mock('./e')
+    jest.spyOn(svc, 'f')
+  })
`
	for _, f := range detect(t, cat, raw) {
		if strings.Contains(strings.ToLower(f.Title), "mocks") {
			t.Fatalf("over-mock must not aggregate across two functions (3+3): %+v", f)
		}
	}
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
