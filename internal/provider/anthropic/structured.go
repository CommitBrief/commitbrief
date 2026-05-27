// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

import (
	"encoding/json"

	sdk "github.com/anthropics/anthropic-sdk-go"
)

// toolName is the Anthropic tool we force the model to call. Every review
// request is wrapped in a single-tool conversation; the model's only valid
// output is a `tool_use` block whose `input` is a JSON document matching
// findingsSchema. The `tool_choice` directive locks this down at the API
// level. See ADR-0014 §4.
const toolName = "report_findings"

// findingsSchema is the Anthropic-flavored JSON Schema for one review's
// findings payload. Mirrors render.Finding — keeping the two in sync is a
// Stage 5 release-check guard.
var findingsSchema = sdk.ToolInputSchemaParam{
	Properties: map[string]any{
		"findings": map[string]any{
			"type":        "array",
			"description": "Review findings produced by the model.",
			"items": map[string]any{
				"type":     "object",
				"required": []string{"severity", "file", "line", "title", "description"},
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
					"line_end": map[string]any{
						"type":        "integer",
						"description": "Line number where the finding ends (1-based, inclusive). Include ONLY for multi-line findings and only when line_end > line; omit otherwise.",
					},
					"title": map[string]any{
						"type":        "string",
						"description": "One-sentence summary of the issue.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "1-3 sentences explaining the issue and its impact.",
					},
					"language": map[string]any{
						"type":        "string",
						"description": "Programming language identifier when a snippet is present.",
					},
					"snippet": map[string]any{
						"type":        "string",
						"description": "Short diff excerpt with - / + prefixes, optional.",
					},
				},
			},
		},
	},
	Required: []string{"findings"},
}

// buildReportTool wraps the Findings schema in the SDK's ToolUnionParam so
// it can be attached to MessageNewParams.Tools.
func buildReportTool() sdk.ToolUnionParam {
	t := sdk.ToolUnionParamOfTool(findingsSchema, toolName)
	// A short Description nudges the model to actually call the tool rather
	// than apologise in text when the diff is empty. (Without it some models
	// fall through to a plain-text refusal we'd then have to graceful-
	// degrade to render.)
	if t.OfTool != nil {
		t.OfTool.Description = sdk.String("Emit the review as structured findings. Always call this tool.")
	}
	return t
}

// extractStructured pulls the JSON document from the first tool_use block
// in the model's response. Returns the raw JSON string and true on
// success; returns "" and false when the model emitted text instead of
// calling the tool (caller falls back to extractText so the renderer can
// degrade gracefully).
func extractStructured(msg *sdk.Message) (string, bool) {
	if msg == nil {
		return "", false
	}
	for _, block := range msg.Content {
		if block.Type != "tool_use" || block.Name != toolName {
			continue
		}
		// block.Input is the raw JSON payload the model produced for the
		// tool. It already conforms to the schema we declared (modulo any
		// genuinely malformed output, which the caller's parse step
		// catches).
		if len(block.Input) == 0 {
			continue
		}
		// Re-encode to compact form so the cache key (a SHA over content)
		// is stable regardless of whitespace the SDK happens to emit.
		raw := block.Input
		out, err := json.Marshal(map[string]json.RawMessage{"findings": rawFindings(raw)})
		if err != nil {
			return "", false
		}
		return string(out), true
	}
	return "", false
}

// rawFindings unwraps the model's tool input into just the `findings`
// array, defending against a model that wraps its findings array in a
// different top-level key. The schema declares `findings` as required so
// the common case is a direct hit.
func rawFindings(raw json.RawMessage) json.RawMessage {
	var envelope struct {
		Findings json.RawMessage `json:"findings"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Findings) > 0 {
		return envelope.Findings
	}
	// Fallback: assume the input *is* the findings array.
	return raw
}
