// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/CommitBrief/commitbrief/internal/i18n"
)

type AskOptions struct {
	AssumeYes      bool
	NonInteractive bool

	// Interactive, when true, makes Confirm render an arrow-key
	// Yes/No toggle (huh) read from the controlling terminal instead
	// of the line-based fallback. Callers derive it from
	// IsStdinTTY(os.Stdin) — it is NOT inferred from the reader,
	// because the review-scoped shared reader is a *bufio.Reader, not
	// the *os.File the TTY check needs. AssumeYes and NonInteractive
	// still take precedence. Has no effect on AskYesNo (line-only).
	Interactive bool

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
	answer, err := readLine(r)
	if err != nil {
		return false, fmt.Errorf("ui: read answer: %w", err)
	}
	if AcceptsYes(answer, opts.Catalog) {
		return true, nil
	}
	return false, nil
}

// readLine reads exactly one line from r without lookahead, returning it
// trimmed and lower-cased for AcceptsYes. When r is already a
// *bufio.Reader (the runReview-scoped shared reader threaded through
// every interactive prompt) it is used directly so each prompt consumes
// its own line; a fresh bufio.Scanner would over-read and swallow the
// answers meant for later prompts (UC-21 — guard → secret scan → token →
// cost all fire in sequence on one review). Non-buffered readers are
// wrapped once. Mirrors guard.readAnswer; both layers share this surgical
// read so the shared-reader contract holds wherever a line is consumed.
func readLine(r io.Reader) (string, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	line, err := br.ReadString('\n')
	if err != nil && line == "" {
		if err == io.EOF {
			return "", nil
		}
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(line)), nil
}

// Confirm asks a yes/no question, defaulting to No. On an interactive
// terminal (opts.Interactive) it renders an arrow-key-selectable Yes/No
// toggle via huh, read from the controlling terminal directly. Otherwise
// it falls back to the line-based AskYesNo over r/w — the path tests and
// non-TTY pipelines exercise. AssumeYes/NonInteractive short-circuit
// before either path, so the interactive toggle never appears in CI.
func Confirm(r io.Reader, w io.Writer, question string, opts AskOptions) (bool, error) {
	if opts.AssumeYes {
		return true, nil
	}
	if opts.NonInteractive {
		return false, nil
	}
	if opts.Interactive {
		return confirmInteractive(question, opts.Catalog)
	}
	return AskYesNo(r, w, question, opts)
}

// confirmInteractive renders the huh Yes/No toggle on the controlling
// terminal (input from os.Stdin, output to os.Stderr so a captured
// stdout — e.g. --json — stays clean). Button labels come from the
// catalog (common.affirmative / common.negative) so non-English locales
// get native Yes/No. The bound value starts false, so "No" is the
// pre-selected default — matching the line-based default-to-no.
func confirmInteractive(question string, catalog *i18n.Catalog) (bool, error) {
	affirmative, negative := "Yes", "No"
	if catalog != nil {
		if s := strings.TrimSpace(catalog.T("common.affirmative")); s != "" && s != "common.affirmative" {
			affirmative = s
		}
		if s := strings.TrimSpace(catalog.T("common.negative")); s != "" && s != "common.negative" {
			negative = s
		}
	}

	confirm := false
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(strings.TrimSpace(question)).
			Affirmative(affirmative).
			Negative(negative).
			Value(&confirm),
	)).WithInput(os.Stdin).WithOutput(os.Stderr)
	if err := form.Run(); err != nil {
		return false, fmt.Errorf("ui: confirm prompt: %w", err)
	}
	return confirm, nil
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
