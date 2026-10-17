// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

import (
	"slices"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/rules"
)

func TestFindingsSchemaDeclaresRuleID(t *testing.T) {
	items := findingsSchema.Properties.(map[string]any)["findings"].(map[string]any)["items"].(map[string]any)
	if !slices.Contains(items["required"].([]string), "rule_id") {
		t.Error("rule_id missing from required")
	}
	prop := items["properties"].(map[string]any)["rule_id"].(map[string]any)
	if got := prop["enum"].([]string); !slices.Equal(got, rules.RuleIDs()) {
		t.Errorf("rule_id enum = %v, want the taxonomy ids", got)
	}
}
