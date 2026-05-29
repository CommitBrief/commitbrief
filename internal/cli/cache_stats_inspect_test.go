// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/cache"
)

// seedCacheEntry writes a valid cache entry at <repo>/.commitbrief/cache/<key>.json.
func seedCacheEntry(t *testing.T, repoRoot, key, provider, model string, createdAt time.Time, content string) {
	t.Helper()
	dir := filepath.Join(repoRoot, ".commitbrief", "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	e := cache.Entry{
		Version:   cache.SchemaVersion,
		CreatedAt: createdAt,
		TTL:       int64((7 * 24 * time.Hour).Seconds()),
		Key:       cache.KeyMeta{Provider: provider, Model: model, Lang: "en", DiffHash: "sha256:deadbeef"},
		Result: cache.Result{
			Content: content,
			Tokens:  cache.Tokens{Input: 1000, Output: 500, Cached: 0},
			Format:  cache.FormatJSON,
		},
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, key+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCacheStatsEmpty(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("cache", "stats"); err != nil {
		t.Fatalf("cache stats on empty repo: %v", err)
	}
	out := e.out.String()
	if !strings.Contains(out, "No cached entries") {
		t.Errorf("empty stats should surface empty-state message; got:\n%s", truncate(out, 400))
	}
}

func TestCacheStatsReportsCountsAndBreakdown(t *testing.T) {
	e := newCLIEnv(t)
	base := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	seedCacheEntry(t, e.repoRoot, "k1", "anthropic", "claude-opus-4-7", base, "aaa")
	seedCacheEntry(t, e.repoRoot, "k2", "anthropic", "claude-opus-4-7", base.Add(time.Hour), "bbb")
	seedCacheEntry(t, e.repoRoot, "k3", "openai", "gpt-4o", base.Add(2*time.Hour), "ccc")

	if err := e.run("cache", "stats"); err != nil {
		t.Fatalf("cache stats: %v", err)
	}
	out := e.out.String()
	for _, want := range []string{
		"Cache: 3", "By provider/model:", "anthropic", "openai",
		"2026-05-20T00:00:00Z", // oldest
		"Size limit: unlimited",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stats output missing %q; got:\n%s", want, truncate(out, 800))
		}
	}
}

func TestCacheStatsShowsBoundedLimit(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("config", "set", "cache.max_size_mb", "50"); err != nil {
		t.Fatalf("config set max_size_mb: %v", err)
	}
	seedCacheEntry(t, e.repoRoot, "k1", "anthropic", "claude-opus-4-7",
		time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), "aaa")

	e.out.Reset()
	if err := e.run("cache", "stats"); err != nil {
		t.Fatalf("cache stats: %v", err)
	}
	out := e.out.String()
	if !strings.Contains(out, "cache.max_size_mb=50") {
		t.Errorf("stats should report the configured size limit; got:\n%s", truncate(out, 800))
	}
}

func TestCacheInspectNotFound(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("cache", "inspect", "nonexistentkey"); err != nil {
		t.Fatalf("cache inspect missing key: %v", err)
	}
	out := e.out.String()
	if !strings.Contains(out, "No cache entry with key") {
		t.Errorf("inspect of missing key should surface not-found; got:\n%s", truncate(out, 400))
	}
}

func TestCacheInspectShowsMetadata(t *testing.T) {
	e := newCLIEnv(t)
	seedCacheEntry(t, e.repoRoot, "abc123", "anthropic", "claude-opus-4-7",
		time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), "review body here")

	if err := e.run("cache", "inspect", "abc123"); err != nil {
		t.Fatalf("cache inspect: %v", err)
	}
	out := e.out.String()
	for _, want := range []string{
		"Provider:", "anthropic", "Model:", "claude-opus-4-7",
		"Created:", "2026-05-20T00:00:00Z", "Format:", "json",
		"input=1000", "Diff hash:", "sha256:deadbeef",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect output missing %q; got:\n%s", want, truncate(out, 800))
		}
	}
	// Content is omitted unless --show-content.
	if strings.Contains(out, "review body here") {
		t.Errorf("inspect must not print content without --show-content; got:\n%s", truncate(out, 800))
	}
}

func TestCacheInspectShowContent(t *testing.T) {
	e := newCLIEnv(t)
	seedCacheEntry(t, e.repoRoot, "abc123", "anthropic", "claude-opus-4-7",
		time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), "review body here")

	if err := e.run("cache", "inspect", "abc123", "--show-content"); err != nil {
		t.Fatalf("cache inspect --show-content: %v", err)
	}
	out := e.out.String()
	if !strings.Contains(out, "review body here") {
		t.Errorf("--show-content should print the cached body; got:\n%s", truncate(out, 800))
	}
}

func TestCacheInspectAcceptsJSONSuffix(t *testing.T) {
	e := newCLIEnv(t)
	seedCacheEntry(t, e.repoRoot, "abc123", "openai", "gpt-4o",
		time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), "x")

	if err := e.run("cache", "inspect", "abc123.json"); err != nil {
		t.Fatalf("cache inspect with .json suffix: %v", err)
	}
	if !strings.Contains(e.out.String(), "gpt-4o") {
		t.Errorf("inspect should accept a key with .json suffix; got:\n%s", truncate(e.out.String(), 400))
	}
}
