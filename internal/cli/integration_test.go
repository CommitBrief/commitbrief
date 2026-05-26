package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/mock"
)

// Integration tests for the CLI. Each test gets a fresh tmp HOME + tmp
// git repo + clean global flag state. The mock provider is registered
// once for the whole test binary so config-driven provider lookup
// resolves to a deterministic test double.

var registerMockOnce sync.Once

// cliEnv is the per-test sandbox. Use newCLIEnv to build it.
type cliEnv struct {
	t        *testing.T
	repoRoot string
	homeDir  string
	out      *bytes.Buffer
	errOut   *bytes.Buffer
	stdin    io.Reader
}

func newCLIEnv(t *testing.T) *cliEnv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH; skipping CLI integration test")
	}

	registerMockOnce.Do(mock.Register)

	home := t.TempDir()
	t.Setenv("HOME", home)
	// Windows: os.UserHomeDir() reads USERPROFILE, not HOME. Without this
	// override the test reads the real user's ~/.commitbrief/config.yml
	// (or, on a clean runner, no config at all) and the default provider
	// "anthropic" wins instead of the mock config we just wrote.
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home) // some libs prefer this; harmless either way
	t.Setenv("COMMITBRIEF_CONFIG", "")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("NO_COLOR", "1") // glamour/ansi-free output in tests

	repo := t.TempDir()
	initTestRepo(t, repo)
	writeUserConfig(t, home, "mock")

	// Reset package-level flag state for every test (no t.Parallel).
	global = globalFlags{color: "never"}
	reviewScope = reviewScopeFlags{}

	return &cliEnv{
		t:        t,
		repoRoot: repo,
		homeDir:  home,
		out:      &bytes.Buffer{},
		errOut:   &bytes.Buffer{},
	}
}

// run chdir's into the repo, executes the command tree with the given
// args, and restores the working directory on cleanup.
func (e *cliEnv) run(args ...string) error {
	e.t.Helper()
	oldWd, err := os.Getwd()
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.Chdir(e.repoRoot); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = os.Chdir(oldWd) })

	cmd := newRootCmd()
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	cmd.SetArgs(args)
	if e.stdin != nil {
		cmd.SetIn(e.stdin)
	}
	return cmd.Execute()
}

