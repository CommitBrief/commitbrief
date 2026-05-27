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

var langNames = map[string]string{
	"en": "English",
	"tr": "Türkçe",
	"de": "Deutsch",
	"fr": "Français",
	"es": "Español",
	"it": "Italiano",
	"pt": "Português",
	"ja": "日本語",
	"zh": "中文",
	"ko": "한국어",
	"ru": "Русский",
	"ar": "العربية",
	"nl": "Nederlands",
	"pl": "Polski",
	"sv": "Svenska",
}

func nameOf(code string) string {
	code = normalize(code)
	if n, ok := langNames[code]; ok {
		return n
	}
	return code
}
