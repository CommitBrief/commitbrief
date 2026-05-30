// SPDX-License-Identifier: GPL-3.0-or-later

package openai

import (
	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
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
// fields (`line_end`, `language`, `snippet` from ADR-0014) are dropped
// from the strict schema and rely on the system prompt instead — strict
// mode rejects optional object properties. The system prompt's JSON
// contract block documents all four optional fields verbatim, so the
// model still receives the same instructions.
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
				"required":             []string{"severity", "file", "line", "title", "description", "suggestion"},
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
						"description": "Line number where the finding starts (1-based).",
					},
					"title": map[string]any{
						"type":        "string",
						"description": "One-sentence summary of the issue.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "1-3 sentences explaining the issue and its impact.",
					},
					"suggestion": map[string]any{
						"type":        "string",
						"description": "Required. 2-3 sentence concrete fix recommendation; answers \"what now?\" with specifics (functions, parameters, approaches) rather than restating the description.",
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

// buildResponsesTextFormat returns the Responses API equivalent of
// buildResponseFormat — the same strict findings schema expressed as a
// `text.format` json_schema config. Used by Responses-API-only models
// (gpt-5.5-pro).
func buildResponsesTextFormat() responses.ResponseTextConfigParam {
	return responses.ResponseTextConfigParam{
		Format: responses.ResponseFormatTextConfigUnionParam{
			OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
				Name:        schemaName,
				Description: sdk.String("Structured findings for a code review."),
				Strict:      sdk.Bool(true),
				Schema:      responseSchema,
			},
		},
	}
}
