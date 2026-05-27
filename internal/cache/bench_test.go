// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"testing"
	"time"
)

// PRD §7.1: cache hit additional latency < 100ms. Cache hits are a
// file read + JSON unmarshal; on tmpfs / modern SSD this is a sub-
// millisecond operation, so the benchmark exists to catch a
// regression (e.g. accidental fsync, extra lookups) rather than to
// race the 100ms ceiling.
//
// Run with:
//
//	go test -bench=. -benchmem -run=^$ ./internal/cache
func BenchmarkCacheHit(b *testing.B) {
	dir := b.TempDir()
	c, err := Open(Options{Dir: dir, TTL: 24 * time.Hour})
	if err != nil {
		b.Fatal(err)
	}
	key := "sha256bench00000000000000000000000000000000000000000000000000000"
	entry := Entry{
		Key: KeyMeta{
			DiffHash: "sha256:abc",
			Provider: "anthropic",
			Model:    "claude-opus-4-7",
			Lang:     "en",
		},
		Result: Result{
			Content: "# Review\n\nLGTM\n",
			Tokens:  Tokens{Input: 1200, Output: 240, Cached: 800},
		},
	}
	if err := c.Put(key, entry); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, hit := c.Get(key); !hit {
			b.Fatal("expected cache hit")
		}
	}
}
