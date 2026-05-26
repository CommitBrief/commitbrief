package gemini

import (
	sdk "google.golang.org/genai"
)

// responseSchema describes the Findings envelope (ADR-0014 §1) using the
// Gemini SDK's *Schema type. Combined with ResponseMIMEType:"application/json"
// in the GenerateContentConfig it forces the model to emit JSON matching
// the shape.
//
// PropertyOrdering is set so the model emits keys in a predictable order;
// this stabilises cache keys (which hash Content) across replays.
func responseSchema() *sdk.Schema {
	return &sdk.Schema{
		Type:     sdk.TypeObject,
		Required: []string{"findings"},
		Properties: map[string]*sdk.Schema{
			"findings": {
				Type:        sdk.TypeArray,
				Description: "Review findings produced by the model.",
				Items: &sdk.Schema{
					Type:             sdk.TypeObject,
					Required:         []string{"severity", "file", "line", "title", "description"},
					PropertyOrdering: []string{"severity", "file", "line", "title", "description", "language", "snippet"},
					Properties: map[string]*sdk.Schema{
						"severity": {
							Type:        sdk.TypeString,
							Enum:        []string{"critical", "high", "medium", "low", "info"},
							Description: "How urgently the finding should be addressed.",
						},
						"file": {
							Type:        sdk.TypeString,
							Description: "Path relative to repo root.",
						},
						"line": {
							Type:        sdk.TypeInteger,
							Description: "Line number in the file (1-based).",
						},
						"title": {
							Type:        sdk.TypeString,
							Description: "One-sentence summary of the issue.",
						},
						"description": {
							Type:        sdk.TypeString,
							Description: "1-3 sentences explaining the issue and its impact.",
						},
						"language": {
							Type:        sdk.TypeString,
							Description: "Programming language identifier for the snippet (optional).",
						},
						"snippet": {
							Type:        sdk.TypeString,
							Description: "Short diff excerpt with - / + prefixes (optional).",
						},
					},
				},
			},
		},
	}
}
