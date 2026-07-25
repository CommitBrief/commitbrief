// SPDX-License-Identifier: GPL-3.0-or-later

package flaky

import (
	"os"
	"path/filepath"
	"testing"
)

// write drops src into a temp file and returns its path. name may include
// subdirectory segments (e.g. "tests/helpers.go"); the parent is created as
// needed.
func write(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestEnclosingTest_Go(t *testing.T) {
	path := write(t, "session_test.go", `package auth

import "testing"

func helper() {}

func TestLogin(t *testing.T) {
	time.Sleep(2 * time.Second)
}
`)
	// Line 8 is the sleep inside TestLogin.
	name, ok := EnclosingTest(path, 8)
	if !ok || name != "TestLogin" {
		t.Fatalf("EnclosingTest = %q, %v; want \"TestLogin\", true", name, ok)
	}
}

func TestEnclosingTest_NoTestAbove(t *testing.T) {
	// A helper with no enclosing test must report not-found rather than
	// guessing: a wrong name would run the wrong test and produce a
	// confident, wrong verdict.
	path := write(t, "helpers_test.go", `package auth

func helper() {
	sleep(1)
}
`)
	if name, ok := EnclosingTest(path, 4); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found", name)
	}
}

func TestEnclosingTest_MissingFile(t *testing.T) {
	if _, ok := EnclosingTest(filepath.Join(t.TempDir(), "nope.go"), 1); ok {
		t.Fatal("EnclosingTest on a missing file reported ok")
	}
}

func TestEnclosingTest_LineOutOfRange(t *testing.T) {
	path := write(t, "x_test.go", "package a\n")
	if _, ok := EnclosingTest(path, 999); ok {
		t.Fatal("EnclosingTest past EOF reported ok")
	}
}

// --- Fix round 1 (Unicode identifiers, boundary tracking) ------------------
//
// Round 1 fixed two bugs in a hand-rolled regex+scope tracker: ASCII-only
// identifier classes silently truncating non-ASCII names, and an upward scan
// with no notion of where a function body ends. Both cases still apply and
// still pass under the round-3 go/parser rewrite below -- a real Go lexer
// handles Unicode identifiers and function boundaries exactly, by
// construction, so these now demonstrate the property rather than pin a
// specific patch.

func TestEnclosingTest_UnicodeName_Go(t *testing.T) {
	path := write(t, "session_test.go", `package auth

func TestGirişKontrolü(t *testing.T) {
	time.Sleep(1 * time.Second)
}
`)
	name, ok := EnclosingTest(path, 4)
	if !ok || name != "TestGirişKontrolü" {
		t.Fatalf("EnclosingTest = %q, %v; want \"TestGirişKontrolü\", true", name, ok)
	}
}

func TestEnclosingTest_HelperAfterClosedTest_Go(t *testing.T) {
	// A helper declared after a test's closing brace must never be
	// attributed to that test.
	path := write(t, "session_test.go", `package auth

func TestOther(t *testing.T) {
	doSomething()
}

func helperNotATest() {
	time.Sleep(1 * time.Second)
}
`)
	if name, ok := EnclosingTest(path, 8); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (helper is not inside TestOther)", name)
	}
}

// --- Fix round 2 (braces/headers inside strings and comments) --------------
//
// Round 2 fixed a hand-rolled brace-depth tracker counting braces inside
// string/comment spans, which desynced it. All of these still apply and
// still pass under the round-3 rewrite: a real Go lexer tokenizes a string
// or comment as a single opaque unit, so a brace or header-shaped run of
// text inside one is never mistaken for real code, by construction --
// nothing to strip, nothing to get wrong.

func TestEnclosingTest_BraceInDoubleQuotedString(t *testing.T) {
	path := write(t, "session_test.go", `package auth

func TestStringBrace(t *testing.T) {
	x := "{"
}
func helperAfterStringBrace() {
	time.Sleep(1)
}
`)
	if name, ok := EnclosingTest(path, 7); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (helper is not inside TestStringBrace)", name)
	}
}

