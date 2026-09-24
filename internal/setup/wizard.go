// SPDX-License-Identifier: GPL-3.0-or-later

package setup

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

// tr returns the catalog string for key, falling back to fallback
// when the catalog is nil or the key is missing/empty. Used to keep
// the wizard usable in test/library contexts without a catalog while
// still pulling localised text in normal CLI runs.
func tr(c *i18n.Catalog, key, fallback string) string {
	if c == nil {
		return fallback
	}
	v := c.T(key)
	if v == "" || v == key {
		return fallback
	}
	return v
}

type ProviderSpec struct {
	Name       string
	Label      string
	NeedsKey   bool
	NeedsURL   bool
	Models     []string
	APIKeyHelp string
}

// specUI is the wizard's own half of a provider spec: the presentation
// text and the shape of the prompts. None of it is a provider fact, so
// none of it belongs in the provider metadata registry — a label like
// "Ollama (local, no API key needed)" or a console URL is copy written
// for this screen.
//
// The slice order is the order the user sees, and it is deliberately not
// the registry's (which sorts alphabetically): the three providers most
// people configure lead, and the keyless local option closes the list.
var specUI = []ProviderSpec{
	{
		Name:       "anthropic",
		Label:      "Anthropic (Claude)",
		NeedsKey:   true,
		APIKeyHelp: "Get an API key from https://console.anthropic.com/",
	},
	{
		Name:       "openai",
		Label:      "OpenAI (GPT)",
		NeedsKey:   true,
		APIKeyHelp: "Get an API key from https://platform.openai.com/",
	},
	{
		Name:       "gemini",
		Label:      "Google Gemini",
		NeedsKey:   true,
		APIKeyHelp: "Get an API key from https://aistudio.google.com/",
	},
	{
		Name:       "deepseek",
		Label:      "DeepSeek",
		NeedsKey:   true,
		APIKeyHelp: "Get an API key from https://platform.deepseek.com/",
	},
	{
		Name:       "mistral",
		Label:      "Mistral",
		NeedsKey:   true,
		APIKeyHelp: "Get an API key from https://console.mistral.ai/",
	},
	{
		Name:       "cohere",
		Label:      "Cohere",
		NeedsKey:   true,
		APIKeyHelp: "Get an API key from https://dashboard.cohere.com/",
	},
	{
		Name:     "ollama",
		Label:    "Ollama (local, no API key needed)",
		NeedsURL: true,
	},
}

// Specs returns the providers the wizard offers, with each one's model list
// read from the provider metadata registry at call time.
//
// It is a function rather than a package-level var for two reasons. The
// registry is populated by init() in the provider packages that
// cmd/commitbrief/main.go blank-imports, so a var initialised at package
// load could observe it half-built; and building fresh means the wizard
// can never show a model list that has drifted from the provider's own
// supportedModels table, which is what the hand-copied literals this
// replaced kept doing (gemini's order had already diverged).
//
// Ollama is the one provider with no static list: it serves whatever the
// user has pulled, so selectModel asks the daemon through OllamaModels and
// never reads Models. Copying the registry's handful of suggestions in
// would present them as the supported set.
func Specs() []ProviderSpec {
	out := make([]ProviderSpec, 0, len(specUI))
	for _, spec := range specUI {
		if !spec.NeedsURL {
			if md, ok := provider.MetadataFor(spec.Name); ok {
				models := make([]string, 0, len(md.Models))
				for _, m := range md.Models {
					models = append(models, m.ID)
				}
				spec.Models = models
			}
		}
		out = append(out, spec)
	}
	return out
}

