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

func Resolve(repo, global *config.Config, env Env) Resolution {
	if repo != nil && repo.Output.Lang != "" {
		return fromConfig(repo.Output.Lang, SourceRepoConfig)
	}
	if global != nil && global.Output.Lang != "" {
		return fromConfig(global.Output.Lang, SourceGlobalConfig)
	}
	if code := parseLocale(env.LANG); code != "" {
		return Resolution{Code: code, Name: nameOf(code), Source: SourceEnvLANG}
	}
	return Resolution{Code: "en", Name: "English", Source: SourceDefault}
}

func fromConfig(raw string, src Source) Resolution {
	code := normalize(raw)
	return Resolution{Code: code, Name: nameOf(code), Source: src}
}
