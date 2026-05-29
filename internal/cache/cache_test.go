// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newCache(t *testing.T) *Cache {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cache")
	c, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func sampleEntry() Entry {
	return Entry{
		Key: KeyMeta{
			DiffHash:         "sha256:abc",
			SystemPromptHash: "sha256:def",
			Model:            "claude-opus-4-7",
			Provider:         "anthropic",
			Lang:             "en",
		},
		Result: Result{
			Content: "review output",
			Tokens:  Tokens{Input: 1000, Output: 500, Cached: 200},
		},
	}
}

func TestComputeDeterministic(t *testing.T) {
	args := ComputeArgs{Diff: "d", SystemPrompt: "s", Provider: "p", Model: "m", Lang: "en"}
	a := Compute(args)
	b := Compute(args)
	if a != b {
		t.Errorf("Compute should be deterministic; got %q vs %q", a, b)
	}
}

func TestComputeChangesOnEachComponent(t *testing.T) {
	base := ComputeArgs{Diff: "d", SystemPrompt: "s", Provider: "p", Model: "m", Lang: "en"}
	baseKey := Compute(base)

	cases := []struct {
		name string
		mut  func(*ComputeArgs)
	}{
		{"Diff", func(a *ComputeArgs) { a.Diff = "different" }},
		{"SystemPrompt", func(a *ComputeArgs) { a.SystemPrompt = "different" }},
		{"Provider", func(a *ComputeArgs) { a.Provider = "different" }},
		{"Model", func(a *ComputeArgs) { a.Model = "different" }},
		{"Lang", func(a *ComputeArgs) { a.Lang = "tr" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			modified := base
			c.mut(&modified)
			if Compute(modified) == baseKey {
				t.Errorf("changing %s should change the key", c.name)
			}
		})
	}
}

func TestComputeKeyLengthIsSHA256Hex(t *testing.T) {
	k := Compute(ComputeArgs{})
	if len(k) != 64 {
		t.Errorf("key length = %d, want 64 (sha256 hex)", len(k))
	}
}

// TestComputeWithContextMarker: a --with-context run (ADR-0017) must not
// alias a diff-only run on the same diff, and — critically — WithContext:
// false must keep the pre-ADR-0017 key byte-for-byte so the upgrade does
// not mass-invalidate existing caches. The expected non-context key is
// recomputed here from the documented formula, independent of Compute, so
// any accidental change to the non-context hashing is caught.
func TestComputeWithContextMarker(t *testing.T) {
	args := ComputeArgs{Diff: "d", SystemPrompt: "s", Provider: "claude-cli", Model: "m", Lang: "en"}

	noCtx := Compute(args)
	withCtx := Compute(ComputeArgs{Diff: "d", SystemPrompt: "s", Provider: "claude-cli", Model: "m", Lang: "en", WithContext: true})
	if noCtx == withCtx {
		t.Error("context and diff-only runs must produce different cache keys")
	}

	// Independent recomputation of the pre-ADR-0017 formula.
	h := sha256.New()
	h.Write([]byte("d"))
	h.Write([]byte("::"))
	h.Write([]byte("s"))
	h.Write([]byte("::"))
	h.Write([]byte("claude-cli"))
	h.Write([]byte(":"))
	h.Write([]byte("m"))
	h.Write([]byte(":"))
	h.Write([]byte("en"))
	h.Write([]byte(":"))
	h.Write([]byte(strconv.Itoa(SchemaVersion)))
	want := hex.EncodeToString(h.Sum(nil))
	if noCtx != want {
		t.Errorf("non-context key changed (would invalidate every cache):\n got  %s\n want %s", noCtx, want)
	}
}

func TestPutGetRoundTrip(t *testing.T) {
	c := newCache(t)
	key := Compute(ComputeArgs{Diff: "d", Model: "m"})
	in := sampleEntry()
	if err := c.Put(key, in); err != nil {
		t.Fatal(err)
	}
	out, ok := c.Get(key)
	if !ok {
		t.Fatal("Get miss after Put")
	}
	if out.Result.Content != in.Result.Content {
		t.Errorf("Content = %q", out.Result.Content)
	}
	if out.Key.Model != in.Key.Model {
		t.Errorf("Key.Model = %q", out.Key.Model)
	}
	if out.Result.Tokens.Input != 1000 {
		t.Errorf("Tokens.Input = %d", out.Result.Tokens.Input)
	}
	if out.Version != SchemaVersion {
		t.Errorf("Version = %d, want %d", out.Version, SchemaVersion)
	}
	if out.CreatedAt.IsZero() {
		t.Error("CreatedAt should be auto-populated")
	}
	if out.TTL == 0 {
		t.Error("TTL should be auto-populated from Cache default")
	}
}

func TestGetMiss(t *testing.T) {
	c := newCache(t)
	_, ok := c.Get("nonexistent-key")
	if ok {
		t.Error("Get on missing key should return false")
	}
}

func TestGetCorruptEntryReturnsMissAndDeletes(t *testing.T) {
	c := newCache(t)
	key := "deadbeef"
	if err := os.MkdirAll(c.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Dir(), key+".json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(key); ok {
		t.Error("corrupt entry should produce miss")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("corrupt entry should have been deleted; stat err = %v", err)
	}
}