func TestEnclosingTest_BraceInRawString(t *testing.T) {
	path := write(t, "session_test.go", `package auth

func TestRawStringBrace(t *testing.T) {
	x := `+"`"+`{`+"`"+`
}
func helperAfterRawStringBrace() {
	time.Sleep(1)
}
`)
	if name, ok := EnclosingTest(path, 7); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (helper is not inside TestRawStringBrace)", name)
	}
}

func TestEnclosingTest_BraceInLineComment(t *testing.T) {
	path := write(t, "session_test.go", `package auth

func TestLineCommentBrace(t *testing.T) {
	// unmatched brace in a comment: {
}
func helperAfterLineCommentBrace() {
	time.Sleep(1)
}
`)
	if name, ok := EnclosingTest(path, 7); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (helper is not inside TestLineCommentBrace)", name)
	}
}

func TestEnclosingTest_BraceInBlockComment(t *testing.T) {
	path := write(t, "session_test.go", `package auth

func TestBlockCommentBrace(t *testing.T) {
	/* unmatched brace: { */
}
func helperAfterBlockCommentBrace() {
	time.Sleep(1)
}
`)
	if name, ok := EnclosingTest(path, 7); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (helper is not inside TestBlockCommentBrace)", name)
	}
}

func TestEnclosingTest_HeaderInsideStringLiteral(t *testing.T) {
	// Header-shaped text that only appears inside a string literal must not
	// be read as a real header. This helper is not itself a test, and the
	// fake "func TestFake(...) {" printed inside the log line must not make
	// it look like one -- a real Go lexer never re-tokenizes string content
	// as source.
	path := write(t, "session_test.go", `package auth

func helperWithFakeTestString() {
	log.Println("func TestFake(t *testing.T) {")
	time.Sleep(1)
}
`)
	if name, ok := EnclosingTest(path, 5); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (no real test header here)", name)
	}
}

func TestEnclosingTest_RealBraces(t *testing.T) {
	// Positive control: a test whose body legitimately contains nested
	// braces (an ordinary if-block) must still resolve correctly.
	path := write(t, "session_test.go", `package auth

func TestRealBraces(t *testing.T) {
	if true {
		doStuff()
	}
	time.Sleep(1)
}
`)
	name, ok := EnclosingTest(path, 7)
	if !ok || name != "TestRealBraces" {
		t.Fatalf("EnclosingTest = %q, %v; want \"TestRealBraces\", true", name, ok)
	}
}

// --- Fix round 3: rewrite on go/parser, Go-only -----------------------------
//
// A third lexical bug turned up in round 2's fix (JS keeps a template
// literal open across an escaped backtick; an unpaired backtick then opens a
// phantom string that swallows everything after it). Three rounds, three
// distinct lexical edge cases in a hand-rolled multi-language scanner, each
// fix correct and each followed by another -- with no evidence of
// convergence. Round 3 replaces the whole scanner for Go with the real Go
// compiler frontend (go/parser + go/ast): exact by construction, nothing
// left to get wrong the way rounds 1-2 did. Every other language now
// deliberately returns ("", false) -- non-Go files keep full static flaky
// detection, they simply never get sandbox-rerun confirmation (ADR-0033
// §10's designed fallback for an unresolved name).
//
// The cases below convert every former non-Go positive-resolution test to
// assert not-found (same fixtures, new contract), and add coverage for the
// judgement calls the rewrite makes explicit: which function-name shapes
// count as a runnable test, and whether a receiver method resolves.

func TestEnclosingTest_Python(t *testing.T) {
	// Non-Go resolution is intentionally unsupported (round 3): only Go is
	// parsed. This fixture used to resolve "test_fetches_user"; it now
	// always returns not-found, regardless of content.
	path := write(t, "test_api.py", `import time

def test_fetches_user():
    time.sleep(2)
`)
	if name, ok := EnclosingTest(path, 4); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (non-Go resolution is unsupported)", name)
	}
}

func TestEnclosingTest_JSBlock(t *testing.T) {
	// Non-Go resolution is intentionally unsupported (round 3).
	path := write(t, "api.spec.ts", "describe('api', () => {\n  it('fetches the user', async () => {\n    await sleep(2000)\n  })\n})\n")
	if name, ok := EnclosingTest(path, 3); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (non-Go resolution is unsupported)", name)
	}
}