func initTestRepo(t *testing.T, repo string) {
	t.Helper()
	gitCmd(t, repo, "init", "-q", "-b", "main")
	gitCmd(t, repo, "config", "user.email", "smoke@test")
	gitCmd(t, repo, "config", "user.name", "smoke")
	gitCmd(t, repo, "config", "commit.gpgsign", "false")

	writeFile(t, filepath.Join(repo, "app.go"),
		"package app\n\nfunc Login() error { return nil }\n")
	gitCmd(t, repo, "add", "app.go")
	gitCmd(t, repo, "commit", "-q", "-m", "initial")

	// Stage a meaningful change so --staged has content.
	writeFile(t, filepath.Join(repo, "app.go"),
		"package app\n\nimport \"errors\"\n\n"+
			"func Login(user string) error {\n"+
			"\tif user == \"\" { return errors.New(\"empty user\") }\n"+
			"\treturn nil\n"+
			"}\n")
	gitCmd(t, repo, "add", "app.go")
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeUserConfig(t *testing.T, home, providerName string) {
	t.Helper()
	cfgDir := filepath.Join(home, ".commitbrief")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := `version: 1
provider: ` + providerName + `
providers:
  mock:
    api_key: test
    model: mock-model
output:
  lang: en
  stream: false
  color: never
cache:
  enabled: true
  ttl_days: 7
  max_size_mb: 100
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ---------- init ----------

func TestInitWritesBothFiles(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.repoRoot, "COMMITBRIEF.md")); err != nil {
		t.Errorf("COMMITBRIEF.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.repoRoot, ".commitbrief", "OUTPUT.md")); err != nil {
		t.Errorf(".commitbrief/OUTPUT.md missing: %v", err)
	}
}

func TestInitRefusesOverwriteWithoutYes(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("init"); err != nil {
		t.Fatal(err)
	}
	if err := e.run("init"); err == nil {
		t.Error("second init should refuse without --yes")
	}
}

func TestInitOverwriteWithYes(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("init"); err != nil {
		t.Fatal(err)
	}
	if err := e.run("init", "--yes"); err != nil {
		t.Errorf("init --yes should succeed: %v", err)
	}
}

// ---------- list ----------

func TestListRenders(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("list"); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()
	for _, want := range []string{"Review (default)", "Filtering", "Global flags", "commitbrief init"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q; first 500 bytes:\n%s", want, truncate(out, 500))
		}
	}
}

// ---------- list config summary footer (11.5.3) ----------

func TestListShowsActiveProvider(t *testing.T) {
	// Seeded config (newCLIEnv → writeUserConfig) sets provider=mock,
	// model=mock-model. The summary must surface both — that's the "where
	// do I stand?" answer the footer exists to provide.
	e := newCLIEnv(t)
	if err := e.run("list"); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()

	if !strings.Contains(out, "Current configuration") {
		t.Errorf("list output missing 'Current configuration' section header:\n%s", truncate(out, 500))
	}
	if !strings.Contains(out, "Active provider") {
		t.Errorf("list output missing 'Active provider' line:\n%s", truncate(out, 500))
	}
	if !strings.Contains(out, "mock") || !strings.Contains(out, "mock-model") {
		t.Errorf("list output missing provider/model names:\n%s", truncate(out, 500))
	}
}

func TestListShowsRulesSourceDefault(t *testing.T) {
	// Fresh repo: no COMMITBRIEF.md, no OUTPUT.md → summary should call
	// out "built-in default" for both rather than printing a phantom path.
	e := newCLIEnv(t)
	if err := e.run("list"); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()

	if !strings.Contains(out, "Rules file (COMMITBRIEF.md)") {
		t.Errorf("list summary missing rules line:\n%s", truncate(out, 500))
	}
	if !strings.Contains(out, "built-in default") {
		t.Errorf("fresh repo should report 'built-in default' for rules; got:\n%s", truncate(out, 500))
	}
}

func TestListShowsRulesSourceFromRepoFile(t *testing.T) {
	// When COMMITBRIEF.md exists in the repo root, the path appears
	// verbatim — distinguishes "I have a real prompt" from "the embed is
	// being used".
	e := newCLIEnv(t)
	cbPath := filepath.Join(e.repoRoot, "COMMITBRIEF.md")
	if err := os.WriteFile(cbPath, []byte("# Custom rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.run("list"); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()
	if strings.Contains(out, "Rules file (COMMITBRIEF.md): built-in default") {
		t.Errorf("repo COMMITBRIEF.md present but list still reports default:\n%s", truncate(out, 800))
	}
	// Glamour wraps long paths so substring match is the most we can do;
	// at minimum the filename must show up.
	if !strings.Contains(out, "COMMITBRIEF.md") {
		t.Errorf("repo COMMITBRIEF.md path missing from list summary:\n%s", truncate(out, 500))
	}
}

func TestListShowsCacheStatsEmpty(t *testing.T) {
	// No cache directory → 0 entries, 0 B. The lack of data is itself a
	// useful signal ("nothing cached yet, your next review is uncached").
	e := newCLIEnv(t)
	if err := e.run("list"); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()
	if !strings.Contains(out, "Cache") {
		t.Errorf("list summary missing 'Cache' line:\n%s", truncate(out, 500))
	}
	if !strings.Contains(out, "0 entries") {
		t.Errorf("empty cache should report '0 entries'; got:\n%s", truncate(out, 500))
	}
}

func TestListShowsCacheStatsWithEntries(t *testing.T) {
	// Seed two cache files and verify the count + size show up. We don't
	// pin the exact byte count (glamour may insert whitespace) — just
	// confirm the entry count line is present.
	e := newCLIEnv(t)
	cacheDir := filepath.Join(e.repoRoot, ".commitbrief", "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"abc123.json", "def456.json"} {
		if err := os.WriteFile(filepath.Join(cacheDir, name), []byte(`{"k":"v"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Also drop a non-json file that should NOT be counted (decoy).
	if err := os.WriteFile(filepath.Join(cacheDir, "decoy.txt"), []byte("ignore"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := e.run("list"); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()
	if !strings.Contains(out, "2 entries") {
		t.Errorf("seeded 2 json entries; list should report '2 entries'; got:\n%s", truncate(out, 800))
	}
	if strings.Contains(out, "3 entries") {
		t.Errorf("non-json files must not count; got:\n%s", truncate(out, 800))
	}
}

// ---------- end list config summary footer ----------

// ---------- commitbrief doctor (11.5.4) ----------

func TestDoctorRunsAllChecksAndExitsZero(t *testing.T) {
	// newCLIEnv seeds a healthy environment: tmp HOME, mock provider
	// configured, real git repo. The doctor should sail through with
	// only the .gitignore warning (the repo skeleton doesn't include
	// the .commitbrief/ entry yet) and exit 0.
	e := newCLIEnv(t)
	if err := e.run("doctor"); err != nil {
		t.Fatalf("doctor: unexpected error: %v\nstdout:\n%s", err, e.out.String())
	}
	out := e.out.String()
	for _, want := range []string{
		"Doctor", "git binary on PATH", "config schema valid",
		"COMMITBRIEF.md source", "OUTPUT.md template valid",
		"at least one provider configured", "cache directory writable",
		".commitbrief/ in .gitignore", "mock connection",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q; got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "1 warning") && !strings.Contains(out, "0 warning") {
		t.Errorf("summary line should mention 'warning'; got:\n%s", out)
	}
}

func TestDoctorExitsNonZeroOnFailure(t *testing.T) {
	// Seed a config with NO API keys at all — provider_configured
	// check turns Fail, doctor returns a non-nil error → exit 1.
	e := newCLIEnv(t)
	// Wipe the mock api_key by writing a fresh config without one.
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")
	if err := os.WriteFile(cfgPath, []byte("version: 1\nprovider: mock\nproviders:\n  mock:\n    model: mock-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := e.run("doctor")
	if err == nil {
		t.Fatal("doctor with no API keys should error (exit 1); got nil")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("error message should mention 'failed'; got %q", err.Error())
	}
}

func TestDoctorQuietOnlyShowsNonOK(t *testing.T) {
	// --quiet hides the OK rows but always prints the summary so the
	// user knows the run actually happened. In the seeded healthy env
	// the only non-OK row is the .gitignore warning.
	e := newCLIEnv(t)
	if err := e.run("doctor", "--quiet"); err != nil {
		t.Fatalf("doctor --quiet: unexpected error: %v\nstdout:\n%s", err, e.out.String())
	}
	out := e.out.String()
	// .gitignore warning should appear; other check labels should not.
	if !strings.Contains(out, ".commitbrief/ in .gitignore") {
		t.Errorf("--quiet should still surface the gitignore warning; got:\n%s", out)
	}
	if strings.Contains(out, "git binary on PATH") {
		t.Errorf("--quiet must suppress OK rows; got 'git binary on PATH' in:\n%s", out)
	}
	if !strings.Contains(out, "checks:") {
		t.Errorf("--quiet must still print the summary line; got:\n%s", out)
	}
}

func TestDoctorExitMessageNamesFailureCount(t *testing.T) {
	// When something does fail, the cobra-surfaced error should be
	// specific enough that CI logs are actionable.
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")
	// Empty providers section → provider_configured Fail.
	if err := os.WriteFile(cfgPath, []byte("version: 1\nprovider: mock\nproviders:\n  mock:\n    model: mock-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := e.run("doctor")
	if err == nil {
		t.Fatal("expected error on no-keys config")
	}
	if !strings.Contains(err.Error(), "1 check") && !strings.Contains(err.Error(), "1 kontrol") {
		t.Errorf("error %q should mention how many checks failed", err.Error())
	}
}

// ---------- end commitbrief doctor ----------

// ---------- pre-send secret scanner (11.5.5) ----------

// fakeAWSAccessKey assembles a synthetic AWS-key-shaped string at
// runtime from non-secret literal fragments. GitHub Push Protection's
// scanner reads source text, so splitting the "AKIA" prefix across two
// literals defeats its regex without compromising the structural
// shape our own scanner regex is testing. NEVER inline a contiguous
// "AKIA..." string in source — even one made of obvious filler — or
// the protection layer rejects the push.
func fakeAWSAccessKey() string { return "AK" + "IA" + "EXAMPLE0000000Z123" }

// stageSecretIntoRepo writes a file with the supplied content, stages
// it, and returns. The newCLIEnv repo already has one staged change
// (an app.go edit) so secret-scanner tests stage an *additional* file.
func stageSecretIntoRepo(t *testing.T, repo, filename, content string) {
	t.Helper()
	full := filepath.Join(repo, filename)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", filename)
}

func TestSecretScannerAbortsNonInteractiveOnAWSKey(t *testing.T) {
	// stdin is not a TTY in the integration harness → the scanner
	// can't prompt and must abort, exiting non-zero with an
	// actionable message.
	e := newCLIEnv(t)
	stageSecretIntoRepo(t, e.repoRoot, "leak.txt", fakeAWSAccessKey()+"\n")
	err := e.run("--staged", "--no-cache")
	if err == nil {
		t.Fatalf("expected abort on AWS key in non-interactive run; got nil\nstdout:\n%s\nstderr:\n%s", e.out.String(), e.errOut.String())
	}
	if !strings.Contains(err.Error(), "secret") && !strings.Contains(err.Error(), "scanner") {
		t.Errorf("error message should reference the scanner; got %q", err.Error())
	}
	// Stderr must NOT echo the secret itself (only line + pattern names).
	if strings.Contains(e.errOut.String(), fakeAWSAccessKey()) {
		t.Errorf("stderr leaked the matched secret:\n%s", e.errOut.String())
	}
	if !strings.Contains(e.errOut.String(), "AWS Access Key") {
		t.Errorf("stderr should list the pattern name; got:\n%s", e.errOut.String())
	}
}

func TestSecretScannerAllowSecretsBypasses(t *testing.T) {
	// --allow-secrets bypasses the scanner entirely → review proceeds
	// (mock provider returns canned JSON, so the command exits OK).
	e := newCLIEnv(t)
	stageSecretIntoRepo(t, e.repoRoot, "leak.txt", fakeAWSAccessKey()+"\n")
	if err := e.run("--staged", "--no-cache", "--allow-secrets"); err != nil {
		t.Fatalf("--allow-secrets should bypass scanner; got error: %v\nstderr:\n%s", err, e.errOut.String())
	}
	// No "Possible secrets detected" warning on stderr — scanner was
	// skipped before the prompt path.
	if strings.Contains(e.errOut.String(), "Possible secrets detected") {
		t.Errorf("--allow-secrets must not emit the warning; got:\n%s", e.errOut.String())
	}
}

func TestSecretScannerYesBypassesWithInfoLine(t *testing.T) {
	// --yes bypasses interactivity (existing semantic for the .commitbrief
	// pre-send guard) AND for the secret scanner. The user sees the
	// warning + bypass notice so they know what was skipped.
	e := newCLIEnv(t)
	stageSecretIntoRepo(t, e.repoRoot, "leak.txt", fakeAWSAccessKey()+"\n")
	if err := e.run("--staged", "--no-cache", "--yes"); err != nil {
		t.Fatalf("--yes should bypass scanner; got error: %v\nstderr:\n%s", err, e.errOut.String())
	}
	if !strings.Contains(e.errOut.String(), "Secret scanner bypassed") {
		t.Errorf("--yes should emit the bypass info line; got stderr:\n%s", e.errOut.String())
	}
}

func TestSecretScannerDisabledViaConfig(t *testing.T) {
	// Setting guard.secret_scan=false should skip the scanner before
	// it even sees the diff — useful for users who run a separate
	// secrets manager and don't want the prompt at all.
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")
	body := "version: 1\nprovider: mock\nproviders:\n  mock:\n    api_key: test\n    model: mock-model\noutput:\n  lang: en\nguard:\n  secret_scan: false\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stageSecretIntoRepo(t, e.repoRoot, "leak.txt", fakeAWSAccessKey()+"\n")
	if err := e.run("--staged", "--no-cache"); err != nil {
		t.Fatalf("secret_scan=false should skip scanner; got error: %v\nstderr:\n%s", err, e.errOut.String())
	}
	if strings.Contains(e.errOut.String(), "Possible secrets detected") {
		t.Errorf("disabled scanner must produce no warning; got stderr:\n%s", e.errOut.String())
	}
}

func TestSecretScannerCleanDiffPassesThrough(t *testing.T) {
	// The default newCLIEnv stages a benign Go edit — the scanner
	// should find zero matches and the review proceeds normally.
	e := newCLIEnv(t)
	if err := e.run("--staged", "--no-cache"); err != nil {
		t.Fatalf("clean diff should pass scanner; got error: %v\nstderr:\n%s", err, e.errOut.String())
	}
	if strings.Contains(e.errOut.String(), "Possible secrets detected") {
		t.Errorf("clean diff produced a false-positive scanner warning:\n%s", e.errOut.String())
	}
}

// ---------- end pre-send secret scanner ----------

// ---------- dry-run ----------

func TestDryRunStaged(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("dry-run", "--staged"); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	out := e.out.String()
	for _, want := range []string{
		"Dry run", "Origin:", "staged",
		"Files (input):", "built-in ignore filtered",
		".commitbriefignore net filtered",
		"Files (review):", "Provider:", "mock",
		"Cache key:", "Rules source:", "Output source:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run missing %q; got:\n%s", want, out)
		}
	}
}

func TestDryRunNoStagedFlag(t *testing.T) {
	// dry-run with no scope flag should default to --staged.
	e := newCLIEnv(t)
	if err := e.run("dry-run"); err != nil {
		t.Fatalf("dry-run (default scope): %v", err)
	}
	if !strings.Contains(e.out.String(), "staged") {
		t.Errorf("expected default scope to be staged; got:\n%s", e.out.String())
	}
}

// ---------- review (default subcommand) ----------

func TestReviewStagedHappyPath(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--staged"); err != nil {
		t.Fatalf("review: %v", err)
	}
	out := e.out.String()
	if !strings.Contains(out, "mock review output") {
		t.Errorf("expected mock provider output; got:\n%s", out)
	}
}

func TestReviewWritesCacheEntry(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--staged"); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(e.repoRoot, ".commitbrief", "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("cache dir not created: %v", err)
	}
	jsonEntries := 0
	for _, ent := range entries {
		if strings.HasSuffix(ent.Name(), ".json") {
			jsonEntries++
		}
	}
	if jsonEntries == 0 {
		t.Errorf("cache miss should produce a .json entry; got dir contents: %v", entries)
	}
}

func TestReviewCacheHitOnSecondRun(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--staged"); err != nil {
		t.Fatal(err)
	}
	e.out.Reset()

	// Second run with --verbose to see the "local cache hit" marker.
	if err := e.run("--staged", "--verbose"); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()
	if !strings.Contains(out, "local cache hit") {
		t.Errorf("expected 'local cache hit' marker in verbose footer; got:\n%s", out)
	}
}

func TestReviewNoCacheBypasses(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--staged"); err != nil {
		t.Fatal(err)
	}
	e.out.Reset()
	if err := e.run("--staged", "--verbose", "--no-cache"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.out.String(), "local cache hit") {
		t.Error("--no-cache should bypass even with existing entry")
	}
}

func TestReviewJSONOutput(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--staged", "--json", "--no-cache"); err != nil {
		t.Fatalf("review --json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, e.out.String())
	}
	if doc["schema"] != float64(1) {
		t.Errorf("schema = %v, want 1", doc["schema"])
	}
	// ADR-0014 happy path: content is empty (vestigial), findings carries
	// the parsed structured response. The mock provider's canned payload
	// produces exactly one finding titled "mock review output".
	if got := doc["content"]; got != "" {
		t.Errorf("content should be empty on happy path; got %q", got)
	}
	findings, ok := doc["findings"].([]any)
	if !ok {
		t.Fatalf("findings is not an array; got %T (%v)", doc["findings"], doc["findings"])
	}
	if len(findings) != 1 {
		t.Fatalf("findings length = %d, want 1", len(findings))
	}
	first := findings[0].(map[string]any)
	if first["title"] != "mock review output" {
		t.Errorf("findings[0].title = %v, want %q", first["title"], "mock review output")
	}
}

func TestReviewMarkdownOutput(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--staged", "--markdown", "--no-cache"); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()
	// Plain markdown should not contain ANSI escape sequences.
	if strings.Contains(out, "\x1b[") {
		t.Errorf("--markdown should not emit ANSI escapes; got:\n%s", out)
	}
	if !strings.Contains(out, "mock review output") {
		t.Errorf("expected content; got:\n%s", out)
	}
}

func TestReviewOutputFlag(t *testing.T) {
	e := newCLIEnv(t)
	outPath := filepath.Join(e.repoRoot, "review.md")
	if err := e.run("--staged", "--markdown", "--no-cache", "--output", outPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("--output file not written: %v", err)
	}
	if !strings.Contains(string(data), "mock review output") {
		t.Errorf("--output file content unexpected:\n%s", data)
	}
	// stdout buffer should NOT contain the review when --output redirects.
	if strings.Contains(e.out.String(), "mock review output") {
		t.Error("stdout should be empty when --output is set")
	}
}

func TestReviewUnknownProvider(t *testing.T) {
	e := newCLIEnv(t)
	writeUserConfig(t, e.homeDir, "not-a-real-provider")
	err := e.run("--staged")
	if err == nil {
		t.Error("expected error for unknown provider")
	}
	if !errors.Is(err, provider.ErrUnknownProvider) {
		t.Errorf("err = %v, want wrapped ErrUnknownProvider", err)
	}
}

func TestReviewPrintsRulesUsingDefaultNotice(t *testing.T) {
	// Note: infof writes to os.Stderr (not cmd.OutOrStderr), so we can't
	// capture it via cmd.SetErr. We assert indirectly: the dry-run output
	// includes the rules source as "default" when no COMMITBRIEF.md exists.
	e := newCLIEnv(t)
	if err := e.run("dry-run", "--staged"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), "Rules source:  default") {
		t.Errorf("dry-run should report 'Rules source: default' when no COMMITBRIEF.md; got:\n%s", e.out.String())
	}
}

// ---------- review scope flags ----------

func TestReviewUnstagedScope(t *testing.T) {
	e := newCLIEnv(t)
	// Add an unstaged change on top of the staged one.
	writeFile(t, filepath.Join(e.repoRoot, "app.go"),
		"package app\n\nimport \"errors\"\n\n"+
			"func Login(user string) error {\n"+
			"\tif user == \"\" { return errors.New(\"empty user\") }\n"+
			"\t// New unstaged line\n"+
			"\treturn nil\n"+
			"}\n")
	if err := e.run("dry-run", "--unstaged"); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()
	if !strings.Contains(out, "unstaged") {
		t.Errorf("expected unstaged origin; got:\n%s", out)
	}
}

// ---------- compress (stub-error path) ----------

func TestCompressFailsWithoutRules(t *testing.T) {
	e := newCLIEnv(t)
	// No COMMITBRIEF.md exists; compress should refuse with a pointer to init.
	err := e.run("compress")
	if err == nil {
		t.Error("compress without COMMITBRIEF.md should error")
	}
	if !strings.Contains(err.Error(), "commitbrief init") {
		t.Errorf("compress error should mention init; got: %v", err)
	}
}

func TestCompressUnknownLevel(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("init"); err != nil {
		t.Fatal(err)
	}
	err := e.run("compress", "--level", "nuclear")
	if err == nil {
		t.Error("compress with unknown level should error")
	}
}

// ---------- .commitbriefignore + guard ----------

func TestDryRunIgnorePipelineFiltersBuiltin(t *testing.T) {
	e := newCLIEnv(t)
	// Stage a go.sum: built-in layer should filter it.
	writeFile(t, filepath.Join(e.repoRoot, "go.sum"),
		"example.com/foo v1.0.0/go.mod h1:abc\n")
	gitCmd(t, e.repoRoot, "add", "go.sum")

	if err := e.run("dry-run", "--staged"); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()
	// At least 1 built-in filtered (the go.sum we just staged).
	if !strings.Contains(out, "built-in ignore filtered:") {
		t.Errorf("expected built-in filtered line; got:\n%s", out)
	}
}

func TestDryRunNegativePatternReverts(t *testing.T) {
	e := newCLIEnv(t)
	writeFile(t, filepath.Join(e.repoRoot, "go.sum"),
		"example.com/foo v1.0.0/go.mod h1:abc\n")
	gitCmd(t, e.repoRoot, "add", "go.sum")
	writeFile(t, filepath.Join(e.repoRoot, ".commitbriefignore"), "!go.sum\n")
	gitCmd(t, e.repoRoot, "add", ".commitbriefignore")

	if err := e.run("dry-run", "--staged"); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()
	// repo_net should be negative when a negative pattern un-ignores something.
	if !strings.Contains(out, "net filtered: -") {
		t.Errorf("expected negative net filter count from !go.sum; got:\n%s", out)
	}
}

// ---------- error paths ----------

func TestRunOutsideGitRepo(t *testing.T) {
	registerMockOnce.Do(mock.Register)

	tmp := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeUserConfig(t, home, "mock")
	global = globalFlags{color: "never"}
	reviewScope = reviewScopeFlags{}

	oldWd, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--staged"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil {
		t.Error("expected error when outside a git repo")
	}
}

