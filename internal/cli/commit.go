// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/cache"
	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/guard"
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

// commitGenMax bounds --generate so a typo can't fan out an unbounded
// (and unboundedly priced) request. Ten distinct messages is already well
// past what a human picks from.
const commitGenMax = 10

// newCommitCmd is the `commitbrief commit` entry point (ADR-0019): generate
// a commit message from the staged diff and, on confirmation, run
// `git commit`. It is the one command that writes to git — every other path
// is read-only (PRD NG4). Staged-only by definition; it does not bind the
// --staged/--unstaged scope flags.
func newCommitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Generate a commit message from staged changes and commit",
		Long: "Ask the configured provider for a commit message describing the " +
			"currently staged diff, then — after you confirm — run `git commit`.\n\n" +
			"Use --type to pick the message format and --generate N to be offered " +
			"several alternatives to choose from. Provider selection (--provider / " +
			"--model / --cli) and the pre-send guard, secret scan, and cost preflight " +
			"all work exactly as they do for a review.\n\n" +
			"Needs an interactive terminal to confirm (or to pick from --generate " +
			"alternatives); pass --yes to commit the first suggestion non-interactively.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCommit(cmd)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&global.commitType, "type", "t", "",
		"commit message format: "+strings.Join(prompt.ValidCommitTypes(), "|")+" (default \"plain\", or commit.type config)")
	f.IntVarP(&global.commitGen, "generate", "g", 0,
		"offer N alternative messages to choose from (default 1, or commit.generate config)")
	return cmd
}

