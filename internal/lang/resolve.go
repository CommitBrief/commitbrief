// SPDX-License-Identifier: GPL-3.0-or-later

package lang

import "github.com/CommitBrief/commitbrief/internal/config"

type Source int

const (
	SourceDefault Source = iota
	SourceGlobalConfig
	SourceRepoConfig
	// SourceCLIFlag is the highest-priority step in the resolution chain:
	// an explicit `--lang` on the command line wins over both config files.
	SourceCLIFlag
)

func (s Source) String() string {
	switch s {
	case SourceCLIFlag:
		return "cli flag"
	case SourceRepoConfig:
		return "repo config"
	case SourceGlobalConfig:
		return "global config"
	case SourceDefault:
		return "default"
	default:
		return "unknown"
	}
}

// Resolution is the outcome of the language chain. Code is the resolved
// language and drives the AI **output** language verbatim (so an output-only
// language like "fr" is preserved). Name is its display name for the prompt
// directive. Source records which step of the chain decided it, for dry-run
// and verbose-footer attribution.
//
// The CLI **interface** language is derived separately via UICatalog(): the
// output language and the interface language share one resolution but the
// interface only localizes to languages we ship a catalog for.
type Resolution struct {
	Code   string
	Name   string
	Source Source
}

// UICatalog returns the language code to load CLI interface strings for: the
// resolved code when we ship a catalog for it (en, tr), otherwise English.
// This is what makes `--lang fr` produce French *output* while the CLI's own
// chrome stays English — the two are resolved from the same chain but the
// interface degrades to English for any language we haven't translated.
func (r Resolution) UICatalog() string {
	if hasUICatalog(r.Code) {
		return r.Code
	}
	return "en"
}

// Resolve walks the language source chain and returns the resolved output
// language:
//
//	--lang flag → repo config (output.lang) → user config (output.lang) → English
//
// At every step a value that is empty OR not a recognized language falls
// through to the next source — it never short-circuits to English mid-chain.
// The system locale (LANG env var) is deliberately NOT consulted: language is
// config-driven only (ADR-0021, superseding the D-21 / UC-09 env-LANG step).
//
// flag is the raw `--lang` value ("" when the flag was not passed). repo and
// global are the raw per-file configs (config.LoadFile, not the merged
// config) so each level is judged on its own stated value.
func Resolve(flag string, repo, global *config.Config) Resolution {
	if r, ok := fromValue(flag, SourceCLIFlag); ok {
		return r
	}
	if repo != nil {
		if r, ok := fromValue(repo.Output.Lang, SourceRepoConfig); ok {
			return r
		}
	}
	if global != nil {
		if r, ok := fromValue(global.Output.Lang, SourceGlobalConfig); ok {
			return r
		}
	}
	return English()
}

// English is the terminal fallback of the chain, and a convenience
// constructor for callers that always want English output (e.g. the eval
// harness, which pins fixtures to a deterministic language).
func English() Resolution {
	return Resolution{Code: "en", Name: displayName("en"), Source: SourceDefault}
}

// fromValue normalizes raw and, when it names a recognized language, returns
// its Resolution tagged with src; otherwise ok=false so the caller advances to
// the next source in the chain.
func fromValue(raw string, src Source) (Resolution, bool) {
	code := normalize(raw)
	if !recognized(code) {
		return Resolution{}, false
	}
	return Resolution{Code: code, Name: displayName(code), Source: src}, true
}
