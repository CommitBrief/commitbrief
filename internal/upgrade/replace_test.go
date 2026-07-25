// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceBinarySwapsContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "commitbrief")
	tmp := filepath.Join(dir, ".commitbrief-new")

	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceBinary(tmp, target); err != nil {
		t.Fatalf("replaceBinary() error = %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW" {
		t.Fatalf("target content = %q, want NEW", got)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temp file still present: %v", err)
	}
}

func TestCleanupStaleIsSafeWhenNothingToClean(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "commitbrief")
	if err := os.WriteFile(target, []byte("BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Must not panic and must not touch the live binary.
	CleanupStale(target)
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target disappeared: %v", err)
	}
}
