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

func TestResolveUnknownLangPreservesCodeAsName(t *testing.T) {
	repo := &config.Config{Output: config.OutputConfig{Lang: "xx"}}
	res := Resolve(repo, nil, Env{})
	if res.Code != "xx" {
		t.Errorf("Code = %q, want xx", res.Code)
	}
	if res.Name != "xx" {
		t.Errorf("Name = %q, want raw code fallback", res.Name)
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
