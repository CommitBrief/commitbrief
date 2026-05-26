package guard

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/diff"
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
	writeWarning(w, triggers)

	if opts.NonInteractive {
		fmt.Fprintln(w, "Aborting (non-interactive mode); pass --yes to override.")
		return Abort, nil
	}

	fmt.Fprint(w, "   Continue? [y/N]: ")

	r := opts.Reader
	if r == nil {
		r = os.Stdin
	}
	answer, err := readAnswer(r)
	if err != nil {
		return Abort, fmt.Errorf("guard: read input: %w", err)
	}
	if isYes(answer) {
		return Continue, nil
	}
	return Abort, nil
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

func writeWarning(w io.Writer, triggers []string) {
	fmt.Fprintln(w, "⚠  This review includes changes under .commitbrief/:")
	for _, p := range triggers {
		fmt.Fprintf(w, "   M %s\n", p)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "   These files are usually user-specific. Committing them may break")
	fmt.Fprintln(w, "   other developers' configurations or leak API keys.")
	fmt.Fprintln(w)
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

func isYes(answer string) bool {
	switch answer {
	case "y", "yes":
		return true
	default:
		return false
	}
}
