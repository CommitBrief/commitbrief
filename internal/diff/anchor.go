// SPDX-License-Identifier: GPL-3.0-or-later

package diff

// GitHub inline-review-comment `side` values. RIGHT is the post-image
// (new file), LEFT is the pre-image (old file).
const (
	SideRight = "RIGHT"
	SideLeft  = "LEFT"
)

// FileAnchors indexes the line numbers in one file that a GitHub inline
// review comment can legally attach to. GitHub accepts a comment on any
// line that appears in the PR diff: on the RIGHT side that is every
// context and added line (numbered in the new file); on the LEFT side
// every context and removed line (numbered in the old file). Indexing
// both lets a finding be pinned to the side it actually lives on instead
// of unconditionally guessing RIGHT — and lets a finding whose line is
// outside the diff be detected before the POST 422s.
type FileAnchors struct {
	right map[int]struct{}
	left  map[int]struct{}
}

// Anchors builds a per-file index of postable comment positions. Keyed
// by the new-file path (FileDiff.Path), falling back to OldPath for pure
// deletions where Path is empty — the same key submitPRReview looks up
// with a finding's File.
func (d Diff) Anchors() map[string]FileAnchors {
	out := make(map[string]FileAnchors, len(d.Files))
	for _, f := range d.Files {
		key := f.Path
		if key == "" {
			key = f.OldPath
		}
		fa := FileAnchors{right: map[int]struct{}{}, left: map[int]struct{}{}}
		for _, h := range f.Hunks {
			oldNo, newNo := h.OldStart, h.NewStart
			for _, l := range h.Lines {
				switch l.Kind {
				case LineContext:
					fa.right[newNo] = struct{}{}
					fa.left[oldNo] = struct{}{}
					oldNo++
					newNo++
				case LineAdd:
					fa.right[newNo] = struct{}{}
					newNo++
				case LineDel:
					fa.left[oldNo] = struct{}{}
					oldNo++
				}
			}
		}
		out[key] = fa
	}
	return out
}

// Resolve maps a finding's reported line number to a postable GitHub
// comment side. preferLeft flips the lookup order so a line valid on
// both sides resolves to LEFT — used for findings about removed code.
// ok is false when the line matches no postable position; the caller
// must not POST it (it would 422) and should fall back to the review
// summary instead of dropping it.
func (fa FileAnchors) Resolve(line int, preferLeft bool) (side string, ok bool) {
	if line <= 0 {
		return "", false
	}
	order := [2]string{SideRight, SideLeft}
	if preferLeft {
		order = [2]string{SideLeft, SideRight}
	}
	for _, s := range order {
		set := fa.right
		if s == SideLeft {
			set = fa.left
		}
		if _, found := set[line]; found {
			return s, true
		}
	}
	return "", false
}
