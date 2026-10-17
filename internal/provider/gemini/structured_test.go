// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

import (
	"slices"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/rules"
)

func TestResponseSchemaDeclaresRuleID(t *testing.T) {
	items := responseSchema().Properties["findings"].Items
	if !slices.Contains(items.Required, "rule_id") {
		t.Error("rule_id missing from required")
	}
	if !slices.Contains(items.PropertyOrdering, "rule_id") {
		t.Error("rule_id missing from PropertyOrdering")
	}
	if got := items.Properties["rule_id"].Enum; !slices.Equal(got, rules.RuleIDs()) {
		t.Errorf("rule_id enum = %v, want the taxonomy ids", got)
	}
}
