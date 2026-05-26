package setup

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"

	"github.com/CommitBrief/commitbrief/internal/config"
)

type ProviderSpec struct {
	Name       string
	Label      string
	NeedsKey   bool
	NeedsURL   bool
	Models     []string
	APIKeyHelp string
}

// DefaultSpecs lists the providers shown in the wizard. The Models slice
// is the user-facing static list; for Ollama (NeedsURL=true) the models
// are discovered dynamically via OllamaModels.
var DefaultSpecs = []ProviderSpec{
	{
		Name:       "anthropic",
		Label:      "Anthropic (Claude)",
		NeedsKey:   true,
		Models:     []string{"claude-opus-4-7", "claude-sonnet-4-6", "claude-haiku-4-5-20251001"},
		APIKeyHelp: "Get an API key from https://console.anthropic.com/",
	},
	{
		Name:       "openai",
		Label:      "OpenAI (GPT)",
		NeedsKey:   true,
		Models:     []string{"gpt-4o", "gpt-4o-mini"},
		APIKeyHelp: "Get an API key from https://platform.openai.com/",
	},
	{
		Name:       "gemini",
		Label:      "Google Gemini",
		NeedsKey:   true,
		Models:     []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-1.5-flash"},
		APIKeyHelp: "Get an API key from https://aistudio.google.com/",
	},
	{
		Name:     "ollama",
		Label:    "Ollama (local, no API key needed)",
		NeedsURL: true,
	},
}

func FindSpec(name string) *ProviderSpec {
	for i := range DefaultSpecs {
		if DefaultSpecs[i].Name == name {
			return &DefaultSpecs[i]
		}
	}
	return nil
}

type Choices struct {
	Provider string
	APIKey   string
	Model    string
	BaseURL  string
	Lang     string
}

type RunOptions struct {
	Local      bool
	RepoRoot   string
	GlobalPath string

	// Specs overrides DefaultSpecs (test injection).
	Specs []ProviderSpec
}

// Apply produces a Config from collected choices, layered on top of the
// supplied base. Pass nil to start from config.Default (first-time setup).
//
// Critical: when base is the result of loading an existing config file,
// API keys and models for *other* providers are preserved intact — only
// the chosen provider's fields are touched. This is what makes
// `commitbrief setup` non-destructive across multiple providers.
func Apply(base *config.Config, choices Choices) *config.Config {
	var cfg *config.Config
	if base != nil {
		cfg = base
	} else {
		cfg = config.Default()
	}
	if choices.Provider != "" {
		cfg.Provider = choices.Provider
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]config.ProviderConfig{}
	}
	pc := cfg.Providers[choices.Provider]
	if choices.APIKey != "" {
		pc.APIKey = choices.APIKey
	}
	if choices.Model != "" {
		pc.Model = choices.Model
	}
	if choices.BaseURL != "" {
		pc.BaseURL = choices.BaseURL
	}
	if choices.Provider != "" {
		cfg.Providers[choices.Provider] = pc
	}
	if choices.Lang != "" {
		cfg.Output.Lang = choices.Lang
	}
	return cfg
}

