// SPDX-License-Identifier: GPL-3.0-or-later

package meta

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

// ---------------------------------------------------------------------
// envVars <-> ApplyEnv coverage
// ---------------------------------------------------------------------

// applyEnvVarNames parses internal/config/env.go's ApplyEnv function and
// returns the literal name of every environment variable it reads via
// os.Getenv, resolving a bare identifier argument (AnthropicAPIKeyEnv, …)
// against that same file's own const declarations.
//
// Parsing the source rather than hand-copying the list here is the point:
// a hand-copied list would drift exactly the way README's old table did
// (silently missing DEEPSEEK_API_KEY/MISTRAL_API_KEY/COHERE_API_KEY), and
// nothing would fail. This fails the moment ApplyEnv reads one more
// variable than envVars() documents.
func applyEnvVarNames(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../config/env.go", nil, 0)
	if err != nil {
		t.Fatalf("parse internal/config/env.go: %v", err)
	}

	consts := map[string]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				consts[name.Name] = v
			}
		}
	}

	var applyEnv *ast.FuncDecl
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == "ApplyEnv" {
			applyEnv = fd
			break
		}
	}
	if applyEnv == nil {
		t.Fatal("internal/config/env.go: func ApplyEnv not found — did it get renamed?")
	}

	var names []string
	ast.Inspect(applyEnv.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Getenv" {
			return true
		}
		if pkgIdent, ok := sel.X.(*ast.Ident); !ok || pkgIdent.Name != "os" {
			return true
		}
		if len(call.Args) != 1 {
			t.Fatalf("os.Getenv call with %d args, want 1", len(call.Args))
			return false
		}
		switch arg := call.Args[0].(type) {
		case *ast.BasicLit:
			v, err := strconv.Unquote(arg.Value)
			if err != nil {
				t.Fatalf("unquote os.Getenv argument %s: %v", arg.Value, err)
			}
			names = append(names, v)
		case *ast.Ident:
			v, ok := consts[arg.Name]
			if !ok {
				t.Fatalf("os.Getenv(%s): %s is not a string constant declared in env.go", arg.Name, arg.Name)
			}
			names = append(names, v)
		default:
			t.Fatalf("os.Getenv called with an argument shape this test does not understand (%T); "+
				"extend applyEnvVarNames before trusting TestEnvVarsCoverApplyEnv again", arg)
		}
		return true
	})
	return names
}

func TestEnvVarsCoverApplyEnv(t *testing.T) {
	want := applyEnvVarNames(t)
	if len(want) == 0 {
		t.Fatal("parsed zero os.Getenv calls out of ApplyEnv; the AST walk is broken, not ApplyEnv itself")
	}

	documented := map[string]bool{}
	for _, v := range envVars() {
		documented[v.Name] = true
	}
	for _, name := range want {
		if !documented[name] {
			t.Errorf("ApplyEnv reads %s but envVars() does not list it — "+
				"a new env var would silently go undocumented in the generated README block", name)
		}
	}

	read := map[string]bool{}
	for _, name := range want {
		read[name] = true
	}
	for _, v := range envVars() {
		if !read[v.Name] {
			t.Errorf("envVars() documents %s as something ApplyEnv reads, but ApplyEnv does not read it", v.Name)
		}
	}
}

// ---------------------------------------------------------------------
// providers
// ---------------------------------------------------------------------

func TestRenderProvidersHandlesCLIBackedProviders(t *testing.T) {
	s := Surface{Providers: []provider.Metadata{
		{Name: "codex-cli", Kind: provider.KindCLI, Binary: "codex"},
	}}
	got := renderProviders(s)

	// Singular grammar (M8): "1 provider ships", not "1 providers ship".
	if !strings.Contains(got, "1 provider ships in the box: 1 CLI-tool-backed.") {
		t.Errorf("intro sentence missing/wrong (singular grammar?):\n%s", got)
	}
	if !strings.Contains(got, "| `codex-cli` | CLI-backed | — | — | — | — |") {
		t.Errorf("empty model list/default model did not degrade to \"—\" columns:\n%s", got)
	}
}

