// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"strings"
)

func (d Diff) String() string {
	var sb strings.Builder
	for _, f := range d.Files {
		sb.WriteString(f.render(false))
	}
	return sb.String()
}

// NumberedString renders the diff like String, but prefixes every hunk
// line with the line number a GitHub inline comment would anchor to:
// the new-file number for added and context lines, the old-file number
// for removed lines. The format is `<n>| <marker><text>` (marker is
// `+`/`-`/space). LLMs count poorly across long hunks and tend to echo
// the `@@` header's start line; handing them the number per line turns
// the `line` field of a finding from an estimate into a copy. The cache
// key still keys off String() — NumberedString is a deterministic
// function of the same diff, so it carries no extra cache identity.
func (d Diff) NumberedString() string {
	var sb strings.Builder
	for _, f := range d.Files {
		sb.WriteString(f.render(true))
	}
	return sb.String()
}

func (f FileDiff) String() string { return f.render(false) }

func (f FileDiff) render(numbered bool) string {
	var sb strings.Builder
	oldPath := f.OldPath
	if oldPath == "" {
		oldPath = f.Path
	}
	newPath := f.Path
	if newPath == "" {
		newPath = f.OldPath
	}
	fmt.Fprintf(&sb, "diff --git a/%s b/%s\n", oldPath, newPath)

	switch f.Mode {
	case ModeAdded:
		sb.WriteString("new file mode 100644\n")
		sb.WriteString("--- /dev/null\n")
		fmt.Fprintf(&sb, "+++ b/%s\n", newPath)
	case ModeDeleted:
		sb.WriteString("deleted file mode 100644\n")
		fmt.Fprintf(&sb, "--- a/%s\n", oldPath)
		sb.WriteString("+++ /dev/null\n")
	case ModeRenamed:
		fmt.Fprintf(&sb, "rename from %s\n", oldPath)
		fmt.Fprintf(&sb, "rename to %s\n", newPath)
		fmt.Fprintf(&sb, "--- a/%s\n", oldPath)
		fmt.Fprintf(&sb, "+++ b/%s\n", newPath)
	case ModeCopied:
		fmt.Fprintf(&sb, "copy from %s\n", oldPath)
		fmt.Fprintf(&sb, "copy to %s\n", newPath)
		fmt.Fprintf(&sb, "--- a/%s\n", oldPath)
		fmt.Fprintf(&sb, "+++ b/%s\n", newPath)
	default:
		fmt.Fprintf(&sb, "--- a/%s\n", oldPath)
		fmt.Fprintf(&sb, "+++ b/%s\n", newPath)
	}

	if f.Binary {
		fmt.Fprintf(&sb, "Binary files a/%s and b/%s differ\n", oldPath, newPath)
		return sb.String()
	}

	for _, h := range f.Hunks {
		sb.WriteString(h.render(numbered))
	}
	return sb.String()
}

func (h Hunk) String() string { return h.render(false) }

func (h Hunk) render(numbered bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	if h.Header != "" {
		sb.WriteString(" ")
		sb.WriteString(h.Header)
	}
	sb.WriteString("\n")

	oldNo, newNo := h.OldStart, h.NewStart
	for _, l := range h.Lines {
		var marker byte
		var num int
		switch l.Kind {
		case LineAdd:
			marker, num = '+', newNo
			newNo++
		case LineDel:
			marker, num = '-', oldNo
			oldNo++
		case LineContext:
			marker, num = ' ', newNo
			oldNo++
			newNo++
		}
		if numbered {
			fmt.Fprintf(&sb, "%d| %c%s\n", num, marker, l.Text)
		} else {
			fmt.Fprintf(&sb, "%c%s\n", marker, l.Text)
		}
	}
	return sb.String()
}
