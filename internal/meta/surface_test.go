// SPDX-License-Identifier: GPL-3.0-or-later

package meta

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/provider"

	// Side-effect imports, mirroring cmd/commitbrief/main.go:14-23. Without
	// them the metadata registry is EMPTY and every provider assertion below
	// passes vacuously — which is the single most likely way this package
	// ships broken. See provider.AllMetadata's doc comment.
	_ "github.com/CommitBrief/commitbrief/internal/provider/anthropic"
	_ "github.com/CommitBrief/commitbrief/internal/provider/claude-cli"
	_ "github.com/CommitBrief/commitbrief/internal/provider/codex-cli"
	_ "github.com/CommitBrief/commitbrief/internal/provider/cohere"
	_ "github.com/CommitBrief/commitbrief/internal/provider/deepseek"
	_ "github.com/CommitBrief/commitbrief/internal/provider/gemini"
	_ "github.com/CommitBrief/commitbrief/internal/provider/gemini-cli"
	_ "github.com/CommitBrief/commitbrief/internal/provider/mistral"
	_ "github.com/CommitBrief/commitbrief/internal/provider/ollama"
	_ "github.com/CommitBrief/commitbrief/internal/provider/openai"
)

// shippedProviders is the set cmd/commitbrief/main.go blank-imports. Kept
// literal rather than derived from the registry: a test that asks the
// registry what it contains and then asserts it contains that cannot fail.
var shippedProviders = []string{
	"anthropic", "claude-cli", "codex-cli", "cohere", "deepseek",
	"gemini", "gemini-cli", "mistral", "ollama", "openai",
}

// newTestTree builds a cobra tree that exercises every rule Build has to get
// right, on a tree this package owns.
//
// internal/cli's newRootCmd is unexported and meta must never import
// internal/cli anyway (that is the import direction the whole design rests
// on), so the real tree is verified elsewhere. What is reproduced here is the
// shape of the real one: root persistent flags, two mutex groups, a
// subcommand that shadows a persistent flag by name, a nested subcommand that
// shadows another, a hidden command, and cobra's own generated help and
// completion commands.
func newTestTree() *cobra.Command {
	root := &cobra.Command{
		Use:     "cbtest",
		Short:   "root short",
		Version: "9.9.9", // makes cobra want to inject --version
	}

	pf := root.PersistentFlags()
	pf.Bool("staged", false, "review the staged diff")
	pf.Bool("unstaged", false, "review the working tree")
	pf.StringP("provider", "p", "", "provider override")
	pf.Bool("cli", false, "route through a CLI-backed provider")
	pf.BoolP("quiet", "q", false, "suppress non-essential output")
	root.MarkFlagsMutuallyExclusive("staged", "unstaged")
	root.MarkFlagsMutuallyExclusive("provider", "cli")

	// Shadows root's persistent --unstaged, exactly as `guard` does.
	guard := &cobra.Command{Use: "guard", Short: "guard short"}
	guard.Flags().Bool("unstaged", false, "review the working tree instead")
	root.AddCommand(guard)

	// Nested: `cache prune --provider` shadows root's persistent --provider.
	cache := &cobra.Command{Use: "cache", Short: "cache short"}
	prune := &cobra.Command{Use: "prune", Short: "prune short"}
	prune.Flags().String("provider", "", "prune only this provider's entries")
	prune.Flags().String("model", "", "prune only this model's entries")
	cache.AddCommand(prune)
	root.AddCommand(cache)

	hidden := &cobra.Command{Use: "internal-only", Short: "hidden short", Hidden: true}
	root.AddCommand(hidden)

	// Cobra's own additions. Normally injected during Execute; forced here so
	// the skip rules are tested against a tree that actually has them.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()

	return root
}

func buildTestSurface(t *testing.T) Surface {
	t.Helper()
	return Build(newTestTree(), json.RawMessage(testMCPSchema))
}