func TestRenderProvidersPricing(t *testing.T) {
	s := Surface{Providers: []provider.Metadata{
		{
			Name: "ollama", Kind: provider.KindLocal, DefaultModel: "llama3",
			Models: []provider.ModelInfo{{ID: "llama3", ContextWindow: 8192, Pricing: provider.Pricing{}}},
		},
		{
			Name: "anthropic", Kind: provider.KindAPI, DefaultModel: "claude-x",
			Models: []provider.ModelInfo{{
				ID: "claude-x", ContextWindow: 1000000,
				Pricing: provider.Pricing{InputPer1M: 5, OutputPer1M: 25, CachedInputPer1M: 0.5},
			}},
		},
		{
			Name: "mistral", Kind: provider.KindAPI, DefaultModel: "mistral-x",
			Models: []provider.ModelInfo{{
				ID: "mistral-x", ContextWindow: 128000,
				Pricing: provider.Pricing{InputPer1M: 2, OutputPer1M: 6}, // no cache discount
			}},
		},
	}}
	got := renderProviders(s)

	if !strings.Contains(got, "| free |") {
		t.Errorf("zero in+out pricing should render as \"free\":\n%s", got)
	}
	if !strings.Contains(got, "$5 / $25 / $0.5") {
		t.Errorf("priced model with a cache discount rendered wrong:\n%s", got)
	}
	// A zero CachedInputPer1M means "same as InputPer1M" per Pricing's own
	// doc comment, and Cost() really does charge the input rate for cached
	// tokens in that case — "—" there used to read as "free"/"n/a" and
	// badly underpriced a cache-heavy run (Faz 07 review N1). "—" is
	// reserved for rows where pricing does not apply at all (CLI-backed).
	if !strings.Contains(got, "$2 / $6 / = $2") {
		t.Errorf("a zero CachedInputPer1M (no discount) must show the real rate as \"= $2\", not \"—\":\n%s", got)
	}
	if !strings.Contains(got, "1,000,000") {
		t.Errorf("context window not thousands-separated:\n%s", got)
	}
}

// TestRenderProvidersOneRowPerModel pins the fix for the misattribution bug:
// a provider with several models must get one row per model, each carrying
// THAT model's own context/price, not the default model's numbers repeated
// across every row.
func TestRenderProvidersOneRowPerModel(t *testing.T) {
	s := Surface{Providers: []provider.Metadata{
		{
			Name: "openai", Kind: provider.KindAPI, DefaultModel: "gpt-5.4-mini",
			Models: []provider.ModelInfo{
				{ID: "gpt-5.4-mini", Default: true, ContextWindow: 400000, Pricing: provider.Pricing{InputPer1M: 0.75, OutputPer1M: 4.5}},
				{ID: "gpt-4o", ContextWindow: 128000, Pricing: provider.Pricing{InputPer1M: 2.5, OutputPer1M: 10}},
			},
		},
	}}
	got := renderProviders(s)

	rows := 0
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "| `openai`") {
			rows++
		}
	}
	if rows != 2 {
		t.Fatalf("openai has 2 models but produced %d rows, want 2:\n%s", rows, got)
	}

	defRow := lineContaining(got, "`gpt-5.4-mini`")
	if !strings.Contains(defRow, "400,000") || !strings.Contains(defRow, "✓") {
		t.Errorf("default model row wrong (want its own context 400,000 and a ✓ marker):\n%s", defRow)
	}
	otherRow := lineContaining(got, "`gpt-4o`")
	if !strings.Contains(otherRow, "128,000") {
		t.Errorf("gpt-4o must show ITS OWN context window (128,000), not gpt-5.4-mini's:\n%s", otherRow)
	}
	if strings.Contains(otherRow, "✓") {
		t.Errorf("gpt-4o is not the default model and must not carry the ✓ marker:\n%s", otherRow)
	}
}

