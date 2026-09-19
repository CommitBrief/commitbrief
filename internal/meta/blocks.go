// SPDX-License-Identifier: GPL-3.0-or-later

package meta

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

// Renderer turns the whole inventory into the markdown that belongs inside
// one named region of README.md. It never errors: every input is data this
// package already validated (Build/ConfigKeys/mcpToolArgs), so a renderer
// degrading gracefully on an empty section (e.g. no providers) is enough —
// there is nothing a renderer could discover that would make "fail the doc
// build" the right response. Docs.Apply is what reports "unknown renderer
// name" for a region whose name isn't a key of this map; the map itself is
// the single place that answers "which region names exist".
type Renderer func(Surface) string

// renderers is the whole registry, keyed by the region name that appears in
// README.md's `<!-- commitbrief:gen NAME -->` markers. docs.go never
// hardcodes a region name — it only ever asks this map.
var renderers = map[string]Renderer{
	"providers":     renderProviders,
	"mcp-tool-args": renderMCPToolArgs,
	"env-vars":      renderEnvVars,
	"config-schema": renderConfigSchema,
}

// ---------------------------------------------------------------------
// markdown table-cell escaping
//
// Review turn 1 (M1/M2) found the mcp-tool-args table genuinely broken as
// markdown, verified against cmark-gfm (the same table extension GitHub
// renders with):
//
//   - A bare '|' inside a table cell splits the row into extra cells EVEN
//     INSIDE A BACKTICK CODE SPAN — GFM's row splitter cuts on raw '|'
//     before code spans are recognized. `critical|high|...` rendered as
//     one giant row whose Description column showed only "critical" and
//     silently dropped everything after the first pipe, including the
//     rest of the enum and the trailing sentence. A backslash-escaped
//     '\|' DOES survive, even inside a code span — verified the same way.
//   - '<word>' outside a code span (array<string>, <dir>) is a syntactically
//     valid raw HTML open tag; GitHub's sanitizer drops unrecognized tags,
//     so "array<string>" rendered as bare "array" and "<dir>" vanished
//     entirely. Wrapping it in a code span (backticks) is what stops it
//     being parsed as HTML at all — verified with cmark-gfm.
//   - A bare '*' outside a code span opens/closes emphasis; the glob
//     examples ("*.go", "internal/**/*.ts") turned part of that sentence
//     italic and ate the literal asterisks a reader needs to see.
//
// escapeCell is the general fix for free-form text entering ANY cell:
// collapse embedded newlines (a GFM table row is one line — an unescaped
// '\n' in the source data would split it into a second, malformed <tr>,
// verified against cmark-gfm), escape '|' unconditionally (cell-splitting
// doesn't care about code spans, verified below), and, OUTSIDE existing
// backtick code spans only, double backslashes and escape '<' and '*' so
// intentional inline code ("`git diff`") is left byte-for-byte alone.
//
// Every value that lands in a table cell must go through this — Review
// turn 2 (N4) flagged that Name/Type/enum values were being hand-wrapped in
// backticks without it, so a future '|' in an enum (or a Name/Type with one)
// would silently bring M1 straight back with no test noticing.
//
// Two things that are NOT bugs, verified against cmark-gfm rather than
// assumed:
//   - Escaping only '<' (not also '>') is enough to stop "<dir>" being
//     read as raw HTML; a lone '>' has no markdown meaning mid-sentence.
//   - A lone '[' needs no escaping here: with no matching ']' to pair with,
//     CommonMark's own "unmatched bracket falls back to literal text" rule
//     already makes it render as-is.
func escapeCell(s string) string {
	s = newlineInCell.ReplaceAllString(s, " ")
	return splitOutsideCode(s, escapeOutsideCode, escapeInsideCode)
}

// newlineInCell matches any line ending so escapeCell can collapse it to a
// single space — a GFM table cell is defined line-by-line, so an embedded
// '\n' (a description that happens to contain one) does not wrap text
// inside the cell, it starts a brand new, malformed table row.
var newlineInCell = regexp.MustCompile(`\r\n|\r|\n`)