const testMCPSchema = `{
  "type": "object",
  "properties": {
    "unstaged": {"type": "boolean", "description": "review the working tree"},
    "diff": {"type": "array", "items": {"type": "string"}, "description": "git diff args"},
    "fail_on": {"type": "string", "enum": ["critical", "high", "none"], "description": "gate severity"}
  },
  "required": ["unstaged"],
  "additionalProperties": false
}`

func commandPaths(s Surface) []string {
	out := make([]string, 0, len(s.Commands))
	for _, c := range s.Commands {
		out = append(out, c.Path)
	}
	return out
}

func flagsNamed(s Surface, name string) []Flag {
	var out []Flag
	for _, f := range s.Flags {
		if f.Name == name {
			out = append(out, f)
		}
	}
	return out
}

func TestBuildSkipsGeneratedCommands(t *testing.T) {
	s := buildTestSurface(t)
	paths := commandPaths(s)

	for _, p := range paths {
		last := p
		if i := strings.LastIndex(p, " "); i >= 0 {
			last = p[i+1:]
		}
		switch last {
		case "help", "completion", "bash", "zsh", "fish", "powershell":
			t.Errorf("generated command %q leaked into the inventory (all paths: %v)", p, paths)
		}
		if strings.HasPrefix(last, "__") {
			t.Errorf("cobra-internal command %q leaked into the inventory", p)
		}
	}

	// The skip must not be so eager that it eats real commands.
	want := []string{"cbtest", "cbtest cache", "cbtest cache prune", "cbtest guard", "cbtest internal-only"}
	got := append([]string(nil), paths...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %v, want %v", got, want)
	}

	// Hidden is recorded, not filtered: the inventory describes the whole
	// surface and the renderer decides what to print.
	for _, c := range s.Commands {
		if c.Path == "cbtest internal-only" && !c.Hidden {
			t.Error("hidden command was recorded as visible")
		}
	}

	// Cobra's injected flags are generated for the same reason.
	if got := flagsNamed(s, "help"); len(got) != 0 {
		t.Errorf("cobra's --help leaked into the inventory: %+v", got)
	}
	if got := flagsNamed(s, "version"); len(got) != 0 {
		t.Errorf("cobra's --version leaked into the inventory: %+v", got)
	}
}

func TestBuildKeepsShadowedFlags(t *testing.T) {
	s := buildTestSurface(t)

	unstaged := flagsNamed(s, "unstaged")
	if len(unstaged) != 2 {
		t.Fatalf("--unstaged rows = %d, want 2 (root persistent + guard's shadow): %+v", len(unstaged), unstaged)
	}
	byCommand := map[string]Flag{}
	for _, f := range unstaged {
		byCommand[f.Command] = f
	}
	if f, ok := byCommand["cbtest"]; !ok || f.Scope != ScopeGlobal {
		t.Errorf("root --unstaged = %+v, want scope %q", f, ScopeGlobal)
	}
	if f, ok := byCommand["cbtest guard"]; !ok || f.Scope != ScopeCommand {
		t.Errorf("guard --unstaged = %+v, want scope %q", f, ScopeCommand)
	}

	providerFlags := flagsNamed(s, "provider")
	if len(providerFlags) != 2 {
		t.Fatalf("--provider rows = %d, want 2 (root persistent + cache prune's shadow): %+v", len(providerFlags), providerFlags)
	}
	for _, f := range providerFlags {
		switch f.Command {
		case "cbtest":
			if f.Scope != ScopeGlobal || f.Shorthand != "p" {
				t.Errorf("root --provider = %+v, want global with shorthand p", f)
			}
		case "cbtest cache prune":
			if f.Scope != ScopeCommand || f.Shorthand != "" {
				t.Errorf("cache prune --provider = %+v, want command scope with no shorthand", f)
			}
		default:
			t.Errorf("unexpected --provider owner %q", f.Command)
		}
	}

	// An inherited flag must NOT be re-reported under every descendant: only
	// a command that declares its own copy gets a row.
	quiet := flagsNamed(s, "quiet")
	if len(quiet) != 1 || quiet[0].Command != "cbtest" {
		t.Errorf("--quiet rows = %+v, want exactly one owned by root", quiet)
	}

	// Type and default survive the walk; the renderer needs both.
	if quiet[0].Type != "bool" || quiet[0].Default != "false" {
		t.Errorf("--quiet type/default = %q/%q, want bool/false", quiet[0].Type, quiet[0].Default)
	}
}

