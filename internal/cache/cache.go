// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type Entry struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	TTL       int64     `json:"ttl"`
	Key       KeyMeta   `json:"key"`
	Result    Result    `json:"result"`
}

type KeyMeta struct {
	DiffHash         string `json:"diff_hash"`
	SystemPromptHash string `json:"system_prompt_hash"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	Lang             string `json:"lang"`
}

type Result struct {
	Content string `json:"content"`
	Tokens  Tokens `json:"tokens"`

	// Format records which renderer path the cached entry should take on
	// replay. Values (ADR-0014 §4):
	//   "json"               — Content is the structured-findings JSON; the
	//                          renderer parses it as usual.
	//   "markdown-fallback"  — provider produced unparseable output; the
	//                          retry also failed. The cached Content is the
	//                          raw text and the renderer skips the parse
	//                          step entirely (no warning re-emitted).
	// Empty string is treated as "json" for backwards compatibility with
	// pre-ADR-0014 cache entries; they'll be invalidated next request
	// anyway via the system-prompt SHA change (ADR-0014 §6).
	Format string `json:"format,omitempty"`
}

type Tokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Cached int `json:"cached"`
}

// Format values for cache.Result.Format. Kept as package-level constants so
// callers (CLI, renderer) avoid stringly-typed comparisons.
const (
	FormatJSON             = "json"
	FormatMarkdownFallback = "markdown-fallback"
	// FormatPlainText is the marker for cache entries written by
	// PlainTextEmitter providers (CLI-based). The Content is the
	// host CLI's already-formatted output — no JSON parse, no
	// retry-once, no degrade warning. Renderer emits Content verbatim.
	FormatPlainText = "plain-text"
)

type Cache struct {
	dir      string
	ttl      time.Duration
	repoRoot string
	now      func() time.Time
}

type Options struct {
	Dir string

	// RepoRoot, if non-empty, enables auto-add of `.commitbrief/` to the
	// repo's .gitignore on the first successful Put.
	RepoRoot string

	// TTL controls how long an entry is considered fresh. Zero falls back
	// to DefaultTTL (7 days).
	TTL time.Duration

	// Now overrides time.Now (test injection); production callers leave it nil.
	Now func() time.Time
}

func Open(opts Options) (*Cache, error) {
	if opts.Dir == "" {
		return nil, errors.New("cache: Dir is required")
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Cache{
		dir:      opts.Dir,
		ttl:      ttl,
		repoRoot: opts.RepoRoot,
		now:      now,
	}, nil
}

func (c *Cache) Get(key string) (Entry, bool) {
	path := c.entryPath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, false
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		// Corrupt entry: drop it silently so the next write replaces.
		_ = os.Remove(path)
		return Entry{}, false
	}
	if e.Version != SchemaVersion {
		return Entry{}, false
	}
	if e.ExpiredAt(c.now()) {
		return Entry{}, false
	}
	return e, true
}

func (c *Cache) Put(key string, entry Entry) error {
	if key == "" {
		return errors.New("cache: empty key")
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return fmt.Errorf("cache: mkdir %s: %w", c.dir, err)
	}

	entry.Version = SchemaVersion
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = c.now().UTC()
	}
	if entry.TTL == 0 {
		entry.TTL = int64(c.ttl.Seconds())
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("cache: marshal: %w", err)
	}
	if err := writeAtomic(c.entryPath(key), data); err != nil {
		return err
	}

	if c.repoRoot != "" {
		if _, err := EnsureGitignore(c.repoRoot); err != nil {
			// .gitignore mutation failure is non-fatal — the cache entry is
			// written. Return the error so callers can log or surface it.
			return fmt.Errorf("cache: entry written but %w", err)
		}
	}
	return nil
}

func (c *Cache) Delete(key string) error {
	path := c.entryPath(key)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cache: delete %s: %w", key, err)
	}
	return nil
}

func (c *Cache) Dir() string { return c.dir }

func (c *Cache) entryPath(key string) string {
	return filepath.Join(c.dir, key+".json")
}
