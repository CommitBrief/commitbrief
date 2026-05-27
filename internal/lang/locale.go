// SPDX-License-Identifier: GPL-3.0-or-later

package lang

import "strings"

func parseLocale(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "."); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "_"); i >= 0 {
		s = s[:i]
	}
	s = strings.ToLower(s)
	if s == "" || s == "c" || s == "posix" {
		return ""
	}
	if len(s) < 2 || len(s) > 3 {
		return ""
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return ""
		}
	}
	return s
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// langNames lists every locale we actually ship translations for.
// UC-09: this used to advertise 15 languages — but only en + tr have
// real catalogs in internal/i18n/messages.*.yml. Listing more in
// `commitbrief providers list`'s sample output and in nameOf() made
// the CLI look multilingual while every unsupported pick silently
// fell back to English at i18n.Load time. Now the map matches the
// shipped reality; the supported() helper drives coercion in
// resolve.go.
var langNames = map[string]string{
	"en": "English",
	"tr": "Türkçe",
}

// supported reports whether we ship a real i18n catalog for the
// given code (case-normalised). Used by Resolve to coerce unknown
// codes back to "en" before they reach i18n.Load.
func supported(code string) bool {
	_, ok := langNames[normalize(code)]
	return ok
}

func nameOf(code string) string {
	code = normalize(code)
	if n, ok := langNames[code]; ok {
		return n
	}
	// Fallback presentation for unsupported codes — Resolve coerces
	// the *active* code to "en", but callers that pass a raw code
	// directly to nameOf (e.g. tests, error messages) still need a
	// non-empty display string.
	return code
}
