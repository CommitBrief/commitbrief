// SPDX-License-Identifier: GPL-3.0-or-later

package eval

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

	"github.com/CommitBrief/commitbrief/internal/render"
)

// defaultLineTolerance is the ± window applied when an expected finding
// does not set its own LineTol. A model that flags the right defect a few
// lines off (diff drift, multi-line statements) should still count as a
// hit; ADR-0018 §2 fixes the default at 3.
const defaultLineTolerance = 3

// ExpectedFinding is one entry in a fixture answer key — a defect the
// review SHOULD surface (ADR-0018 §1). Category is reporting metadata, not
// a match criterion: the locked findings schema carries no category field.
type ExpectedFinding struct {
	ID          string          `json:"id"`
	File        string          `json:"file"`
	Line        int             `json:"line"`
	LineTol     int             `json:"line_tol,omitempty"`
	Category    string          `json:"category"`
	MinSeverity render.Severity `json:"min_severity,omitempty"`
	Summary     string          `json:"summary"`
}

// tolerance returns the effective line window for this expected finding.
func (e ExpectedFinding) tolerance() int {
	if e.LineTol > 0 {
		return e.LineTol
	}
	return defaultLineTolerance
}

// SilenceAnchor marks a line a good review should NOT flag. A produced
// finding landing on (File, ~Line) is a measured false positive
// (ADR-0018 §2).
type SilenceAnchor struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// answerKey is the on-disk shape of expected.json.
type answerKey struct {
	Language         string            `json:"language"`
	ExpectedFindings []ExpectedFinding `json:"expected_findings"`
	MustStaySilentOn []SilenceAnchor   `json:"must_stay_silent_on"`

	// HeldOut marks a fixture as part of the held-out slice (ADR-0018
	// §Goodhart). Held-out fixtures must never be inspected while tuning
	// the prompt or the corpus; they exist only to give an unbiased
	// generalization estimate. If dev-slice recall improves but held-out
	// recall does not, the change overfit the corpus rather than improving
	// reviews. Default false → the fixture is in the tunable dev slice.
	HeldOut bool `json:"held_out,omitempty"`
}

// Fixture is one known-answer corpus entry: a diff plus its answer key.
// MockResponse is the scripted findings JSON used by the deterministic
// tier; it is empty when the fixture ships no mock_response.json.
type Fixture struct {
	Name             string
	Dir              string
	Language         string
	Diff             string
	Expected         []ExpectedFinding
	MustStaySilentOn []SilenceAnchor
	MockResponse     string

	// HeldOut marks the fixture as part of the generalization-only slice;
	// see answerKey.HeldOut.
	HeldOut bool
}

// Categories returns the distinct categories a fixture exercises: the
// category of each expected finding, or "clean" for a clean control (no
// expected findings). Used to check that the held-out slice is
// representative rather than concentrated in one category.
func (fx Fixture) Categories() []string {
	if len(fx.Expected) == 0 {
		return []string{"clean"}
	}
	seen := map[string]struct{}{}
	var out []string
	for _, e := range fx.Expected {
		if _, ok := seen[e.Category]; ok {
			continue
		}
		seen[e.Category] = struct{}{}
		out = append(out, e.Category)
	}
	return out
}

// LoadFixture reads a single corpus directory: input.diff + expected.json
// (required) and mock_response.json (optional, for the deterministic tier).
func LoadFixture(dir string) (Fixture, error) {
	name := filepath.Base(dir)

	diffBytes, err := os.ReadFile(filepath.Join(dir, "input.diff"))
	if err != nil {
		return Fixture{}, fmt.Errorf("eval: fixture %q: read input.diff: %w", name, err)
	}

	keyBytes, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		return Fixture{}, fmt.Errorf("eval: fixture %q: read expected.json: %w", name, err)
	}
	var key answerKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return Fixture{}, fmt.Errorf("eval: fixture %q: parse expected.json: %w", name, err)
	}
	for i, e := range key.ExpectedFindings {
		if e.File == "" {
			return Fixture{}, fmt.Errorf("eval: fixture %q: expected finding %d: missing file", name, i)
		}
		if e.MinSeverity != "" && !e.MinSeverity.IsValid() {
			return Fixture{}, fmt.Errorf("eval: fixture %q: expected finding %d: invalid min_severity %q", name, i, e.MinSeverity)
		}
	}

	mockResp, err := os.ReadFile(filepath.Join(dir, "mock_response.json"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Fixture{}, fmt.Errorf("eval: fixture %q: read mock_response.json: %w", name, err)
	}

	return Fixture{
		Name:             name,
		Dir:              dir,
		Language:         key.Language,
		Diff:             string(diffBytes),
		Expected:         key.ExpectedFindings,
		MustStaySilentOn: key.MustStaySilentOn,
		MockResponse:     string(mockResp),
		HeldOut:          key.HeldOut,
	}, nil
}

// LoadCorpus loads every fixture under root — each child directory that
// contains an input.diff. Fixtures are returned sorted by name so every
// run iterates deterministically.
func LoadCorpus(root string) ([]Fixture, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("eval: read corpus %q: %w", root, err)
	}
	var fixtures []Fixture
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, statErr := os.Stat(filepath.Join(dir, "input.diff")); statErr != nil {
			continue // not a fixture directory
		}
		fx, loadErr := LoadFixture(dir)
		if loadErr != nil {
			return nil, loadErr
		}
		fixtures = append(fixtures, fx)
	}
	if len(fixtures) == 0 {
		return nil, fmt.Errorf("eval: no fixtures found under %q", root)
	}
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].Name < fixtures[j].Name })
	return fixtures, nil
}

// LanguageDistribution counts fixtures per answer-key language. Every
// reported ratio (recall/precision/fpr) must carry its language mix
// alongside n, so a results row and a `make eval` summary both build this
// from the same fixture slice they scored. A fixture whose expected.json
// omits "language" is counted under "unknown" rather than silently
// dropped — a gap in the corpus metadata should be visible, not hidden.
func LanguageDistribution(fixtures []Fixture) map[string]int {
	out := map[string]int{}
	for _, fx := range fixtures {
		lang := fx.Language
		if lang == "" {
			lang = "unknown"
		}
		out[lang]++
	}
	return out
}

// CorpusFingerprint returns a stable SHA-256 fingerprint of the corpus
// rooted at root (ADR-0043 §4): for every fixture directory in
// directory-name order (the same order LoadCorpus returns), the fixture's
// name plus the raw bytes of its input.diff and expected.json.
// README.md and mock_response.json are deliberately excluded — one
// documents a fixture, the other only drives the mock tier, and neither is
// part of what a live measurement is graded against, so editing either
// must not change the fingerprint a results row is keyed to. Reads files
// directly rather than going through LoadFixture so the fingerprint covers
// the exact on-disk expected.json bytes, not the struct JSON round-tripped
// back through it.
func CorpusFingerprint(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("eval: fingerprint corpus %q: %w", root, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(root, entry.Name(), "input.diff")); statErr != nil {
			continue // not a fixture directory
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		dir := filepath.Join(root, name)
		diffBytes, err := os.ReadFile(filepath.Join(dir, "input.diff"))
		if err != nil {
			return "", fmt.Errorf("eval: fingerprint fixture %q: read input.diff: %w", name, err)
		}
		keyBytes, err := os.ReadFile(filepath.Join(dir, "expected.json"))
		if err != nil {
			return "", fmt.Errorf("eval: fingerprint fixture %q: read expected.json: %w", name, err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(diffBytes)
		h.Write([]byte{0})
		h.Write(keyBytes)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
