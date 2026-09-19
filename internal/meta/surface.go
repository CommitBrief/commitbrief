// SPDX-License-Identifier: GPL-3.0-or-later

package meta

import (
	"encoding/json"
	"sort"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

// SchemaVersion is the version of the surface.json contract. Bump it only
// for a breaking shape change; adding an optional field is not one. The
// consumers are this repo's own doc renderers, so the value exists to make a
// stale artifact detectable, not to support old readers.
const SchemaVersion = 1

// Surface is the whole code-derived inventory of the CLI.
//
// Field order here is the order the marshalled artifact reads in, chosen so a
// human skimming surface.json meets the sections in the order the
// documentation presents them. Every slice is non-nil after Build so the JSON
// carries `[]` rather than `null` for an empty section — a renderer should
// never have to distinguish the two.
type Surface struct {
	Schema      int                 `json:"schema"`
	Providers   []provider.Metadata `json:"providers"`
	Commands    []Command           `json:"commands"`
	Flags       []Flag              `json:"flags"`
	ConfigKeys  []ConfigKey         `json:"config_keys"`
	MCPToolArgs []MCPArg            `json:"mcp_tool_args"`

	// EnvVars is appended last rather than slotted next to ConfigKeys: it
	// was added in a later phase (ADR-0039 follow-up) purely to feed the
	// "env-vars" generated README block, and appending avoids reshuffling
	// every existing field's json output for consumers that index the
	// artifact positionally.
	EnvVars []EnvVar `json:"env_vars"`
}

// EnvVar is one environment variable ApplyEnv (internal/config/env.go)
// reads.
//
// This list exists because rendering "env-vars" straight from
// provider.Metadata's APIKeyEnv would silently under-report: that field
// only carries the SIX provider-credential variables, while ApplyEnv also
// reads OLLAMA_HOST, COMMITBRIEF_PROVIDER and COMMITBRIEF_MODEL. Every name
// below is either one of the config.*Env constants (so a rename in
// internal/config/env.go is caught by the Go compiler) or one of the two
// ApplyEnv still spells as a literal (COMMITBRIEF_PROVIDER,
// COMMITBRIEF_MODEL) — TestEnvVarsCoverApplyEnv parses ApplyEnv's own
// source and fails if it reads a variable that isn't listed here, so a
// future env var added to ApplyEnv cannot go silently undocumented the way
// DEEPSEEK_API_KEY / MISTRAL_API_KEY / COHERE_API_KEY did (README's old,
// hand-written table named only three of the six provider credentials).
//
// It deliberately does NOT cover every environment variable CommitBrief
// reads anywhere (COMMITBRIEF_CONFIG in internal/cli/context.go, NO_COLOR /
// COMMITBRIEF_NO_COLOR in internal/ui/color.go, or LANG's explicit
// non-effect per ADR-0021) — those aren't config OVERRIDES ApplyEnv
// applies, so a "linked config key" column has nothing to point them at.
// README documents them as ordinary hand-written prose immediately after
// the generated env-vars region instead.
type EnvVar struct {
	// Name is the exact variable name os.Getenv reads.
	Name string `json:"name"`

	// Effect is a one-line, hand-written description. env.go's source has
	// no room for prose (they are bare `const` declarations), so this
	// cannot be reflected the way ConfigKeys() reflects over the Config
	// struct — it is maintained here, next to the name that anchors it to
	// the constant that would break the build if it drifted.
	Effect string `json:"effect"`

	// ConfigKey is the dotted config.Config path this variable overrides,
	// using the same <name> placeholder ConfigKeys() uses for a free-form
	// provider entry (COMMITBRIEF_MODEL applies to whichever provider is
	// currently active, not a fixed one).
	ConfigKey string `json:"config_key,omitempty"`
}

// envVars is the fixed inventory behind the "env-vars" generated block. See
// EnvVar's doc comment for why this list is scoped to exactly what ApplyEnv
// reads, no more and no less.
func envVars() []EnvVar {
	return []EnvVar{
		{Name: config.AnthropicAPIKeyEnv, Effect: "Anthropic API credential — overrides `providers.anthropic.api_key` in config.", ConfigKey: "providers.anthropic.api_key"},
		{Name: config.OpenAIAPIKeyEnv, Effect: "OpenAI API credential — overrides `providers.openai.api_key` in config.", ConfigKey: "providers.openai.api_key"},
		{Name: config.GeminiAPIKeyEnv, Effect: "Google Gemini API credential — overrides `providers.gemini.api_key` in config.", ConfigKey: "providers.gemini.api_key"},
		{Name: config.DeepSeekAPIKeyEnv, Effect: "DeepSeek API credential — overrides `providers.deepseek.api_key` in config.", ConfigKey: "providers.deepseek.api_key"},
		{Name: config.MistralAPIKeyEnv, Effect: "Mistral API credential — overrides `providers.mistral.api_key` in config.", ConfigKey: "providers.mistral.api_key"},
		{Name: config.CohereAPIKeyEnv, Effect: "Cohere API credential — overrides `providers.cohere.api_key` in config.", ConfigKey: "providers.cohere.api_key"},
		// Unconditional, not "when unset": ApplyEnv (internal/config/env.go)
		// does `p.BaseURL = v` with no check of the existing value, and it
		// runs AFTER config is loaded (internal/cli/context.go). A stale
		// OLLAMA_HOST left in the shell silently wins over an explicit
		// providers.ollama.base_url in config.yml — TestApplyEnvOllamaHost
		// pins exactly this. Faz 07 review M4.
		{Name: config.OllamaHostEnv, Effect: "Ollama base URL — overrides `providers.ollama.base_url` in config UNCONDITIONALLY, even if you set base_url explicitly.", ConfigKey: "providers.ollama.base_url"},
		{Name: "COMMITBRIEF_PROVIDER", Effect: "Selects the active provider for this run — overrides `provider` in config.", ConfigKey: "provider"},
		{Name: "COMMITBRIEF_MODEL", Effect: "Selects the active provider's model for this run — overrides `providers.<name>.model` in config.", ConfigKey: "providers.<name>.model"},
	}
}

// MCPArg is one argument of the MCP `review` tool, lifted out of the tool's
// JSON Schema.
//
// It is flattened deliberately: the schema is the contract, but documentation
// wants a table, and a table wants one row per argument with the enum and the
// element type already resolved.
type MCPArg struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Items       string   `json:"items,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Build walks a fully constructed cobra tree and returns the inventory.
//
// root must be the root command; mcpSchema is the MCP review tool's input
// schema (internal/cli's reviewToolInputSchema). Both arrive as parameters so
// this package never imports internal/cli — see the package comment.
//
// Build does not mutate the tree. In particular it does not call cobra's
// InitDefaultHelpFlag / InitDefaultHelpCmd to normalize what cobra injects;
// an inventory that rewrites the thing it is inventorying is a trap. It
// filters cobra's own additions out instead.
//
// There is no error return. Every input is a program-internal structure, and
// the only thing that can go wrong — an unparseable MCP schema — is better
// reported as an empty section than as a failure that blocks generating the
// other four.
func Build(root *cobra.Command, mcpSchema json.RawMessage) Surface {
	cmds, flags := walkCommands(root)

	providers := provider.AllMetadata() // already sorted by name
	if providers == nil {
		providers = []provider.Metadata{}
	}

	return Surface{
		Schema:      SchemaVersion,
		Providers:   providers,
		Commands:    cmds,
		Flags:       flags,
		ConfigKeys:  ConfigKeys(),
		MCPToolArgs: mcpToolArgs(mcpSchema),
		EnvVars:     envVars(),
	}
}

// MarshalIndent renders the inventory as the committed artifact: two-space
// indent and a trailing newline, so the file is an ordinary POSIX text file
// that git diffs sanely and an editor does not "fix" on save.
func (s Surface) MarshalIndent() ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// mcpSchemaDoc is the subset of JSON Schema the review tool actually uses.
// Decoding into a narrow struct rather than map[string]any keeps the parse
// total: a property shape meta does not understand degrades to empty fields
// instead of a type assertion panic.
type mcpSchemaDoc struct {
	Properties map[string]struct {
		Type        string   `json:"type"`
		Description string   `json:"description"`
		Enum        []string `json:"enum"`
		Items       struct {
			Type string `json:"type"`
		} `json:"items"`
	} `json:"properties"`
	Required []string `json:"required"`
}

// mcpToolArgs flattens the tool schema into sorted rows.
//
// The sort is load-bearing, not cosmetic: the schema decodes into a Go map
// and map iteration order is randomized, so an unsorted result would make the
// committed artifact differ between two runs of the same binary.
func mcpToolArgs(raw json.RawMessage) []MCPArg {
	out := []MCPArg{}
	if len(raw) == 0 {
		return out
	}

	var doc mcpSchemaDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		// A malformed schema is a programming error in internal/cli, and the
		// test there guards it. Here it must not take the other four sections
		// down with it.
		return out
	}

	required := make(map[string]bool, len(doc.Required))
	for _, name := range doc.Required {
		required[name] = true
	}

	for name, prop := range doc.Properties {
		arg := MCPArg{
			Name:        name,
			Type:        prop.Type,
			Items:       prop.Items.Type,
			Required:    required[name],
			Description: prop.Description,
		}
		if len(prop.Enum) > 0 {
			// Copy: the decoded slice is owned by this function's doc value,
			// but the row outlives it and callers may retain it.
			arg.Enum = append([]string(nil), prop.Enum...)
		}
		out = append(out, arg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
