// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package upgrade

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupStaleRemovesOldFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "commitbrief.exe")
	old := target + ".old"

	if err := os.WriteFile(target, []byte("BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("PREVIOUS"), 0o755); err != nil {
		t.Fatal(err)
	}

	CleanupStale(target)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf(".old file still present: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target disappeared: %v", err)
	}
}
