// SPDX-License-Identifier: GPL-3.0-or-later

package rules

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTaxonomyShape(t *testing.T) {
	if got := len(taxonomy); got != 30 {
		t.Fatalf("taxonomy has %d rules, want 30", got)
	}
	seen := map[string]bool{}
	cats := map[string]bool{}
	for _, c := range categories {
		cats[c] = true
	}
	for _, r := range taxonomy {
		if !ruleIDPattern.MatchString(r.ID) {
			t.Errorf("id %q does not match %s", r.ID, ruleIDPattern)
		}
		if seen[r.ID] {
			t.Errorf("duplicate id %q", r.ID)
		}
		seen[r.ID] = true
		if !cats[r.Category()] {
			t.Errorf("id %q has unknown category %q", r.ID, r.Category())
		}
		if strings.TrimSpace(r.Definition) == "" {
			t.Errorf("id %q has an empty definition", r.ID)
		}
		switch r.Level {
		case LevelError, LevelWarning, LevelNote:
		default:
			t.Errorf("id %q has invalid level %q", r.ID, r.Level)
		}
	}
	for _, id := range []string{RuleOther, RuleTestFlaky, RuleHardcodedSecret} {
		if !IsValidRuleID(id) {
			t.Errorf("named constant %q is not in the taxonomy", id)
		}
	}
}

func TestNormaliseRuleID(t *testing.T) {
	cases := []struct {
		in       string
		want     string
		fallback bool
	}{
		{"sec.injection", "sec.injection", false},
		{"  SEC.Injection \n", "sec.injection", false},
		{"", RuleOther, true},
		{"   ", RuleOther, true},
		{"sec.made-up", RuleOther, true},
		{"injection", RuleOther, true},
		{"general.other", RuleOther, false},
	}
	for _, c := range cases {
		got, fb := NormaliseRuleID(c.in)
		if got != c.want || fb != c.fallback {
			t.Errorf("NormaliseRuleID(%q) = (%q, %v), want (%q, %v)", c.in, got, fb, c.want, c.fallback)
		}
	}
}

func TestNormaliseRuleIDAlias(t *testing.T) {
	ruleAliases["sec.sqli"] = "sec.injection"
	t.Cleanup(func() { delete(ruleAliases, "sec.sqli") })
	got, fb := NormaliseRuleID("SEC.SQLI")
	if got != "sec.injection" || fb {
		t.Errorf("alias not resolved: (%q, %v)", got, fb)
	}
}

func TestAliasTableTargetsAreValid(t *testing.T) {
	for from, to := range ruleAliases {
		if !IsValidRuleID(to) {
			t.Errorf("alias %q -> %q targets an unknown id", from, to)
		}
		if IsValidRuleID(from) {
			t.Errorf("alias %q shadows a canonical id", from)
		}
	}
}

func TestTaxonomyPromptListsEveryRule(t *testing.T) {
	p := taxonomyPrompt()
	for _, r := range taxonomy {
		if !strings.Contains(p, "- "+r.ID+": ") {
			t.Errorf("prompt block is missing %q", r.ID)
		}
	}
}

func TestTaxonomyIsACopy(t *testing.T) {
	a := Taxonomy()
	a[0].ID = "x.y"
	if taxonomy[0].ID == "x.y" {
		t.Error("Taxonomy() leaked the internal slice")
	}
}

// docRow matches a table row of the contract document.
var docRow = regexp.MustCompile("^\\| `([^`]+)` \\| (\\w+) \\| (.+) \\|$")

// TestTaxonomyMatchesContractDoc keeps .ssot/contracts/rule-taxonomy.md and
// the in-code list identical (id, level, definition, order). The .ssot tree
// is private to the maintainer and absent from the public repository, so the
// test skips when it cannot find the document.
func TestTaxonomyMatchesContractDoc(t *testing.T) {
	path := findContractDoc(t)
	if path == "" {
		t.Skip(".ssot/contracts/rule-taxonomy.md not reachable from this checkout")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []Rule
	for _, line := range strings.Split(string(raw), "\n") {
		m := docRow.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		rows = append(rows, Rule{ID: m[1], Level: RuleLevel(m[2]), Definition: m[3]})
	}
	if len(rows) != len(taxonomy) {
		t.Fatalf("doc lists %d rules, code lists %d", len(rows), len(taxonomy))
	}
	for i, want := range taxonomy {
		if rows[i] != want {
			t.Errorf("row %d differs:\n doc:  %+v\n code: %+v", i, rows[i], want)
		}
	}
}

func findContractDoc(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		p := filepath.Join(dir, ".ssot", "contracts", "rule-taxonomy.md")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
