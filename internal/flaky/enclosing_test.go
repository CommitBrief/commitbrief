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