func TestGetWrongSchemaVersionReturnsMiss(t *testing.T) {
	c := newCache(t)
	key := "abc"
	if err := os.MkdirAll(c.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Dir(), key+".json")
	if err := os.WriteFile(path, []byte(`{"version":999,"created_at":"2026-05-26T00:00:00Z","ttl":3600,"result":{"content":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(key); ok {
		t.Error("entry from future schema should miss")
	}
}

func TestExpiredEntryReturnsMiss(t *testing.T) {
	frozen := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	c, _ := Open(Options{
		Dir: filepath.Join(t.TempDir(), "cache"),
		TTL: time.Hour,
		Now: func() time.Time { return frozen },
	})

	e := sampleEntry()
	if err := c.Put("k1", e); err != nil {
		t.Fatal(err)
	}

	// Advance virtual clock past TTL
	c.now = func() time.Time { return frozen.Add(2 * time.Hour) }
	if _, ok := c.Get("k1"); ok {
		t.Error("entry beyond TTL should miss")
	}
}

func TestExpiredAtZeroTTL(t *testing.T) {
	e := Entry{CreatedAt: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), TTL: 0}
	if e.ExpiredAt(time.Now()) {
		t.Error("TTL=0 should mean never expires")
	}
}

func TestOpenRequiresDir(t *testing.T) {
	_, err := Open(Options{})
	if err == nil {
		t.Error("Open with empty Dir should error")
	}
}

func TestOpenDefaultsTTL(t *testing.T) {
	c, err := Open(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if c.ttl != DefaultTTL {
		t.Errorf("ttl = %v, want %v", c.ttl, DefaultTTL)
	}
}

func TestPutEmptyKeyErrors(t *testing.T) {
	c := newCache(t)
	if err := c.Put("", sampleEntry()); err == nil {
		t.Error("Put with empty key should error")
	}
}

func TestDelete(t *testing.T) {
	c := newCache(t)
	_ = c.Put("k", sampleEntry())
	if _, ok := c.Get("k"); !ok {
		t.Fatal("setup: entry not visible")
	}
	if err := c.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("k"); ok {
		t.Error("entry visible after Delete")
	}
	// Delete on missing key is a no-op
	if err := c.Delete("k"); err != nil {
		t.Errorf("Delete on missing key should not error: %v", err)
	}
}

func TestWriteAtomicCleanedUpOnRenameFailure(t *testing.T) {
	// Best-effort: simulate rename failure by putting a directory at the
	// target path so Rename to it fails. The temp file should be removed.
	dir := t.TempDir()
	target := filepath.Join(dir, "stuck.json")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	err := writeAtomic(target, []byte("x"))
	if err == nil {
		t.Error("expected error when renaming to a directory")
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file should have been cleaned up; stat = %v", err)
	}
}

func TestEnsureGitignoreCreatesFile(t *testing.T) {
	dir := t.TempDir()
	modified, err := EnsureGitignore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !modified {
		t.Error("modified should be true when creating .gitignore")
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), gitignoreEntry) {
		t.Errorf(".gitignore missing %q; content:\n%s", gitignoreEntry, data)
	}
}

func TestEnsureGitignoreAppendsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("# existing\nnode_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modified, err := EnsureGitignore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !modified {
		t.Error("modified should be true when appending")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "node_modules/") {
		t.Error("existing content lost")
	}
	if !strings.Contains(string(data), gitignoreEntry) {
		t.Error("new entry missing")
	}
}

func TestEnsureGitignoreIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("# existing\n.commitbrief/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modified, err := EnsureGitignore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if modified {
		t.Error("modified should be false when entry already present")
	}
}

func TestEnsureGitignoreEmptyRepoRootNoOp(t *testing.T) {
	modified, err := EnsureGitignore("")
	if err != nil {
		t.Fatal(err)
	}
	if modified {
		t.Error("empty repoRoot should be no-op")
	}
}

func TestCachePutTriggersGitignore(t *testing.T) {
	repo := t.TempDir()
	c, err := Open(Options{
		Dir:      filepath.Join(repo, ".commitbrief", "cache"),
		RepoRoot: repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Put("k", sampleEntry()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatalf(".gitignore was not created: %v", err)
	}
	if !strings.Contains(string(data), gitignoreEntry) {
		t.Errorf(".gitignore missing entry; content:\n%s", data)
	}
}

func TestEntryRoundTripJSON(t *testing.T) {
	c := newCache(t)
	in := sampleEntry()
	in.TTL = 3600
	in.CreatedAt = time.Date(2026, 5, 26, 1, 2, 3, 0, time.UTC)
	_ = c.Put("key", in)

	raw, err := os.ReadFile(filepath.Join(c.Dir(), "key.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Sanity check: required JSON fields per ADR-0008 are present
	for _, want := range []string{
		`"version"`, `"created_at"`, `"ttl"`,
		`"key"`, `"diff_hash"`, `"system_prompt_hash"`, `"model"`, `"lang"`,
		`"result"`, `"content"`, `"tokens"`, `"input"`, `"output"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("on-disk JSON missing %q; content:\n%s", want, raw)
		}
	}
}
