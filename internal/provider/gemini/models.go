// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

const (
	Name = "gemini"

	// ModelPro31 is a preview model: the public pricing page lists the Pro
	// tier only as gemini-3.1-pro-preview. Update to the stable ID once
	// Google promotes it out of preview.
	ModelPro31       = "gemini-3.1-pro-preview"
	ModelFlash35     = "gemini-3.5-flash"
	ModelFlashLite31 = "gemini-3.1-flash-lite"
	ModelFlash38     = "gemini-3.8-flash"
	ModelFlashLite35 = "gemini-3.5-flash-lite"

	DefaultModel = ModelFlash38
)

var supportedModels = []string{
	ModelPro31,
	ModelFlash35,
	ModelFlashLite31,
	ModelFlash38,
	ModelFlashLite35,
}

// thinkingModels lists the models that think by default, so thought tokens
// consume the MaxOutputTokens budget (see defaultMaxTokensFor). The Lite tier
// is deliberately left out: it is the cost-oriented tier with minimal default
// thinking, so the historical 4096 ceiling stays; a truncation there is still
// caught by the ADR-0031 repair retry. Unknown/custom model IDs also keep 4096.
var thinkingModels = map[string]bool{
	ModelPro31:   true,
	ModelFlash35: true,
	ModelFlash38: true,
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
