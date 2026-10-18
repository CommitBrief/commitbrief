// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/provider"
	_ "github.com/CommitBrief/commitbrief/internal/provider/claude-cli"
	"github.com/CommitBrief/commitbrief/internal/provider/mock"
	_ "github.com/CommitBrief/commitbrief/internal/provider/openai-compatible"
)

const compatFindings = `{"findings":[{"severity":"low","file":"app.go","line":5,` +
	`"title":"Empty check","description":"d","suggestion":"s"}]}`

// compatServer answers every chat completion with one valid findings
// envelope and counts the requests it saw.
func compatServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	n := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*n++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "object": "chat.completion", "created": 1, "model": "m",
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": compatFindings}}},
			"usage": map[string]any{"prompt_tokens": 1000, "completion_tokens": 100, "total_tokens": 1100},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, n
}

func writeCompatConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".commitbrief")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func compatConfig(baseURL, extra string) string {
	return `version: 1
provider: openai-compatible
providers:
  openai-compatible:
    base_url: ` + baseURL + `/v1
    model: my-model
` + extra + `output:
  lang: en
  stream: false
  color: never
cost:
  warn_threshold_usd: 0.0000001
`
}

// D3: no declared price -> "unknown", one stderr note, no cost prompt, no error.
func TestReviewUnpricedOpenAICompatibleCostUnknown(t *testing.T) {
	e := newCLIEnv(t)
	srv, hits := compatServer(t)
	writeCompatConfig(t, e.homeDir, compatConfig(srv.URL, ""))

	// Non-TTY stdin: a cost confirmation would abort the run, so a clean
	// exit proves the prompt was skipped.
	if err := e.run("--staged", "--no-cache", "--verbose"); err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", err, e.errOut.String())
	}
	if *hits != 1 {
		t.Fatalf("server saw %d requests, want 1", *hits)
	}
	stderr := e.errOut.String()
	if got := strings.Count(stderr, "cost is shown as unknown"); got != 1 {
		t.Errorf("unknown-cost note printed %d times, want exactly 1\nstderr:\n%s", got, stderr)
	}
	if strings.Contains(stderr, "Estimated cost") {
		t.Errorf("cost confirmation must be skipped for unknown cost\nstderr:\n%s", stderr)
	}
	all := e.out.String() + stderr
	if !strings.Contains(all, "unknown") {
		t.Errorf("cost should read unknown in the footer\n%s", all)
	}
	if strings.Contains(all, "$0.0000") {
		t.Errorf("unknown cost must not be rendered as $0.0000\n%s", all)
	}
}

// With a declared price the run behaves like any priced provider: a real
// figure, no unknown note.
func TestReviewPricedOpenAICompatibleShowsCost(t *testing.T) {
	e := newCLIEnv(t)
	srv, _ := compatServer(t)
	writeCompatConfig(t, e.homeDir, compatConfig(srv.URL, `    pricing:
      my-model:
        input_per_1m: 1.0
        output_per_1m: 2.0
`)+"")
	// Raise the threshold so the declared price does not trigger the prompt.
	cfg, _ := os.ReadFile(filepath.Join(e.homeDir, ".commitbrief", "config.yml"))
	writeCompatConfig(t, e.homeDir, strings.Replace(string(cfg), "0.0000001", "100", 1))

	if err := e.run("--staged", "--no-cache", "--verbose"); err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", err, e.errOut.String())
	}
	all := e.out.String() + e.errOut.String()
	if strings.Contains(all, "cost is shown as unknown") {
		t.Errorf("priced run must not print the unknown note\n%s", all)
	}
	if !strings.Contains(all, "$0.0012") {
		t.Errorf("expected declared-price cost $0.0012 (1000*1+100*2 per 1M)\n%s", all)
	}
}

func TestQuietSuppressesUnknownCostNote(t *testing.T) {
	e := newCLIEnv(t)
	srv, _ := compatServer(t)
	writeCompatConfig(t, e.homeDir, compatConfig(srv.URL, ""))
	if err := e.run("--staged", "--no-cache", "--quiet"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(e.errOut.String(), "cost is shown as unknown") {
		t.Errorf("--quiet must silence the note\n%s", e.errOut.String())
	}
}

func TestProvidersListShowsOpenAICompatible(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("providers", "list"); err != nil {
		t.Fatalf("providers list: %v", err)
	}
	if !strings.Contains(e.out.String(), "openai-compatible") {
		t.Errorf("providers output lacks openai-compatible:\n%s", e.out.String())
	}
}

func TestReviewOpenAICompatibleMissingModelErrors(t *testing.T) {
	e := newCLIEnv(t)
	writeCompatConfig(t, e.homeDir, `version: 1
provider: openai-compatible
providers:
  openai-compatible:
    base_url: http://127.0.0.1:1/v1
`)
	err := e.run("--staged", "--no-cache")
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("want 'model is required' error, got %v", err)
	}
}

