// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// UnknownKey is one configuration key that has no home in Config.
//
// It exists because the load path is a round-trip through map[string]any
// (see load.go): yaml.v3 quietly discards any mapping key without a matching
// struct field, so a typo like `regexp:` for `regex:` used to produce a
// zero-valued field instead of an error — leaving the user silently
// unprotected. Path is the dotted YAML path of the offender (slice elements
// carry their index, e.g. guard.secret_patterns[0].regexp), Source is the
// file the key came from, and Allowed is the sibling key set at that level.
type UnknownKey struct {
	Path    string
	Source  string
	Allowed []string

	// TopLevel is true when the offender sits at the document root rather
	// than inside a known section. It exists purely to shape Error()'s
	// hint: a root-level typo is the one place an `x-` prefix (see
	// walkMapping) would have silenced it, so that's the one place the
	// message suggests it.
	TopLevel bool
}

// Error follows the established "name the offender, list the allowed set"
// wording (cf. internal/cli/config.go's `unknown field %q in guard`).
//
// This is deliberately a plain, English-only Go error — it is NOT run
// through the i18n catalog (fixed 2026-09-19, review turu 2 item 10: an
// earlier version of this comment claimed "the CLI boundary is what
// localizes it", but resolveContext (internal/cli/context.go) returns this
// error as-is via errors.As, and internal/config cannot import internal/i18n
// without an import cycle risk anyway). The sibling warning for the lenient
// path (config.unknown_key_ignored) IS localized, at the CLI layer, because
// that one is a literal built there from scratch; this one is the terminal,
// English message for the strict failure path and is treated like any other
// wrapped stdlib/library error in this codebase (e.g. yaml.v3's own parse
// errors, which are English too).
func (u UnknownKey) Error() string {
	msg := fmt.Sprintf("config: %s: unknown key %q (allowed: %s)",
		u.Source, u.Path, strings.Join(u.Allowed, ", "))
	// Review turu 3, item 13: an empty key has no useful "did you mean" or
	// "x-" spelling to offer (`try "x-"` is not actionable), so neither hint
	// applies. This can only happen at the document root, since a mapping
	// key can't be empty deeper in a struct-shaped section either — but the
	// check is unconditional so it never depends on that staying true.
	if !u.TopLevel || u.Path == "" {
		return msg
	}
	// Review turu 2, item 8: the "x-" hint is a silencer — applying it to an
	// actual typo (`cahce:`) would make the mistake permanent, which is
	// exactly the failure mode strict validation exists to prevent. So
	// "did you mean" is offered whenever the key is a plausible typo of
	// something real.
	//
	// Review turu 3, item 12: a plausible typo and a genuine anchor-holder
	// name are not mutually exclusive — the absolute distance-2 threshold
	// also catches real anchor holders that happen to be close to an
	// allowed key (`common: &d` is 2 edits from "command"). Returning only
	// the "did you mean" hint there left that user with no supported way
	// forward, so both hints are offered together as separate sentences:
	// the reader picks whichever matches what they actually meant.
	if suggestion, ok := closestAllowedKey(u.Path, u.Allowed); ok {
		msg += fmt.Sprintf("; did you mean %q?", suggestion)
		msg += fmt.Sprintf(" Or, if %q is meant to hold only a YAML anchor (e.g. for `<<:` merging), prefix it instead: \"x-%s\".", u.Path, u.Path)
		return msg
	}
	msg += fmt.Sprintf("; a top-level key holding only a YAML anchor (e.g. for `<<:` merging) is allowed under an \"x-\" prefix — try \"x-%s\"", u.Path)
	return msg
}

// closestAllowedKey returns the allowed key nearest to key by Levenshtein
// edit distance, when it is close enough to be a plausible typo rather than
// a deliberately different name. The threshold is small and absolute (not
// proportional to key length): config key names are short, and a generous
// threshold would start matching keys that are not actually related.
func closestAllowedKey(key string, allowed []string) (string, bool) {
	const maxDistance = 2
	best := ""
	bestDist := maxDistance + 1
	for _, a := range allowed {
		if d := levenshtein(key, a); d < bestDist {
			bestDist, best = d, a
		}
	}
	if bestDist > maxDistance {
		return "", false
	}
	return best, true
}

// levenshtein computes the classic edit distance (insert/delete/substitute)
// between two strings with an O(len(a)*len(b))-time, O(min(len(a),len(b)))-space
// DP. Config key names are a handful of characters, so the naive algorithm
// is plenty fast and needs no dependency.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	// Iterate over the shorter string in the inner loop to bound the extra
	// space by min, not max.
	if len(ra) < len(rb) {
		ra, rb = rb, ra
	}
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

