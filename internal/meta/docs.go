// SPDX-License-Identifier: GPL-3.0-or-later

package meta

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Region markers bracket a generated block in a markdown file:
//
//	<!-- commitbrief:gen NAME -->
//	…rendered content…
//	<!-- commitbrief:end NAME -->
//
// They are HTML comments so the markers themselves render invisibly.
//
// Regions is a dumb line scanner: it does not know about fenced code blocks
// and will happily "find" a marker pair written as a ```-fenced EXAMPLE of
// the syntax (Faz 07 review M7). That is not unsafe — a documentation
// example almost certainly reuses an existing region name, which Regions
// already rejects as a duplicate, or introduces a fresh name Apply/Verify
// then reject as an unregistered renderer, so the failure is loud and the
// build breaks rather than corrupting a region — but it IS a footgun the
// day someone adds a properly-fenced usage example to this file. Teaching
// Regions to skip fenced spans is left for whoever hits it (or Faz 08, if
// this mechanism grows a wiki/site consumer, where a code-fenced example is
// more likely to show up).

var (
	genMarker = regexp.MustCompile(`^<!--\s*commitbrief:gen\s+(\S+)\s*-->\s*$`)
	endMarker = regexp.MustCompile(`^<!--\s*commitbrief:end\s+(\S+)\s*-->\s*$`)
)

// Region is one `<!-- commitbrief:gen NAME -->` … `<!-- commitbrief:end NAME
// -->` span, located by 0-based line index into the file it was found in.
// StartLine and EndLine are the marker lines themselves; the generated
// content lives strictly between them.
type Region struct {
	Name      string
	StartLine int
	EndLine   int
}

// Regions scans markdown for region marker pairs and validates the marker
// syntax itself — NOT whether a renderer exists for each name; that is
// Apply/Verify's job, since only they have a Surface-shaped reason to talk
// about renderers at all.
//
// It errors on exactly the three ways a marker typo can silently switch a
// drift guard off (Faz 07's stated failure mode): a region opened but never
// closed (including one gen marker opening before the previous one closed —
// nesting is not supported, so that is "never closed" too), a region name
// used more than once, and an end marker that does not match the region it
// is supposedly closing (including one with no open region at all). Any of
// these returns an error instead of silently accepting a malformed file, so
// Apply/Verify can never render into the wrong place or skip a broken
// region without noticing.
func Regions(markdown string) ([]Region, error) {
	lines := strings.Split(markdown, "\n")

	var regions []Region
	seen := map[string]bool{}
	var open *Region

	for i, line := range lines {
		if m := genMarker.FindStringSubmatch(line); m != nil {
			name := m[1]
			if open != nil {
				return nil, fmt.Errorf(
					"docs: region %q (opened at line %d) is not closed before region %q starts at line %d",
					open.Name, open.StartLine+1, name, i+1,
				)
			}
			if seen[name] {
				return nil, fmt.Errorf("docs: region %q is opened more than once (again at line %d)", name, i+1)
			}
			seen[name] = true
			open = &Region{Name: name, StartLine: i}
			continue
		}
		if m := endMarker.FindStringSubmatch(line); m != nil {
			name := m[1]
			switch {
			case open == nil:
				return nil, fmt.Errorf("docs: end marker for region %q at line %d has no matching start", name, i+1)
			case open.Name != name:
				return nil, fmt.Errorf(
					"docs: end marker for region %q at line %d does not match open region %q (opened at line %d)",
					name, i+1, open.Name, open.StartLine+1,
				)
			}
			open.EndLine = i
			regions = append(regions, *open)
			open = nil
			continue
		}
	}

	if open != nil {
		return nil, fmt.Errorf("docs: region %q (opened at line %d) is never closed", open.Name, open.StartLine+1)
	}

	return regions, nil
}