func runCommit(cmd *cobra.Command) error {
	ctx := cmd.Context()
	app, err := resolveContext(true)
	if err != nil {
		return err
	}

	// Resolve format + count (flag > config > built-in default) and validate
	// up front so a typo fails before any provider call.
	ctype, err := resolveCommitType(app)
	if err != nil {
		return err
	}
	count, err := resolveCommitCount(app)
	if err != nil {
		return err
	}

	// Reject flags that imply a conflicting intent. --json/--markdown/--output
	// drive the findings renderers (commit emits no findings); --file/--dir
	// would narrow the *described* subset while `git commit` still commits the
	// whole index — a mismatch we refuse rather than mislead.
	if global.json || global.markdown || global.output != "" {
		return errors.New(app.Catalog.T("commit.flag_conflict_output"))
	}
	if len(global.files) > 0 || len(global.dirs) > 0 {
		return errors.New(app.Catalog.T("commit.flag_conflict_filter"))
	}

	// Committing needs confirmation we can only get on a TTY. A non-TTY run
	// must pass --yes to commit the top suggestion unattended; otherwise we
	// abort before spending anything (the selector/confirm can't render).
	interactive := ui.IsStdinTTY(os.Stdin)
	if !interactive && !global.yes {
		return errors.New(app.Catalog.T("commit.non_interactive"))
	}

	prog := ui.NewProgress(cmd.ErrOrStderr(), ui.ParseColorMode(global.color), global.quiet)
	defer prog.Close()

	prog.Start(app.Catalog.T("progress.searching"))
	raw, err := fetchDiff(app.Repo, reviewScopeFlags{staged: true}, nil)
	if err != nil {
		prog.Fail(err)
		return err
	}
	parsedRaw, err := diff.Parse(raw)
	if err != nil {
		prog.Fail(err)
		return err
	}
	if parsedRaw.Empty() {
		prog.Finish()
		return errors.New(app.Catalog.T("commit.no_staged"))
	}

	// Show what was detected, then narrow to the reviewed subset for the
	// prompt. If the ignore layers strip everything (e.g. only a lockfile is
	// staged), fall back to the raw staged diff — the user staged it and
	// wants a message, so describe it rather than refuse.
	prog.Info(app.Catalog.T("commit.detected_files", parsedRaw.FileCount()))
	for _, name := range stagedFileNames(parsedRaw) {
		prog.Info("    " + name)
	}
	parsed := diff.Filter(parsedRaw, buildMatcher(app.RepoRoot))
	if parsed.Empty() {
		// prog.Info (not infof) so the notice joins the animated stage tree
		// instead of writing raw to stderr underneath the spinner's redraws.
		prog.Info(app.Catalog.T("commit.all_filtered"))
		parsed = parsedRaw
	}
	diffText := parsed.String()

	// One shared reader handed to the guard / secret / cost handlers to
	// satisfy their *bufio.Reader fallback signature (UC-21: a single buffer
	// over os.Stdin, never several competing ones). NOTE: on a TTY these
	// prompts — and ui.Select / the final Confirm — render via huh, which
	// reads the controlling terminal (os.Stdin) directly and ignores this
	// reader, so .Read is never actually called on it here. The line-based
	// fallback that *would* read it only runs non-interactively, which commit
	// refuses above (it requires a TTY or --yes). bufio.NewReader does not
	// read at construction, so nothing — including type-ahead — is ever
	// trapped in this buffer ahead of a huh form. No input contention.
	stdinReader := bufio.NewReader(os.Stdin)

	prog.Pause()
	if res, _ := guard.CheckDiffForLocalConfig(parsed, guard.Options{
		AssumeYes:      global.yes,
		NonInteractive: !interactive,
		Interactive:    interactive,
		Catalog:        app.Catalog,
		Reader:         stdinReader,
	}); res == guard.Abort {
		return errors.New("aborted by pre-send guard")
	}
	// Pre-send secret scan on the staged diff (ADR-0007). --allow-secrets is
	// the only bypass; --yes deliberately does NOT bypass it, so a CI
	// auto-commit still aborts on a leaked credential.
	if app.Config.Guard.SecretScan && !global.allowSecrets {
		if matches := guard.ScanForSecrets(diffText); len(matches) > 0 {
			if abort := handleSecretMatches(cmd, app, matches, stdinReader); abort {
				return errors.New(app.Catalog.T("guard.secrets.aborted_user"))
			}
		}
	}
	prog.Resume()

	prog.Start(app.Catalog.T("progress.preparing"))
	prov, err := provider.New(app.Config.Provider, app.Config.Providers[app.Config.Provider])
	if err != nil {
		prog.Fail(err)
		return err
	}
	model := app.Config.Providers[app.Config.Provider].Model
	if model == "" {
		model = prov.DefaultModel()
	}
	p := prompt.BuildCommitMessage(diffText, prompt.CommitOptions{Type: ctype, Count: count})

	// --show-prompt: dump the exact prompt and stop (no provider call, no
	// cost). Placed after the guard/secret scan so a secret in the dump is
	// surfaced first.
	if global.showPrompt {
		prog.Finish()
		prog.Clear()
		return showPromptOutput(cmd, p)
	}

	// Commit messages are always English (ADR-0019), so the cache key and the
	// request both pin lang "en" regardless of the review --lang.
	const commitLang = "en"
	cacheKey := cache.Compute(cache.ComputeArgs{
		Diff:         diffText,
		SystemPrompt: p.System,
		Provider:     prov.Name(),
		Model:        model,
		Lang:         commitLang,
		Mode:         "commit",
	})
	cacheStore, err := openCache(app.RepoRoot, app.Config.Cache)
	if err != nil {
		// prog.Info (not infof): the preparing stage is still animating here.
		prog.Info(app.Catalog.T("review.cache_disabled", err))
	}

	var content string
	if !global.noCache && cacheStore != nil {
		if entry, hit := cacheStore.Get(cacheKey); hit {
			content = entry.Result.Content
		}
	}

	if content == "" {
		prog.Finish() // preparing → done

		if app.Config.Guard.TokenPreflight {
			prog.Pause()
			if abort := handleTokenPreflight(cmd, app, prov, p, model, stdinReader); abort {
				return errors.New(app.Catalog.T("guard.tokens.aborted_user"))
			}
			prog.Resume()
		}
		if !global.noCostCheck {
			estUsage := provider.Usage{
				InputTokens:  p.EstimatedTokens(),
				OutputTokens: commitOutputTokens(count),
			}
			estCost := resolvePricing(app.Config, prov, model).Cost(estUsage)
			prog.Pause()
			if abort := handleCostPreflight(cmd, app, estCost, stdinReader); abort {
				return errors.New(app.Catalog.T("cost.aborted_user"))
			}
			prog.Resume()
		}

		prog.Start(app.Catalog.T("commit.generating"))
		resp, callErr := prov.Review(ctx, provider.Request{
			Model:        model,
			SystemPrompt: p.System,
			UserPrompt:   p.User,
			Lang:         commitLang,
			FreeForm:     true,
		})
		if callErr != nil {
			prog.Fail(callErr)
			return fmt.Errorf("provider %s: %w", prov.Name(), callErr)
		}
		prog.Finish()
		content = resp.Content

		if !global.noCache && cacheStore != nil {
			_ = cacheStore.Put(cacheKey, cache.Entry{
				Key: cache.KeyMeta{
					Provider: prov.Name(),
					Model:    model,
					Lang:     commitLang,
				},
				Result: cache.Result{
					Content: content,
					Format:  cache.FormatPlainText,
					Tokens: cache.Tokens{
						Input:  resp.Usage.InputTokens,
						Output: resp.Usage.OutputTokens,
						Cached: resp.Usage.CachedInputTokens,
					},
				},
			})
		}
	} else {
		prog.Finish() // preparing → done (cache hit, no call)
	}
	prog.Clear()

	msgs := prompt.ParseMessages(content, count)
	if len(msgs) == 0 {
		return errors.New(app.Catalog.T("commit.parse_failed"))
	}
	if count > 1 && len(msgs) < count {
		infof("%s", app.Catalog.T("commit.fewer_messages", len(msgs), count))
	}

	out := cmd.OutOrStdout()
	chosen := msgs[0]
	selected := false
	if len(msgs) > 1 && !global.yes {
		idx, selErr := ui.Select(app.Catalog.T("commit.select_prompt"), messageSubjects(msgs))
		if selErr != nil {
			return selErr
		}
		chosen = msgs[idx]
		selected = true
	}

	if selected {
		if _, err := fmt.Fprintf(out, "\n%s\n", app.Catalog.T("commit.selected_header")); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(out, "\n%s\n\n", strings.TrimSpace(chosen)); err != nil {
		return err
	}

	if !global.yes {
		ok, confErr := ui.Confirm(stdinReader, cmd.ErrOrStderr(), app.Catalog.T("commit.confirm"),
			ui.AskOptions{Interactive: true, DefaultYes: true, Catalog: app.Catalog})
		if confErr != nil {
			return confErr
		}
		if !ok {
			infof("%s", app.Catalog.T("commit.aborted"))
			return nil
		}
	}

	summary, err := git.Commit(ctx, app.RepoRoot, chosen)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "%s\n%s\n", app.Catalog.T("commit.committed"), summary); err != nil {
		return err
	}
	return nil
}