// FindSpec looks up one provider's wizard spec, or nil when the wizard
// does not offer that provider. The returned pointer addresses a fresh
// copy, so mutating it changes nothing for the next caller.
func FindSpec(name string) *ProviderSpec {
	specs := Specs()
	return findSpecIn(specs, name)
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

	// Specs overrides the Specs() default list (test injection).
	Specs []ProviderSpec

	// Catalog drives prompt titles, validation messages, and the
	// connection-test result lines into the active locale. When nil,
	// English defaults are used so package consumers without a catalog
	// in hand (tests, library users) still get a functional wizard.
	// The CLI layer always passes app.Catalog; see UC-16 in
	// PATCH_ROADMAP.
	Catalog *i18n.Catalog

	// IgnoreUnknownKeys mirrors `commitbrief --ignore-unknown-config`: when
	// set, loading the existing config at the target path tolerates a key
	// the schema doesn't define instead of failing the wizard outright.
	// setup is the tool's own repair path for a broken config file, so it
	// must never be the one command the escape hatch can't reach — the CLI
	// layer has already warned about the offending key via resolveContext
	// before Run is called, so this load stays silent about it.
	//
	// Unlike `config set` / `providers use` (internal/cli/config.go,
	// providers.go), Run does NOT refuse to write when the load reports an
	// ignored key (Wave 0 review turu 2, item 6). Those two commands refuse
	// because they decode the file into a typed config.Config and rewrite
	// the WHOLE thing, so an unknown key has nowhere to land and is
	// silently destroyed by the round-trip. Run has exactly the same
	// decode-and-rewrite shape, but it is the deliberate exception: it is
	// the tool's own from-scratch recovery path for a config broken enough
	// to need this flag, has no claim to preserving content it never
	// understood in the first place, and refusing it here would relock the
	// exact user the escape hatch exists to rescue.
	IgnoreUnknownKeys bool
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

// LoadRunConfig performs the pure, non-interactive first step of Run:
// resolving where the wizard will write (repo-local or user-level, per
// opts.Local) and loading any existing config already at that path,
// honoring opts.IgnoreUnknownKeys exactly as Run does. It runs entirely
// before any prompt is constructed and touches no terminal.
//
// It is split out of Run so callers — this package's own tests, and
// internal/cli's — can assert "did config validation let this proceed"
// without a controlling terminal. huh cannot be driven headlessly: it
// blocks in a raw console read that observes neither context
// cancellation nor a redirected stdin, and on a runner that still has a
// console attached (Windows CI) it never fails fast the way it does on a
// TTY-less Linux/macOS runner — so a test that only reaches this point
// through the real prompt is racing a 10-minute per-package timeout on
// one platform and not the others. Exercising this function directly
// instead makes the assertion deterministic on every platform.
func LoadRunConfig(opts RunOptions) (targetPath string, base *config.Config, err error) {
	targetPath, err = targetConfigPath(opts)
	if err != nil {
		return "", nil, err
	}
	// The returned findings are intentionally discarded (not gated on, unlike
	// config set / providers use): setup rewrites the file from scratch by
	// design and is itself the recovery route for a config broken enough to
	// need IgnoreUnknownKeys, so refusing to proceed here would relock the
	// exact user this flag exists to rescue. See the doc comment on
	// RunOptions.IgnoreUnknownKeys.
	base, _, err = config.LoadFileWith(targetPath, config.LoadOptions{IgnoreUnknownKeys: opts.IgnoreUnknownKeys})
	if err != nil {
		return "", nil, fmt.Errorf("setup: read existing config %s: %w", targetPath, err)
	}
	return targetPath, base, nil
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
		specs = Specs()
	}

	// Resolve the target write path up front so we can load any existing
	// config at that path before the wizard prompts. First-time runs see
	// a nil base and fall through to config.Default in Apply.
	targetPath, base, err := LoadRunConfig(opts)
	if err != nil {
		return nil, err
	}

	var choices Choices
	if err := selectProvider(ctx, specs, &choices, opts.Catalog); err != nil {
		return nil, err
	}
	spec := findSpecIn(specs, choices.Provider)
	if spec == nil {
		return nil, fmt.Errorf("setup: unknown provider %q", choices.Provider)
	}

	if spec.NeedsKey {
		// If the target config already holds a key for the chosen provider,
		// let the user leave the prompt blank to keep it — switching the
		// active provider/model shouldn't force a key re-entry. Apply()
		// preserves the existing key on empty input.
		hasExistingKey := base != nil && base.Providers[choices.Provider].APIKey != ""
		if err := promptAPIKey(ctx, spec, &choices, opts.Catalog, hasExistingKey); err != nil {
			return nil, err
		}
	}
	if spec.NeedsURL {
		choices.BaseURL = OllamaDefaultBaseURL
		if err := promptBaseURL(ctx, &choices, opts.Catalog); err != nil {
			return nil, err
		}
	}
	if err := selectModel(ctx, spec, &choices, opts.Catalog); err != nil {
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

func selectProvider(ctx context.Context, specs []ProviderSpec, choices *Choices, cat *i18n.Catalog) error {
	options := make([]huh.Option[string], 0, len(specs))
	for _, s := range specs {
		options = append(options, huh.NewOption(s.Label, s.Name))
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(tr(cat, "setup.provider.prompt", "Which provider would you like to configure?")).
			Description(tr(cat, "setup.provider.help", "Pick the LLM provider you have an API key for.")).
			Options(options...).
			Value(&choices.Provider),
	))
	return form.RunWithContext(ctx)
}

func promptAPIKey(ctx context.Context, spec *ProviderSpec, choices *Choices, cat *i18n.Catalog, hasExistingKey bool) error {
	input := huh.NewInput().
		EchoMode(huh.EchoModePassword).
		Value(&choices.APIKey)
	if hasExistingKey {
		// A key already exists: allow an empty submission (Apply keeps the
		// stored key) so the user can change only the provider/model.
		input = input.
			Title(tr(cat, "setup.api_key.prompt_keep", "Enter a new API key (leave blank to keep the existing one):")).
			Description(tr(cat, "setup.api_key.help_keep", "A key is already configured for this provider. Leave blank to keep it, or enter a new one to replace it."))
	} else {
		input = input.
			Title(tr(cat, "setup.api_key.prompt", "Enter your API key:")).
			Description(spec.APIKeyHelp).
			Validate(notEmptyFor(cat))
	}
	form := huh.NewForm(huh.NewGroup(input))
	return form.RunWithContext(ctx)
}

func promptBaseURL(ctx context.Context, choices *Choices, cat *i18n.Catalog) error {
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title(tr(cat, "setup.base_url.prompt", "Ollama base URL:")).
			Value(&choices.BaseURL),
	))
	return form.RunWithContext(ctx)
}

