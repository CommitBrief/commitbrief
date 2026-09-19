// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/lang"
	"github.com/CommitBrief/commitbrief/internal/logo"
	"github.com/CommitBrief/commitbrief/internal/ui"
	"github.com/CommitBrief/commitbrief/internal/version"
)

// sandboxRerunDefault is the rerun count used when --sandbox-rerun is passed
// without an explicit value. Five reruns is enough to surface a mixed
// pass+fail (the early-exit means a flaky test usually confirms in 2) while
// keeping a deterministic all-pass/all-fail campaign cheap.
const sandboxRerunDefault = 5

type globalFlags struct {
	json           bool
	markdown       bool
	output         string
	noCache        bool
	yes            bool
	verbose        bool
	quiet          bool
	compact        bool
	allowSecrets   bool
	noCostCheck    bool
	noFlaky        bool
	sandboxRerun   int  // --sandbox-rerun[=N]; re-run a statically flagged flaky candidate N times in isolation to confirm/refute it (ADR-0022); 0 = off, bare flag = sandboxRerunDefault
	noSandbox      bool // internal: agent-facing paths (mcp, guard) force sandbox rerun off (ADR-0033); never a registered flag
	noArchitecture bool // --no-architecture; skip architecture-aware review even if architecture.json exists (ADR-0030)
	updateBaseline bool // --update-baseline; absorb the current findings into .commitbrief/baseline.json instead of filtering (ADR-0027)
	noBaseline     bool // --no-baseline; ignore the baseline for this run (ADR-0027)
	copy           bool
	suggestCommit  bool
	commitType     string // commit: --type <format>; "" → commit.type config → "plain"
	commitGen      int    // commit: --generate <N>; 0 → commit.generate config → 1
	failOn         string
	minSeverity    string
	lang           string
	provider       string
	model          string
	color          string
	// --ignore-unknown-config; proceed past a config key the schema has no
	// field for, warning about each one on stderr. A flag and not a config
	// key on purpose: the config file is what is failing.
	ignoreUnknownConfig bool
	timeout             string   // --timeout <dur|seconds>; bounds the whole command run and raises provider-internal caps; "" / "0" = built-in behavior
	cli                 string   // --cli <name>; shorthand that resolves to provider "<name>-cli"
	withContext         bool     // --with-context; CLI providers only — let the host CLI read project files beyond the diff (ADR-0017)
	showPrompt          bool     // --show-prompt; print the assembled system+user prompt and exit (no provider call)
	files               []string // global --file (repeatable); path filter applied post-parse
	dirs                []string // global --dir (repeatable); prefix filter applied post-parse
	excludeFiles        []string // global --exclude-file (repeatable); path denylist applied after --file/--dir (ADR-0035)
	excludeDirs         []string // global --exclude-dir (repeatable); dir denylist applied after --file/--dir (ADR-0035)
	// Commit-level filters (ADR-0035). Any of authors/committers/startDate/
	// endDate/text switches diff acquisition from `git diff` to a commit
	// walk; merges and maxCommits only modify such a walk.
	authors    []string // --author (repeatable); matches name or email, case-insensitive
	committers []string // --committer (repeatable)
	startDate  string   // --start-date YYYY-MM-DD (inclusive)
	endDate    string   // --end-date YYYY-MM-DD (inclusive; expanded to end-of-day)
	text       string   // --text; matches the commit message or a branch name
	maxCommits int      // --max-commits; 0 → git.DefaultMaxCommits
	merges     bool     // --merges; include merge commits (default: excluded)
	genMan     string   // hidden: --gen-man <dir> writes man pages and exits
	genSurface string   // hidden: --gen-surface <file> writes the surface inventory and exits
}

