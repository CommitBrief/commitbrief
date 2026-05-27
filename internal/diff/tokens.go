// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import "github.com/CommitBrief/commitbrief/internal/tokens"

// EstimateTokens returns a rough token count from byte length using
// the chars/4 heuristic. Backed by the shared internal/tokens helper
// so providers, compress, and the diff layer never drift apart.
// Provider-side exact tokenizers can replace this when they're
// cheap to call from outside the provider's process.
func EstimateTokens(s string) int { return tokens.Estimate(s) }

func (d Diff) EstimateTokens() int {
	n := 0
	for _, f := range d.Files {
		n += EstimateTokens(f.Path) + EstimateTokens(f.OldPath)
		for _, h := range f.Hunks {
			n += EstimateTokens(h.Header)
			for _, l := range h.Lines {
				n += EstimateTokens(l.Text) + 1
			}
		}
	}
	return n
}