// ---------- D4: context_window ----------

type namedProvider struct {
	*mock.Provider
	name string
}

func (n namedProvider) Name() string { return n.name }

func TestResolveContextWindowOverride(t *testing.T) {
	prov := mock.New()
	prov.Window = 123

	cfg := config.Default()
	cfg.Provider = "deepseek"
	if got := resolveContextWindow(cfg, prov, "m"); got != 123 {
		t.Errorf("unset context_window must fall back to the provider table: got %d", got)
	}

	cfg.Providers["deepseek"] = config.ProviderConfig{ContextWindow: 4096}
	if got := resolveContextWindow(cfg, prov, "m"); got != 4096 {
		t.Errorf("context_window must win: got %d", got)
	}

	cfg.Providers["deepseek"] = config.ProviderConfig{ContextWindow: -5}
	if got := resolveContextWindow(cfg, prov, "m"); got != 123 {
		t.Errorf("non-positive context_window is ignored: got %d", got)
	}
	if got := resolveContextWindow(nil, prov, "m"); got != 123 {
		t.Errorf("nil config falls back: got %d", got)
	}
}

func TestTokenPreflightUsesConfiguredContextWindow(t *testing.T) {
	resetGlobalFlags(t)
	cmd, errBuf := stubCmd(t)
	app := stubApp(t, 0)
	app.Config.Provider = "deepseek"
	app.Config.Providers["deepseek"] = config.ProviderConfig{ContextWindow: 10}

	prov := mock.New() // built-in window is 100_000: only the override can trip the guard
	p := prompt.Prompt{
		System: strings.Repeat("system prompt content ", 40),
		User:   strings.Repeat("diff line content ", 40),
	}
	if !handleTokenPreflight(cmd, app, prov, p, "mock-model", emptyStdin()) {
		t.Fatal("configured context_window=10 must make the preflight abort")
	}
	if !strings.Contains(errBuf.String(), "10") {
		t.Errorf("preflight message should cite the configured window:\n%s", errBuf.String())
	}
}

func TestCostIsUnknown(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "openai-compatible"

	oc := namedProvider{mock.New(), "openai-compatible"}
	if !costIsUnknown(cfg, oc, "my-model") {
		t.Error("openai-compatible without pricing must be unknown")
	}
	cfg.Providers["openai-compatible"] = config.ProviderConfig{
		Pricing: map[string]config.ModelPricing{"my-model": {InputPer1M: 1, OutputPer1M: 2}},
	}
	if costIsUnknown(cfg, oc, "my-model") {
		t.Error("declared price must not be unknown")
	}
	// Free-by-design providers and providers with no metadata are never unknown.
	for _, name := range []string{"ollama", "claude-cli", "mock", "anthropic"} {
		if costIsUnknown(config.Default(), namedProvider{mock.New(), name}, "m") {
			t.Errorf("%s must never be reported as unknown cost", name)
		}
	}
	if _, ok := provider.MetadataFor("openai-compatible"); !ok {
		t.Fatal("openai-compatible metadata not registered")
	}
}

// ---------- D4: api_key_env ----------

func TestResolveAPIKeyEnv(t *testing.T) {
	t.Setenv("MY_KEY", "from-my-key")
	t.Setenv("EMPTY_KEY", "")

	// api_key_env beats the folded fixed-env/api_key value.
	pc, err := provider.ResolveAPIKeyEnv("anthropic", config.ProviderConfig{APIKey: "from-fixed-env", APIKeyEnv: "MY_KEY"})
	if err != nil || pc.APIKey != "from-my-key" {
		t.Fatalf("got (%q, %v), want from-my-key", pc.APIKey, err)
	}

	// set but empty -> error naming the variable.
	_, err = provider.ResolveAPIKeyEnv("anthropic", config.ProviderConfig{APIKey: "k", APIKeyEnv: "EMPTY_KEY"})
	if err == nil || !strings.Contains(err.Error(), "EMPTY_KEY") || !strings.Contains(err.Error(), "providers.anthropic.api_key_env") {
		t.Fatalf("want error naming provider and variable, got %v", err)
	}

	// unset field leaves the block untouched.
	in := config.ProviderConfig{APIKey: "keep", Model: "x"}
	out, err := provider.ResolveAPIKeyEnv("anthropic", in)
	if err != nil || out.APIKey != "keep" || out.Model != "x" {
		t.Fatalf("unset api_key_env changed the block: %+v, %v", out, err)
	}
}

