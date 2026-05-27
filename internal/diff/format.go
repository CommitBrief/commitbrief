// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"strings"
)

func (d Diff) String() string {
	var sb strings.Builder
	for _, f := range d.Files {
		sb.WriteString(f.String())
	}
	return sb.String()
}

func (f FileDiff) String() string {
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
		sb.WriteString(h.String())
	}
	return sb.String()
}

func (h Hunk) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	if h.Header != "" {
		sb.WriteString(" ")
		sb.WriteString(h.Header)
	}
	sb.WriteString("\n")
	for _, l := range h.Lines {
		switch l.Kind {
		case LineAdd:
			sb.WriteString("+")
		case LineDel:
			sb.WriteString("-")
		case LineContext:
			sb.WriteString(" ")
		}
		sb.WriteString(l.Text)
		sb.WriteString("\n")
	}
	return sb.String()
}
