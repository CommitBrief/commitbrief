// SPDX-License-Identifier: GPL-3.0-or-later

package flaky

import (
	"os"
	"path/filepath"
	"testing"
)

// write drops src into a temp file and returns its path.
func write(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
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

func TestEnclosingTest_Python(t *testing.T) {
	path := write(t, "test_api.py", `import time

def test_fetches_user():
    time.sleep(2)
`)
	name, ok := EnclosingTest(path, 4)
	if !ok || name != "test_fetches_user" {
		t.Fatalf("EnclosingTest = %q, %v; want \"test_fetches_user\", true", name, ok)
	}
}

func TestEnclosingTest_JSBlock(t *testing.T) {
	path := write(t, "api.spec.ts", "describe('api', () => {\n  it('fetches the user', async () => {\n    await sleep(2000)\n  })\n})\n")
	name, ok := EnclosingTest(path, 3)
	if !ok || name != "fetches the user" {
		t.Fatalf("EnclosingTest = %q, %v; want \"fetches the user\", true", name, ok)
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

// --- Fix round 1 regressions -----------------------------------------------
//
// Both groups below pin down bugs adversarial review found in the original
// implementation: (1) Go's regexp \w / [A-Za-z0-9_] classes are ASCII-only,
// so a non-ASCII identifier (routine input -- the maintainer's own working
// language is Turkish) was silently truncated, producing a wrong-but-valid
// name; (2) the original upward regex scan had no notion of where a
// function body ends, so a helper declared after a test's closing brace was
// misattributed to that test. ADR-0033 §10 names a wrong name as the worst
// outcome this helper can produce -- worse than reporting not-found.

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

func TestEnclosingTest_UnicodeName_Python(t *testing.T) {
	path := write(t, "test_payments.py", `def test_ödeme_başarılı():
    time.sleep(1)
`)
	name, ok := EnclosingTest(path, 2)
	if !ok || name != "test_ödeme_başarılı" {
		t.Fatalf("EnclosingTest = %q, %v; want \"test_ödeme_başarılı\", true", name, ok)
	}
}

func TestEnclosingTest_UnicodeName_PHP(t *testing.T) {
	path := write(t, "PaymentTest.php", `<?php
class PaymentTest {
    public function testÖdemeBaşarılı() {
        sleep(1);
    }
}
`)
	name, ok := EnclosingTest(path, 4)
	if !ok || name != "testÖdemeBaşarılı" {
		t.Fatalf("EnclosingTest = %q, %v; want \"testÖdemeBaşarılı\", true", name, ok)
	}
}

func TestEnclosingTest_UnicodeName_Java(t *testing.T) {
	path := write(t, "PaymentTest.java", `class PaymentTest {
    void testÖdemeBaşarılı() {
        Thread.sleep(1000);
    }
}
`)
	name, ok := EnclosingTest(path, 3)
	if !ok || name != "testÖdemeBaşarılı" {
		t.Fatalf("EnclosingTest = %q, %v; want \"testÖdemeBaşarılı\", true", name, ok)
	}
}

func TestEnclosingTest_HelperAfterClosedTest_Go(t *testing.T) {
	// A helper declared after a test's closing brace must never be
	// attributed to that test: a purely-upward regex scan with no notion of
	// where a function body ends will do exactly that.
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

func TestEnclosingTest_HelperAfterClosedTest_Python(t *testing.T) {
	// Python's own dedent rule closes a def's scope the moment a later
	// line's indentation drops back to (or below) the def's own indent.
	path := write(t, "test_payments.py", `def test_something():
    pass

def helper_not_a_test():
    time.sleep(1)
`)
	if name, ok := EnclosingTest(path, 5); ok {
		t.Fatalf("EnclosingTest = %q, true; want not found (helper is not inside test_something)", name)
	}
}
