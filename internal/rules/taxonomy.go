// SPDX-License-Identifier: GPL-3.0-or-later

package rules

import (
	"regexp"
	"strings"
)

// TaxonomyVersion is the version of the closed rule taxonomy (ADR-0045).
// Ids are only ever added: an id is never removed or renamed, because policy
// files and inline suppression markers refer to them. A renamed id keeps
// resolving through ruleAliases instead.
const TaxonomyVersion = 1

// Rule ids that other packages reference directly: the fallback bucket and
// the two deterministic producers that never ask the model.
const (
	// RuleOther is the K4 fallback: an empty, unknown or unparseable
	// rule_id resolves here.
	RuleOther = "general.other"
	// RuleTestFlaky is always emitted by the static flaky pre-pass
	// (internal/flaky, ADR-0022).
	RuleTestFlaky = "test.flaky"
	// RuleHardcodedSecret is always emitted by `commitbrief leaks`
	// (ADR-0036).
	RuleHardcodedSecret = "sec.hardcoded-secret"
)

// RuleLevel is a rule's default SARIF level. It is independent of a finding's
// severity: the level only feeds a SARIF rule's defaultConfiguration.
type RuleLevel string

const (
	LevelError   RuleLevel = "error"
	LevelWarning RuleLevel = "warning"
	LevelNote    RuleLevel = "note"
)

// Rule is one entry of the closed taxonomy.
type Rule struct {
	ID         string
	Definition string
	Level      RuleLevel
}

// Category returns the part of the id before the dot ("sec", "perf", ...).
func (r Rule) Category() string {
	cat, _, _ := strings.Cut(r.ID, ".")
	return cat
}

// ruleIDPattern is the id grammar: <category>.<rule>, ASCII lowercase, digits
// and '-' in the rule part.
var ruleIDPattern = regexp.MustCompile(`^[a-z]+\.[a-z0-9-]+$`)

// categories is the closed set of id prefixes.
var categories = []string{
	"sec", "correctness", "concurrency", "perf", "api", "test",
	"arch", "maint", "style", "docs", "general",
}

// taxonomy is the single in-code copy of the rule list. The prompt block, the
// normaliser and (later) SARIF, policy and suppression validation all read it;
// .ssot/contracts/rule-taxonomy.md mirrors it and a test keeps them equal.
var taxonomy = []Rule{
	{"sec.injection", "Untrusted input reaches a query, shell, template or interpreter without neutralisation (SQL, command, LDAP, template)", LevelError},
	{"sec.xss", "Untrusted data is written into HTML or JS without context-appropriate escaping", LevelError},
	{"sec.path-traversal", "A user-controlled path reaches the file system without being confined", LevelError},
	{"sec.ssrf", "A user-controlled URL or host is requested server-side without an allowlist", LevelError},
	{"sec.hardcoded-secret", "A credential, key or token literal is present in source", LevelError},
	{"sec.authz", "An authentication or authorisation check is missing or can be bypassed", LevelError},
	{"sec.unsafe-deserialization", "Untrusted bytes are deserialised into objects or code", LevelError},
	{"sec.crypto-misuse", "Weak algorithm, poor randomness, fixed IV or salt, or disabled TLS verification", LevelWarning},
	{"sec.data-exposure", "A secret or personal data is written to a log, an error or a response", LevelWarning},
	{"correctness.nil-deref", "A value that may be nil, null, undefined or None is dereferenced", LevelError},
	{"correctness.logic-error", "A condition, operator or flow does not do what the code intends", LevelWarning},
	{"correctness.boundary", "Off-by-one, empty, overflow or other boundary cases are handled wrongly", LevelWarning},
	{"correctness.error-handling", "An error is ignored, swallowed, wrapped wrongly or not propagated", LevelWarning},
	{"correctness.resource-leak", "A file, connection, lock or goroutine is not released on every path", LevelWarning},
	{"correctness.api-misuse", "A library or standard-library API is called against its contract", LevelWarning},
	{"concurrency.race", "Shared state is accessed concurrently without synchronisation", LevelError},
	{"concurrency.deadlock", "Lock ordering or a channel wait can block forever", LevelError},
	{"perf.algorithmic", "Asymptotically poor algorithm or repeated work on a hot path", LevelWarning},
	{"perf.n-plus-one", "A per-item query or IO call inside a loop", LevelWarning},
	{"perf.allocation", "Avoidable memory allocation or copying on a hot path", LevelNote},
	{"api.breaking-change", "A public interface or contract changes incompatibly", LevelWarning},
	{"test.missing", "No test covers the changed behaviour", LevelNote},
	{"test.flaky", "Test depends on time, order, randomness or network (the ADR-0022 static pre-pass also produces this)", LevelWarning},
	{"arch.boundary-violation", "The change violates a declared architectural constraint (architecture.json, ADR-0030)", LevelWarning},
	{"maint.complexity", "Structure is hard to follow and could be simplified", LevelNote},
	{"maint.duplication", "Logic that should be shared has been copied", LevelNote},
	{"maint.dead-code", "Unreachable or unused code", LevelNote},
	{"style.naming", "Misleading or inconsistent identifier", LevelNote},
	{"docs.mismatch", "A comment or document contradicts the code", LevelNote},
	{"general.other", "None of the above (the fallback)", LevelNote},
}

