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

	DefaultModel = ModelFlash35
)

var supportedModels = []string{
	ModelPro31,
	ModelFlash35,
	ModelFlashLite31,
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
