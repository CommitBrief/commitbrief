// SPDX-License-Identifier: GPL-3.0-or-later

package lang

import "github.com/CommitBrief/commitbrief/internal/config"

type Env struct {
	LANG string
}

type Source int

const (
	SourceDefault Source = iota
	SourceEnvLANG
	SourceGlobalConfig
	SourceRepoConfig
	// SourceCLIFlag is the highest-priority step in the D-21 chain: when the
	// user explicitly passes --lang on the command line, that wins over all
	// other sources. Resolve() does not return it directly (it has no access
	// to flags); callers apply the override after Resolve() and stamp this
	// Source so dry-run and verbose footer attribution stay accurate.
	SourceCLIFlag
)

func (s Source) String() string {
	switch s {
	case SourceCLIFlag:
		return "cli flag"
	case SourceRepoConfig:
		return "repo config"
	case SourceGlobalConfig:
		return "global config"
	case SourceEnvLANG:
		return "LANG env"
	case SourceDefault:
		return "default"
	default:
		return "unknown"
	}
}

type Resolution struct {
	Code   string
	Name   string
	Source Source
}

// Resolve walks the D-21 source chain (repo config → global config →
// LANG env → built-in default) and returns the active locale.
//
// UC-09: when the resolved code is not one we ship a catalog for, we
// silently coerce it to "en" while keeping the original Source so
// dry-run and verbose-footer attribution still reflect *where* the
// choice came from. Erroring or warning loudly would punish users
// with legitimate non-en locales (LANG=de_DE.UTF-8 on a developer
// laptop) — silent coerce keeps the CLI usable and matches the
// existing i18n.Load fallback semantics one layer below.
func Resolve(repo, global *config.Config, env Env) Resolution {
	if repo != nil && repo.Output.Lang != "" {
		return coerce(fromConfig(repo.Output.Lang, SourceRepoConfig))
	}
	if global != nil && global.Output.Lang != "" {
		return coerce(fromConfig(global.Output.Lang, SourceGlobalConfig))
	}
	if code := parseLocale(env.LANG); code != "" {
		return coerce(Resolution{Code: code, Name: nameOf(code), Source: SourceEnvLANG})
	}
	return Resolution{Code: "en", Name: "English", Source: SourceDefault}
}

// coerce returns r untouched when r.Code names a supported locale,
// otherwise it swaps Code+Name for English while preserving Source.
// Centralised so every entry point in Resolve hits the same logic.
func coerce(r Resolution) Resolution {
	if supported(r.Code) {
		return r
	}
	r.Code = "en"
	r.Name = "English"
	return r
}

func fromConfig(raw string, src Source) Resolution {
	code := normalize(raw)
	return Resolution{Code: code, Name: nameOf(code), Source: src}
}

// CoerceCLIFlag turns a raw --lang value into a Resolution tagged with
// SourceCLIFlag, applying the same UC-09 coercion as Resolve so an
// unsupported code does not bypass the supported() filter just
// because the user passed it on the command line.
func CoerceCLIFlag(raw string) Resolution {
	return coerce(Resolution{
		Code:   normalize(raw),
		Name:   nameOf(normalize(raw)),
		Source: SourceCLIFlag,
	})
}
