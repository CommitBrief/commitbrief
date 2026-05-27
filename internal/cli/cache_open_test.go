// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"path/filepath"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
)

// UC-02 regression guards. The cache.* knobs in config used to be
// completely inert — review.go always opened a store with default TTL
// regardless of cache.enabled or cache.ttl_days. These tests pin the
// new behaviour: enabled=false returns a nil store (callers already
// nil-check), enabled=true honours the config-supplied dir.

func TestOpenCacheDisabledReturnsNilStore(t *testing.T) {
	dir := t.TempDir()
	cfg := config.CacheConfig{Enabled: false, TTLDays: 7}
	store, err := openCache(dir, cfg)
	if err != nil {
		t.Fatalf("openCache disabled: unexpected error %v", err)
	}
	if store != nil {
		t.Errorf("disabled cache must return nil store; got %#v", store)
	}
}

func TestOpenCacheEnabledReturnsStore(t *testing.T) {
	dir := t.TempDir()
	cfg := config.CacheConfig{Enabled: true, TTLDays: 1}
	store, err := openCache(dir, cfg)
	if err != nil {
		t.Fatalf("openCache enabled: %v", err)
	}
	if store == nil {
		t.Fatal("enabled cache must return a non-nil store")
	}
	want := filepath.Join(dir, ".commitbrief", "cache")
	if got := store.Dir(); got != want {
		t.Errorf("store.Dir() = %q, want %q", got, want)
	}
}

func TestOpenCacheEmptyRepoErrors(t *testing.T) {
	// The function still requires a repo root — cache lives inside the
	// repo and there's no fallback location.
	cfg := config.CacheConfig{Enabled: true}
	if _, err := openCache("", cfg); err == nil {
		t.Errorf("empty repo root should error")
	}
}