// ruleAliases maps a retired or alternate spelling to its canonical id. It is
// empty at taxonomy version 1: ids are never renamed yet. When one is, the old
// id is added here so existing policy files and markers keep resolving.
var ruleAliases = map[string]string{}

// ruleIndex is the id -> position lookup, built once.
var ruleIndex = func() map[string]int {
	m := make(map[string]int, len(taxonomy))
	for i, r := range taxonomy {
		m[r.ID] = i
	}
	return m
}()

// Taxonomy returns a copy of the rule list in its canonical order.
func Taxonomy() []Rule {
	out := make([]Rule, len(taxonomy))
	copy(out, taxonomy)
	return out
}

// RuleIDs returns every id in canonical order.
func RuleIDs() []string {
	out := make([]string, len(taxonomy))
	for i, r := range taxonomy {
		out[i] = r.ID
	}
	return out
}

// LookupRule returns the rule for a canonical id.
func LookupRule(id string) (Rule, bool) {
	i, ok := ruleIndex[id]
	if !ok {
		return Rule{}, false
	}
	return taxonomy[i], true
}

// IsValidRuleID reports whether id is a canonical taxonomy id.
func IsValidRuleID(id string) bool {
	_, ok := ruleIndex[id]
	return ok
}

// NormaliseRuleID resolves a model-supplied rule_id to a canonical id: trim,
// lowercase, alias table, then membership. Empty and unknown values become
// RuleOther. fallback reports whether the result is the fallback because the
// input was empty or unknown (an input that literally said "general.other" is
// not counted) so the caller can log how many findings were normalised.
func NormaliseRuleID(raw string) (id string, fallback bool) {
	id = strings.ToLower(strings.TrimSpace(raw))
	if to, ok := ruleAliases[id]; ok {
		id = to
	}
	if IsValidRuleID(id) {
		return id, false
	}
	return RuleOther, true
}

// taxonomyPrompt renders the <rule_taxonomy> block body: one "id: definition"
// line per rule, generated from the same table the normaliser uses.
func taxonomyPrompt() string {
	var sb strings.Builder
	sb.WriteString("Assign every finding exactly one rule_id from this closed list. Copy the id verbatim; use general.other only when no other id fits.\n")
	for _, r := range taxonomy {
		sb.WriteString("- ")
		sb.WriteString(r.ID)
		sb.WriteString(": ")
		sb.WriteString(r.Definition)
		sb.WriteString("\n")
	}
	return sb.String()
}