func TestBuildRecordsMutexGroups(t *testing.T) {
	// First, prove the literal annotation key still matches the linked cobra.
	// cobra's own constant is unexported, so this assertion is the only thing
	// standing between a cobra rename and an inventory that silently loses
	// every mutex group.
	probe := newTestTree()
	raw := probe.PersistentFlags().Lookup("staged").Annotations[mutuallyExclusiveAnnotation]
	if len(raw) == 0 {
		t.Fatalf("annotation key %q reads empty off a flag cobra just marked mutually exclusive; cobra changed the key",
			mutuallyExclusiveAnnotation)
	}

	s := Build(probe, json.RawMessage(testMCPSchema))

	got := map[string][]string{}
	for _, f := range s.Flags {
		if f.Command == "cbtest" && len(f.MutexGroups) > 0 {
			got[f.Name] = f.MutexGroups
		}
	}

	// Both members of a group carry the group, and --cli belongs to one group
	// here; in the real tree it belongs to three, which is why the field is a
	// slice rather than a string.
	want := map[string][]string{
		"staged":   {"staged unstaged"},
		"unstaged": {"staged unstaged"},
		"provider": {"provider cli"},
		"cli":      {"provider cli"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mutex groups = %v, want %v", got, want)
	}

	// The recorded slice must be a copy, not an alias into pflag's live
	// annotation map: s was built from probe, so writing through it and
	// rebuilding from probe is what exposes an alias.
	for _, f := range s.Flags {
		if f.Name == "staged" && len(f.MutexGroups) > 0 {
			f.MutexGroups[0] = "mutated"
			break
		}
	}
	for _, g := range Build(probe, nil).Flags {
		if g.Name == "staged" && len(g.MutexGroups) > 0 && g.MutexGroups[0] == "mutated" {
			t.Error("MutexGroups aliases pflag's annotation slice")
		}
	}
}

func TestConfigKeysUsePlaceholdersForFreeFormMaps(t *testing.T) {
	s := buildTestSurface(t)

	index := map[string]ConfigKey{}
	for _, k := range s.ConfigKeys {
		index[k.Path] = k
	}

	for _, path := range []string{
		"providers",
		"providers.<name>",
		"providers.<name>.model",
		"providers.<name>.pricing",
		"providers.<name>.pricing.<model>",
		"providers.<name>.pricing.<model>.input_per_1m",
	} {
		if _, ok := index[path]; !ok {
			t.Errorf("missing config key %q", path)
		}
	}

	if k := index["providers.<name>.model"]; !k.FreeForm {
		t.Error("providers.<name>.model should be marked free_form")
	}
	if k := index["providers.<name>.pricing.<model>.input_per_1m"]; k.Type != "float" {
		t.Errorf("pricing leaf type = %q, want float", k.Type)
	}

	// The seeded provider names from config.Default() must NOT appear: they
	// are four of ten, and naming them would document deepseek, mistral and
	// cohere out of existence.
	for path := range index {
		for _, seeded := range []string{"anthropic", "openai", "gemini", "ollama"} {
			if strings.HasPrefix(path, "providers."+seeded) {
				t.Errorf("config key %q names a seeded map key instead of a placeholder", path)
			}
		}
	}

	// Concrete keys still carry their default, and a slice of structs is
	// expanded rather than left opaque.
	if k := index["cache.ttl_days"]; k.Type != "int" || k.Default != "7" {
		t.Errorf("cache.ttl_days = %+v, want int/7", k)
	}
	if k := index["output.stream"]; k.Default != "true" {
		t.Errorf("output.stream default = %q, want true", k.Default)
	}
	if k := index["cost.warn_threshold_usd"]; k.Default != "0.5" {
		t.Errorf("cost.warn_threshold_usd default = %q, want 0.5", k.Default)
	}
	if _, ok := index["guard.secret_patterns[].regex"]; !ok {
		t.Error("missing guard.secret_patterns[].regex; slice elements are not being walked")
	}
	if k := index["review.sandbox_command"]; k.Type != "[]string" {
		t.Errorf("review.sandbox_command type = %q, want []string", k.Type)
	}
}

// buildStampFields are the json names rule 5 forbids anywhere in the tree.
var buildStampFields = map[string]bool{
	"version": true, "commit": true, "date": true, "built": true,
	"build": true, "build_date": true, "built_at": true,
	"commit_hash": true, "revision": true, "timestamp": true,
}

func TestSurfaceHasNoBuildStamp(t *testing.T) {
	// A type-level check rather than a substring scan of the JSON: "version"
	// is a legitimate config KEY (config.Config.Version) and "commit" is a
	// legitimate config section, so only a field NAME can be judged.
	var walk func(t reflect.Type, path string, seen map[reflect.Type]bool)
	walk = func(rt reflect.Type, path string, seen map[reflect.Type]bool) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" {
				name = strings.ToLower(f.Name)
			}
			if buildStampFields[name] {
				t.Errorf("%s.%s is a build stamp; surface.json is committed and would churn on every build", path, name)
			}
			walk(f.Type, path+"."+name, seen)
		}
	}
	walk(reflect.TypeOf(Surface{}), "surface", map[reflect.Type]bool{})

	// And nothing sneaks a stamp in as a value either.
	b, err := buildTestSurface(t).MarshalIndent()
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	for _, needle := range []string{`"built`, `"buildDate"`, `"commit_hash"`} {
		if bytes.Contains(b, []byte(needle)) {
			t.Errorf("marshalled surface contains %q", needle)
		}
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	// Two independent trees of identical shape, so the test also catches a
	// dependence on map iteration order inside Build rather than only a
	// dependence on the tree object.
	a, err := Build(newTestTree(), json.RawMessage(testMCPSchema)).MarshalIndent()
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	for i := 0; i < 8; i++ {
		b, err := Build(newTestTree(), json.RawMessage(testMCPSchema)).MarshalIndent()
		if err != nil {
			t.Fatalf("MarshalIndent: %v", err)
		}
		if !bytes.Equal(a, b) {
			t.Fatalf("Build is not deterministic on run %d", i)
		}
	}

	if !bytes.HasSuffix(a, []byte("\n")) {
		t.Error("the artifact must end with a newline")
	}

	keys := ConfigKeys()
	if !sort.SliceIsSorted(keys, func(i, j int) bool { return keys[i].Path < keys[j].Path }) {
		t.Error("config keys are not sorted by dotted path")
	}

	// Empty sections marshal as [] rather than null, so a renderer never has
	// to tell the two apart.
	empty, err := Build(&cobra.Command{Use: "bare"}, nil).MarshalIndent()
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if bytes.Contains(empty, []byte("null")) {
		t.Errorf("empty sections marshalled as null:\n%s", empty)
	}
}