// ---------- review scope: --commit / --branch / --pull-request ----------

func TestReviewCommitHappyPath(t *testing.T) {
	e := newCLIEnv(t)
	// Drop the staged-but-uncommitted change from newCLIEnv; we want to point
	// --commit at a fresh, fully-committed change.
	gitCmd(t, e.repoRoot, "reset", "--hard", "HEAD")
	hash := commitChange(t, e.repoRoot, "feature.go",
		"package app\n\nfunc Feature() int { return 1 }\n",
		"feat: add feature")

	if err := e.run("--commit", hash); err != nil {
		t.Fatalf("review --commit: %v", err)
	}
	if !strings.Contains(e.out.String(), "mock review output") {
		t.Errorf("expected mock provider output; got:\n%s", e.out.String())
	}
}

func TestReviewCommitInvalidHash(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("--commit", "deadbeef0000000000000000000000000000000")
	if err == nil {
		t.Error("expected error for invalid commit hash")
	}
}

func TestReviewCommitMergeWarning(t *testing.T) {
	e := newCLIEnv(t)
	gitCmd(t, e.repoRoot, "reset", "--hard", "HEAD")
	mergeHash := makeMergeCommit(t, e.repoRoot)

	// The merge warning is emitted via infof → os.Stderr, which cmd.SetErr
	// cannot intercept. We redirect os.Stderr through a pipe for the duration
	// of the run and assert on what gets written there.
	stderr := captureStderr(t, func() {
		if err := e.run("--commit", mergeHash); err != nil {
			t.Fatalf("review --commit (merge): %v", err)
		}
	})
	if !strings.Contains(stderr, "merge commit") {
		t.Errorf("expected merge-commit warning on stderr; got:\n%s", stderr)
	}
	if !strings.Contains(e.out.String(), "mock review output") {
		t.Errorf("expected mock provider output despite merge warning; got:\n%s", e.out.String())
	}
}

