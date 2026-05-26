package diff

// EstimateTokens returns a rough token count from byte length using the
// chars/4 heuristic. Anthropic and OpenAI tokenizers vary, but chars/4 is a
// stable upper-bound estimate for English + code in v1; per-provider exact
// counts can replace this once the provider modules ship.
func EstimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	return (len(s) + 3) / 4
}

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