func TestBuildSeesEveryShippedProvider(t *testing.T) {
	s := buildTestSurface(t)

	if len(s.Providers) == 0 {
		t.Fatal("no providers: the blank imports at the top of this file are missing, " +
			"and every provider assertion in this package is passing vacuously")
	}

	got := make([]string, 0, len(s.Providers))
	for _, p := range s.Providers {
		got = append(got, p.Name)
	}
	if !reflect.DeepEqual(got, shippedProviders) {
		t.Errorf("providers = %v, want %v (sorted, exactly the set main.go blank-imports)", got, shippedProviders)
	}

	// Metadata must be usable, not merely present. The bar differs by kind:
	// an api/local provider is documented by its default model, whereas the
	// CLI-backed three deliberately leave DefaultModel and Models empty —
	// the host CLI owns model selection, and the "binary@version" string
	// clireview synthesises for the cache key is not a model a reader could
	// pass to --model. For those, the binary name is the fact docs need.
	for _, p := range s.Providers {
		switch p.Kind {
		case provider.KindAPI, provider.KindLocal:
			if p.DefaultModel == "" {
				t.Errorf("provider %q (%s) has no default model", p.Name, p.Kind)
			}
		case provider.KindCLI:
			if p.Binary == "" {
				t.Errorf("CLI-backed provider %q names no binary", p.Name)
			}
		default:
			t.Errorf("provider %q has unknown kind %q", p.Name, p.Kind)
		}
	}

	if s.Schema != SchemaVersion {
		t.Errorf("schema = %d, want %d", s.Schema, SchemaVersion)
	}
}

