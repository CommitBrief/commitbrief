// SPDX-License-Identifier: GPL-3.0-or-later

package guard

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

type Result int

const (
	Continue Result = iota
	Abort
)

func (r Result) String() string {
	switch r {
	case Continue:
		return "continue"
	case Abort:
		return "abort"
	default:
		return "unknown"
	}
}

type Options struct {
	AssumeYes      bool
	NonInteractive bool
	Writer         io.Writer
	Reader         io.Reader

	// Catalog plumbs i18n into the .commitbrief/* write-guard so the
	// user-visible warning, file lines, prompt, and abort messages
	// honour the active locale. Nil → English defaults (legacy
	// behaviour). Every CLI caller should pass app.Catalog so
	// Turkish users actually see Turkish here (UC-15).
	Catalog *i18n.Catalog
}

// PathPrefix is the trigger condition: any diff file whose path starts with
// this string (i.e., lives under the .commitbrief/ directory) prompts the
// user. Root-level COMMITBRIEF.md and .commitbriefignore are intentionally
// excluded — they are team-shared by design (ADR-0007).
const PathPrefix = ".commitbrief/"

func CheckDiffForLocalConfig(d diff.Diff, opts Options) (Result, error) {
	triggers := Triggers(d)
	if len(triggers) == 0 {
		return Continue, nil
	}
	if opts.AssumeYes {
		return Continue, nil
	}

	w := opts.Writer
	if w == nil {
		w = os.Stderr
	}
	writeWarning(w, triggers, opts.Catalog)

	if opts.NonInteractive {
		_, _ = fmt.Fprintln(w, tr(opts.Catalog, "guard.non_interactive",
			"Aborting (non-interactive mode); pass --yes to override."))
		return Abort, nil
	}

	prompt := tr(opts.Catalog, "guard.prompt", "   Continue?")
	suffix := ui.PromptSuffix(opts.Catalog)
	_, _ = fmt.Fprintf(w, "%s %s: ", prompt, suffix)

	r := opts.Reader
	if r == nil {
		r = os.Stdin
	}
	answer, err := readAnswer(r)
	if err != nil {
		return Abort, fmt.Errorf("guard: read input: %w", err)
	}
	if ui.AcceptsYes(answer, opts.Catalog) {
		return Continue, nil
	}
	return Abort, nil
}

// tr is a tiny wrapper that lets us pass a nil catalog without
// guarding every call site. nil catalog → fallback string, exposing
// the same English UX the CLI used to produce hardcoded.
func tr(c *i18n.Catalog, key, fallback string) string {
	if c == nil {
		return fallback
	}
	v := c.T(key)
	if v == "" || v == key {
		return fallback
	}
	return v
}

func Triggers(d diff.Diff) []string {
	var paths []string
	for _, f := range d.Files {
		path := triggeredPath(f)
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func triggeredPath(f diff.FileDiff) string {
	for _, p := range []string{f.Path, f.OldPath} {
		if isUnderLocalDir(p) {
			if f.Path != "" {
				return f.Path
			}
			return f.OldPath
		}
	}
	return ""
}

func isUnderLocalDir(path string) bool {
	if path == "" {
		return false
	}
	return strings.HasPrefix(path, PathPrefix)
}

func writeWarning(w io.Writer, triggers []string, catalog *i18n.Catalog) {
	_, _ = fmt.Fprintln(w, tr(catalog, "guard.warning_header",
		"⚠  This review includes changes under .commitbrief/:"))
	for _, p := range triggers {
		_, _ = fmt.Fprintln(w, fmt.Sprintf(
			tr(catalog, "guard.warning_file", "   M %s"), p))
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, tr(catalog, "guard.warning_detail",
		"   These files are usually user-specific. Committing them may break\n"+
			"   other developers' configurations or leak API keys."))
	_, _ = fmt.Fprintln(w)
}

func readAnswer(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", nil
	}
	return strings.TrimSpace(strings.ToLower(scanner.Text())), nil
}