// Run drives the interactive wizard via huh. The terminal must support a
// TTY; callers should branch on non-TTY environments before invoking.
// Returns the final config (already persisted to disk per opts).
//
// Run is non-destructive across providers: it loads the existing config
// file at the target path (global or repo, per opts.Local) before the
// wizard runs, then layers the user's choices on top. API keys for
// providers the user did *not* pick this round are preserved intact.
func Run(ctx context.Context, opts RunOptions) (*config.Config, error) {
	specs := opts.Specs
	if specs == nil {
		specs = DefaultSpecs
	}

	// Resolve the target write path up front so we can load any existing
	// config at that path before the wizard prompts. First-time runs see
	// a nil base and fall through to config.Default in Apply.
	targetPath, err := targetConfigPath(opts)
	if err != nil {
		return nil, err
	}
	base, err := config.LoadFile(targetPath)
	if err != nil {
		return nil, fmt.Errorf("setup: read existing config %s: %w", targetPath, err)
	}

	var choices Choices
	if err := selectProvider(ctx, specs, &choices); err != nil {
		return nil, err
	}
	spec := findSpecIn(specs, choices.Provider)
	if spec == nil {
		return nil, fmt.Errorf("setup: unknown provider %q", choices.Provider)
	}

	if spec.NeedsKey {
		if err := promptAPIKey(ctx, spec, &choices); err != nil {
			return nil, err
		}
	}
	if spec.NeedsURL {
		choices.BaseURL = OllamaDefaultBaseURL
		if err := promptBaseURL(ctx, &choices); err != nil {
			return nil, err
		}
	}
	if err := selectModel(ctx, spec, &choices); err != nil {
		return nil, err
	}

	cfg := Apply(base, choices)
	pc := cfg.Providers[choices.Provider]
	if err := TestConnection(ctx, choices.Provider, pc); err != nil {
		return cfg, fmt.Errorf("setup: connection test failed: %w", err)
	}

	if opts.Local {
		if _, err := WriteRepoConfig(opts.RepoRoot, cfg); err != nil {
			return cfg, err
		}
	} else {
		if err := WriteConfig(targetPath, cfg); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

// targetConfigPath resolves where Run will write — repo-local or
// user-level — based on opts.Local. Extracted so the existing-config
// load and the final write hit the same path.
func targetConfigPath(opts RunOptions) (string, error) {
	if opts.Local {
		if opts.RepoRoot == "" {
			return "", fmt.Errorf("setup: --local requires a repo root")
		}
		return RepoConfigPath(opts.RepoRoot), nil
	}
	if opts.GlobalPath != "" {
		return opts.GlobalPath, nil
	}
	return GlobalConfigPath()
}

func selectProvider(ctx context.Context, specs []ProviderSpec, choices *Choices) error {
	options := make([]huh.Option[string], 0, len(specs))
	for _, s := range specs {
		options = append(options, huh.NewOption(s.Label, s.Name))
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Which provider would you like to configure?").
			Options(options...).
			Value(&choices.Provider),
	))
	return form.RunWithContext(ctx)
}

func promptAPIKey(ctx context.Context, spec *ProviderSpec, choices *Choices) error {
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Enter your API key").
			Description(spec.APIKeyHelp).
			EchoMode(huh.EchoModePassword).
			Validate(notEmpty).
			Value(&choices.APIKey),
	))
	return form.RunWithContext(ctx)
}

func promptBaseURL(ctx context.Context, choices *Choices) error {
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Ollama base URL").
			Value(&choices.BaseURL),
	))
	return form.RunWithContext(ctx)
}

func selectModel(ctx context.Context, spec *ProviderSpec, choices *Choices) error {
	models := spec.Models
	if spec.NeedsURL {
		discovered, err := OllamaModels(ctx, choices.BaseURL)
		if err != nil || len(discovered) == 0 {
			// Fall back to free-text entry so the user is not blocked by a
			// transient Ollama outage or first-run empty model list.
			return huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title("Model name (could not discover from Ollama)").
					Validate(notEmpty).
					Value(&choices.Model),
			)).RunWithContext(ctx)
		}
		models = discovered
	}
	options := make([]huh.Option[string], 0, len(models))
	for _, m := range models {
		options = append(options, huh.NewOption(m, m))
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Pick a model").
			Options(options...).
			Value(&choices.Model),
	))
	return form.RunWithContext(ctx)
}

func notEmpty(s string) error {
	if s == "" {
		return fmt.Errorf("value cannot be empty")
	}
	return nil
}

func findSpecIn(specs []ProviderSpec, name string) *ProviderSpec {
	for i := range specs {
		if specs[i].Name == name {
			return &specs[i]
		}
	}
	return nil
}
