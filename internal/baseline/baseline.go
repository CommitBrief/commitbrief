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
// A finding is identified by the ADR-0045 fingerprint stamped on it
// (render.Finding.Fingerprint = sha256(path, rule_id, symbol)): line-drift
// resilient, and independent of the LLM-volatile Title, Description and
// Snippet prose. A version 2 file stores those fingerprints.
//
// Known coarseness (accepted in ADR-0045 §Negative): the identity ignores the
// Title, so baselining one finding also hides any later finding with the same
// file, rule and symbol, and a finding with no symbol (typically
// general.other) hides every same-rule finding in that file.
//
// A version 1 file (written before ADR-0045) stores the legacy formula
//
//	sha256(File + "\0" + Severity + "\0" + normalize(Title))
//
// and is still read with that formula so an existing baseline keeps working
// until the developer migrates it. The v1 hash is one-way (neither rule_id nor
// symbol can be recovered from it), so migration is lossy by construction: it
// is a fresh `--update-baseline` over the same range that created the file.
// The caller surfaces that as a one-time warning (Set.Legacy).
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
// migrate an older file rather than silently mis-reading it. Version 2 keys
// on render.Finding.Fingerprint (ADR-0045); version 1 keyed on
// legacyFingerprint and is still readable.
const FileVersion = 2

// legacyFileVersion is the pre-ADR-0045 file format, read-only.
const legacyFileVersion = 1

// relPath is the repo-relative location of the baseline file. Kept relative
// so callers can join it onto any repo root (and so tests can assert it).
var relPath = filepath.Join(".commitbrief", "baseline.json")

// Path returns the absolute baseline.json path for the given repo root.
func Path(repoRoot string) string {
	return filepath.Join(repoRoot, relPath)
}

// Set is the in-memory form of a loaded baseline: the accepted fingerprints
// plus the file format version that decides which formula a finding is
// matched with. The zero value is a valid empty set — Matches returns false
// and Len is 0 — so a missing file degrades to "baseline off" without
// special-casing at the call site.
type Set struct {
	version int
	fps     map[string]struct{}
}

// Contains reports whether fp is an accepted fingerprint, as stored. It does
// not apply any formula; use Matches for a finding. Safe on a zero Set.
func (s Set) Contains(fp string) bool {
	_, ok := s.fps[fp]
	return ok
}

// Len returns the number of accepted fingerprints. Safe on a zero Set.
func (s Set) Len() int { return len(s.fps) }

// Legacy reports whether the set was loaded from a non-empty version 1 file,
// i.e. it is matched with the pre-ADR-0045 formula and should be migrated.
func (s Set) Legacy() bool { return s.version == legacyFileVersion && len(s.fps) > 0 }

// Matches reports whether finding f is accepted by the baseline. A version 2
// set compares the finding's stamped Fingerprint; a version 1 set compares the
// legacy formula, which still reads Severity and Title off the finding. A
// finding with no stamped fingerprint never matches a version 2 set.
func (s Set) Matches(f render.Finding) bool {
	if len(s.fps) == 0 {
		return false
	}
	if s.version == legacyFileVersion {
		return s.Contains(legacyFingerprint(f))
	}
	return f.Fingerprint != "" && s.Contains(f.Fingerprint)
}

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

// legacyFingerprint derives the version 1 identity of a finding per
// ADR-0027: sha256(File + "\0" + Severity + "\0" + normalize(Title)),
// hex-encoded. The NUL separators keep the three fields unambiguous (no field
// can contain a NUL), so "ab" + "c" and "a" + "bc" never collide. It is kept
// only to read version 1 baseline files; nothing writes it any more.
func legacyFingerprint(f render.Finding) string {
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
//
// Versions 1 and 2 are read (see the package comment); any other version is a
// hard error so a file from a newer binary is never half-understood.
func Load(repoRoot string) (Set, error) {
	if repoRoot == "" {
		return Set{}, nil
	}
	data, err := os.ReadFile(Path(repoRoot))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Set{}, nil
		}
		return Set{}, fmt.Errorf("baseline: read %s: %w", relPath, err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return Set{}, fmt.Errorf("baseline: parse %s: %w", relPath, err)
	}
	if f.Version != FileVersion && f.Version != legacyFileVersion {
		return Set{}, fmt.Errorf("baseline: %s has unsupported version %d (expected %d); re-run with --update-baseline to rewrite it", relPath, f.Version, FileVersion)
	}
	fps := make(map[string]struct{}, len(f.Fingerprints))
	for _, fp := range f.Fingerprints {
		fps[fp] = struct{}{}
	}
	return Set{version: f.Version, fps: fps}, nil
}

// Write replaces baseline.json with the fingerprints of the given findings,
// always as a version 2 file (rewriting a version 1 file is the migration).
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
		fp := f.Fingerprint
		if fp == "" {
			continue // unstamped finding: no identity to accept
		}
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
	if set.Len() == 0 {
		return findings, 0
	}
	kept = make([]render.Finding, 0, len(findings))
	for _, f := range findings {
		if set.Matches(f) {
			baselined++
			continue
		}
		kept = append(kept, f)
	}
	return kept, baselined
}
