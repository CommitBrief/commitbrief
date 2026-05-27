// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/cache"
)

func TestParseAgeDuration(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		in   string
		want time.Duration
		bad  bool
	}{
		{"7d", 7 * day, false},
		{"1d", 1 * day, false},
		{"2w", 14 * day, false},
		{"3m", 90 * day, false},
		{"1y", 365 * day, false},
		{"0d", 0, false},
		// Bad inputs — explicit rejection is the whole point of this
		// parser. Each case represents a typo we'd rather catch than
		// silently re-interpret.
		{"7", 0, true},          // missing unit
		{"d", 0, true},          // missing count
		{"", 0, true},           // empty
		{"7h", 0, true},         // unit not in d/w/m/y set
		{"7.5d", 0, true},       // decimal not supported (we keep parser stupid)
		{"-7d", 0, true},        // negatives don't make sense for "older than"
		{"1y2m", 0, true},       // we only accept a single count+unit pair
		{"  7d  ", 0, true},     // whitespace not tolerated
		{"forever", 0, true},    // arbitrary string
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseAgeDuration(c.in)
			if c.bad {
				if err == nil {
					t.Errorf("parseAgeDuration(%q) = %v, want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAgeDuration(%q): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("parseAgeDuration(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// writePruneEntry seeds a cache file with the given metadata. mtime
// on disk is set to created so age-based filtering can be exercised.
func writePruneEntry(t *testing.T, dir, name string, e cache.Entry) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".json")
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, e.CreatedAt, e.CreatedAt); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPruneCacheDir_KeepLastDropsOlderEntries(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	// Seed five recent entries; all within the age window. keep-last=3
	// must drop the two oldest.
	for i := 0; i < 5; i++ {
		writePruneEntry(t, dir, "e"+string(rune('0'+i)), cache.Entry{
			CreatedAt: now.Add(time.Duration(-i) * time.Hour),
			Key:       cache.KeyMeta{Provider: "mock", Model: "m1"},
		})
	}
	r, err := pruneCacheDir(dir, pruneCriteria{
		keepLast:  3,
		olderThan: 30 * 24 * time.Hour, // generous; only keep-last gates here
		now:       now,
	})
	if err != nil {
		t.Fatalf("pruneCacheDir: %v", err)
	}
	if r.removed != 2 || r.surviving != 3 {
		t.Errorf("keep-last=3 over 5 entries: removed=%d surviving=%d, want 2/3", r.removed, r.surviving)
	}
}

func TestPruneCacheDir_OlderThanDropsAgedEntries(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	// 3 entries: today, 5d old, 30d old. older-than=7d → only the 30d
	// entry should go.
	writePruneEntry(t, dir, "fresh", cache.Entry{
		CreatedAt: now,
		Key:       cache.KeyMeta{Provider: "mock"},
	})
	writePruneEntry(t, dir, "midage", cache.Entry{
		CreatedAt: now.Add(-5 * 24 * time.Hour),
		Key:       cache.KeyMeta{Provider: "mock"},
	})
	writePruneEntry(t, dir, "stale", cache.Entry{
		CreatedAt: now.Add(-30 * 24 * time.Hour),
		Key:       cache.KeyMeta{Provider: "mock"},
	})
	r, err := pruneCacheDir(dir, pruneCriteria{
		keepLast:  1_000_000, // effectively unlimited; only age gates
		olderThan: 7 * 24 * time.Hour,
		now:       now,
	})
	if err != nil {
		t.Fatalf("pruneCacheDir: %v", err)
	}
	if r.removed != 1 || r.surviving != 2 {
		t.Errorf("older-than=7d over 3 entries: removed=%d surviving=%d, want 1/2", r.removed, r.surviving)
	}
	// Confirm the right one survived.
	if _, err := os.Stat(filepath.Join(dir, "stale.json")); !os.IsNotExist(err) {
		t.Errorf("stale entry should be gone; stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fresh.json")); err != nil {
		t.Errorf("fresh entry should survive; stat err=%v", err)
	}
}

func TestPruneCacheDir_ProviderFilterScopesPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	// Two mock entries (one fresh, one ancient) + two anthropic entries
	// (one fresh, one ancient). --provider mock with --older-than 7d
	// should drop ONLY the ancient mock entry; the ancient anthropic
	// entry stays untouched because it falls outside the provider
	// filter.
	writePruneEntry(t, dir, "m_fresh", cache.Entry{
		CreatedAt: now,
		Key:       cache.KeyMeta{Provider: "mock"},
	})
	writePruneEntry(t, dir, "m_old", cache.Entry{
		CreatedAt: now.Add(-30 * 24 * time.Hour),
		Key:       cache.KeyMeta{Provider: "mock"},
	})
	writePruneEntry(t, dir, "a_fresh", cache.Entry{
		CreatedAt: now,
		Key:       cache.KeyMeta{Provider: "anthropic"},
	})
	writePruneEntry(t, dir, "a_old", cache.Entry{
		CreatedAt: now.Add(-30 * 24 * time.Hour),
		Key:       cache.KeyMeta{Provider: "anthropic"},
	})

	r, err := pruneCacheDir(dir, pruneCriteria{
		keepLast:  1000,
		olderThan: 7 * 24 * time.Hour,
		provider:  "mock",
		now:       now,
	})
	if err != nil {
		t.Fatalf("pruneCacheDir: %v", err)
	}
	if r.removed != 1 {
		t.Errorf("provider=mock with --older-than 7d: removed=%d, want 1", r.removed)
	}
	// anthropic entries must still be on disk.
	if _, err := os.Stat(filepath.Join(dir, "a_old.json")); err != nil {
		t.Errorf("anthropic ancient entry should survive provider filter; stat err=%v", err)
	}
}

func TestPruneCacheDir_ModelFilterScopesPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	writePruneEntry(t, dir, "sonnet_old", cache.Entry{
		CreatedAt: now.Add(-30 * 24 * time.Hour),
		Key:       cache.KeyMeta{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	})
	writePruneEntry(t, dir, "opus_old", cache.Entry{
		CreatedAt: now.Add(-30 * 24 * time.Hour),
		Key:       cache.KeyMeta{Provider: "anthropic", Model: "claude-opus-4-7"},
	})

	r, err := pruneCacheDir(dir, pruneCriteria{
		keepLast:  1000,
		olderThan: 7 * 24 * time.Hour,
		model:     "claude-sonnet-4-6",
		now:       now,
	})
	if err != nil {
		t.Fatalf("pruneCacheDir: %v", err)
	}
	if r.removed != 1 {
		t.Errorf("model filter: removed=%d, want 1", r.removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "opus_old.json")); err != nil {
		t.Errorf("opus entry should survive model filter; stat err=%v", err)
	}
}

func TestPruneCacheDir_MissingDirIsNotAnError(t *testing.T) {
	// Calling prune on a brand-new repo with no cache dir yet should
	// quietly succeed — same pattern as `cache clear`.
	dir := filepath.Join(t.TempDir(), "never-existed")
	r, err := pruneCacheDir(dir, pruneCriteria{
		keepLast:  500,
		olderThan: 7 * 24 * time.Hour,
		now:       time.Now(),
	})
	if err != nil {
		t.Fatalf("missing dir should be tolerated; got %v", err)
	}
	if r.removed != 0 || r.surviving != 0 {
		t.Errorf("missing dir result = %+v, want zero result", r)
	}
}

func TestPruneCacheDir_DefaultsKeepRecentEntries(t *testing.T) {
	// Simulating the spec's default invocation: keep-last=500 +
	// older-than=7d. With three entries (today / 3d / 10d) the 10d
	// entry should be the only one pruned. Mirrors the
	// `cache prune` (no flags) UX guarantee.
	dir := t.TempDir()
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	writePruneEntry(t, dir, "today", cache.Entry{
		CreatedAt: now,
		Key:       cache.KeyMeta{Provider: "mock"},
	})
	writePruneEntry(t, dir, "three", cache.Entry{
		CreatedAt: now.Add(-3 * 24 * time.Hour),
		Key:       cache.KeyMeta{Provider: "mock"},
	})
	writePruneEntry(t, dir, "ten", cache.Entry{
		CreatedAt: now.Add(-10 * 24 * time.Hour),
		Key:       cache.KeyMeta{Provider: "mock"},
	})
	r, err := pruneCacheDir(dir, pruneCriteria{
		keepLast:  defaultKeepLast,
		olderThan: 7 * 24 * time.Hour,
		now:       now,
	})
	if err != nil {
		t.Fatalf("pruneCacheDir: %v", err)
	}
	if r.removed != 1 || r.surviving != 2 {
		t.Errorf("defaults over 3 entries: removed=%d surviving=%d, want 1/2", r.removed, r.surviving)
	}
}
