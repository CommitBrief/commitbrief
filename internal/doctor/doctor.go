// SPDX-License-Identifier: GPL-3.0-or-later

// Package doctor provides a pipeline health check surface for the
// `commitbrief doctor` subcommand. Each "check" inspects one concern
// (git binary on PATH, config validity, provider reachability, cache
// writability, …) and returns a [Result] the CLI formats into a single
// status table. The aim is to reduce support burden — when something
// goes wrong, a user can self-diagnose before opening an issue.
//
// Checks live as methods on [Runner] rather than discrete types so the
// CLI command can compose them without a registration dance. Per-check
// dependencies (config snapshot, repo root, user home) come from the
// Runner's fields; nothing in this package mutates global state.
package doctor

import (
	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/i18n"
)

// Status is a coarse health label for a single check. Three levels keep
// the table readable: green for OK, yellow for advisory issues that
// don't block use, red for actual failures.
type Status int

const (
	// StatusOK — the checked condition holds. Detail is informational
	// (e.g. the resolved path of a found binary).
	StatusOK Status = iota
	// StatusWarn — the condition isn't ideal but the CLI can still
	// operate (e.g. one of several configured providers fails its ping).
	StatusWarn
	// StatusFail — the condition blocks normal operation (e.g. git
	// binary missing, no provider configured at all). Exit code 1.
	StatusFail
)

// String returns a stable label for debugging/test assertions. The CLI
// uses iconFor() for the user-facing glyph, not these strings.
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusWarn:
		return "warn"
	case StatusFail:
		return "fail"
	default:
		return "unknown"
	}
}

// Result is the output of a single check. Name is a short user-facing
// label (left column in the doctor table); Detail is the free-form
// right-hand explanation (a resolved path, an error message, etc.).
type Result struct {
	Name   string
	Status Status
	Detail string
}

// Runner holds the dependencies shared across all checks. Tests build
// minimal Runners directly; the CLI builds one from the resolved app
// context. Catalog may be nil — in that case check names fall back to
// their English defaults.
type Runner struct {
	// RepoRoot is the resolved git repository root, or "" when running
	// outside a repo. Checks that depend on a repo (cache writability,
	// .gitignore content) short-circuit on empty.
	RepoRoot string

	// Home is the resolved user home directory, used by OUTPUT.md
	// fallback lookup. Empty home skips the user-level layer.
	Home string

	// Config is the merged effective configuration. Never nil at the
	// CLI layer; tests can pass an explicit [config.Default()] minimum.
	Config *config.Config

	// Catalog provides i18n translations for check names. Nil falls
	// back to a no-op catalog (returns the key verbatim).
	Catalog *i18n.Catalog
}

// t looks up an i18n key with optional format args, defaulting to the
// key itself when no catalog is attached.
func (r *Runner) t(key string, args ...any) string {
	if r.Catalog == nil {
		return key
	}
	return r.Catalog.T(key, args...)
}

// Summary counts results by status. Used by the CLI to print the
// trailing "N checks: X ok, Y warning, Z failed" line and to compute
// the process exit code.
type Summary struct {
	Total    int
	OK       int
	Warnings int
	Failed   int
}

// Summarize tallies a result slice into a [Summary]. Linear scan; the
// result set is small (≤ 10 checks in practice).
func Summarize(results []Result) Summary {
	s := Summary{Total: len(results)}
	for _, r := range results {
		switch r.Status {
		case StatusOK:
			s.OK++
		case StatusWarn:
			s.Warnings++
		case StatusFail:
			s.Failed++
		}
	}
	return s
}
