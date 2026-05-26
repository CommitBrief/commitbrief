package gemini

const (
	Name = "gemini"

	ModelPro2_5   = "gemini-2.5-pro"
	ModelFlash2_5 = "gemini-2.5-flash"
	ModelFlash1_5 = "gemini-1.5-flash"

	DefaultModel = ModelPro2_5
)

var supportedModels = []string{
	ModelPro2_5,
	ModelFlash2_5,
	ModelFlash1_5,
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
