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
}

// Error follows the established "name the offender, list the allowed set"
// wording (cf. internal/cli/config.go's `unknown field %q in guard`).
// internal/config carries no i18n catalog, so this is a plain Go error; the
// CLI boundary is what localizes it.
func (u UnknownKey) Error() string {
	return fmt.Sprintf("config: %s: unknown key %q (allowed: %s)",
		u.Source, u.Path, strings.Join(u.Allowed, ", "))
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
	for key, val := range m {
		ft, ok := fields[key]
		if !ok {
			*found = append(*found, UnknownKey{Path: join(prefix, key), Source: source, Allowed: allowed})
			continue
		}
		walkValue(ft, val, join(prefix, key), source, found)
	}
}

// walkValue descends into whatever the field's type allows.
func walkValue(t reflect.Type, val any, path, source string, found *[]UnknownKey) {
	for t.Kind() == reflect.Ptr {
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
			for ft.Kind() == reflect.Ptr {
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
