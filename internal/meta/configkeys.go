// SPDX-License-Identifier: GPL-3.0-or-later

package meta

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/arch"
	"github.com/CommitBrief/commitbrief/internal/config"
)

// ConfigKey is one settable key in .commitbrief.yml.
type ConfigKey struct {
	// Path is the dotted YAML path. A free-form map contributes a
	// placeholder segment rather than a real key: providers.<name>.model.
	Path string `json:"path"`

	// Type is the YAML-facing shape: bool, int, float, string, []string,
	// object, []object, map[string]object.
	Type string `json:"type"`

	// Default is the value rendered as text — either config.Default()'s
	// actual field value, or an entry from effectiveDefaults when the real
	// behavioral default is applied downstream instead of in config.Default()
	// (review.architecture_file: config.Default() leaves it "" on purpose so
	// arch.Discover's own auto-discovery stays a silent no-op, but the
	// documented default is arch.DefaultFilename). Meaningful only when
	// HasDefault is true.
	Default string `json:"default,omitempty"`

	// HasDefault distinguishes "the default is the empty string" (e.g.
	// command.default, whose real default IS "") from "there is no default to
	// report" (a struct/slice/map node, or anything below a free-form map key
	// or inside a slice element). Default's omitempty alone cannot tell those
	// apart — both render as an absent "default" key — which is exactly how
	// review.architecture_file and command.default used to look identical in
	// the generated inventory despite one of them having a real default and
	// the other legitimately having none.
	HasDefault bool `json:"has_default,omitempty"`

	// FreeForm marks a key that lives under a user-chosen map key, so its
	// Path contains a placeholder segment and its name is not fixed.
	FreeForm bool `json:"free_form,omitempty"`
}

// effectiveDefaults documents a leaf key whose behavioral default is applied
// downstream of config.Default() rather than baked into the struct's zero
// value, keyed by the leaf's dotted Path. Referencing the owning package's
// exported constant (arch.DefaultFilename) instead of repeating the literal
// "architecture.json" keeps this in lockstep with the code that actually
// applies it (internal/arch.Discover) — the same fix applied to the
// apiKeyEnv/env.go drift in the provider packages.
var effectiveDefaults = map[string]string{
	"review.architecture_file": arch.DefaultFilename,
}

// mapPlaceholders names the placeholder segment emitted for each free-form
// map, keyed by the map field's own (already substituted) path.
//
// The names are not invented here: <name> and <model> are the spellings the
// config contract and the existing docs use, and a generated docs table that
// called them <key> would read as a regression. Anything not listed falls
// back to <key>.
var mapPlaceholders = map[string]string{
	"providers":                "<name>",
	"providers.<name>.pricing": "<model>",
}