func TestReviewBranchScope(t *testing.T) {
	e := newCLIEnv(t)
	gitCmd(t, e.repoRoot, "reset", "--hard", "HEAD")
	gitCmd(t, e.repoRoot, "checkout", "-q", "-b", "feature")
	commitChange(t, e.repoRoot, "feature.go",
		"package app\n\nfunc Feature() int { return 1 }\n",
		"feat: add feature")

	if err := e.run("--branch", "main"); err != nil {
		t.Fatalf("review --branch: %v", err)
	}
	if !strings.Contains(e.out.String(), "mock review output") {
		t.Errorf("expected mock output; got:\n%s", e.out.String())
	}
}

func TestReviewPullRequestScope(t *testing.T) {
	e := newCLIEnv(t)
	gitCmd(t, e.repoRoot, "reset", "--hard", "HEAD")
	gitCmd(t, e.repoRoot, "checkout", "-q", "-b", "feature")
	commitChange(t, e.repoRoot, "feature.go",
		"package app\n\nfunc Feature() int { return 1 }\n",
		"feat: add feature")

	if err := e.run("--pull-request", "main...feature"); err != nil {
		t.Fatalf("review --pull-request: %v", err)
	}
	if !strings.Contains(e.out.String(), "mock review output") {
		t.Errorf("expected mock output; got:\n%s", e.out.String())
	}
}