func TestEnclosingTest_UnicodeName_Python(t *testing.T) {
	// Non-Go resolution is intentionally unsupported (round 3).
	path := write(t, "test_payments.py", `def test_ödeme_başarılı():
    time.sleep(1)
`)
	if name, ok := EnclosingTest(path, 2); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (non-Go resolution is unsupported)", name)
	}
}

func TestEnclosingTest_UnicodeName_PHP(t *testing.T) {
	// Non-Go resolution is intentionally unsupported (round 3).
	path := write(t, "PaymentTest.php", `<?php
class PaymentTest {
    public function testÖdemeBaşarılı() {
        sleep(1);
    }
}
`)
	if name, ok := EnclosingTest(path, 4); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (non-Go resolution is unsupported)", name)
	}
}

func TestEnclosingTest_UnicodeName_Java(t *testing.T) {
	// Non-Go resolution is intentionally unsupported (round 3).
	path := write(t, "PaymentTest.java", `class PaymentTest {
    void testÖdemeBaşarılı() {
        Thread.sleep(1000);
    }
}
`)
	if name, ok := EnclosingTest(path, 3); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (non-Go resolution is unsupported)", name)
	}
}

func TestEnclosingTest_HelperAfterClosedTest_Python(t *testing.T) {
	// Non-Go resolution is intentionally unsupported (round 3).
	path := write(t, "test_payments.py", `def test_something():
    pass

def helper_not_a_test():
    time.sleep(1)
`)
	if name, ok := EnclosingTest(path, 5); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (non-Go resolution is unsupported)", name)
	}
}

func TestEnclosingTest_PythonDocstringDedentConfusion(t *testing.T) {
	// Non-Go resolution is intentionally unsupported (round 3).
	path := write(t, "test_payments.py", `def test_something():
    """
def fake_helper():
    pass
    """
    time.sleep(1)
`)
	if name, ok := EnclosingTest(path, 6); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (non-Go resolution is unsupported)", name)
	}
}

func TestEnclosingTest_UnparsableGo(t *testing.T) {
	// A half-edited or otherwise invalid source file must degrade safely,
	// never guess -- go/parser reports an error and EnclosingTest must not
	// fall back to any partial/best-effort AST it might still return.
	path := write(t, "broken_test.go", `package auth

func TestBroken(t *testing.T) {
	x := (
`)
	if name, ok := EnclosingTest(path, 4); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (file does not parse)", name)
	}
}

func TestEnclosingTest_ReceiverMethodNotRunnable(t *testing.T) {
	// A testify-suite-style method is not directly invocable via
	// `go test -run '^TestThing$'` -- only the outer TestMySuite(t
	// *testing.T) function that calls suite.Run is. Resolving to the
	// method's own name would produce a plausible-looking name `-run` can
	// never match -- the same confident-wrong-verdict failure as any other
	// wrong name (ADR-0033 §10). This was a latent bug in rounds 1-2 too
	// (their Go pattern had an explicit optional-receiver group); the
	// rewrite closes it by requiring fn.Recv == nil.
	path := write(t, "suite_test.go", `package auth

type MySuite struct {
	suite.Suite
}

func (s *MySuite) TestThing() {
	s.Equal(1, 1)
}
`)
	if name, ok := EnclosingTest(path, 8); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (receiver method is not go-test-runnable)", name)
	}
}

func TestEnclosingTest_LineBetweenFunctions(t *testing.T) {
	// A line that falls between two top-level functions -- inside neither
	// one's body -- must resolve to not-found, not to whichever function
	// happens to be nearest.
	path := write(t, "session_test.go", `package auth

func TestFirst(t *testing.T) {
	doSomething()
}

func TestSecond(t *testing.T) {
	doSomethingElse()
}
`)
	if name, ok := EnclosingTest(path, 6); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (line 6 is between the two functions)", name)
	}
}