// ConfigKeys reflects over config.Config and returns every key, sorted by
// dotted path.
//
// The sort is what makes the committed artifact stable: the walk itself is in
// struct declaration order, but map fields are walked too and a future
// refactor that reorders fields should not reshuffle the whole file. Sorting
// by path also groups each section together, which is how the docs read.
//
// The reflection rules mirror internal/config/schema.go's ValidateKeys
// (yamlFields / walkValue) on purpose: the same yaml-tag resolution, the same
// inline flattening, and above all the same stop-at-the-key treatment of
// map[string]T. Two different answers to "what is a legal config key" is
// exactly the drift this package exists to prevent. The difference is that
// schema.go walks a decoded YAML document and meta walks the TYPE, so this
// side needs no value to descend.
func ConfigKeys() []ConfigKey {
	out := []ConfigKey{}
	walkConfigNode(
		reflect.TypeOf(config.Config{}),
		reflect.ValueOf(*config.Default()),
		"", false, false, &out,
	)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// walkConfigNode emits a row for one node and descends into it.
//
// v is the corresponding value from config.Default(), or the zero Value once
// the walk has crossed into territory Default() cannot address (below a map
// key, or inside a slice element). emit is false only for a slice element,
// whose own "path[]" row would be noise — its fields are what a user sets.
func walkConfigNode(t reflect.Type, v reflect.Value, path string, freeForm, isElem bool, out *[]ConfigKey) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
		if v.IsValid() {
			if v.IsNil() {
				v = reflect.Value{}
			} else {
				v = v.Elem()
			}
		}
	}

	if path != "" && !isElem {
		def, hasDefault := renderDefault(t, v)
		if override, ok := effectiveDefaults[path]; ok {
			def, hasDefault = override, true
		}
		*out = append(*out, ConfigKey{
			Path:       path,
			Type:       typeName(t),
			Default:    def,
			HasDefault: hasDefault,
			FreeForm:   freeForm,
		})
	}

	switch t.Kind() {
	case reflect.Struct:
		for _, f := range yamlFields(t) {
			var fv reflect.Value
			if v.IsValid() {
				fv = v.FieldByIndex(f.Index)
			}
			walkConfigNode(f.Type, fv, join(path, f.Name), freeForm, false, out)
		}

	case reflect.Map:
		// Stop at the key. providers.<name> and
		// providers.<name>.pricing.<model> are free-form namespaces whose keys
		// the user chooses — Default() seeds only four providers, so naming
		// them here would document deepseek/mistral/cohere out of existence.
		// The VALUE type is still ours, so recurse into it with a placeholder
		// segment and no default.
		walkConfigNode(t.Elem(), reflect.Value{}, join(path, placeholderFor(path)), true, false, out)

	case reflect.Slice, reflect.Array:
		// Only a slice of structs has keys below it (guard.secret_patterns is
		// []SecretPatternConfig). A []string is a leaf and its row above is
		// the whole story.
		elem := t.Elem()
		for elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		if elem.Kind() == reflect.Struct {
			walkConfigNode(elem, reflect.Value{}, path+"[]", freeForm, true, out)
		}
	}
}

// yamlField is one exported field of a config struct, resolved to the key
// yaml.v3 would use for it.
type yamlField struct {
	Name  string
	Type  reflect.Type
	Index []int
}

// yamlFields lists t's fields in declaration order, flattening `,inline`
// structs into the parent level exactly as yaml.v3 does. Declaration order is
// the order the documented schema reads in; the final sort happens once, in
// ConfigKeys.
func yamlFields(t reflect.Type) []yamlField {
	fields := make([]yamlField, 0, t.NumField())

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
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() != reflect.Struct {
				continue
			}
			for _, sub := range yamlFields(ft) {
				sub.Index = append([]int{i}, sub.Index...)
				fields = append(fields, sub)
			}
			continue
		}
		fields = append(fields, yamlField{Name: name, Type: f.Type, Index: []int{i}})
	}
	return fields
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

// typeName renders a Go type the way the config documentation talks about it.
// Widths are deliberately collapsed — a reader setting cache.ttl_days cares
// that it is an integer, not that it is an int rather than an int64.
func typeName(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.String:
		return "string"
	case reflect.Slice, reflect.Array:
		return "[]" + typeName(t.Elem())
	case reflect.Map:
		return "map[" + typeName(t.Key()) + "]" + typeName(t.Elem())
	case reflect.Struct:
		return "object"
	default:
		return t.Kind().String()
	}
}

// renderDefault prints a scalar default as YAML would carry it, plus
// whether the node has one at all. Composites report no default: a map or a
// struct default is the sum of its leaves, each of which already has its own
// row. Reporting hasDefault=false there (rather than "") is what lets a
// caller tell "no default" apart from a leaf whose real default happens to
// be the empty string.
func renderDefault(t reflect.Type, v reflect.Value) (value string, hasDefault bool) {
	if !v.IsValid() {
		return "", false
	}
	switch t.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(v.Bool()), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		// 'g' with -1 precision keeps 0.5 as "0.5" instead of "0.500000".
		return strconv.FormatFloat(v.Float(), 'g', -1, 64), true
	case reflect.String:
		return v.String(), true
	default:
		return "", false
	}
}

func placeholderFor(path string) string {
	if p, ok := mapPlaceholders[path]; ok {
		return p
	}
	return "<key>"
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
