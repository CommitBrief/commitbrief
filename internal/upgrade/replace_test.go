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

// TestCleanupStaleRemovesTempFiles covers the orphan an interrupted
// InstallManual (Ctrl-C, before its defers run) leaves behind: a
// ".commitbrief-dl-*" or ".commitbrief-bin-*" scratch file next to the
// target, never cleaned up until the next upgrade sweeps it.
func TestCleanupStaleRemovesTempFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "commitbrief")
	if err := os.WriteFile(target, []byte("BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := []string{
		filepath.Join(dir, ".commitbrief-dl-abc123"),
		filepath.Join(dir, ".commitbrief-bin-abc123"),
	}
	for _, f := range stale {
		if err := os.WriteFile(f, []byte("orphan"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	CleanupStale(target)

	for _, f := range stale {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("stale temp file still present: %s", f)
		}
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target disappeared: %v", err)
	}
}
