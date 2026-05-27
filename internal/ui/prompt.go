// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/i18n"
)

type AskOptions struct {
	AssumeYes      bool
	NonInteractive bool

	// Catalog, when non-nil, controls the prompt suffix ("[y/N]" vs
	// "[e/H]") and the accepted-affirmative vocabulary
	// (common.yes_short / common.yes_long). When nil, falls back to
	// English defaults so existing callers that don't have a catalog
	// handy keep working — but EVERY interactive caller in the CLI
	// should pass one so non-English locales actually get their
	// native input accepted (see UC-14 in PATCH_ROADMAP).
	//
	// The EN forms ("y"/"yes") are accepted unconditionally regardless
	// of catalog, so a user typing English in a Turkish session also
	// proceeds.
	Catalog *i18n.Catalog
}

// AskYesNo asks question on w and reads a yes/no response from r.
// AssumeYes short-circuits to true; NonInteractive (without AssumeYes)
// short-circuits to false. Default (empty answer) is no.
//
// Accepted-yes vocabulary: case-insensitive, trimmed match against
// "y"/"yes" (always) plus the active catalog's `common.yes_short` /
// `common.yes_long` (Turkish: "e"/"evet"). See AskOptions.Catalog.
func AskYesNo(r io.Reader, w io.Writer, question string, opts AskOptions) (bool, error) {
	if opts.AssumeYes {
		return true, nil
	}
	if opts.NonInteractive {
		return false, nil
	}
	suffix := PromptSuffix(opts.Catalog)
	if _, err := fmt.Fprintf(w, "%s %s: ", question, suffix); err != nil {
		return false, fmt.Errorf("ui: write prompt: %w", err)
	}
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("ui: read answer: %w", err)
		}
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if AcceptsYes(answer, opts.Catalog) {
		return true, nil
	}
	return false, nil
}

// AcceptsYes reports whether `answer` is an affirmative response. The
// EN defaults ("y"/"yes") are always accepted. When catalog is
// non-nil, the catalog's `common.yes_short` / `common.yes_long`
// strings are added to the accepted set (Turkish: "e"/"evet").
//
// `answer` is assumed already trimmed and lower-cased by the caller.
// Catalog values are normalised here so a catalog with mixed case
// still matches.
func AcceptsYes(answer string, catalog *i18n.Catalog) bool {
	switch answer {
	case "y", "yes":
		return true
	}
	if catalog == nil {
		return false
	}
	short := strings.ToLower(strings.TrimSpace(catalog.T("common.yes_short")))
	long := strings.ToLower(strings.TrimSpace(catalog.T("common.yes_long")))
	switch answer {
	case short, long:
		return short != "" || long != ""
	}
	return false
}

// PromptSuffix returns the y/N choice suffix for prompts ("[y/N]" or
// the catalog's localised form like "[e/H]"). Falls back to English
// when catalog is nil or the key is missing.
func PromptSuffix(catalog *i18n.Catalog) string {
	if catalog == nil {
		return "[y/N]"
	}
	s := strings.TrimSpace(catalog.T("common.prompt_yn"))
	if s == "" {
		return "[y/N]"
	}
	return s
}

// IsStdinTTY reports whether the given reader is a TTY. Used by the CLI to
// decide whether to set AskOptions.NonInteractive automatically.
func IsStdinTTY(r io.Reader) bool {
	return isTerminalReader(r)
}
