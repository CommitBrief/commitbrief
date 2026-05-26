package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/version"
)

type globalFlags struct {
	json     bool
	markdown bool
	output   string
	noCache  bool
	yes      bool
	verbose  bool
	quiet    bool
	lang     string
	provider string
	model    string
	color    string
}

var global globalFlags

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "commitbrief",
		Short:         "Local LLM-powered code review of git diffs",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Info(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReview(cmd, reviewScope)
		},
	}

	flags := cmd.PersistentFlags()
	flags.BoolVar(&global.json, "json", false, "emit machine-readable JSON output")
	flags.BoolVar(&global.markdown, "markdown", false, "emit plain markdown (no ANSI)")
	flags.StringVarP(&global.output, "output", "o", "", "write output to file instead of stdout")
	flags.BoolVar(&global.noCache, "no-cache", false, "bypass cache (read and write)")
	flags.BoolVarP(&global.yes, "yes", "y", false, "auto-confirm prompts (pre-send guard, init overwrite)")
	flags.BoolVarP(&global.verbose, "verbose", "v", false, "show token/cost/latency footer")
	flags.BoolVarP(&global.quiet, "quiet", "q", false, "suppress info messages on stderr")
	flags.StringVar(&global.lang, "lang", "", "override output language (e.g. tr, en)")
	flags.StringVar(&global.provider, "provider", "", "override configured provider")
	flags.StringVar(&global.model, "model", "", "override configured model")
	flags.StringVar(&global.color, "color", "auto", "color output: auto, always, never")

	// Review-scope flags live on root so `commitbrief --staged` works without
	// a subcommand. They are re-bound on `dry-run` (see newDryRunCmd) since
	// it walks the same pipeline.
	bindScopeFlags(cmd)

	cmd.AddCommand(
		newInitCmd(),
		newSetupCmd(),
		newDryRunCmd(),
		newListCmd(),
		newCompressCmd(),
	)
	return cmd
}

// Execute is the package entry point used by cmd/commitbrief/main.go.
func Execute() {
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