func TestBuildParsesMCPToolArgs(t *testing.T) {
	s := buildTestSurface(t)

	want := []MCPArg{
		{Name: "diff", Type: "array", Items: "string", Description: "git diff args"},
		{Name: "fail_on", Type: "string", Enum: []string{"critical", "high", "none"}, Description: "gate severity"},
		{Name: "unstaged", Type: "boolean", Required: true, Description: "review the working tree"},
	}
	if !reflect.DeepEqual(s.MCPToolArgs, want) {
		t.Errorf("mcp tool args = %+v, want %+v", s.MCPToolArgs, want)
	}

	// A missing or unparseable schema empties the section instead of taking
	// the other four down with it.
	if got := Build(newTestTree(), nil).MCPToolArgs; len(got) != 0 {
		t.Errorf("nil schema produced %+v, want empty", got)
	}
	if got := Build(newTestTree(), json.RawMessage("{not json")).MCPToolArgs; len(got) != 0 {
		t.Errorf("broken schema produced %+v, want empty", got)
	}
}

// ---------------------------------------------------------------------
// envVars <-> ApplyEnv coverage
//
// Moved from blocks_test.go (ADR-0041 §3 "Moved, not deleted"): the
// generated-README renderer that consumed this pairing is gone, but the
// pairing itself is still a real inventory guarantee — envVars() must keep
// documenting exactly what internal/config's ApplyEnv actually reads — so
// the coverage check stays, now against surface.go's envVars() directly.
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
				"a new env var would silently go undocumented in surface.json (and the site's env-var table)", name)
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
// MCP tool args row count vs. the real schema
//
// Moved from docs_test.go (ADR-0041 §3 "Moved, not deleted"): pins the
// specific regression this test was written to catch — README's MCP table
// was hand-written and stuck at 8 rows while the real tool schema already
// had 19 arguments. The generated-README renderer is gone, but surface.json
// (produced by --gen-surface, untouched by this phase) is still the
// artifact whose row count must track the real schema.
// ---------------------------------------------------------------------

func loadRealSurface(t *testing.T) Surface {
	t.Helper()
	data, err := os.ReadFile("surface.json")
	if err != nil {
		t.Fatalf("read surface.json: %v (run `go run ./cmd/commitbrief --gen-surface internal/meta/surface.json` first)", err)
	}
	var s Surface
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal surface.json: %v", err)
	}
	return s
}

func TestMCPToolArgsRowCountMatchesRealSchema(t *testing.T) {
	s := loadRealSurface(t)
	if len(s.MCPToolArgs) != 19 {
		t.Errorf("surface.json has %d MCP tool args, want 19 — "+
			"either the schema changed (update this test) or surface.json is stale (regenerate it)",
			len(s.MCPToolArgs))
	}
}
