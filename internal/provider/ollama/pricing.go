package ollama

import "github.com/CommitBrief/commitbrief/internal/provider"

// pricingFor returns zero pricing for every Ollama model: requests run on
// the user's own hardware, so token usage carries no per-token monetary
// cost. The Usage figures are still useful for context-window planning
// and `--verbose` token reporting.
func pricingFor(_ string) provider.Pricing {
	return provider.Pricing{}
}