func TestApplyAPIKeyEnvLenient(t *testing.T) {
	t.Setenv("SET_KEY", "v")
	t.Setenv("UNSET_KEY", "")
	cfg := config.Default()
	cfg.Providers["deepseek"] = config.ProviderConfig{APIKey: "old", APIKeyEnv: "SET_KEY"}
	cfg.Providers["mistral"] = config.ProviderConfig{APIKey: "stay", APIKeyEnv: "UNSET_KEY"}
	cfg.Providers["cohere"] = config.ProviderConfig{APIKey: "plain"}

	applyAPIKeyEnvLenient(cfg)

	if got := cfg.Providers["deepseek"].APIKey; got != "v" {
		t.Errorf("deepseek key = %q, want v", got)
	}
	if got := cfg.Providers["mistral"].APIKey; got != "stay" {
		t.Errorf("empty env must not clobber (lenient): got %q", got)
	}
	if got := cfg.Providers["cohere"].APIKey; got != "plain" {
		t.Errorf("block without api_key_env changed: %q", got)
	}
}

// End to end: api_key_env pointing at an empty variable fails the review.
func TestReviewAPIKeyEnvEmptyIsError(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("PINNED_KEY", "")
	writeCompatConfig(t, e.homeDir, `version: 1
provider: openai-compatible
providers:
  openai-compatible:
    base_url: http://127.0.0.1:1/v1
    model: m
    api_key_env: PINNED_KEY
`)
	err := e.run("--staged", "--no-cache")
	if err == nil || !strings.Contains(err.Error(), "PINNED_KEY") {
		t.Fatalf("want api_key_env error, got %v", err)
	}
}

// MINOR 4: the two D4 overrides are inert for *-cli providers. claude-cli is
// registered with Kind CLI, so this exercises the real classification rather
// than a name list.
func TestResolveContextWindowInertForCLIProvider(t *testing.T) {
	prov := namedProvider{mock.New(), "claude-cli"}
	prov.Window = 123
	cfg := config.Default()
	cfg.Providers["claude-cli"] = config.ProviderConfig{ContextWindow: 4096}
	if got := resolveContextWindow(cfg, prov, "m"); got != 123 {
		t.Errorf("context_window must be ignored for a CLI provider: got %d, want 123", got)
	}

	t.Setenv("EMPTY_KEY", "")
	pc := config.ProviderConfig{APIKey: "k", APIKeyEnv: "EMPTY_KEY"}
	out, err := provider.ResolveAPIKeyEnv("claude-cli", pc)
	if err != nil || out.APIKey != "k" {
		t.Errorf("api_key_env must be inert for a CLI provider: (%q, %v)", out.APIKey, err)
	}
	cfg.Providers["claude-cli"] = pc
	applyAPIKeyEnvLenient(cfg)
	if got := cfg.Providers["claude-cli"].APIKey; got != "k" {
		t.Errorf("lenient api_key_env changed a CLI provider key: %q", got)
	}
}

// ---------- MAJOR 1: unknown cost on every cost surface ----------

func TestDryRunUnknownCost(t *testing.T) {
	e := newCLIEnv(t)
	srv, hits := compatServer(t)
	writeCompatConfig(t, e.homeDir, compatConfig(srv.URL, ""))
	if err := e.run("dry-run", "--staged"); err != nil {
		t.Fatalf("dry-run: %v\nstderr:\n%s", err, e.errOut.String())
	}
	all := e.out.String() + e.errOut.String()
	if !strings.Contains(all, "Cost estimate:       unknown") {
		t.Errorf("dry-run must report unknown cost\n%s", all)
	}
	if strings.Contains(all, "$0.0000") {
		t.Errorf("dry-run must not render unknown cost as $0.0000\n%s", all)
	}
	if got := strings.Count(e.errOut.String(), "cost is shown as unknown"); got != 1 {
		t.Errorf("unknown-cost note printed %d times, want 1\n%s", got, e.errOut.String())
	}
	if *hits != 0 {
		t.Errorf("dry-run must not call the provider, saw %d requests", *hits)
	}
}