// escapeOutsideCode is applied to every stretch of a cell's text that lies
// OUTSIDE a backtick code span. '|' is escaped here too, not just inside
// code spans (see escapeInsideCode) — GFM's row-splitter cuts on a raw '|'
// wherever it sits, code span or not.
//
// Backslash is doubled here, and ONLY here — never in escapeInsideCode, and
// never as a separate global pass before this one runs (an earlier version
// escaped '|' globally FIRST and then doubled backslashes in a second,
// separate outside-only pass; that second pass could not tell an
// escaping-inserted '\' from one already in the source, so it doubled its
// own '\|' into '\\|' and broke right back to a raw, row-splitting '|').
// Because splitOutsideCode calls this func exactly once per outside
// stretch of the ORIGINAL text, backslash-doubling and pipe-escaping both
// see the real source text and nothing else.
//
// CommonMark backslash-escapes do not apply inside a code span (its
// content is verbatim), so doubling unconditionally — the bug this
// replaces — turned a code span quoting the Windows path C:\Users\x into a
// literal C:\\Users\\x, verified against cmark-gfm. Run outside a span,
// doubling is exactly the standard defense against a stray '\' before an
// ASCII punctuation character being swallowed as an unintended escape (a
// regex fragment like \. would otherwise lose its backslash entirely,
// silently changing what the regex means).
func escapeOutsideCode(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "<", `\<`)
	s = strings.ReplaceAll(s, "*", `\*`)
	return s
}