func TestEnclosingTest_FuzzRecognized(t *testing.T) {
	// Fuzz targets are matched by `go test -run` (`go help testflag`: "run
	// only those tests, examples, and fuzz tests...") and have the same
	// run/observe/repeat shape sandbox-rerun exists for.
	path := write(t, "codec_test.go", `package codec

func FuzzDecode(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		Decode(data)
	})
}
`)
	name, ok := EnclosingTest(path, 5)
	if !ok || name != "FuzzDecode" {
		t.Fatalf("EnclosingTest = %q, %v; want \"FuzzDecode\", true", name, ok)
	}
}

func TestEnclosingTest_BenchmarkNotRecognized(t *testing.T) {
	// `go test -run` does not select benchmarks at all (`-bench` does);
	// reporting a Benchmark* name would render a `-run` pattern that
	// matches zero tests -- exit 0, a confident wrong "passed every rerun"
	// verdict (ADR-0033 §10).
	path := write(t, "codec_test.go", `package codec

func BenchmarkDecode(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Decode(nil)
	}
}
`)
	if name, ok := EnclosingTest(path, 4); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (Benchmark is not -run-selectable)", name)
	}
}

func TestEnclosingTest_ExampleNotRecognized(t *testing.T) {
	// Technically selectable via `-run`, but excluded: an Example's
	// pass/fail is output comparison, not the run/observe/repeat shape
	// sandbox-rerun confirms, and the static detector's rules are not
	// realistically going to fire inside a doctest-style Example body.
	path := write(t, "codec_test.go", `package codec

func ExampleDecode() {
	Decode(nil)
	// Output:
}
`)
	if name, ok := EnclosingTest(path, 4); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (Example is deliberately excluded)", name)
	}
}

func TestEnclosingTest_LowercaseAfterPrefixNotRecognized(t *testing.T) {
	// Replicates `go test`'s own naming rule: "Testfoo" is not a real test
	// -- the go tool itself never registers it as one, since the rune right
	// after "Test" is lowercase -- so `-run '^Testfoo$'` would match zero
	// tests, the same phantom-target failure as any other wrong name. This
	// was also a latent gap in rounds 1-2 (their regex accepted any
	// identifier-shaped suffix).
	path := write(t, "codec_test.go", `package codec

func Testfoo(t *testing.T) {
	time.Sleep(1)
}
`)
	if name, ok := EnclosingTest(path, 4); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (go test does not register Testfoo as a test)", name)
	}
}

// --- Fix round 4: guard on the _test.go suffix ------------------------------
//
// go/parser correctly resolves a Test-shaped function from ANY .go file,
// _test.go or not -- go/ast has no notion of Go's own "is this file compiled
// into the test binary" rule, which is a pure filename convention the build
// tool applies, not something the parser or the language spec enforces.
// `go test` only ever discovers test functions in *_test.go files; a name
// resolved from a plain .go file renders a `-run` pattern that matches zero
// tests -- exit 0, a false VerdictTransient, the same failure chain ADR-0033
// §10 exists to prevent, now closed at the one place a caller cannot forget
// to check it.
//
// This is reachable, not theoretical: flaky.isTestFile (internal/flaky/
// flaky.go) has a directory-based rule -- any path with a "tests/", "test/",
// "spec/", "e2e/", "cypress/", or "__tests__/" segment qualifies for static
// scanning, regardless of the file's own name or suffix. An ordinary Go
// source file living in such a directory (e.g. internal/foo/tests/helpers.go)
// can carry a flaky finding and hand this function exactly this path.

func TestEnclosingTest_NonTestGoFile(t *testing.T) {
	// The realistic path: an ordinary .go file (not *_test.go) inside a
	// "tests/" directory, which flaky.isTestFile's directory rule already
	// lets the static detector scan.
	path := write(t, "tests/helpers.go", `package tests

func TestLooksLikeATest(t *testing.T) {
	sleep(1)
}
`)
	if name, ok := EnclosingTest(path, 4); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (helpers.go is not a _test.go file)", name)
	}
}