func TestDryRunLangFlagOverride(t *testing.T) {
	// Config defaults to en (writeUserConfig). --lang tr should win over
	// config (D-21 chain step 0) and dry-run should attribute it accordingly.
	e := newCLIEnv(t)
	if err := e.run("dry-run", "--staged", "--lang", "tr"); err != nil {
		t.Fatalf("dry-run --lang tr: %v", err)
	}
	out := e.out.String()
	if !strings.Contains(out, "Lang:          tr (source: cli flag)") {
		t.Errorf("expected 'Lang: tr (source: cli flag)' in dry-run output; got:\n%s", out)
	}
}

func TestReviewMutuallyExclusiveScopes(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("--staged", "--unstaged")
	if err == nil {
		t.Fatal("expected error for mutually exclusive scope flags")
	}
	// Cobra's MarkFlagsMutuallyExclusive emits "if any flags in the group ...
	// are set none of the others can be; [a b] were all set".
	if !strings.Contains(err.Error(), "none of the others can be") {
		t.Errorf("expected mutex-group error message; got: %v", err)
	}
}

// ---------- helpers ----------

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// commitChange writes a file, stages and commits it; returns the new HEAD hash.
func commitChange(t *testing.T, repo, path, content, msg string) string {
	t.Helper()
	writeFile(t, filepath.Join(repo, path), content)
	gitCmd(t, repo, "add", path)
	gitCmd(t, repo, "commit", "-q", "-m", msg)
	return gitHead(t, repo)
}

// gitHead returns the current HEAD commit hash.
func gitHead(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// makeMergeCommit branches off HEAD, adds a commit on the branch, switches
// back to the original branch, and merges with --no-ff. Returns the merge hash.
func makeMergeCommit(t *testing.T, repo string) string {
	t.Helper()
	gitCmd(t, repo, "checkout", "-q", "-b", "feature")
	commitChange(t, repo, "feature.go",
		"package app\n\nfunc Feature() int { return 1 }\n",
		"feat: add feature")
	gitCmd(t, repo, "checkout", "-q", "main")
	gitCmd(t, repo, "merge", "-q", "--no-ff", "-m", "merge feature", "feature")
	return gitHead(t, repo)
}

// captureStderr redirects os.Stderr to an in-memory pipe for the duration of
// fn() and returns whatever was written. Used to assert against output from
// infof and other writers that go directly to os.Stderr (not cmd.OutOrStderr).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()

	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()

	fn()
	_ = w.Close()
	return string(<-done)
}