// commitOutputTokens estimates the output token spend for a commit-message
// call. Unlike a structured review (estimateOutputTokens, tuned for 200–1500
// token reports), a commit message is tiny — a subject plus an optional short
// body — so we budget ~150 tokens per requested message instead of reusing
// the review heuristic, which for --generate 10 would overestimate by ~10x
// and trip spurious cost warnings.
func commitOutputTokens(count int) int {
	const perMessage = 150
	if count < 1 {
		count = 1
	}
	return count * perMessage
}

// resolveCommitType applies flag > config > built-in-default precedence and
// validates the result against the closed set.
func resolveCommitType(app *appContext) (prompt.CommitType, error) {
	raw := global.commitType
	if raw == "" {
		raw = app.Config.Commit.Type
	}
	if raw == "" {
		raw = string(prompt.CommitPlain)
	}
	t, ok := prompt.ParseCommitType(raw)
	if !ok {
		return "", errors.New(app.Catalog.T("commit.type_invalid", raw, strings.Join(prompt.ValidCommitTypes(), ", ")))
	}
	return t, nil
}

// resolveCommitCount applies flag > config > built-in-default precedence.
// A zero (flag unset, config unset) resolves to 1; a negative value or one
// above the cap is an error.
func resolveCommitCount(app *appContext) (int, error) {
	n := global.commitGen
	if n == 0 {
		n = app.Config.Commit.Generate
	}
	if n == 0 {
		n = 1
	}
	if n < 1 {
		return 0, errors.New(app.Catalog.T("commit.generate_invalid"))
	}
	if n > commitGenMax {
		return 0, errors.New(app.Catalog.T("commit.generate_too_many", commitGenMax))
	}
	return n, nil
}

// stagedFileNames returns a display name per file in the diff (post-change
// path, falling back to the old path for pure deletions).
func stagedFileNames(d diff.Diff) []string {
	names := make([]string, 0, len(d.Files))
	for _, f := range d.Files {
		name := f.Path
		if name == "" {
			name = f.OldPath
		}
		names = append(names, name)
	}
	return names
}

// messageSubjects returns the first line of each message, for the --generate
// selection list (huh handles its own truncation/scrolling).
func messageSubjects(msgs []string) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = firstLine(m)
	}
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