func TestEnclosingTest_TestGoFileInTestsDir(t *testing.T) {
	// Positive control paired with TestEnclosingTest_NonTestGoFile: the
	// guard is a filename check (does the base name end in "_test.go"),
	// not a directory check -- a real _test.go file inside the very same
	// "tests/" directory shape must still resolve, so the guard cannot be
	// passing by rejecting everything under that directory.
	path := write(t, "tests/helpers_test.go", `package tests

func TestRealOne(t *testing.T) {
	sleep(1)
}
`)
	name, ok := EnclosingTest(path, 4)
	if !ok || name != "TestRealOne" {
		t.Fatalf("EnclosingTest = %q, %v; want \"TestRealOne\", true", name, ok)
	}
}

// --- Fix round 5: the file must actually be COMPILED, not just named right --
//
// go/parser applies no build constraints -- it happily parses a
// `//go:build integration` file, a GOOS-suffixed file that doesn't match the
// host, or an underscore/dot-prefixed file the go tool ignores outright, and
// each of those can still yield a real-looking Test-shaped name. `go test
// -run '^NAME$'` against any of them matches zero tests: exit 0, a false
// VerdictTransient, the exact failure chain ADR-0033 §10 exists to prevent.
// Round 5 closes this with go/build.MatchFile (does this file actually get
// compiled here, with these tags?) plus a separate testdata/ segment check,
// since MatchFile does not cover testdata on its own (the go tool excludes it
// by skipping the whole directory during package discovery, not per file).

func TestEnclosingTest_BuildTagExcluded(t *testing.T) {
	// A file gated behind a build tag nobody has set must not resolve, even
	// though it parses fine and contains a real-looking test name.
	path := write(t, "integration_test.go", `//go:build integration

package auth

func TestSlowLogin(t *testing.T) {
	time.Sleep(2 * time.Second)
}
`)
	if name, ok := EnclosingTest(path, 5); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (file is excluded by an unset build tag)", name)
	}
}

func TestEnclosingTest_TestdataSegment(t *testing.T) {
	// go/build.MatchFile alone reports true for a file under testdata/ (the
	// go tool excludes the directory during package discovery, not this
	// file individually), so the separate path-segment check must reject it.
	path := write(t, "testdata/sample_test.go", `package testdata

func TestFixture(t *testing.T) {
	sleep(1)
}
`)
	if name, ok := EnclosingTest(path, 4); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (file lives under testdata/)", name)
	}
}

func TestEnclosingTest_GOOSSuffixedExcluded(t *testing.T) {
	// A "_plan9_test.go" filename restricts the file to GOOS=plan9. The CI
	// matrix (ubuntu/macos/windows) and every developer machine this ships
	// on never satisfy that, so this must always resolve to not found.
	path := write(t, "login_plan9_test.go", `package auth

func TestPlan9Login(t *testing.T) {
	time.Sleep(1)
}
`)
	if name, ok := EnclosingTest(path, 4); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (GOOS-suffixed file excluded on this host)", name)
	}
}

func TestEnclosingTest_LeadingUnderscoreFileExcluded(t *testing.T) {
	// The go tool ignores any file whose base name starts with "_" or "."
	// outright; go/build.MatchFile replicates that.
	path := write(t, "_login_test.go", `package auth

func TestUnderscoreLogin(t *testing.T) {
	time.Sleep(1)
}
`)
	if name, ok := EnclosingTest(path, 4); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (leading-underscore file is ignored by the go tool)", name)
	}
}

func TestEnclosingTest_OrdinaryCompiledFileStillResolves(t *testing.T) {
	// Positive control: an ordinary _test.go file with no build tag, no
	// GOOS/GOARCH suffix, no testdata/ segment, and no ignored-name prefix
	// must still resolve after round 5 -- the new guard must not be passing
	// by rejecting everything.
	path := write(t, "ordinary_test.go", `package auth

func TestOrdinary(t *testing.T) {
	time.Sleep(1)
}
`)
	name, ok := EnclosingTest(path, 4)
	if !ok || name != "TestOrdinary" {
		t.Fatalf("EnclosingTest = %q, %v; want \"TestOrdinary\", true", name, ok)
	}
}
