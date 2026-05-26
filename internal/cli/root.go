package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

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
			return runReview(cmd.Context(), reviewScope)
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

	// review-scope flags live on root so `commitbrief --staged` works without
	// a subcommand name.
	rflags := cmd.Flags()
	rflags.BoolVarP(&reviewScope.staged, "staged", "s", false, "review staged changes (default)")
	rflags.BoolVarP(&reviewScope.unstaged, "unstaged", "u", false, "review unstaged changes")
	rflags.StringVarP(&reviewScope.file, "file", "f", "", "review changes in a single file")
	rflags.StringVarP(&reviewScope.commit, "commit", "c", "", "review changes in a commit hash")
	rflags.StringVar(&reviewScope.pr, "pull-request", "", "review a PR-style diff target...feature")
	rflags.StringVarP(&reviewScope.branch, "branch", "b", "", "review current branch vs target ref")

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
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
