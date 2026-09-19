// SPDX-License-Identifier: GPL-3.0-or-later

package meta

import (
	"encoding/json"
	"sort"

	"github.com/spf13/cobra"

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