var global globalFlags

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "commitbrief",
		Short:         "Local LLM-powered code review of git diffs",
		SilenceUsage:  true,
		SilenceErrors: true,
		// version.Info() already starts with "commitbrief X.Y.Z (commit …,
		// built …)" — cobra's default template prefixes "{cmd.Use} version "
		// which would print "commitbrief version commitbrief 0.7.0 …".
		// Override with a bare template so --version is just the Info() string.
		Version: version.Info(),
		// PersistentPreRunE fires before every command's RunE. We use it as
		// the gen-man interception point so `commitbrief --gen-man <dir>` (or
		// even attached to a subcommand) short-circuits to man-page emission
		// instead of running a review. os.Exit(0) is the deliberate end-state.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if global.genSurface != "" {
				if err := writeSurface(cmd.Root(), global.genSurface); err != nil {
					return fmt.Errorf("gen-surface: %w", err)
				}
				fmt.Fprintf(os.Stderr, "wrote surface inventory to %s\n", global.genSurface)
				os.Exit(0)
			}
			if global.genMan == "" {
				return nil
			}
			header := &doc.GenManHeader{Title: "COMMITBRIEF", Section: "1"}
			if err := doc.GenManTree(cmd.Root(), header, global.genMan); err != nil {
				return fmt.Errorf("gen-man: %w", err)
			}
			fmt.Fprintf(os.Stderr, "wrote man pages to %s\n", global.genMan)
			os.Exit(0)
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReview(cmd, reviewScope, nil)
		},
	}
	cmd.SetVersionTemplate("{{.Version}}\n")

	flags := cmd.PersistentFlags()
	flags.BoolVar(&global.json, "json", false, "emit machine-readable JSON output")
	flags.BoolVar(&global.markdown, "markdown", false, "emit plain markdown (no ANSI)")
	flags.StringVarP(&global.output, "output", "o", "", "write output to file instead of stdout")
	flags.BoolVar(&global.noCache, "no-cache", false, "bypass cache (read and write)")
	flags.BoolVarP(&global.yes, "yes", "y", false, "auto-confirm prompts (pre-send guard, init overwrite)")
	flags.BoolVarP(&global.verbose, "verbose", "v", false, "show token/cost/latency footer")
	flags.BoolVarP(&global.quiet, "quiet", "q", false, "suppress info messages on stderr")
	flags.BoolVar(&global.compact, "compact", false, "one-line per finding (dense review output)")
	flags.BoolVar(&global.allowSecrets, "allow-secrets", false, "bypass the pre-send secret scanner (use with care)")
	flags.BoolVar(&global.noCostCheck, "no-cost-check", false, "skip the pre-send cost estimate prompt")
	flags.BoolVar(&global.noFlaky, "no-flaky", false, "skip the deterministic flaky-test detector (ADR-0022)")
	flags.IntVar(&global.sandboxRerun, "sandbox-rerun", 0, "confirm flagged flaky tests by re-running each in isolation N times (ADR-0022); mixed pass+fail = confirmed flaky, all-fail = real failure, all-pass = transient. 0 = off; bare --sandbox-rerun uses N="+strconv.Itoa(sandboxRerunDefault)+". Requires a bound rerun executor")
	// Optional-value flag: `--sandbox-rerun` (no value) opts in with the
	// default N; `--sandbox-rerun=5` sets N explicitly; absent leaves the
	// config/default. NoOptDefVal is what makes the value optional in pflag.
	if f := flags.Lookup("sandbox-rerun"); f != nil {
		f.NoOptDefVal = strconv.Itoa(sandboxRerunDefault)
	}
	flags.BoolVar(&global.noArchitecture, "no-architecture", false, "skip architecture-aware review (do not read architecture.json into the prompt) for this run (ADR-0030)")
	flags.BoolVar(&global.updateBaseline, "update-baseline", false, "rewrite .commitbrief/baseline.json from the current findings (accepts them all) instead of filtering this run; user-private, gitignored (ADR-0027)")
	flags.BoolVar(&global.noBaseline, "no-baseline", false, "ignore the signal-control baseline for this run (show everything, even baselined findings)")
	flags.BoolVar(&global.copy, "copy", false, "copy findings (severity, path, title, description) to the system clipboard via OSC 52 + native tool")
	cmd.MarkFlagsMutuallyExclusive("update-baseline", "no-baseline")
	flags.BoolVar(&global.suggestCommit, "suggest-commit", false, "after the review, suggest a Conventional Commit message for the staged diff (requires --staged; prints to stdout; not with --json/--markdown/--output)")
	flags.StringVar(&global.failOn, "fail-on", "", "exit 1 if any finding meets/exceeds severity (critical|high|medium|low|info|any|none)")
	flags.StringVar(&global.minSeverity, "min-severity", "", "hide findings below this severity in the rendered output (critical|high|medium|low|info); --json and --fail-on still see the full set")
	flags.StringVar(&global.lang, "lang", "", "AI output language (e.g. tr, fr); the CLI interface localizes for en/tr only, output for any recognized language. Resolution: --lang → repo config → user config → English")
	flags.StringVar(&global.provider, "provider", "", "override configured provider")
	flags.StringVar(&global.model, "model", "", "override configured model")
	flags.StringVar(&global.color, "color", "auto", "color output: auto, always, never")
	flags.BoolVar(&global.ignoreUnknownConfig, "ignore-unknown-config", false, "continue when config.yml holds a key the schema doesn't define, instead of failing; every ignored key is still named in a warning on stderr (the setting has no effect — fix the key)")
	flags.StringVar(&global.timeout, "timeout", "", "bound the whole run to this `duration` — 90s, 10m, 1h30m, or a bare number of seconds (600). Covers diff, provider call, render, and time spent at a confirmation prompt. Also RAISES the provider's own cap (claude/gemini/codex-cli 5m, ollama 5m, Anthropic SDK 10m), which a deadline alone cannot do. Unset or 0 keeps those built-ins. Resolution: --timeout → review.timeout → built-in")
	flags.StringSliceVarP(&global.files, "file", "f", nil, "review only these files or globs (e.g. `*.go`, `internal/**/*.ts`; repeatable, one pattern per flag — patterns can't be comma-joined); combines with the active scope flag")
	flags.StringSliceVarP(&global.dirs, "dir", "d", nil, "review only files under these directories or matching dir globs (e.g. `internal/**`; repeatable, one pattern per flag); combines with the active scope flag")
	flags.StringSliceVar(&global.excludeFiles, "exclude-file", nil, "skip these files or globs (repeatable, one pattern per flag); same matching rules as --file, applied after it so an exclusion wins")
	flags.StringSliceVar(&global.excludeDirs, "exclude-dir", nil, "skip files under these directories or matching dir globs (repeatable, one pattern per flag); applied after --dir so an exclusion wins")
	// Commit-level filters (ADR-0035). Long-form only: the short-flag
	// namespace is reserved for the flags that were already common.
	flags.StringSliceVar(&global.authors, "author", nil, "review only commits authored by these people (repeatable; matches name or email, case-insensitive). Switches the scope to a commit walk")
	flags.StringSliceVar(&global.committers, "committer", nil, "review only commits committed by these people (repeatable; matches name or email, case-insensitive)")
	flags.StringVar(&global.startDate, "start-date", "", "review only commits on or after this date (YYYY-MM-DD, inclusive)")
	flags.StringVar(&global.endDate, "end-date", "", "review only commits on or before this date (YYYY-MM-DD, inclusive)")
	flags.StringVar(&global.text, "text", "", "review only commits whose message contains this text, plus commits unique to a branch whose name contains it (case-insensitive)")
	flags.IntVar(&global.maxCommits, "max-commits", 0, "cap how many matching commits enter the review (0 = "+strconv.Itoa(git.DefaultMaxCommits)+"); only meaningful with another commit filter")
	flags.BoolVar(&global.merges, "merges", false, "include merge commits in a commit-filtered review (excluded by default); only meaningful with another commit filter")
	flags.StringVar(&global.cli, "cli", "", "use a locally-installed CLI tool (claude|gemini|codex) as the review backend; shorthand for --provider <name>-cli")
	flags.BoolVar(&global.withContext, "with-context", false, "let the CLI provider read project files beyond the diff to ground the review (CLI providers only; the host CLI's agent reads your repo — see --help)")
	flags.BoolVar(&global.showPrompt, "show-prompt", false, "print the exact system + user prompt that would be sent, then exit (no provider call, no cost)")
	cmd.MarkFlagsMutuallyExclusive("provider", "cli")
	// UC-07: CLI providers emit pre-formatted plain text that goes
	// straight to the user. --json / --markdown drive structured
	// renderers that don't apply to a CLI provider's response (and
	// would silently strip the formatting we just paid the host CLI
	// for). Surface this as a cobra-level conflict instead of letting
	// the user discover it via mangled output.
	cmd.MarkFlagsMutuallyExclusive("cli", "json")
	cmd.MarkFlagsMutuallyExclusive("cli", "markdown")

	// Hidden: drives scripts/manpage.sh; not part of the user-visible surface.
	flags.StringVar(&global.genMan, "gen-man", "", "generate man pages into <dir> and exit (hidden)")
	// Hidden: drives scripts/docs-gen.sh; not part of the user-visible surface.
	flags.StringVar(&global.genSurface, "gen-surface", "", "write the CLI surface inventory to <file> and exit (hidden)")
	_ = cmd.PersistentFlags().MarkHidden("gen-man")
	_ = cmd.PersistentFlags().MarkHidden("gen-surface")

	// Review-scope flags live on root so `commitbrief --staged` works without
	// a subcommand. They are re-bound on `dry-run` (see newDryRunCmd) since
	// it walks the same pipeline.
	bindScopeFlags(cmd)

	cmd.AddCommand(
		newInitCmd(),
		newSetupCmd(),
		newProvidersCmd(),
		newConfigCmd(),
		newDoctorCmd(),
		newInstallHookCmd(),
		newDryRunCmd(),
		newListCmd(),
		newCompressCmd(),
		newCacheCmd(),
		newDiffCmd(),
		newRemoteCmd(),
		newCommitCmd(),
		newSummaryCmd(),
		newMCPCmd(),
		newGuardCmd(),
		newUpgradeCmd(),
		newMapCmd(),
		newLeaksCmd(),
	)
	return cmd
}