func selectModel(ctx context.Context, spec *ProviderSpec, choices *Choices, cat *i18n.Catalog) error {
	models := spec.Models
	if spec.NeedsURL {
		discovered, err := OllamaModels(ctx, choices.BaseURL)
		if err != nil || len(discovered) == 0 {
			// Fall back to free-text entry so the user is not blocked by a
			// transient Ollama outage or first-run empty model list.
			return huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title(tr(cat, "setup.model.discover_failed", "Model name (could not discover from Ollama):")).
					Validate(notEmptyFor(cat)).
					Value(&choices.Model),
			)).RunWithContext(ctx)
		}
		models = discovered
	}
	form := huh.NewForm(huh.NewGroup(newModelSelect(spec, models, choices, cat)))
	return form.RunWithContext(ctx)
}

// newModelSelect builds the model picker. It is split out of selectModel so
// tests can inspect the options and the preselected value without driving a
// terminal (huh cannot run headlessly; see LoadRunConfig).
//
// For a static model list (every NeedsKey provider) an empty choices.Model is
// first set to the provider's DefaultModel, so pressing Enter accepts the
// default rather than whichever model the catalog happens to list first
// (ADR-0043). Discovered lists (ollama) keep huh's first-entry behaviour:
// there is no default we could name for whatever the user has pulled.
func newModelSelect(spec *ProviderSpec, models []string, choices *Choices, cat *i18n.Catalog) *huh.Select[string] {
	var md provider.Metadata
	var haveMD bool
	if !spec.NeedsURL {
		md, haveMD = provider.MetadataFor(spec.Name)
	}
	if haveMD && choices.Model == "" && slices.Contains(models, md.DefaultModel) {
		choices.Model = md.DefaultModel
	}
	options := make([]huh.Option[string], 0, len(models))
	for _, m := range models {
		options = append(options, huh.NewOption(modelLabel(m, md, haveMD, cat), m))
	}
	return huh.NewSelect[string]().
		Title(tr(cat, "setup.model.prompt", "Pick a model:")).
		Options(options...).
		Value(&choices.Model)
}

// modelLabel renders one picker entry. Only the label carries price and
// signal; the option value stays the bare model ID so the config the wizard
// writes is unchanged.
//
// A cataloged model shows its list price per 1M input/output tokens from the
// provider metadata and "not measured": no benchmark results ship with the
// binary yet, so the wizard makes no quality claim it cannot back. A model
// with no price is marked "local" instead of showing a misleading $0. The
// only such model today is a discovered ollama one: static lists come from
// md.Models, so every static entry has a catalog row. The boundary is
// deliberate — a zero-priced API model (e.g. a free preview) added to the
// catalog would be mislabeled "local", which TestModelLabelsShowPriceAndSignal
// catches by requiring a "$" on every static label; give such a model its
// own label then instead of relaxing the test.
func modelLabel(model string, md provider.Metadata, haveMD bool, cat *i18n.Catalog) string {
	const sep = " · "
	var pricing provider.Pricing
	if haveMD {
		for _, mi := range md.Models {
			if mi.ID == model {
				pricing = mi.Pricing
				break
			}
		}
	}
	var b strings.Builder
	b.WriteString(model)
	if pricing.InputPer1M == 0 && pricing.OutputPer1M == 0 {
		b.WriteString(sep + tr(cat, "setup.model.local", "local"))
	} else {
		b.WriteString(sep + "$" + formatPrice(pricing.InputPer1M) + "/1M " + tr(cat, "setup.model.price_in", "in"))
		b.WriteString(sep + "$" + formatPrice(pricing.OutputPer1M) + "/1M " + tr(cat, "setup.model.price_out", "out"))
		b.WriteString(sep + tr(cat, "setup.model.not_measured", "not measured"))
	}
	if haveMD && model == md.DefaultModel {
		b.WriteString(sep + tr(cat, "setup.model.default", "default"))
	}
	return b.String()
}

// formatPrice prints a USD-per-1M figure with no trailing zeros ("3",
// "0.15", "1.25") so the label stays as short as the catalog's numbers.
func formatPrice(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// notEmptyFor builds a validator closure that emits a localised error
// when the user submits an empty value. We need a closure because
// huh.Validate accepts `func(string) error`, not a method-style
// callback.
func notEmptyFor(cat *i18n.Catalog) func(string) error {
	return func(s string) error {
		if s == "" {
			return fmt.Errorf("%s", tr(cat, "setup.api_key.empty", "value cannot be empty"))
		}
		return nil
	}
}

func findSpecIn(specs []ProviderSpec, name string) *ProviderSpec {
	for i := range specs {
		if specs[i].Name == name {
			return &specs[i]
		}
	}
	return nil
}