func TestCompressUnknownCost(t *testing.T) {
	e := newCLIEnv(t)
	srv, _ := compatServer(t)
	if err := e.run("init"); err != nil {
		t.Fatal(err)
	}
	writeCompatConfig(t, e.homeDir, compatConfig(srv.URL, ""))
	e.out.Reset()
	e.errOut.Reset()
	// The fake endpoint answers with a findings envelope, not rules, so the
	// run may abort as "larger"; the cost block is printed before that.
	_ = e.run("compress", "--dry-run", "--yes")
	out := e.out.String()
	if !strings.Contains(out, "Per-review input savings: unknown") {
		t.Errorf("savings must read unknown\n%s\nstderr:\n%s", out, e.errOut.String())
	}
	if !strings.Contains(out, "cost unknown") {
		t.Errorf("call cost must read unknown\n%s", out)
	}
	if strings.Contains(out, "$0.0000") {
		t.Errorf("unknown cost must not be rendered as a dollar figure\n%s", out)
	}
	if got := strings.Count(e.errOut.String(), "cost is shown as unknown"); got != 1 {
		t.Errorf("unknown-cost note printed %d times, want 1\n%s", got, e.errOut.String())
	}
}

func TestRemotePRNoPostUnknownCost(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	srv, hits := compatServer(t)
	writeCompatConfig(t, e.homeDir, compatConfig(srv.URL, ""))
	r := &fakeGH{whoami: "tester", prMeta: prMetaJSON("tester", "stable"), diff: sampleDiff}

	oldWd, _ := os.Getwd()
	_ = os.Chdir(e.repoRoot)
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	var out, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	// Non-TTY stdin plus a near-zero threshold: a cost confirmation would
	// abort, so success proves the preflight was skipped.
	if err := runRemotePR(cmd, "42", remotePRFlags{noPost: true}, r); err != nil {
		t.Fatalf("remote pr --no-post: %v\nstderr:\n%s", err, errBuf.String())
	}
	if *hits != 1 {
		t.Fatalf("server saw %d requests, want 1", *hits)
	}
	all := out.String() + errBuf.String()
	if got := strings.Count(errBuf.String(), "cost is shown as unknown"); got != 1 {
		t.Errorf("unknown-cost note printed %d times, want 1\n%s", got, all)
	}
	if strings.Contains(all, "$0.0000") {
		t.Errorf("unknown cost must not be rendered as $0.0000\n%s", all)
	}
}

// ---------- MINOR 3: http to a non-loopback host ----------

func TestInsecureRemoteBaseURL(t *testing.T) {
	cases := []struct {
		url      string
		insecure bool
	}{
		{"http://127.0.0.1:8000/v1", false},
		{"http://localhost:1234/v1", false},
		{"http://[::1]:8000/v1", false},
		{"https://api.example.com/v1", false},
		{"http://10.0.0.5:8000/v1", true},
		{"http://vllm.internal/v1", true},
		{"", false},
	}
	for _, c := range cases {
		if _, got := insecureRemoteBaseURL(c.url); got != c.insecure {
			t.Errorf("insecureRemoteBaseURL(%q) = %v, want %v", c.url, got, c.insecure)
		}
	}
}

func TestInsecureHTTPNoteOnRun(t *testing.T) {
	// A 10.x address never answers; dry-run does not call the provider, so
	// only the note is observable.
	e := newCLIEnv(t)
	writeCompatConfig(t, e.homeDir, compatConfig("http://10.0.0.5:8000", ""))
	if err := e.run("dry-run", "--staged", "--quiet"); err != nil {
		t.Fatalf("dry-run: %v\nstderr:\n%s", err, e.errOut.String())
	}
	stderr := e.errOut.String()
	if got := strings.Count(stderr, "plain http to a non-loopback host"); got != 1 {
		t.Errorf("http warning printed %d times, want 1 (shown even under --quiet)\n%s", got, stderr)
	}
	if !strings.Contains(stderr, "10.0.0.5:8000") {
		t.Errorf("warning should name the host\n%s", stderr)
	}
}

func TestNoInsecureHTTPNoteForLoopback(t *testing.T) {
	e := newCLIEnv(t)
	srv, _ := compatServer(t) // http://127.0.0.1:port
	writeCompatConfig(t, e.homeDir, compatConfig(srv.URL, ""))
	if err := e.run("--staged", "--no-cache"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(e.errOut.String(), "non-loopback") {
		t.Errorf("loopback http must not warn\n%s", e.errOut.String())
	}
}

// ---------- MINOR 2 (acceptance): api_key_env beats api_key on the wire ----------

func TestReviewAPIKeyEnvBeatsAPIKeyOnWire(t *testing.T) {
	e := newCLIEnv(t)
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "object": "chat.completion", "created": 1, "model": "m",
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": compatFindings}}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("PINNED_KEY", "env-wins")
	writeCompatConfig(t, e.homeDir, compatConfig(srv.URL, "    api_key: file-loses\n    api_key_env: PINNED_KEY\n"))
	if err := e.run("--staged", "--no-cache"); err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", err, e.errOut.String())
	}
	if auth != "Bearer env-wins" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer env-wins")
	}
}

