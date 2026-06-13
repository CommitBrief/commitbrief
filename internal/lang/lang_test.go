// SPDX-License-Identifier: GPL-3.0-or-later

package lang

import (
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
)

func cfgLang(code string) *config.Config {
	return &config.Config{Output: config.OutputConfig{Lang: code}}
}

// --- chain order -----------------------------------------------------------

func TestResolveFlagWins(t *testing.T) {
	res := Resolve("fr", cfgLang("tr"), cfgLang("de"))
	if res.Code != "fr" || res.Source != SourceCLIFlag {
		t.Fatalf("flag should win; got %+v", res)
	}
}

func TestResolveRepoOverGlobal(t *testing.T) {
	res := Resolve("", cfgLang("tr"), cfgLang("de"))
	if res.Code != "tr" || res.Source != SourceRepoConfig {
		t.Fatalf("repo config should win over global; got %+v", res)
	}
}

func TestResolveGlobalWhenNoRepo(t *testing.T) {
	res := Resolve("", cfgLang(""), cfgLang("de"))
	if res.Code != "de" || res.Source != SourceGlobalConfig {
		t.Fatalf("global config should be used; got %+v", res)
	}
}

func TestResolveDefaultEnglish(t *testing.T) {
	res := Resolve("", nil, nil)
	if res.Code != "en" || res.Source != SourceDefault {
		t.Fatalf("empty chain should default to English; got %+v", res)
	}
	if res.Name != "English" {
		t.Errorf("Name = %q, want English", res.Name)
	}
}

// --- fall-through on empty / invalid ---------------------------------------

func TestResolveInvalidFlagFallsThrough(t *testing.T) {
	// An unrecognized --lang value does NOT short-circuit to English: it
	// falls through to the repo config (the user's actual mistake-tolerant
	// requirement).
	res := Resolve("zzz", cfgLang("tr"), cfgLang("de"))
	if res.Code != "tr" || res.Source != SourceRepoConfig {
		t.Fatalf("invalid flag should fall through to repo; got %+v", res)
	}
}

func TestResolveInvalidRepoFallsThroughToGlobal(t *testing.T) {
	res := Resolve("", cfgLang("nope"), cfgLang("fr"))
	if res.Code != "fr" || res.Source != SourceGlobalConfig {
		t.Fatalf("invalid repo lang should fall through to global; got %+v", res)
	}
}

func TestResolveAllInvalidFallsToEnglish(t *testing.T) {
	res := Resolve("xx", cfgLang("yy"), cfgLang("zz"))
	if res.Code != "en" || res.Source != SourceDefault {
		t.Fatalf("all-invalid chain should land at English; got %+v", res)
	}
}

func TestResolveEmptyRepoFallsThrough(t *testing.T) {
	res := Resolve("", cfgLang(""), cfgLang("tr"))
	if res.Source != SourceGlobalConfig {
		t.Fatalf("empty repo lang should fall through; got %+v", res)
	}
}

// --- output-only languages (no UI catalog) ---------------------------------

func TestResolveOutputOnlyLanguagePreserved(t *testing.T) {
	// fr is recognized for OUTPUT but has no UI catalog: the resolved Code
	// stays "fr" (so the AI answers in French) while UICatalog() degrades to
	// English (so the CLI chrome stays English). This is the core of the
	// decoupling the maintainer asked for.
	res := Resolve("fr", nil, nil)
	if res.Code != "fr" {
		t.Errorf("output Code = %q, want fr (preserved, not coerced)", res.Code)
	}
	if res.Name != "French" {
		t.Errorf("Name = %q, want French", res.Name)
	}
	if res.UICatalog() != "en" {
		t.Errorf("UICatalog() = %q, want en (no fr catalog)", res.UICatalog())
	}
}

func TestResolveCatalogLanguageUsesItForBoth(t *testing.T) {
	res := Resolve("tr", nil, nil)
	if res.Code != "tr" || res.UICatalog() != "tr" {
		t.Errorf("tr should drive both output and UI; got Code=%q UICatalog=%q", res.Code, res.UICatalog())
	}
	if res.Name != "Türkçe" {
		t.Errorf("Name = %q, want Türkçe", res.Name)
	}
}

func TestUICatalogEnglishForUntranslated(t *testing.T) {
	for _, code := range []string{"fr", "de", "ja", "ar"} {
		if got := (Resolution{Code: code}).UICatalog(); got != "en" {
			t.Errorf("UICatalog(%q) = %q, want en", code, got)
		}
	}
	for _, code := range []string{"en", "tr"} {
		if got := (Resolution{Code: code}).UICatalog(); got != code {
			t.Errorf("UICatalog(%q) = %q, want %q", code, got, code)
		}
	}
}

// --- normalization + helpers -----------------------------------------------

func TestResolveNormalizesCase(t *testing.T) {
	res := Resolve("TR", nil, nil)
	if res.Code != "tr" {
		t.Errorf("Code = %q, want lowercased tr", res.Code)
	}
}

func TestEnglishConstructor(t *testing.T) {
	res := English()
	if res.Code != "en" || res.Name != "English" {
		t.Errorf("English() = %+v, want en/English", res)
	}
}

func TestRecognized(t *testing.T) {
	for _, ok := range []string{"en", "tr", "fr", "DE", "  es  ", "ja"} {
		if !Recognized(ok) {
			t.Errorf("Recognized(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "zzz", "english", "xx", "123"} {
		if Recognized(bad) {
			t.Errorf("Recognized(%q) = true, want false", bad)
		}
	}
}

func TestUICatalogFor(t *testing.T) {
	cases := map[string]string{"tr": "tr", "TR": "tr", "fr": "en", "": "en", "zzz": "en"}
	for in, want := range cases {
		if got := UICatalogFor(in); got != want {
			t.Errorf("UICatalogFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSourceString(t *testing.T) {
	cases := map[Source]string{
		SourceCLIFlag:      "cli flag",
		SourceRepoConfig:   "repo config",
		SourceGlobalConfig: "global config",
		SourceDefault:      "default",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Source(%d).String() = %q, want %q", s, got, want)
		}
	}
}