// missingRenderers reports every name registered in the renderers map that
// does NOT appear among regions, sorted for deterministic error messages.
//
// This is the other half of "every marker typo must fail loudly" (Faz 07
// review M3): Regions already rejects a name used twice, so the only way
// left for a renderer to end up with something other than exactly one
// region is zero — its marker pair deleted outright, misspelled
// (`commitbrief:generate`/`:stop` do not match genMarker/endMarker at all,
// so Regions simply never sees them), or indented (genMarker/endMarker are
// anchored at the start of the line, so leading whitespace also makes them
// invisible to Regions). All three were verified to leave Regions' own
// checks green — nothing short of an explicit "does every renderer have a
// region" pass catches them, and without it a formatter or a rebase could
// silently turn this drift guard off for one block, permanently.
//
// KNOWN LIMITATION, DO NOT "FIX" BY WEAKENING THIS CHECK (Faz 07 review
// N5): this compares against the whole PACKAGE-LEVEL renderers map, so it
// silently assumes every document ever passed to Apply/Verify is meant to
// contain all four regions. That is true for README.md today. It stops
// being true the day this engine is pointed at a second file with only
// some of the regions — e.g. a Faz 08 wiki generator targeting
// wiki/Providers-and-pricing.md, which by design has only "providers" —
// and this will then report "config-schema, env-vars, mcp-tool-args" as
// missing from a document that was never supposed to have them.
//
// That false positive is loud and fail-closed, so it is not unsafe, only
// annoying. The fix, when that day comes, is to make the expected name set
// a PARAMETER (e.g. Verify(markdown, s, wantNames)) rather than always
// asking the global renderers map — NOT to delete or loosen this check.
// This function is the one thing standing between a deleted marker pair
// and a drift guard that stays disabled forever; whoever "fixes" the
// wiki-generator noise by weakening it would be removing the exact
// protection Faz 07 was written to add.
func missingRenderers(regions []Region) []string {
	present := make(map[string]bool, len(regions))
	for _, r := range regions {
		present[r.Name] = true
	}
	var missing []string
	for name := range renderers {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// Apply renders every region in markdown against s and returns the updated
// document. Region markers themselves are left untouched; only the content
// strictly between a region's start and end marker is replaced.
//
// Renderer names are validated for every region, and every registered
// renderer is checked to have a region, BEFORE any content is replaced —
// so a markdown document with a bad region (unknown renderer) or a MISSING
// one (see missingRenderers) comes back as an error, not a half-applied
// document that silently regenerates the good regions and leaves the
// broken one exactly as stale as it was.
func Apply(markdown string, s Surface) (string, error) {
	regions, err := Regions(markdown)
	if err != nil {
		return "", err
	}
	for _, r := range regions {
		if _, ok := renderers[r.Name]; !ok {
			return "", fmt.Errorf("docs: region %q at line %d has no registered renderer", r.Name, r.StartLine+1)
		}
	}
	if missing := missingRenderers(regions); len(missing) > 0 {
		return "", fmt.Errorf("docs: registered renderer(s) with no region in the document: %s", strings.Join(missing, ", "))
	}

	lines := strings.Split(markdown, "\n")

	// Splice from the bottom of the file up, so an earlier region's
	// StartLine/EndLine (computed once, up front, against the ORIGINAL
	// line numbering) stays valid even after a later region's line count
	// changes.
	for i := len(regions) - 1; i >= 0; i-- {
		r := regions[i]
		rendered := renderers[r.Name](s)

		var body []string
		if rendered != "" {
			body = strings.Split(rendered, "\n")
		}

		merged := make([]string, 0, len(lines)+len(body))
		merged = append(merged, lines[:r.StartLine+1]...)
		merged = append(merged, body...)
		merged = append(merged, lines[r.EndLine:]...)
		lines = merged
	}

	return strings.Join(lines, "\n"), nil
}

// Verify reports whether every generated region in markdown already matches
// what Surface would render, AND that every registered renderer has exactly
// one such region — the read-only counterpart to Apply, meant for a CI gate
// (Faz 08) that must fail loudly on drift rather than rewrite the file. A
// non-nil error names every region that is out of date and every renderer
// missing a region, not just the first problem found, so a single CI
// failure shows the whole list to fix.
func Verify(markdown string, s Surface) error {
	regions, err := Regions(markdown)
	if err != nil {
		return err
	}

	lines := strings.Split(markdown, "\n")

	var stale []string
	for _, r := range regions {
		renderer, ok := renderers[r.Name]
		if !ok {
			return fmt.Errorf("docs: region %q at line %d has no registered renderer", r.Name, r.StartLine+1)
		}
		current := strings.Join(lines[r.StartLine+1:r.EndLine], "\n")
		if current != renderer(s) {
			stale = append(stale, r.Name)
		}
	}
	missing := missingRenderers(regions)

	if len(stale) == 0 && len(missing) == 0 {
		return nil
	}

	var msg strings.Builder
	msg.WriteString("docs:")
	if len(stale) > 0 {
		fmt.Fprintf(&msg, " generated region(s) out of date: %s.", strings.Join(stale, ", "))
	}
	if len(missing) > 0 {
		// A missing region is the M3 failure mode: a deleted, misspelled,
		// or indented marker pair leaves the drift guard permanently off
		// for that block without this check ever going red.
		fmt.Fprintf(&msg, " registered renderer(s) with NO region in the document (deleted/misspelled/indented marker?): %s.", strings.Join(missing, ", "))
	}
	msg.WriteString(" Run `go test ./internal/meta -run TestDocsInSync -update`.")
	return fmt.Errorf("%s", msg.String())
}
