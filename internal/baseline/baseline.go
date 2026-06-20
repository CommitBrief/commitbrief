// SPDX-License-Identifier: GPL-3.0-or-later

// Package baseline implements signal control SC1 (ADR-0027): a
// user-private, per-developer record of already-accepted findings so that
// repeat runs surface only NEW issues instead of re-dumping the whole
// brownfield backlog every time.
//
// The record lives at <repoRoot>/.commitbrief/baseline.json. That directory
// is already gitignored by `setup --local`, so the file never enters a diff,
// never reaches CI, and never weakens the senior/CI gate — those still see
// every finding. The baseline only quiets the local developer's own runs.
//
// A finding is identified by a line-drift-resilient fingerprint:
//
//	sha256(File + "\0" + Severity + "\0" + normalize(Title))
//
// Line is deliberately excluded (code shifts as the file grows, and a
// brownfield baseline must survive that); Description and Snippet are
// excluded because they are LLM-volatile prose that would churn the
// fingerprint on every re-run. normalize trims, collapses internal
// whitespace, and lowercases the Title. The documented limit is that the
// same Title twice in one file at the same severity collides into a single
// fingerprint; the escape hatch is `--update-baseline`.
package baseline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/render"
)

// FileVersion is the schema version stamped into baseline.json. It is the
// file's own format version and is unrelated to the locked --json schema
// (render.SchemaVersion); bumping it lets a future format change reject or
// migrate an older file rather than silently mis-reading it.
const FileVersion = 1

// relPath is the repo-relative location of the baseline file. Kept relative
// so callers can join it onto any repo root (and so tests can assert it).
var relPath = filepath.Join(".commitbrief", "baseline.json")

// Path returns the absolute baseline.json path for the given repo root.
func Path(repoRoot string) string {
	return filepath.Join(repoRoot, relPath)
}

// Set is the in-memory form of a loaded baseline: the set of accepted
// fingerprints. The zero value (nil map) is a valid empty set — every
// Contains call returns false — so a missing file degrades to "baseline
// off" without special-casing at the call site.
type Set map[string]struct{}

// Contains reports whether fp is an accepted fingerprint. Safe on a nil Set.
func (s Set) Contains(fp string) bool {
	_, ok := s[fp]
	return ok
}

// Len returns the number of accepted fingerprints. Safe on a nil Set.
func (s Set) Len() int { return len(s) }

// file is the on-disk JSON shape of baseline.json. Fingerprints are stored
// sorted (Write guarantees it) so the file is deterministic and diff-stable
// even though the file never enters git — a stable file is still friendlier
// to a developer who opens it.
type file struct {
	Version      int      `json:"version"`
	Fingerprints []string `json:"fingerprints"`
}

// normalizeTitle trims surrounding whitespace, collapses every internal run
// of whitespace to a single space, and lowercases the result. This is the
// `normalize(Title)` term of the fingerprint formula; keeping it here (and
// not inlined) makes the formula auditable and testable in one place.
func normalizeTitle(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " "))
}

// Fingerprint derives the stable identity of a finding per ADR-0027:
// sha256(File + "\0" + Severity + "\0" + normalize(Title)), hex-encoded.
// The NUL separators keep the three fields unambiguous (no field can
// contain a NUL), so "ab" + "c" and "a" + "bc" never collide.
func Fingerprint(f render.Finding) string {
	h := sha256.New()
	h.Write([]byte(f.File))
	h.Write([]byte{0})
	h.Write([]byte(f.Severity))
	h.Write([]byte{0})
	h.Write([]byte(normalizeTitle(f.Title)))
	return hex.EncodeToString(h.Sum(nil))
}

// Load reads the baseline for repoRoot. A missing file is NOT an error — it
// yields an empty Set and a nil error, so a first-ever run (or a developer
// who never opted in) simply has nothing baselined. A present-but-malformed
// file IS an error so a corrupted baseline surfaces loudly instead of
// silently unhiding findings the developer thought were accepted.
func Load(repoRoot string) (Set, error) {
	if repoRoot == "" {
		return Set{}, nil
	}
	data, err := os.ReadFile(Path(repoRoot))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Set{}, nil
		}
		return nil, fmt.Errorf("baseline: read %s: %w", relPath, err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("baseline: parse %s: %w", relPath, err)
	}
	if f.Version != FileVersion {
		return nil, fmt.Errorf("baseline: %s has unsupported version %d (expected %d); re-run with --update-baseline to rewrite it", relPath, f.Version, FileVersion)
	}
	set := make(Set, len(f.Fingerprints))
	for _, fp := range f.Fingerprints {
		set[fp] = struct{}{}
	}
	return set, nil
}

// Write replaces baseline.json with the fingerprints of the given findings.
// It is the `--update-baseline` action: the whole current set becomes the
// new accepted baseline (an absorb, not a merge), so removing a finding from
// the code and re-baselining drops its fingerprint too. The parent
// .commitbrief/ directory is created if absent; the write is atomic
// (temp + rename) so an interrupted run never leaves a half-written file.
// Returns the number of distinct fingerprints written.
func Write(repoRoot string, findings []render.Finding) (int, error) {
	if repoRoot == "" {
		return 0, errors.New("baseline: empty repo root")
	}
	seen := make(map[string]struct{}, len(findings))
	fps := make([]string, 0, len(findings))
	for _, f := range findings {
		fp := Fingerprint(f)
		if _, dup := seen[fp]; dup {
			continue
		}
		seen[fp] = struct{}{}
		fps = append(fps, fp)
	}
	sort.Strings(fps)

	data, err := json.MarshalIndent(file{Version: FileVersion, Fingerprints: fps}, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("baseline: marshal: %w", err)
	}
	data = append(data, '\n')

	path := Path(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, fmt.Errorf("baseline: mkdir %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return 0, fmt.Errorf("baseline: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("baseline: rename: %w", err)
	}
	return len(fps), nil
}

// Filter splits findings into the ones to KEEP (not in the baseline) and a
// count of how many were dropped because their fingerprint is baselined.
// This is a TRUE removal (ADR-0027): the kept slice is what fail-on, JSON
// findings[], and display all see — distinct from the display-only
// --min-severity filter. An empty/nil Set keeps everything (count 0), so the
// no-baseline-file path is a transparent pass-through. The kept slice is
// always non-nil (callers downstream distinguish nil = graceful degrade from
// empty = clean), preserving that invariant.
func Filter(findings []render.Finding, set Set) (kept []render.Finding, baselined int) {
	if len(set) == 0 {
		return findings, 0
	}
	kept = make([]render.Finding, 0, len(findings))
	for _, f := range findings {
		if set.Contains(Fingerprint(f)) {
			baselined++
			continue
		}
		kept = append(kept, f)
	}
	return kept, baselined
}