// ---------- Review fix round 2 ----------

// commit and summary send the diff to the same endpoint, so the plain-http
// warning must fire there too. --show-prompt stops before the provider call
// but after the note, so the unreachable 10.x host is never contacted.
func TestInsecureHTTPNoteOnCommitAndSummary(t *testing.T) {
	for _, sub := range []string{"commit", "summary"} {
		t.Run(sub, func(t *testing.T) {
			e := newCLIEnv(t)
			writeCompatConfig(t, e.homeDir, compatConfig("http://10.0.0.5:8000", ""))
			if err := e.run(sub, "--show-prompt", "--quiet", "--yes"); err != nil {
				t.Fatalf("%s: %v\nstderr:\n%s", sub, err, e.errOut.String())
			}
			stderr := e.errOut.String()
			if got := strings.Count(stderr, "plain http to a non-loopback host"); got != 1 {
				t.Errorf("%s: http warning printed %d times, want 1\n%s", sub, got, stderr)
			}
			if strings.Contains(stderr, "cost is shown as unknown") {
				t.Errorf("%s shows no cost, so no unknown-cost note\n%s", sub, stderr)
			}
		})
	}
}

// dry-run must not swallow a provider construction error: with api_key_env
// pointing at an unset variable it reports the failure, keeps the cost
// honest ("unknown", never $0.0000) and still warns about plain http.
func TestDryRunProviderErrorIsReported(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("CB_TEST_UNSET_KEY", "")
	writeCompatConfig(t, e.homeDir, compatConfig("http://10.0.0.5:8000", "    api_key_env: CB_TEST_UNSET_KEY\n"))
	if err := e.run("dry-run", "--staged"); err != nil {
		t.Fatalf("dry-run: %v\nstderr:\n%s", err, e.errOut.String())
	}
	stderr := e.errOut.String()
	if !strings.Contains(stderr, "Provider unavailable") || !strings.Contains(stderr, "CB_TEST_UNSET_KEY") {
		t.Errorf("dry-run must surface the provider error\n%s", stderr)
	}
	if !strings.Contains(stderr, "plain http to a non-loopback host") {
		t.Errorf("dry-run must still warn about plain http\n%s", stderr)
	}
	all := e.out.String() + stderr
	if !strings.Contains(all, "Cost estimate:       unknown") {
		t.Errorf("cost must read unknown when the provider cannot be built\n%s", all)
	}
	if strings.Contains(all, "$0.0000") {
		t.Errorf("cost must not be $0.0000\n%s", all)
	}
}

// The posting path (Action `comment` mode) prints the footer with the
// unknown cost, not $0.0000.
func TestRemotePRPostingUnknownCost(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	srv, hits := compatServer(t)
	writeCompatConfig(t, e.homeDir, compatConfig(srv.URL, ""))
	r := &fakeGH{
		whoami:     "tester",
		prMeta:     prMetaJSON("contributor", "stable"),
		commitsSeq: []string{`{"commits":[{"oid":"stable"}]}`},
		diff:       sampleDiff,
	}

	oldWd, _ := os.Getwd()
	_ = os.Chdir(e.repoRoot)
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	var errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetErr(&errBuf)
	if err := runRemotePR(cmd, "42", remotePRFlags{}, r); err != nil {
		t.Fatalf("remote pr (posting): %v\nstderr:\n%s", err, errBuf.String())
	}
	if *hits != 1 {
		t.Fatalf("server saw %d requests, want 1", *hits)
	}
	stderr := errBuf.String()
	if !strings.Contains(stderr, "unknown") {
		t.Errorf("posting footer must show unknown cost\n%s", stderr)
	}
	if strings.Contains(stderr, "$0.0000") {
		t.Errorf("posting footer must not show $0.0000\n%s", stderr)
	}
	if got := strings.Count(stderr, "cost is shown as unknown"); got != 1 {
		t.Errorf("unknown-cost note printed %d times, want 1\n%s", got, stderr)
	}
}