func TestFormatThousands(t *testing.T) {
	cases := map[int]string{0: "0", 999: "999", 1000: "1,000", 32768: "32,768", 1000000: "1,000,000"}
	for in, want := range cases {
		if got := formatThousands(in); got != want {
			t.Errorf("formatThousands(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestFormatPricingCachedZeroMeansSameAsInput pins N1 directly against
// gpt-5.5-pro's real numbers: Pricing.CachedInputPer1M == 0 means "same as
// InputPer1M" (Pricing's own doc comment) and Cost() really does charge the
// input rate for cached tokens in that case — rendering "—" there read as
// "free"/"not applicable" and badly underpriced a cache-heavy run.
func TestFormatPricingCachedZeroMeansSameAsInput(t *testing.T) {
	got := formatPricing(provider.Pricing{InputPer1M: 30, OutputPer1M: 180})
	if got != "$30 / $180 / = $30" {
		t.Errorf("formatPricing = %q, want %q (cached shown as the real rate it equals, not \"—\")", got, "$30 / $180 / = $30")
	}

	// A non-zero cache discount is unaffected.
	got = formatPricing(provider.Pricing{InputPer1M: 5, OutputPer1M: 25, CachedInputPer1M: 0.5})
	if got != "$5 / $25 / $0.5" {
		t.Errorf("formatPricing with a real discount = %q, want %q", got, "$5 / $25 / $0.5")
	}

	// Free (ollama, or any future local provider) still renders as "free",
	// not "$0 / $0 / = $0".
	if got := formatPricing(provider.Pricing{}); got != "free" {
		t.Errorf("formatPricing(zero pricing) = %q, want %q", got, "free")
	}
}

// TestRenderProvidersCountsUnknownKindIntoTotal pins N6: a provider.Kind
// this switch does not know about must not silently vanish from the
// breakdown while still being counted in the total (making the parts sum
// to less than the stated total with no visible reason why).
func TestRenderProvidersCountsUnknownKindIntoTotal(t *testing.T) {
	s := Surface{Providers: []provider.Metadata{
		{Name: "anthropic", Kind: provider.KindAPI, DefaultModel: "x", Models: []provider.ModelInfo{{ID: "x"}}},
		{Name: "mystery", Kind: provider.Kind("quantum")},
	}}
	got := renderProviders(s)
	if !strings.Contains(got, "2 providers ship in the box: 1 API, 1 other.") {
		t.Errorf("an unrecognized Kind must still be counted, in an \"other\" bucket:\n%s", got)
	}
}

// ---------------------------------------------------------------------
// escapeCell edge cases (Faz 07 review N3): none of these are reachable
// from today's surface.json, but the drift gate freezes whatever it sees,
// so they are worth pinning before real data ever exercises them.
// ---------------------------------------------------------------------

func TestEscapeCellDoesNotDoubleBackslashInsideCodeSpan(t *testing.T) {
	got := escapeCell("path `C:\\Users\\x` on Windows")
	want := "path `C:\\Users\\x` on Windows"
	if got != want {
		t.Errorf("escapeCell doubled a backslash INSIDE a code span:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestEscapeCellDoublesBackslashOutsideCodeSpan(t *testing.T) {
	// A bare backslash outside any code span, followed by ASCII
	// punctuation, must be doubled or CommonMark consumes it as an escape
	// and the backslash silently disappears from the rendered output
	// (e.g. a regex fragment like \. would render as a bare period).
	got := escapeCell(`a regex like \.`)
	want := `a regex like \\.`
	if got != want {
		t.Errorf("escapeCell = %q, want %q", got, want)
	}
}

func TestEscapeCellHandlesMultiBacktickCodeSpans(t *testing.T) {
	// A double-backtick span is the standard way to quote text that
	// itself contains a single backtick; CommonMark closes it at the next
	// run of exactly two backticks, not at the first backtick it sees.
	got := escapeCell("``a * b < c``")
	want := "``a * b < c``"
	if got != want {
		t.Errorf("escapeCell mishandled a double-backtick span:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestEscapeCellCollapsesEmbeddedNewlines(t *testing.T) {
	for _, in := range []string{"line one\nline two", "line one\r\nline two", "line one\rline two"} {
		got := escapeCell(in)
		if strings.ContainsAny(got, "\r\n") {
			t.Errorf("escapeCell(%q) = %q, still contains a line ending — would split the table row", in, got)
		}
		if got != "line one line two" {
			t.Errorf("escapeCell(%q) = %q, want %q", in, got, "line one line two")
		}
	}
}

// ---------------------------------------------------------------------
// mcp-tool-args
// ---------------------------------------------------------------------

func TestRenderMCPToolArgs(t *testing.T) {
	s := Surface{MCPToolArgs: []MCPArg{
		{Name: "diff", Type: "array", Items: "string", Description: "git diff args"},
		{Name: "fail_on", Type: "string", Enum: []string{"critical", "high", "none"}, Description: "gate severity"},
		{Name: "unstaged", Type: "boolean", Required: true, Description: "review the working tree"},
	}}
	got := renderMCPToolArgs(s)

	// Type is its own code span — this is what stops "array<string>" being
	// read as a raw HTML open tag (M2): a bare '<' outside a code span
	// vanishes under GitHub's HTML sanitizer, verified against cmark-gfm.
	if !strings.Contains(got, "| `diff` | `array<string>` | — | git diff args |") {
		t.Errorf("array<items> type rendering wrong:\n%s", got)
	}
	// Enum values are comma-joined, each its own code span — NOT joined
	// with a bare '|' (M1: a raw '|' inside a table cell splits the row
	// into extra cells EVEN INSIDE A CODE SPAN, verified against cmark-gfm;
	// `critical|high|none` used to truncate the whole row at "critical").
	if !strings.Contains(got, "| `fail_on` | `string` | — | Allowed: `critical`, `high`, `none` — gate severity |") {
		t.Errorf("enum rendering wrong:\n%s", got)
	}
	if !strings.Contains(got, "| `unstaged` | `boolean` | yes | review the working tree |") {
		t.Errorf("required rendering wrong:\n%s", got)
	}
}

// TestRenderMCPToolArgsEscapesDescription pins the two other M2 findings on
// the real "dir" and "file" argument descriptions: a bare "<dir>" and the
// bare '*' glob wildcards must survive as literal text, not be silently
// swallowed as raw HTML / emphasis markup.
func TestRenderMCPToolArgsEscapesDescription(t *testing.T) {
	s := Surface{MCPToolArgs: []MCPArg{
		{Name: "dir", Type: "array", Items: "string",
			Description: "A plain value is a <dir>/ prefix; a glob value is matched gitignore-style."},
		{Name: "file", Type: "array", Items: "string",
			Description: `A plain value is an exact path; a value containing */?/[ is a gitignore-style glob (e.g. "*.go", "internal/**/*.ts").`},
		{Name: "exclude_file", Type: "array", Items: "string",
			Description: "Same matching rules as `file`, applied after it so an exclusion wins."},
	}}
	got := renderMCPToolArgs(s)

	// Escaping just the '<' is enough — verified against cmark-gfm: a lone,
	// unescaped '>' has no markdown meaning mid-sentence, only '<dir>' as a
	// complete open-tag-shaped span gets swallowed as raw HTML.
	if !strings.Contains(got, `\<dir>`) {
		t.Errorf("expected escaped \\<dir>, not found in:\n%s", got)
	}
	if !strings.Contains(got, `\*/?/[ is a gitignore-style glob (e.g. "\*.go", "internal/\*\*/\*.ts")`) {
		t.Errorf("glob asterisks must be escaped so they render literally, not as emphasis:\n%s", got)
	}
	// An intentional code span in the ORIGINAL description ("`file`") must
	// survive untouched — escaping applies outside code spans only.
	if !strings.Contains(got, "Same matching rules as `file`, applied after it") {
		t.Errorf("an existing intentional code span must not be mangled by escaping:\n%s", got)
	}
}

// TestRenderMCPToolArgsEscapesPipeInDescription pins M1 for prose: a raw
// '|' in Description — not inside any code span — must come out escaped.
func TestRenderMCPToolArgsEscapesPipeInDescription(t *testing.T) {
	s := Surface{MCPToolArgs: []MCPArg{
		{Name: "weird", Type: "string", Description: "a | b"},
	}}
	got := renderMCPToolArgs(s)
	if strings.Contains(got, "a | b") {
		t.Errorf("a bare '|' in Description must be escaped, not left to split the row:\n%s", got)
	}
	if !strings.Contains(got, `a \| b`) {
		t.Errorf("expected the escaped form 'a \\| b' in:\n%s", got)
	}
}

// TestRenderMCPToolArgsEscapesPipeInEnumNameAndType pins N4: a schema
// literally naming this test claimed to cover the enum path while its body
// only ever exercised Description — so a '|' in an enum value (or in Name
// or Type) went straight through unescaped, in a table cell THIS FUNCTION
// hand-wraps in backticks, which does not protect '|' (a backtick code
// span never protects it from GFM's row splitter — see escapeCell's doc
// comment). This test actually drives all three paths.
func TestRenderMCPToolArgsEscapesPipeInEnumNameAndType(t *testing.T) {
	s := Surface{MCPToolArgs: []MCPArg{
		{Name: "a|weird|name", Type: "one|two", Enum: []string{"x|y", "z"}, Description: "d"},
	}}
	got := renderMCPToolArgs(s)

	if strings.Count(got, "\n") != 2 {
		// header + separator + exactly one data row: a live, unescaped '|'
		// anywhere above would split that one row into more lines.
		t.Fatalf("expected exactly 3 lines (header, separator, one data row), got:\n%s", got)
	}
	if !strings.Contains(got, "`a\\|weird\\|name`") {
		t.Errorf("Name with a '|' not escaped:\n%s", got)
	}
	if !strings.Contains(got, "`one\\|two`") {
		t.Errorf("Type with a '|' not escaped:\n%s", got)
	}
	if !strings.Contains(got, "`x\\|y`, `z`") {
		t.Errorf("enum value with a '|' not escaped:\n%s", got)
	}
}

// ---------------------------------------------------------------------
// env-vars
// ---------------------------------------------------------------------

func TestRenderEnvVars(t *testing.T) {
	s := Surface{EnvVars: []EnvVar{
		{Name: "FOO_API_KEY", Effect: "Foo credential.", ConfigKey: "providers.foo.api_key"},
		{Name: "COMMITBRIEF_MODEL", Effect: "Overrides model.", ConfigKey: "providers.<name>.model"},
	}}
	got := renderEnvVars(s)
	if !strings.Contains(got, "| `FOO_API_KEY` | Foo credential. | `providers.foo.api_key` |") {
		t.Errorf("env var row rendered wrong:\n%s", got)
	}
}

// ---------------------------------------------------------------------
// config-schema
// ---------------------------------------------------------------------

func TestRenderConfigSchemaDistinguishesHasDefault(t *testing.T) {
	s := Surface{ConfigKeys: []ConfigKey{
		{Path: "command", Type: "object"},
		{Path: "command.default", Type: "string", Default: "", HasDefault: true},
		{Path: "providers", Type: "map[string]object"},
		{Path: "providers.<name>", Type: "object", FreeForm: true},
		{Path: "providers.<name>.model", Type: "string", FreeForm: true}, // HasDefault false
	}}
	got := renderConfigSchema(s)

	if !strings.Contains(got, `default: ""`) {
		t.Fatalf("command.default missing from schema:\n%s", got)
	}
	// Both render `key: ""` — only the trailing comment tells them apart,
	// which is the entire point of HasDefault (Faz 07's "bilinmesi
	// gerekenler" note #2).
	defaultLine := lineContaining(got, "default: \"\"")
	modelLine := lineContaining(got, "model: \"\"")
	if strings.Contains(defaultLine, "no built-in default") {
		t.Errorf("command.default (HasDefault=true, default=\"\") wrongly marked \"no built-in default\":\n%s", defaultLine)
	}
	if !strings.Contains(modelLine, "no built-in default") {
		t.Errorf("providers.<name>.model (HasDefault=false) must be marked \"no built-in default\" to stay distinguishable from command.default:\n%s", modelLine)
	}
}

func TestRenderConfigSchemaArchitectureFileFootnote(t *testing.T) {
	// review.architecture_file must be present in effectiveDefaults
	// (configkeys.go) for this test to mean anything.
	if _, ok := effectiveDefaults["review.architecture_file"]; !ok {
		t.Fatal("effectiveDefaults no longer overrides review.architecture_file; update this test and the renderer's dipnot")
	}

	s := Surface{ConfigKeys: []ConfigKey{
		{Path: "review", Type: "object"},
		{Path: "review.architecture_file", Type: "string", Default: "architecture.json", HasDefault: true},
	}}
	got := renderConfigSchema(s)

	line := lineContaining(got, "architecture_file:")
	if !strings.Contains(line, `"architecture.json"`) {
		t.Errorf("architecture_file value wrong:\n%s", line)
	}
	if !strings.Contains(line, "effective default") {
		t.Errorf("architecture_file must flag itself as an EFFECTIVE default, distinct from a stored one:\n%s", line)
	}
}

func TestRenderConfigSchemaRendersSliceOfStructsAsListItem(t *testing.T) {
	s := Surface{ConfigKeys: []ConfigKey{
		{Path: "guard", Type: "object"},
		{Path: "guard.secret_patterns", Type: "[]object"},
		{Path: "guard.secret_patterns[].name", Type: "string"},
		{Path: "guard.secret_patterns[].regex", Type: "string"},
	}}
	got := renderConfigSchema(s)

	want := []string{
		"  secret_patterns:",
		"    - name: \"\"",
		"      regex: \"\"",
	}
	for _, w := range want {
		if !containsLineWithPrefix(got, w) {
			t.Errorf("missing expected line prefix %q in:\n%s", w, got)
		}
	}
}

func lineContaining(text, needle string) string {
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return ""
}

func containsLineWithPrefix(text, prefix string) bool {
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}
