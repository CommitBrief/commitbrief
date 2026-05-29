// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeEntryFile drops a syntactically valid entry at dir/<name>.json
// with the given CreatedAt and a padded Content so its on-disk size is
// roughly controllable. Returns the file's actual size.
func writeEntryFile(t *testing.T, dir, name string, createdAt time.Time, pad int) int64 {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	e := Entry{
		Version:   SchemaVersion,
		CreatedAt: createdAt,
		TTL:       3600,
		Result:    Result{Content: strings.Repeat("x", pad), Format: FormatJSON},
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatalf("stat %s: %v", path, err)
	return false
}

func TestEnforceSizeLimitUnlimited(t *testing.T) {
	dir := t.TempDir()
	writeEntryFile(t, dir, "a", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 1000)
	for _, max := range []int64{0, -1} {
		removed, freed, err := enforceSizeLimit(dir, max, "")
		if err != nil {
			t.Fatalf("max=%d: %v", max, err)
		}
		if removed != 0 || freed != 0 {
			t.Errorf("max=%d: removed=%d freed=%d, want 0/0 (unlimited)", max, removed, freed)
		}
	}
}

func TestEnforceSizeLimitNoOpUnderCap(t *testing.T) {
	dir := t.TempDir()
	size := writeEntryFile(t, dir, "a", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 100)
	removed, freed, err := enforceSizeLimit(dir, size+1000, "")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 || freed != 0 {
		t.Errorf("under cap: removed=%d freed=%d, want 0/0", removed, freed)
	}
}

func TestEnforceSizeLimitMissingDir(t *testing.T) {
	removed, freed, err := enforceSizeLimit(filepath.Join(t.TempDir(), "absent"), 10, "")
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if removed != 0 || freed != 0 {
		t.Errorf("missing dir: removed=%d freed=%d, want 0/0", removed, freed)
	}
}

func TestEnforceSizeLimitEvictsOldestFirst(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Three equally-sized entries, oldest → newest.
	s := writeEntryFile(t, dir, "old", base, 500)
	writeEntryFile(t, dir, "mid", base.Add(time.Hour), 500)
	writeEntryFile(t, dir, "new", base.Add(2*time.Hour), 500)

	// Cap that fits ~2 entries forces exactly one eviction (the oldest).
	removed, freed, err := enforceSizeLimit(dir, 2*s+s/2, "")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if freed != s {
		t.Errorf("freed = %d, want %d", freed, s)
	}
	if exists(t, filepath.Join(dir, "old.json")) {
		t.Error("oldest entry should have been evicted")
	}
	if !exists(t, filepath.Join(dir, "mid.json")) || !exists(t, filepath.Join(dir, "new.json")) {
		t.Error("newer entries should survive")
	}
}

func TestEnforceSizeLimitProtectsKeep(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// "old" is both the oldest AND the protected entry — it must survive
	// even though oldest-first would normally evict it first.
	writeEntryFile(t, dir, "old", base, 500)
	writeEntryFile(t, dir, "new", base.Add(time.Hour), 500)

	// Cap forces one eviction; protected "old" is spared, so "new" goes.
	removed, _, err := enforceSizeLimit(dir, 600, "old.json")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if !exists(t, filepath.Join(dir, "old.json")) {
		t.Error("protected entry must never be evicted")
	}
	if exists(t, filepath.Join(dir, "new.json")) {
		t.Error("unprotected entry should have been evicted")
	}
}

func TestEnforceSizeLimitSingleEntryOverCapSurvivesWhenProtected(t *testing.T) {
	dir := t.TempDir()
	writeEntryFile(t, dir, "big", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 5000)
	removed, freed, err := enforceSizeLimit(dir, 100, "big.json")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 || freed != 0 {
		t.Errorf("removed=%d freed=%d, want 0/0 (lone protected entry stays over-budget)", removed, freed)
	}
	if !exists(t, filepath.Join(dir, "big.json")) {
		t.Error("protected entry should survive even when alone over cap")
	}
}

func TestEnforceSizeLimitIgnoresNonEntryFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A stray temp file from an in-flight write must not be counted or
	// removed by the size sweep.
	tmp := filepath.Join(dir, "inflight.json.tmp")
	if err := os.WriteFile(tmp, make([]byte, 4000), 0o600); err != nil {
		t.Fatal(err)
	}
	writeEntryFile(t, dir, "a", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 100)
	removed, _, err := enforceSizeLimit(dir, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	// Only the .json entry is a candidate; the .tmp is invisible to the sweep.
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (.tmp ignored)", removed)
	}
	if !exists(t, tmp) {
		t.Error(".tmp file should be left untouched")
	}
}

func TestPutEvictsWhenOverMaxSize(t *testing.T) {
	frozen := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	now := frozen
	dir := filepath.Join(t.TempDir(), "cache")
	c, err := Open(Options{
		Dir:          dir,
		MaxSizeBytes: 1200, // fits ~1 padded entry below
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	big := Entry{Result: Result{Content: strings.Repeat("y", 800)}}

	// First Put: under cap, survives.
	if err := c.Put("first", big); err != nil {
		t.Fatal(err)
	}
	// Advance the clock so the second entry is unambiguously newer.
	now = frozen.Add(time.Hour)
	if err := c.Put("second", big); err != nil {
		t.Fatal(err)
	}

	if _, ok := c.Get("first"); ok {
		t.Error("oldest entry should have been evicted on the over-cap Put")
	}
	if _, ok := c.Get("second"); !ok {
		t.Error("just-written entry must survive eviction")
	}
}
