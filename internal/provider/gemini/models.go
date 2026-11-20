// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

const (
	Name = "gemini"

	// ModelPro31 is a preview model: the public pricing page lists the Pro
	// tier only as gemini-3.1-pro-preview. Update to the stable ID once
	// Google promotes it out of preview.
	ModelPro31 = "gemini-3.1-pro-preview"
	// ModelFlash35 is deprecated (2026-10-08): Google auto-routes it to
	// gemini-3.6-flash. Kept so existing configs keep their pricing row.
	ModelFlash35     = "gemini-3.5-flash"
	ModelFlashLite31 = "gemini-3.1-flash-lite"
	ModelFlash38     = "gemini-3.8-flash"
	ModelFlashLite35 = "gemini-3.5-flash-lite"

	// Gemini 4 family (GA 2026-11). Google does not label it Pro/Flash/Lite:
	// carbon is the strongest reasoning model, neon sits between hydrogen
	// and carbon (the upgrade target for gemini-3.1-pro-preview), and
	// hydrogen is the fast, cost-efficient Flash-class successor. There is
	// no Gemini 4 Lite; the Lite tier stays gemini-3.5-flash-lite.
	ModelCarbon4   = "gemini-4-carbon"
	ModelNeon4     = "gemini-4-neon"
	ModelHydrogen4 = "gemini-4-hydrogen"

	DefaultModel = ModelFlash38
)

var supportedModels = []string{
	ModelPro31,
	ModelFlash35,
	ModelFlashLite31,
	ModelFlash38,
	ModelFlashLite35,
	ModelCarbon4,
	ModelNeon4,
	ModelHydrogen4,
}

// maxOutputTokensCeiling is the default largest MaxOutputTokens value
// buildParams and the empty-content retry will request (client.go). Most
// catalog models have a 65536-token output limit; unknown/custom model IDs
// get the same cap, and an API rejection of it surfaces as a normal error.
// Models with a lower limit are listed in maxOutputTokensOverrides.
const maxOutputTokensCeiling int32 = 65536

// maxOutputTokensOverrides holds the catalog models whose output limit is
// below maxOutputTokensCeiling. gemini-4-carbon's model page says 65536
// synchronous, but models.list reports outputTokenLimit 32768 (checked
// 2026-11-20); the API value is what request validation enforces.
var maxOutputTokensOverrides = map[string]int32{
	ModelCarbon4: 32768,
}

// maxOutputTokensFor returns the output-token ceiling for model.
func maxOutputTokensFor(model string) int32 {
	if c, ok := maxOutputTokensOverrides[model]; ok {
		return c
	}
	return maxOutputTokensCeiling
}

// thinkingModels lists the models that think by default, so thought tokens
// consume the MaxOutputTokens budget (see defaultMaxTokensFor). The Lite tier
// is deliberately left out: it is the cost-oriented tier with minimal default
// thinking, so the historical 4096 ceiling stays; a truncation there is still
// caught by the ADR-0031 repair retry. Unknown/custom model IDs also keep 4096.
var thinkingModels = map[string]bool{
	ModelPro31:     true,
	ModelFlash35:   true,
	ModelFlash38:   true,
	ModelCarbon4:   true,
	ModelNeon4:     true,
	ModelHydrogen4: true,
}

func Models() []string {
	out := make([]string, len(supportedModels))
	copy(out, supportedModels)
	return out
}

func IsModelSupported(model string) bool {
	for _, m := range supportedModels {
		if m == model {
			return true
		}
	}
	return false
}
