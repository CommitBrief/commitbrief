// SPDX-License-Identifier: GPL-3.0-or-later

package lang

import "strings"

// displayNames maps a normalized ISO-639 language code to a display name used
// in the output-language prompt directive ("Respond in <name> (ISO <code>)").
//
// Membership here defines which `--lang` / config values are *recognized*: a
// value outside this set is treated as invalid and falls through to the next
// source in the resolution chain (see resolve.go). The AI **output** language
// may be ANY of these; the CLI's own **interface** strings are only localized
// for the subset we ship a catalog for (see uiCatalogs).
//
// en/tr keep their established names (the two shipped UI locales); every other
// entry uses the English display name — the ISO code travels alongside it in
// the prompt, so the provider is never ambiguous about the target language.
var displayNames = map[string]string{
	"en": "English",
	"tr": "Türkçe",
	"fr": "French",
	"de": "German",
	"es": "Spanish",
	"pt": "Portuguese",
	"it": "Italian",
	"nl": "Dutch",
	"ru": "Russian",
	"uk": "Ukrainian",
	"pl": "Polish",
	"cs": "Czech",
	"sk": "Slovak",
	"sl": "Slovenian",
	"hr": "Croatian",
	"sr": "Serbian",
	"bg": "Bulgarian",
	"ro": "Romanian",
	"hu": "Hungarian",
	"el": "Greek",
	"sv": "Swedish",
	"no": "Norwegian",
	"da": "Danish",
	"fi": "Finnish",
	"is": "Icelandic",
	"et": "Estonian",
	"lv": "Latvian",
	"lt": "Lithuanian",
	"ja": "Japanese",
	"zh": "Chinese",
	"ko": "Korean",
	"ar": "Arabic",
	"he": "Hebrew",
	"fa": "Persian",
	"hi": "Hindi",
	"bn": "Bengali",
	"ur": "Urdu",
	"id": "Indonesian",
	"ms": "Malay",
	"vi": "Vietnamese",
	"th": "Thai",
	"az": "Azerbaijani",
	"kk": "Kazakh",
	"ka": "Georgian",
	"hy": "Armenian",
	"ca": "Catalan",
	"eu": "Basque",
	"gl": "Galician",
	"af": "Afrikaans",
	"sw": "Swahili",
}

// uiCatalogs is the set of languages we actually ship interface translations
// for (internal/i18n/messages.*.yml). The CLI's own strings localize only to
// these; for any other recognized output language the interface stays English.
var uiCatalogs = map[string]bool{
	"en": true,
	"tr": true,
}

// normalize lowercases and trims a raw language token so map lookups are
// case- and whitespace-insensitive ("  TR " → "tr").
func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// recognized reports whether code (after normalization) names a language we
// will ask the provider to produce output in. Unrecognized values fall
// through to the next source in the resolution chain.
func recognized(code string) bool {
	_, ok := displayNames[normalize(code)]
	return ok
}

// Recognized is the exported form of recognized, for callers that validate a
// language value before storing it (e.g. `config set output.lang`).
func Recognized(code string) bool { return recognized(code) }

// hasUICatalog reports whether we ship interface translations for code, i.e.
// whether the CLI's own strings can be localized to it (else they stay English
// while the AI output still uses the resolved language).
func hasUICatalog(code string) bool {
	return uiCatalogs[normalize(code)]
}

// displayName returns the prompt display name for a recognized code, falling
// back to the normalized code itself (callers only reach this with recognized
// codes; the fallback just guarantees a non-empty string).
func displayName(code string) string {
	code = normalize(code)
	if n, ok := displayNames[code]; ok {
		return n
	}
	return code
}

// UICatalogFor maps a raw language value (e.g. a --lang flag) to the interface
// catalog code to load: the value itself when we ship a catalog for it, else
// English. Used by early-error paths that have a raw flag but no resolved
// Resolution yet.
func UICatalogFor(raw string) string {
	if hasUICatalog(raw) {
		return normalize(raw)
	}
	return "en"
}
