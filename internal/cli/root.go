// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/logo"
	"github.com/CommitBrief/commitbrief/internal/ui"
	"github.com/CommitBrief/commitbrief/internal/version"
)

type globalFlags struct {
	json         bool
	markdown     bool
	output       string
	noCache      bool
	yes          bool
	verbose      bool
	quiet        bool
	compact      bool
	allowSecrets bool
	noCostCheck  bool
	copy         bool
	failOn       string
	lang         string
	provider     string
	model        string
	color        string
	cli          string   // --cli <name>; shorthand that resolves to provider "<name>-cli"
	files        []string // global --file (repeatable); path filter applied post-parse
	dirs         []string // global --dir (repeatable); prefix filter applied post-parse
	genMan       string   // hidden: --gen-man <dir> writes man pages and exits
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
	flags.BoolVar(&global.copy, "copy", false, "copy findings (severity, path, title, description) to the system clipboard via OSC 52 + native tool")
	flags.StringVar(&global.failOn, "fail-on", "", "exit 1 if any finding meets/exceeds severity (critical|high|medium|low|info|any|none)")
	flags.StringVar(&global.lang, "lang", "", "override output language (e.g. tr, en)")
	flags.StringVar(&global.provider, "provider", "", "override configured provider")
	flags.StringVar(&global.model, "model", "", "override configured model")
	flags.StringVar(&global.color, "color", "auto", "color output: auto, always, never")
	flags.StringSliceVarP(&global.files, "file", "f", nil, "review only these files (repeatable); combines with the active scope flag")
	flags.StringSliceVarP(&global.dirs, "dir", "d", nil, "review only files under these directories (repeatable); combines with the active scope flag")
	flags.StringVar(&global.cli, "cli", "", "use a locally-installed CLI tool (claude|gemini) as the review backend; shorthand for --provider <name>-cli")
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
	_ = cmd.PersistentFlags().MarkHidden("gen-man")

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
	)
	return cmd
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
	if ui.ColorEnabled(os.Stderr, ui.ColorAuto) {
		logo.Print(os.Stderr, version.Version)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := newRootCmd()
	if err := root.ExecuteContext(ctx); err != nil {
		// Best-effort error-prefix translation. appContext isn't built at
		// this layer (cobra surfaces errors from RunE before/instead of
		// resolveContext), so we honor only --lang and LANG env — the
		// remaining D-21 steps need configs we cannot safely load here.
		cat := pickErrorCatalog()
		fmt.Fprintln(os.Stderr, cat.T("common.error_prefix"), err)
		os.Exit(1)
	}
}

// pickErrorCatalog returns the i18n catalog used for the top-level "Error:"
// prefix when a command fails before appContext is resolved.
func pickErrorCatalog() *i18n.Catalog {
	code := global.lang
	if code == "" {
		// Read the first two letters of LANG (e.g. "tr_TR.UTF-8" → "tr").
		if env := os.Getenv("LANG"); len(env) >= 2 {
			code = env[:2]
		}
	}
	if cat, err := i18n.Load(code); err == nil {
		return cat
	}
	cat, _ := i18n.Load(i18n.DefaultLang)
	return cat
}
