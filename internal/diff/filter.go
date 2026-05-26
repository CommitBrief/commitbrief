package diff

import "github.com/CommitBrief/commitbrief/internal/ignore"

func Filter(d Diff, m *ignore.Matcher) Diff {
	if m == nil || m.Len() == 0 {
		return d
	}
	out := Diff{Origin: d.Origin, Args: d.Args}
	for _, f := range d.Files {
		if shouldExclude(f, m) {
			continue
		}
		out.Files = append(out.Files, f)
	}
	return out
}

func shouldExclude(f FileDiff, m *ignore.Matcher) bool {
	if f.Path != "" && m.Match(f.Path) {
		return true
	}
	// For renames/deletes the post-change path may be empty; fall back to OldPath.
	if f.OldPath != "" && m.Match(f.OldPath) {
		return true
	}
	return false
}
