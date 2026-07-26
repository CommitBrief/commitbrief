// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package upgrade

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReplaceBinaryRollsBackWhenSecondRenameFails covers the branch that
// decides whether a failed upgrade leaves a working install behind. It is
// forced through osRename rather than left to chance, because the natural
// trigger (two consecutive renames failing in one directory) cannot be
// reproduced reliably.
func TestReplaceBinaryRollsBackWhenSecondRenameFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "commitbrief.exe")
	tmp := filepath.Join(dir, ".commitbrief-new")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}

	original := osRename
	calls := 0
	osRename = func(from, to string) error {
		calls++
		if calls == 2 { // the tmp → target rename
			return errors.New("induced failure")
		}
		return original(from, to)
	}
	t.Cleanup(func() { osRename = original })

	if err := replaceBinary(tmp, target); err == nil {
		t.Fatal("replaceBinary() error = nil, want the induced failure")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("target missing after rollback — the install was left broken: %v", err)
	}
	if string(got) != "OLD" {
		t.Fatalf("target content = %q, want the original OLD restored", got)
	}
}

// TestReplaceBinaryReportsFailedRollback asserts the worst case is at
// least diagnosable: when the rollback fails too, the error must name the
// backup path so the user can recover by hand.
func TestReplaceBinaryReportsFailedRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "commitbrief.exe")
	tmp := filepath.Join(dir, ".commitbrief-new")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}

	original := osRename
	calls := 0
	osRename = func(from, to string) error {
		calls++
		if calls >= 2 { // both the swap and the rollback fail
			return errors.New("induced failure")
		}
		return original(from, to)
	}
	t.Cleanup(func() { osRename = original })

	err := replaceBinary(tmp, target)
	if err == nil {
		t.Fatal("replaceBinary() error = nil, want a failed-rollback error")
	}
	if !strings.Contains(err.Error(), target+".old") {
		t.Fatalf("error must name the recovery path %q, got: %v", target+".old", err)
	}
}

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