// versionFlagRequested reports whether args contain the --version flag
// before a "--" terminator. Cobra owns the flag itself; Execute only
// peeks so it can suppress the branding logo for `commitbrief --version`
// and keep that invocation's output a single parseable line.
func versionFlagRequested(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--version" {
			return true
		}
	}
	return false
}

// Execute is the package entry point used by cmd/commitbrief/main.go.
func Execute() {
	// UC-18: on Windows the VT100 escape mode needs to be opted into
	// before any ANSI codes hit stdout/stderr — the unix build of
	// EnableANSI is a documented no-op, so calling unconditionally
	// costs nothing on POSIX. Errors are intentionally swallowed:
	// failure here just means colors won't render correctly on a
	// legacy console, and we'd rather degrade silently than abort
	// the review.
	_ = ui.EnableANSI(os.Stdout)
	_ = ui.EnableANSI(os.Stderr)

	// Branding: render the CommitBrief logo on every run so the mark
	// is the first thing the user sees. Writes to stderr only so a
	// piped stdout (`commitbrief --json | jq`, `--markdown > file`)
	// stays uncorrupted; gated on a TTY-capable stderr so redirected
	// CI logs don't fill up with raw 24-bit color escapes. The version
	// string is the resolved value (ldflags-injected at release time,
	// debug.BuildInfo for `go install`, or "dev" for ad-hoc builds).
	//
	// Suppressed for `--version`: that flag is meant to emit a single
	// machine-parseable line — cobra prints version.Info() to stdout —
	// so the logo (which lands on stderr above it on a TTY) must not
	// appear. We peek os.Args because the logo prints before cobra
	// parses flags; --help and every other invocation keep the logo.
	if ui.ColorEnabled(os.Stderr, ui.ColorAuto) && !versionFlagRequested(os.Args[1:]) {
		logo.Print(os.Stderr, version.Version)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := newRootCmd()
	// Default-command expansion: a truly bare `commitbrief` (no args) is
	// rewritten to the configured `command.default` token list, so a user
	// can make `commitbrief` mean e.g. `--unstaged --cli gemini`. Any
	// explicit flag or subcommand bypasses this — the user is being
	// explicit. Empty/unset default leaves the built-in `--staged` behavior
	// untouched. See config.CommandConfig.
	if expanded, ok := expandDefault(os.Args[1:], loadDefaultCommand()); ok {
		root.SetArgs(expanded)
	}
	if err := root.ExecuteContext(ctx); err != nil {
		// Best-effort error-prefix translation. appContext isn't built at
		// this layer (cobra surfaces errors from RunE before/instead of
		// resolveContext), so we honor only the --lang flag — the remaining
		// chain steps need configs we cannot safely load here (ADR-0021).
		cat := pickErrorCatalog()
		fmt.Fprintln(os.Stderr, cat.T("common.error_prefix"), err)
		os.Exit(1)
	}
}

// expandDefault decides the args cobra should run for a bare invocation.
// When rawArgs is empty AND defaultCmd has at least one token, it returns
// the whitespace-split default and true. Otherwise it returns rawArgs
// unchanged and false (so any explicit flag/subcommand, or an empty
// default, leaves behavior exactly as before). Kept pure for testing —
// the config load lives in loadDefaultCommand.
func expandDefault(rawArgs []string, defaultCmd string) ([]string, bool) {
	if len(rawArgs) > 0 {
		return rawArgs, false
	}
	tokens := strings.Fields(defaultCmd)
	if len(tokens) == 0 {
		return rawArgs, false
	}
	return tokens, true
}

// loadDefaultCommand best-effort reads config.command.default for the
// pre-parse expansion. It loads the same global+repo config layers as
// resolveContext but swallows errors and returns "" — a malformed config
// must not break a bare `commitbrief`; resolveContext will surface the
// real error once the command actually runs.
func loadDefaultCommand() string {
	repoRoot := ""
	if root, err := git.FindRepo(""); err == nil {
		repoRoot = root
	}
	globalPath, repoPath := configFilePaths(repoRoot)
	cfg, err := config.Load(globalPath, repoPath)
	if err != nil {
		return ""
	}
	return cfg.Command.Default
}

// pickErrorCatalog returns the i18n catalog used for the top-level "Error:"
// prefix when a command fails before appContext is resolved.
func pickErrorCatalog() *i18n.Catalog {
	// Early-error catalog: before appContext exists we only have the --lang
	// flag (no config loaded yet), so honor it when it names a UI-translated
	// language and fall back to English otherwise. The system locale is not
	// consulted (ADR-0021).
	if cat, err := i18n.Load(lang.UICatalogFor(global.lang)); err == nil {
		return cat
	}
	cat, _ := i18n.Load(i18n.DefaultLang)
	return cat
}
