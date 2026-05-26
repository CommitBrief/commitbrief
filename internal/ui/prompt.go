package ui

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

type AskOptions struct {
	AssumeYes      bool
	NonInteractive bool
}

// AskYesNo asks question on w and reads a yes/no response from r.
// AssumeYes short-circuits to true; NonInteractive (without AssumeYes)
// short-circuits to false. Default (empty answer) is no.
// Recognises: y, yes (case-insensitive, trimmed).
func AskYesNo(r io.Reader, w io.Writer, question string, opts AskOptions) (bool, error) {
	if opts.AssumeYes {
		return true, nil
	}
	if opts.NonInteractive {
		return false, nil
	}
	if _, err := fmt.Fprintf(w, "%s [y/N]: ", question); err != nil {
		return false, fmt.Errorf("ui: write prompt: %w", err)
	}
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("ui: read answer: %w", err)
		}
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// IsStdinTTY reports whether the given reader is a TTY. Used by the CLI to
// decide whether to set AskOptions.NonInteractive automatically.
func IsStdinTTY(r io.Reader) bool {
	return isTerminalReader(r)
}
