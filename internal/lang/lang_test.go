// SPDX-License-Identifier: GPL-3.0-or-later

package lang

import (
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
)

func TestParseLocale(t *testing.T) {
	cases := map[string]string{
		"tr_TR.UTF-8": "tr",
		"en_US.UTF-8": "en",
		"en_US":       "en",
		"tr":          "tr",
		"C":           "",
		"POSIX":       "",
		"":            "",
		"klingon":     "",
		"sr_RS@latin": "sr",
		"TR_tr":       "tr",
		"x":           "",
		"abcde":       "",
		"123":         "",
		"  fr  ":      "fr",
	}
	for input, want := range cases {
		if got := parseLocale(input); got != want {
			t.Errorf("parseLocale(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResolveRepoConfig(t *testing.T) {
	repo := &config.Config{Output: config.OutputConfig{Lang: "tr"}}
	global := &config.Config{Output: config.OutputConfig{Lang: "en"}}
	res := Resolve(repo, global, Env{LANG: "fr_FR.UTF-8"})
	if res.Code != "tr" {
		t.Errorf("Code = %q, want tr", res.Code)
	}
	if res.Source != SourceRepoConfig {
		t.Errorf("Source = %v, want SourceRepoConfig", res.Source)
	}
	if res.Name != "Türkçe" {
		t.Errorf("Name = %q, want Türkçe", res.Name)
	}
}

func TestResolveGlobalConfig(t *testing.T) {
	global := &config.Config{Output: config.OutputConfig{Lang: "en"}}
	res := Resolve(nil, global, Env{LANG: "fr_FR.UTF-8"})
	if res.Code != "en" || res.Source != SourceGlobalConfig {
		t.Errorf("Resolve() = %+v, want code=en source=global", res)
	}
}

func TestResolveEmptyRepoLangFallsThrough(t *testing.T) {
	repo := &config.Config{Output: config.OutputConfig{Lang: ""}}
	global := &config.Config{Output: config.OutputConfig{Lang: "tr"}}
	res := Resolve(repo, global, Env{})
	if res.Source != SourceGlobalConfig {
		t.Errorf("Empty repo lang should fall through; got %+v", res)
	}
}

func TestResolveEnvLANG(t *testing.T) {
	res := Resolve(nil, nil, Env{LANG: "tr_TR.UTF-8"})
	if res.Code != "tr" || res.Source != SourceEnvLANG {
		t.Errorf("Resolve() = %+v, want code=tr source=env", res)
	}
}

func TestResolveDefault(t *testing.T) {
	res := Resolve(nil, nil, Env{})
	if res.Code != "en" || res.Source != SourceDefault {
		t.Errorf("Resolve() = %+v, want code=en source=default", res)
	}
	if res.Name != "English" {
		t.Errorf("Name = %q, want English", res.Name)
	}
}

func TestResolveBadEnvLANGFallsToDefault(t *testing.T) {
	for _, bad := range []string{"C", "POSIX", "klingon", ""} {
		res := Resolve(nil, nil, Env{LANG: bad})
		if res.Source != SourceDefault {
			t.Errorf("LANG=%q should fall to default; got source=%v", bad, res.Source)
		}
	}
}

func TestResolveNilConfigsHandled(t *testing.T) {
	res := Resolve(nil, nil, Env{LANG: "tr_TR.UTF-8"})
	if res.Code != "tr" {
		t.Errorf("nil configs should not panic; got %+v", res)
	}
}

func TestResolveUnknownLangSilentlyCoercesToEnglish(t *testing.T) {
	// UC-09 regression guard. Pre-v0.9.2 the resolver preserved any
	// unknown code (e.g. "de") and i18n.Load *separately* fell back
	// to English, giving a confusing "Resolution says de but output
	// is English" mismatch. The locale surface is now narrowed to
	// {en, tr} and Resolve coerces silently — Source is preserved so
	// the dry-run footer still attributes the original config layer
	// even when the code itself is rewritten.
	repo := &config.Config{Output: config.OutputConfig{Lang: "de"}}
	res := Resolve(repo, nil, Env{})
	if res.Code != "en" {
		t.Errorf("Code = %q, want en (coerced)", res.Code)
	}
	if res.Name != "English" {
		t.Errorf("Name = %q, want English", res.Name)
	}
	if res.Source != SourceRepoConfig {
		t.Errorf("Source = %v, want SourceRepoConfig preserved through coercion", res.Source)
	}
}

func TestResolveUnknownEnvLANGCoercesToEnglish(t *testing.T) {
	// Same UC-09 coercion when the unsupported code arrives through
	// the LANG env var path.
	res := Resolve(nil, nil, Env{LANG: "fr_FR.UTF-8"})
	if res.Code != "en" {
		t.Errorf("Code = %q, want en (coerced from LANG=fr_FR)", res.Code)
	}
	if res.Source != SourceEnvLANG {
		t.Errorf("Source = %v, want SourceEnvLANG preserved", res.Source)
	}
}

func TestCoerceCLIFlagUnknownLandsAtEnglish(t *testing.T) {
	// UC-09 — CLI flag path mirrors the config path. --lang=de must
	// also land at "en" so i18n.Load doesn't hit a missing catalog.
	res := CoerceCLIFlag("de")
	if res.Code != "en" {
		t.Errorf("Code = %q, want en", res.Code)
	}
	if res.Source != SourceCLIFlag {
		t.Errorf("Source = %v, want SourceCLIFlag", res.Source)
	}
}

func TestCoerceCLIFlagSupportedPasses(t *testing.T) {
	if res := CoerceCLIFlag("tr"); res.Code != "tr" || res.Name != "Türkçe" {
		t.Errorf("supported tr should pass through; got %+v", res)
	}
}

func TestResolveNormalizesCase(t *testing.T) {
	repo := &config.Config{Output: config.OutputConfig{Lang: "TR"}}
	res := Resolve(repo, nil, Env{})
	if res.Code != "tr" {
		t.Errorf("Code = %q, want lowercased tr", res.Code)
	}
}

func TestSourceString(t *testing.T) {
	cases := map[Source]string{
		SourceRepoConfig:   "repo config",
		SourceGlobalConfig: "global config",
		SourceEnvLANG:      "LANG env",
		SourceDefault:      "default",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Source(%d).String() = %q, want %q", s, got, want)
		}
	}
}
