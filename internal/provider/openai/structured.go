package openai

import (
	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// schemaName is the json_schema.name OpenAI requires; must be a-z0-9_-.
const schemaName = "review_findings"

// responseSchema is the JSON Schema (subset OpenAI accepts in strict mode)
// describing the Findings envelope from ADR-0014 §1. The model is forced
// to produce JSON matching this shape via response_format.
//
// OpenAI strict mode imposes: all required fields must be listed in
// `required`, no additional properties, no unsupported keywords. Optional
// `language` and `snippet` from ADR-0014 are dropped from the strict
// schema and rely on the system prompt instead — strict mode rejects
// optional object properties.
var responseSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"findings"},
	"properties": map[string]any{
		"findings": map[string]any{
			"type":        "array",
			"description": "Review findings produced by the model.",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"severity", "file", "line", "title", "description"},
				"properties": map[string]any{
					"severity": map[string]any{
						"type":        "string",
						"enum":        []string{"critical", "high", "medium", "low", "info"},
						"description": "How urgently the finding should be addressed.",
					},
					"file": map[string]any{
						"type":        "string",
						"description": "Path relative to repo root.",
					},
					"line": map[string]any{
						"type":        "integer",
						"description": "Line number in the file (1-based).",
					},
					"title": map[string]any{
						"type":        "string",
						"description": "One-sentence summary of the issue.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "1-3 sentences explaining the issue and its impact.",
					},
				},
			},
		},
	},
}

// buildResponseFormat returns the ResponseFormat union value OpenAI's
// Chat Completions API expects for native structured output. Strict=true
// asks OpenAI to refuse the request rather than fall through to a model
// that ignores the schema.
func buildResponseFormat() sdk.ChatCompletionNewParamsResponseFormatUnion {
	return sdk.ChatCompletionNewParamsResponseFormatUnion{
		OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
			JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
				Name:        schemaName,
				Description: sdk.String("Structured findings for a code review."),
				Strict:      sdk.Bool(true),
				Schema:      responseSchema,
			},
		},
	}
}
