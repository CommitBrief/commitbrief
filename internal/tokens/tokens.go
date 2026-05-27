// SPDX-License-Identifier: GPL-3.0-or-later

// Package tokens carries the shared token-count heuristic used by
// every provider's EstimateTokens implementation plus the
// internal/diff and internal/compress consumers. The chars/4
// approximation is intentionally crude — it's an upper-bound
// estimate good enough for the cost preflight and context-window
// gate. Per-provider exact tokenizers would supersede it when
// they're cheap to call from outside the provider's process.
//
// Why a dedicated leaf package: keeping the heuristic in one place
// prevents drift between providers. The package is import-cycle
// safe (depends on nothing) so any package can pull it in without
// dragging the diff/compress trees along.
package tokens

// Estimate returns a rough token count using the chars/4 heuristic.
// `(len(s) + 3) / 4` rounds up so empty input gets 0 and short
// strings get at least 1 token.
func Estimate(s string) int {
	if len(s) == 0 {
		return 0
	}
	return (len(s) + 3) / 4
}
