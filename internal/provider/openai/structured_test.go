// SPDX-License-Identifier: GPL-3.0-or-later

package openai

import (
	"slices"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/rules"
)

// Strict mode forbids additional properties, so rule_id must be declared and
// required or the model could never emit it (ADR-0045).
func TestResponseSchemaDeclaresRuleID(t *testing.T) {
	items := responseSchema["properties"].(map[string]any)["findings"].(map[string]any)["items"].(map[string]any)
	if !slices.Contains(items["required"].([]string), "rule_id") {
		t.Error("rule_id missing from required")
	}
	prop := items["properties"].(map[string]any)["rule_id"].(map[string]any)
	if got := prop["enum"].([]string); !slices.Equal(got, rules.RuleIDs()) {
		t.Errorf("rule_id enum = %v, want the taxonomy ids", got)
	}
}