// escapeInsideCode is applied to a code span's CONTENT (the text strictly
// between its delimiter backticks). Nothing but '|' is touched — code span
// content is otherwise verbatim per CommonMark, which is the entire reason
// a description quotes something in backticks to begin with.
func escapeInsideCode(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// splitOutsideCode walks s looking for CommonMark code spans and applies
// outside/inside to whichever kind of text it is currently looking at.
//
// A code span is delimited by a run of N backticks and closed by the NEXT
// run of EXACTLY N backticks (CommonMark §6.1) — a shorter or longer run in
// between is just more content. The naive single-backtick-only regex this
// replaced (backtick, any non-backtick run, backtick) mishandled a
// double-backtick span such as one quoting "a * b < c": verified against
// cmark-gfm that the naive regex closed the span one backtick early,
// leaking a stray backtick into the rendered code and leaving the rest as
// ordinary (now wrongly un-escaped) text. If an opening run never finds a
// matching closer, CommonMark treats those backticks as literal text
// rather than as a span — handled below by falling back to outside() and
// resuming the scan right after them, not by swallowing the rest of s.
func splitOutsideCode(s string, outside, inside func(string) string) string {
	var b strings.Builder
	i, n := 0, len(s)
	for i < n {
		start := strings.IndexByte(s[i:], '`')
		if start < 0 {
			b.WriteString(outside(s[i:]))
			break
		}
		start += i
		b.WriteString(outside(s[i:start]))

		openLen := 0
		for start+openLen < n && s[start+openLen] == '`' {
			openLen++
		}

		closeStart := -1
		for j := start + openLen; j < n; {
			if s[j] != '`' {
				j++
				continue
			}
			runStart := j
			runLen := 0
			for j < n && s[j] == '`' {
				runLen++
				j++
			}
			if runLen == openLen {
				closeStart = runStart
				break
			}
			// A run of the wrong length is just more span content;
			// keep scanning for one that matches openLen exactly.
		}

		if closeStart < 0 {
			// No matching closer anywhere in the rest of s: per
			// CommonMark this opening run was never a code span to
			// begin with. Treat exactly those backticks as literal
			// text and resume the normal scan right after them, so
			// everything that would have been "inside" is correctly
			// re-examined as ordinary text (and its own '<'/'*'/etc.
			// escaped) rather than silently skipped.
			b.WriteString(outside(s[start : start+openLen]))
			i = start + openLen
			continue
		}

		spanEnd := closeStart + openLen
		b.WriteString(s[start : start+openLen])              // opening backticks, verbatim
		b.WriteString(inside(s[start+openLen : closeStart])) // span content
		b.WriteString(s[closeStart:spanEnd])                 // closing backticks, verbatim
		i = spanEnd
	}
	return b.String()
}

// ---------------------------------------------------------------------
// providers
// ---------------------------------------------------------------------

// renderProviders renders the provider/pricing table, one row PER MODEL.
//
// A one-row-per-provider table with a single Context/$-per-1M pair (the
// default model's) was tried first and rejected: every multi-model provider
// has models with different context windows and rates, so a reader looking
// up e.g. gpt-4o's context window from that table got gpt-5.4-mini's number
// instead — silently wrong. A generated "authoritative" table that
// misattributes a number is worse than the prose it replaced, which never
// claimed a single figure for five different models. One row per model
// means every number in the table belongs to the model named on that row,
// full stop.
//
// The lead sentence is generated FROM the same slice the table counts
// providers from, not hand-typed: that is the whole point of this block
// (ADR-0039) — before it, README claimed "four API providers + two
// CLI-tool-backed" while the table beneath it already had ten provider
// rows, and nothing caught the mismatch because nothing derived one from
// the other.
func renderProviders(s Surface) string {
	var apiN, localN, cliN int
	for _, p := range s.Providers {
		switch p.Kind {
		case provider.KindAPI:
			apiN++
		case provider.KindLocal:
			localN++
		case provider.KindCLI:
			cliN++
		}
	}

	var parts []string
	if apiN > 0 {
		parts = append(parts, strconv.Itoa(apiN)+" API")
	}
	if localN > 0 {
		parts = append(parts, strconv.Itoa(localN)+" local")
	}
	if cliN > 0 {
		parts = append(parts, strconv.Itoa(cliN)+" CLI-tool-backed")
	}
	// Every provider.Kind branch above is enumerated by hand, so a FOURTH
	// kind added later would fall through the switch untouched — the total
	// below still counts it, but it would silently vanish from this
	// breakdown (Faz 07 review N6): "11 providers ship in the box: 6 API,
	// 1 local, 3 CLI-tool-backed" with the sum one short of the total and
	// no visible sign why. Closing the gap with an explicit bucket beats
	// discovering the mismatch by doing the arithmetic yourself.
	if other := len(s.Providers) - (apiN + localN + cliN); other > 0 {
		parts = append(parts, strconv.Itoa(other)+" other")
	}

	noun, verb := "providers", "ship"
	if len(s.Providers) == 1 {
		noun, verb = "provider", "ships"
	}

	// Built as one continuous sentence, THEN word-wrapped: the numbers and
	// kind list are dynamic, so hand-placed '\n's (the previous version)
	// wrap consistently only for whatever counts happened to exist at the
	// time — reviewed as inconsistent wrapping in Faz 07 review N6 (a
	// 115-char first line against ~66-char continuation lines).
	intro := fmt.Sprintf(
		"%d %s %s in the box: %s. Context and price are per model, not per "+
			// Deliberately vague about WHERE the notes live (not "below
			// the table"): they're hand-written, right outside this
			// generated region, and a future edit could move them
			// without this sentence knowing — asserting a specific
			// position here would then silently start lying (M8).
			"provider. See the accompanying notes for what it can't say "+
			"(preview status, latency, auth, invocation).",
		len(s.Providers), noun, verb, strings.Join(parts, ", "),
	)

	var b strings.Builder
	b.WriteString(wordWrap(intro, 76))
	b.WriteString("\n\n")

	b.WriteString("| Provider | Kind | Model | Default | Context | $/1M in / out / cached |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, p := range s.Providers {
		if len(p.Models) == 0 {
			// CLI-backed providers register neither DefaultModel nor
			// Models on purpose — the host CLI, not CommitBrief, owns
			// model selection for those. One row, dashes throughout.
			writeProviderModelRow(&b, p, "—", "—", "—", "—")
			continue
		}
		for _, m := range p.Models {
			def := "—"
			if m.Default {
				def = "✓"
			}
			writeProviderModelRow(&b, p, "`"+m.ID+"`", def, formatThousands(m.ContextWindow), formatPricing(m.Pricing))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeProviderModelRow(b *strings.Builder, p provider.Metadata, model, def, context, price string) {
	b.WriteString("| `")
	b.WriteString(p.Name)
	b.WriteString("` | ")
	b.WriteString(kindLabel(p.Kind))
	b.WriteString(" | ")
	b.WriteString(model)
	b.WriteString(" | ")
	b.WriteString(def)
	b.WriteString(" | ")
	b.WriteString(context)
	b.WriteString(" | ")
	b.WriteString(price)
	b.WriteString(" |\n")
}

func kindLabel(k provider.Kind) string {
	switch k {
	case provider.KindAPI:
		return "API"
	case provider.KindLocal:
		return "Local"
	case provider.KindCLI:
		return "CLI-backed"
	default:
		return string(k)
	}
}

// formatPricing renders one model's $/1M rates as "in / out / cached".
// Zero input+output together mean genuinely free (ollama, and any future
// local provider) rather than "$0" — Pricing's own doc comment says a zero
// Pricing means free/not metered.
//
// A zero CACHED rate is a DIFFERENT thing and must not render the same "—"
// as "free": Pricing.CachedInputPer1M's doc comment says zero means "same
// as InputPer1M", and Cost() (internal/provider/pricing.go) really does
// charge the input rate for cached tokens in that case —
//
//	cachedRate := p.CachedInputPer1M
//	if cachedRate == 0 { cachedRate = p.InputPer1M }
//
// Rendering "—" there (Faz 07 review N1) made gpt-5.5-pro's row read
// "$30 / $180 / —", which a reader prices as "no cost for cached input" —
// backwards, given cached input there actually costs the full $30/1M.
// "—" is reserved for where pricing genuinely does not apply at all (the
// CLI-backed rows, which never call this function); a real per-model rate
// that happens to equal the input rate is shown as "= $<rate>" instead, so
// the number a cache-heavy run actually pays is always on the page.
func formatPricing(p provider.Pricing) string {
	if p.InputPer1M == 0 && p.OutputPer1M == 0 {
		return "free"
	}
	cached := formatPrice(p.CachedInputPer1M)
	if p.CachedInputPer1M == 0 {
		cached = "= " + formatPrice(p.InputPer1M)
	}
	return formatPrice(p.InputPer1M) + " / " + formatPrice(p.OutputPer1M) + " / " + cached
}

func formatPrice(v float64) string {
	return "$" + strconv.FormatFloat(v, 'f', -1, 64)
}

// formatThousands renders a token count with thousands separators
// (1000000 -> "1,000,000") instead of a lossy "1M"/"128K" abbreviation — a
// generated reference table should say exactly what the code says, not an
// editorialized rounding of it.
func formatThousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// wordWrap greedily wraps s to width, breaking only on spaces (s is
// expected to already be one logical sentence/paragraph with no embedded
// newlines). Used for prose whose length depends on generated data — a
// provider count, say — so hand-placed line breaks can't wrap consistently
// for every possible value (Faz 07 review N6).
func wordWrap(s string, width int) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}

	var b strings.Builder
	lineLen := len(words[0])
	b.WriteString(words[0])
	for _, w := range words[1:] {
		if lineLen+1+len(w) > width {
			b.WriteByte('\n')
			lineLen = 0
		} else {
			b.WriteByte(' ')
			lineLen++
		}
		b.WriteString(w)
		lineLen += len(w)
	}
	return b.String()
}

// ---------------------------------------------------------------------
// mcp-tool-args
// ---------------------------------------------------------------------

// renderMCPToolArgs renders the MCP `review` tool's argument table.
//
// s.MCPToolArgs is already sorted by name (mcpToolArgs in surface.go) for
// the same reason this whole package exists: a Go map's iteration order is
// randomized, and a doc block that shuffled between builds would fail its
// own drift check nondeterministically.
func renderMCPToolArgs(s Surface) string {
	var b strings.Builder
	b.WriteString("| Argument | Type | Required | Description |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, a := range s.MCPToolArgs {
		typ := a.Type
		if a.Items != "" {
			typ += "<" + a.Items + ">"
		}

		required := "—"
		if a.Required {
			required = "yes"
		}

		desc := escapeCell(a.Description)
		if len(a.Enum) > 0 {
			// Comma-joined, each value its own code span — NOT joined with a
			// bare '|' (that used to truncate the whole row; see the
			// escapeCell doc comment above) and not one shared code span
			// either (a '|' inside a single span still splits the row).
			//
			// Each value goes through escapeInsideCode before being
			// wrapped, same as Name and Type below: these three are hand-
			// wrapped in backticks BY THIS FUNCTION, so from the rendered
			// document's point of view their content IS code-span content
			// — escapeCell's full outside-style escaping (backslash
			// doubling, '<', '*') would be wrong here for the same reason
			// it is wrong inside any other code span, and was verified to
			// visibly leak a backslash into `array<string>` during this
			// fix. Only '|' can still break the table structurally despite
			// the backticks, so only '|' needs handling. Faz 07 review N4.
			enum := make([]string, len(a.Enum))
			for i, v := range a.Enum {
				enum[i] = "`" + escapeInsideCode(v) + "`"
			}
			desc = "Allowed: " + strings.Join(enum, ", ") + " — " + desc
		}

		b.WriteString("| `")
		b.WriteString(escapeInsideCode(a.Name))
		b.WriteString("` | `")
		b.WriteString(escapeInsideCode(typ))
		b.WriteString("` | ")
		b.WriteString(required)
		b.WriteString(" | ")
		b.WriteString(desc)
		b.WriteString(" |\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------------------------------------------------------------------
// env-vars
// ---------------------------------------------------------------------

// renderEnvVars renders the table of environment variables ApplyEnv
// (internal/config/env.go) consults. It deliberately covers ONLY those —
// see EnvVar's doc comment on why COMMITBRIEF_CONFIG / NO_COLOR /
// COMMITBRIEF_NO_COLOR / LANG stay as hand-written prose right after this
// region in README.md instead of being folded in here.
//
// The lead sentence states the precedence explicitly: ApplyEnv runs AFTER
// config is loaded and merged (internal/cli/context.go calls
// config.ApplyEnv(cfg) once cfg already reflects user+repo config), and
// every one of these vars overwrites the field unconditionally — there is
// no "only if unset" check anywhere in ApplyEnv. Review turn 1 (M4) found
// exactly that false claim on the OLLAMA_HOST row ("used when
// providers.ollama.base_url is unset") contradicting ApplyEnv's own
// unconditional `p.BaseURL = v`, and found the "env beats config" sentence
// missing entirely from the six credential rows. Both are fixed at the
// source here (envVars() in surface.go), not patched in the README.
func renderEnvVars(s Surface) string {
	var b strings.Builder
	b.WriteString("These are read AFTER config is loaded and merged, so each one below\n")
	b.WriteString("always overrides its config value for this run — including one left\n")
	b.WriteString("over in your shell from a previous session.\n\n")
	b.WriteString("| Variable | Effect | Config key |\n")
	b.WriteString("|---|---|---|\n")
	for _, v := range s.EnvVars {
		key := "—"
		if v.ConfigKey != "" {
			key = "`" + v.ConfigKey + "`"
		}
		b.WriteString("| `")
		b.WriteString(v.Name)
		b.WriteString("` | ")
		b.WriteString(escapeCell(v.Effect))
		b.WriteString(" | ")
		b.WriteString(key)
		b.WriteString(" |\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------------------------------------------------------------------
// config-schema
// ---------------------------------------------------------------------

// renderConfigSchema renders the full .commitbrief.yml schema as an
// annotated YAML block, reconstructed from the flat, alphabetically-sorted
// ConfigKeys() list.
func renderConfigSchema(s Surface) string {
	root := buildSchemaTree(s.ConfigKeys)
	lines := renderSchemaChildren(sortedChildren(root), 0)

	width := 0
	for _, l := range lines {
		if l.Comment != "" && len(l.Text) > width {
			width = len(l.Text)
		}
	}

	var b strings.Builder
	b.WriteString("```yaml\n")
	b.WriteString("# Schema reference generated from code: every settable key, its type,\n")
	b.WriteString("# and its built-in default (when it has one). <name> and <model> are\n")
	b.WriteString("# placeholders you choose — see \"Providers and pricing\" above for the\n")
	b.WriteString("# real provider names.\n")
	for _, l := range lines {
		b.WriteString(l.Text)
		if l.Comment != "" {
			b.WriteString(strings.Repeat(" ", width-len(l.Text)+2))
			b.WriteString("# ")
			b.WriteString(l.Comment)
		}
		b.WriteString("\n")
	}
	b.WriteString("```")
	return b.String()
}

// yamlLine is one rendered line of the schema, split into the YAML text and
// its trailing comment so renderConfigSchema can align every comment into a
// common column instead of the ragged-right look a naive line-by-line
// render would produce.
type yamlLine struct {
	Text    string
	Comment string
}

// schemaNode is one segment of a ConfigKey.Path, reassembled into a tree so
// the flat, alphabetically-sorted list ConfigKeys() returns can be rendered
// as nested YAML instead of dotted paths.
type schemaNode struct {
	name     string
	isList   bool
	row      *ConfigKey
	children map[string]*schemaNode
}

// buildSchemaTree reassembles the dotted ConfigKey paths into a tree.
//
// A path segment ending in "[]" (walkConfigNode's marker for a slice-of-
// struct element, e.g. "guard.secret_patterns[].regex") does not become its
// own tree node — it marks the node for its own name (without the suffix)
// as a list, so guard.secret_patterns (the row with type []object) and
// guard.secret_patterns[].name/.regex (the rows with no row of their own at
// the "[]" level) merge into ONE node that renders as a YAML sequence of
// one example item, instead of two disconnected entries.
func buildSchemaTree(keys []ConfigKey) *schemaNode {
	root := &schemaNode{children: map[string]*schemaNode{}}
	for i := range keys {
		k := &keys[i]
		cur := root
		for _, seg := range strings.Split(k.Path, ".") {
			name := strings.TrimSuffix(seg, "[]")
			isList := strings.HasSuffix(seg, "[]")
			child, ok := cur.children[name]
			if !ok {
				child = &schemaNode{name: name, children: map[string]*schemaNode{}}
				cur.children[name] = child
			}
			if isList {
				child.isList = true
			}
			cur = child
		}
		cur.row = k
	}
	return root
}

func sortedChildren(n *schemaNode) []*schemaNode {
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*schemaNode, len(names))
	for i, name := range names {
		out[i] = n.children[name]
	}
	return out
}

// renderSchemaChildren renders a sibling group at the given depth (2 spaces
// per level), in one flat, ordered slice of lines.
func renderSchemaChildren(children []*schemaNode, depth int) []yamlLine {
	var lines []yamlLine
	for _, c := range children {
		lines = append(lines, renderSchemaNode(c, depth)...)
	}
	return lines
}

// renderSchemaNode renders n and everything below it.
//
// A leaf (no children) is one "key: value" line. A list node (isList) wraps
// its children's lines as a single YAML sequence item — the "- " marker
// replaces the first line's own indent rather than adding to it, and every
// following line gets two extra spaces, which is what keeps a
// multi-field list item's fields aligned under each other instead of under
// the dash. Anything else is a plain nested mapping: a "key:" header line
// followed by its children one level deeper.
func renderSchemaNode(n *schemaNode, depth int) []yamlLine {
	indent := strings.Repeat("  ", depth)

	if len(n.children) == 0 {
		return []yamlLine{{
			Text:    indent + n.name + ": " + leafValue(n.row),
			Comment: leafComment(n.row),
		}}
	}

	children := sortedChildren(n)

	if n.isList {
		itemDepth := depth + 1
		items := renderSchemaChildren(children, itemDepth)
		if len(items) > 0 {
			plain := strings.Repeat("  ", itemDepth)
			items[0].Text = plain + "- " + strings.TrimPrefix(items[0].Text, plain)
			for i := 1; i < len(items); i++ {
				items[i].Text = "  " + items[i].Text
			}
		}
		header := yamlLine{Text: indent + n.name + ":"}
		if n.row != nil {
			header.Comment = n.row.Type
		}
		return append([]yamlLine{header}, items...)
	}

	header := yamlLine{Text: indent + n.name + ":"}
	if n.row != nil && strings.HasPrefix(n.row.Type, "map[") {
		header.Comment = n.row.Type + " — key is user-chosen"
	}
	return append([]yamlLine{header}, renderSchemaChildren(children, depth+1)...)
}

// leafValue renders the scalar shown for a leaf key: its actual default
// when it has one (config.Default()'s value, or an effectiveDefaults
// override — see configkeys.go), otherwise a type-shaped placeholder.
func leafValue(row *ConfigKey) string {
	if row == nil {
		return "null"
	}
	if !row.HasDefault {
		return leafPlaceholder(row.Type)
	}
	switch row.Type {
	case "bool", "int", "float":
		return row.Default
	default:
		return strconv.Quote(row.Default)
	}
}

func leafPlaceholder(t string) string {
	switch t {
	case "bool":
		return "false"
	case "int", "float":
		return "0"
	default:
		if strings.HasPrefix(t, "[]") {
			return "[]"
		}
		return `""`
	}
}

// leafComment is the trailing "# ..." annotation for a leaf.
//
// HasDefault is surfaced explicitly ("no built-in default") rather than
// silently rendering an empty-looking placeholder: command.default has a
// real default of "" (HasDefault true), while providers.<name>.model has NO
// default at all (HasDefault false) — both would otherwise print as
// `key: ""` with nothing to tell them apart, which is exactly the ambiguity
// HasDefault was added to resolve.
//
// review.architecture_file gets a further note: config.Default() stores ""
// there on purpose (so arch.Discover's auto-discovery stays a silent no-op),
// but effectiveDefaults (configkeys.go) overrides the REPORTED default to
// "architecture.json" — the value auto-discovery actually applies. Printing
// that as an ordinary default would contradict "config get
// review.architecture_file", which still returns "".
func leafComment(row *ConfigKey) string {
	if row == nil {
		return ""
	}
	if !row.HasDefault {
		return row.Type + " — no built-in default"
	}
	if _, ok := effectiveDefaults[row.Path]; ok {
		return row.Type + " — effective default; `config get` returns \"\" (auto-discovery applies this)"
	}
	return row.Type
}