// ValidateKeys reports every key in m that Config has no field for.
//
// The legal key set is derived by reflecting over Config's yaml tags rather
// than hand-written, so adding a field to Config can never leave this
// validator behind — drift here would reintroduce exactly the silent
// discard it exists to prevent.
//
// Findings are returned sorted by path: Go randomizes map iteration, and an
// error message that changes between runs is not a usable error message.
func ValidateKeys(m map[string]any, source string) []UnknownKey {
	var found []UnknownKey
	walkMapping(reflect.TypeOf(Config{}), m, "", source, &found)
	sort.Slice(found, func(i, j int) bool { return found[i].Path < found[j].Path })
	return found
}

// unknownKeyError folds findings into a single error. errors.Join keeps each
// finding reachable through errors.As (callers want the structured value),
// while a lone finding still renders as the exact one-line target shape.
func unknownKeyError(found []UnknownKey) error {
	if len(found) == 0 {
		return nil
	}
	errs := make([]error, len(found))
	for i, f := range found {
		errs[i] = f
	}
	return errors.Join(errs...)
}

// walkMapping validates one YAML mapping against one struct type.
func walkMapping(t reflect.Type, m map[string]any, prefix, source string, found *[]UnknownKey) {
	fields, allowed := yamlFields(t)
	topLevel := prefix == ""
	for key, val := range m {
		if topLevel && strings.HasPrefix(key, "x-") {
			// A YAML anchor needs a key to hang off of even when nothing
			// downstream reads that key directly, e.g.:
			//   x-defaults: &d {ttl_days: 3}
			//   cache: {<<: *d, enabled: true}
			// "x-" is the common extension-prefix convention (docker-compose,
			// OpenAPI); we don't validate what's under it. Exempt only at
			// the document root — the same prefix inside a known section is
			// far more likely a typo than an anchor holder, so it still gets
			// rejected there.
			continue
		}
		ft, ok := fields[key]
		if !ok {
			*found = append(*found, UnknownKey{Path: join(prefix, key), Source: source, Allowed: allowed, TopLevel: topLevel})
			continue
		}
		walkValue(ft, val, join(prefix, key), source, found)
	}
}

// walkValue descends into whatever the field's type allows.
func walkValue(t reflect.Type, val any, path, source string, found *[]UnknownKey) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Struct:
		// A non-mapping here is a type error, not an unknown key; the typed
		// decode in Load/LoadFile reports those with yaml.v3's own wording.
		if sub, ok := asStringMap(val); ok {
			walkMapping(t, sub, path, source, found)
		}

	case reflect.Map:
		// Stop at the key: providers.<name> and providers.<name>.pricing.<model>
		// are free-form namespaces. Default() seeds only four providers, so
		// rejecting an unseeded key here would break every deepseek/mistral/
		// cohere user. The VALUE type is still ours, so recurse into it.
		sub, ok := asStringMap(val)
		if !ok {
			return
		}
		for k, v := range sub {
			walkValue(t.Elem(), v, path+"."+k, source, found)
		}

	case reflect.Slice, reflect.Array:
		// guard.secret_patterns is []SecretPatternConfig: an unknown key
		// inside an element is the case this whole validator exists for, so
		// elements are walked with their index in the path.
		items, ok := val.([]any)
		if !ok {
			return
		}
		for i, item := range items {
			walkValue(t.Elem(), item, fmt.Sprintf("%s[%d]", path, i), source, found)
		}
	}
}

// yamlFields maps the yaml key of every exported field of t to its type, and
// returns the allowed key list in declaration order (stable, and it reads the
// way the documented schema does).
func yamlFields(t reflect.Type) (map[string]reflect.Type, []string) {
	fields := make(map[string]reflect.Type, t.NumField())
	allowed := make([]string, 0, t.NumField())

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported: yaml.v3 never sees it either
			continue
		}
		name, inline := yamlName(f)
		if name == "-" {
			continue
		}
		if inline {
			// An inlined struct's keys live at this level; flatten them so
			// they are not reported as unknown.
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				sub, subAllowed := yamlFields(ft)
				for k, v := range sub {
					fields[k] = v
				}
				allowed = append(allowed, subAllowed...)
			}
			continue
		}
		fields[name] = f.Type
		allowed = append(allowed, name)
	}
	return fields, allowed
}

// yamlName resolves the key yaml.v3 would use for a field, plus whether the
// field is inlined. Absent a tag, yaml.v3 lowercases the field name.
func yamlName(f reflect.StructField) (name string, inline bool) {
	tag, ok := f.Tag.Lookup("yaml")
	if !ok || tag == "" {
		return strings.ToLower(f.Name), false
	}
	parts := strings.Split(tag, ",")
	for _, opt := range parts[1:] {
		if opt == "inline" {
			inline = true
		}
	}
	name = parts[0]
	if name == "" {
		name = strings.ToLower(f.Name)
	}
	return name, inline
}

// asStringMap accepts both shapes a YAML mapping can take: yaml.v3 decodes
// into map[string]any, but a map[any]any can still arrive from other decoders
// or from a document with non-string keys.
func asStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			s, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[s] = val
		}
		return out, true
	default:
		return nil, false
	}
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
