// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/git"
)

const (
	scannerInitialBuf = 64 * 1024
	scannerMaxBuf     = 16 * 1024 * 1024
)

func Parse(input git.Diff) (Diff, error) {
	out := Diff{Origin: input.Origin, Args: input.Args}
	if input.Content == "" {
		return out, nil
	}

	scanner := bufio.NewScanner(strings.NewReader(input.Content))
	scanner.Buffer(make([]byte, scannerInitialBuf), scannerMaxBuf)

	var (
		cur     *FileDiff
		curHunk *Hunk
	)

	flushHunk := func() {
		if cur != nil && curHunk != nil {
			cur.Hunks = append(cur.Hunks, *curHunk)
			curHunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			// Pre-split Path / OldPath once so the ignore matcher and
			// any later consumer don't re-split per call.
			if cur.Path != "" {
				cur.PathParts = strings.Split(cur.Path, "/")
			}
			if cur.OldPath != "" {
				cur.OldPathParts = strings.Split(cur.OldPath, "/")
			}
			out.Files = append(out.Files, *cur)
			cur = nil
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			cur = parseDiffHeader(line)

		case cur == nil:
			// Stray content before any diff header; ignore.

		case strings.HasPrefix(line, "Binary files "):
			flushHunk()
			cur.Binary = true

		case strings.HasPrefix(line, "rename from "):
			cur.OldPath = strings.TrimPrefix(line, "rename from ")
			cur.Mode = ModeRenamed

		case strings.HasPrefix(line, "rename to "):
			cur.Path = strings.TrimPrefix(line, "rename to ")
			cur.Mode = ModeRenamed

		case strings.HasPrefix(line, "copy from "):
			cur.OldPath = strings.TrimPrefix(line, "copy from ")
			cur.Mode = ModeCopied

		case strings.HasPrefix(line, "copy to "):
			cur.Path = strings.TrimPrefix(line, "copy to ")
			cur.Mode = ModeCopied

		case strings.HasPrefix(line, "new file mode "):
			cur.Mode = ModeAdded

		case strings.HasPrefix(line, "deleted file mode "):
			cur.Mode = ModeDeleted

		case strings.HasPrefix(line, "--- "):
			p := strings.TrimPrefix(line, "--- ")
			if p == "/dev/null" {
				cur.Mode = ModeAdded
			} else if cur.Mode != ModeRenamed && cur.Mode != ModeCopied {
				cur.OldPath = stripPathPrefix(p, "a/")
			}

		case strings.HasPrefix(line, "+++ "):
			p := strings.TrimPrefix(line, "+++ ")
			if p == "/dev/null" {
				cur.Mode = ModeDeleted
			} else if cur.Mode != ModeRenamed && cur.Mode != ModeCopied {
				cur.Path = stripPathPrefix(p, "b/")
			}

		case strings.HasPrefix(line, "@@"):
			flushHunk()
			h, err := parseHunkHeader(line)
			if err != nil {
				return out, fmt.Errorf("diff: parse hunk header %q: %w", line, err)
			}
			curHunk = h

		case curHunk == nil:
			// In-file metadata we don't model (index, similarity, etc.)

		default:
			if len(line) == 0 {
				curHunk.Lines = append(curHunk.Lines, HunkLine{Kind: LineContext})
				continue
			}
			switch line[0] {
			case '+':
				curHunk.Lines = append(curHunk.Lines, HunkLine{Kind: LineAdd, Text: line[1:]})
			case '-':
				curHunk.Lines = append(curHunk.Lines, HunkLine{Kind: LineDel, Text: line[1:]})
			case ' ':
				curHunk.Lines = append(curHunk.Lines, HunkLine{Kind: LineContext, Text: line[1:]})
				// '\' lines are "\ No newline at end of file" annotations — drop them.
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return out, fmt.Errorf("diff: scan: %w", err)
	}
	flushFile()
	out.addedLines, out.deletedLines = countLineKinds(out.Files)
	return out, nil
}

func parseDiffHeader(line string) *FileDiff {
	f := &FileDiff{Mode: ModeModified}
	rest := strings.TrimPrefix(line, "diff --git ")
	if i := strings.Index(rest, " b/"); i >= 0 {
		f.OldPath = stripPathPrefix(rest[:i], "a/")
		f.Path = stripPathPrefix(rest[i+1:], "b/")
	}
	return f
}

func parseHunkHeader(line string) (*Hunk, error) {
	// "@@ -10,5 +12,7 @@ optional context"
	rest := strings.TrimPrefix(line, "@@")
	end := strings.Index(rest, "@@")
	if end < 0 {
		return nil, fmt.Errorf("missing closing @@")
	}
	counts := strings.TrimSpace(rest[:end])
	h := &Hunk{
		Header: strings.TrimSpace(rest[end+2:]),
	}
	parts := strings.Fields(counts)
	if len(parts) < 2 {
		return nil, fmt.Errorf("expected two ranges, got %q", counts)
	}
	if !strings.HasPrefix(parts[0], "-") {
		return nil, fmt.Errorf("first range must start with -, got %q", parts[0])
	}
	if !strings.HasPrefix(parts[1], "+") {
		return nil, fmt.Errorf("second range must start with +, got %q", parts[1])
	}
	var err error
	h.OldStart, h.OldLines, err = parseRange(parts[0][1:])
	if err != nil {
		return nil, fmt.Errorf("old range %q: %w", parts[0][1:], err)
	}
	h.NewStart, h.NewLines, err = parseRange(parts[1][1:])
	if err != nil {
		return nil, fmt.Errorf("new range %q: %w", parts[1][1:], err)
	}
	return h, nil
}

func parseRange(s string) (start, count int, err error) {
	if comma := strings.Index(s, ","); comma >= 0 {
		start, err = strconv.Atoi(s[:comma])
		if err != nil {
			return 0, 0, err
		}
		count, err = strconv.Atoi(s[comma+1:])
		return start, count, err
	}
	start, err = strconv.Atoi(s)
	return start, 1, err
}

func stripPathPrefix(s, prefix string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}
