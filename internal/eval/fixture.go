// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
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
