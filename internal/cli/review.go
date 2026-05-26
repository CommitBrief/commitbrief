package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/cache"
	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/guard"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/ignore"
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

type reviewScopeFlags struct {
	staged   bool
	unstaged bool
	file     string
	commit   string
	pr       string
	branch   string
}

var reviewScope reviewScopeFlags

func bindScopeFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.BoolVarP(&reviewScope.staged, "staged", "s", false, "review staged changes (default)")
	f.BoolVarP(&reviewScope.unstaged, "unstaged", "u", false, "review unstaged changes")
	f.StringVarP(&reviewScope.file, "file", "f", "", "review changes in a single file")
	f.StringVarP(&reviewScope.commit, "commit", "c", "", "review changes in a commit hash")
	f.StringVar(&reviewScope.pr, "pull-request", "", "review a PR-style diff target...feature")
	f.StringVarP(&reviewScope.branch, "branch", "b", "", "review current branch vs target ref")
	cmd.MarkFlagsMutuallyExclusive("staged", "unstaged", "file", "commit", "pull-request", "branch")
}

func runReview(cmd *cobra.Command, scope reviewScopeFlags) error {
	ctx := cmd.Context()
	app, err := resolveContext(true)
	if err != nil {
		return err
	}
	rawDiff, err := fetchDiff(app.Repo, scope, app.Catalog)
	if err != nil {
		return err
	}
	if rawDiff.IsMerge {
		infof("%s", app.Catalog.T("cli.warn.merge_commit", scope.commit))
	}
	parsed, err := diff.Parse(rawDiff)
	if err != nil {
		return err
	}
	matcher := buildMatcher(app.RepoRoot)
	parsed = diff.Filter(parsed, matcher)
	if parsed.Empty() {
		infof("%s", app.Catalog.T("review.no_changes"))
		return nil
	}

	loaded, err := rules.Load(app.RepoRoot)
	if err != nil {
		return err
	}
	if loaded.Source == rules.SourceDefault {
		infof("%s", app.Catalog.T("rules.using_default"))
	}
	outputLoaded, err := rules.LoadOutput(app.RepoRoot, userHome())
	if err != nil {
		return err
	}
	if outputLoaded.Source == rules.SourceDefault {
		infof("%s", app.Catalog.T("rules.output.using_default"))
	} else if err := render.ValidateOutputTemplate(outputLoaded.Content); err != nil {
		// Pre-send guard (ADR-0014 §5): bail out before any provider call so
		// a malformed user template doesn't burn tokens. The embedded
		// default is presumed-valid via release-check.sh and skipped here.
		return errors.New(app.Catalog.T("output.template.invalid", outputLoaded.Path, err.Error()))
	}

	if res, _ := guard.CheckDiffForLocalConfig(parsed, guard.Options{
		AssumeYes:      global.yes,
		NonInteractive: !ui.IsStdinTTY(os.Stdin),
	}); res == guard.Abort {
		return errors.New("aborted by pre-send guard")
	}

	p := prompt.Build(loaded, app.Lang, parsed.String())

	prov, err := provider.New(app.Config.Provider, app.Config.Providers[app.Config.Provider])
	if err != nil {
		return err
	}
	model := app.Config.Providers[app.Config.Provider].Model
	if model == "" {
		model = prov.DefaultModel()
	}

	cacheKey := cache.Compute(cache.ComputeArgs{
		Diff:         parsed.String(),
		SystemPrompt: p.System,
		Provider:     prov.Name(),
		Model:        model,
		Lang:         app.Lang.Code,
	})

	cacheStore, err := openCache(app.RepoRoot)
	if err != nil {
		infof("%s", app.Catalog.T("review.cache_disabled", err))
	}

	if !global.noCache && cacheStore != nil {
		if entry, hit := cacheStore.Get(cacheKey); hit {
			usage := provider.Usage{
				InputTokens:       entry.Result.Tokens.Input,
				OutputTokens:      entry.Result.Tokens.Output,
				CachedInputTokens: entry.Result.Tokens.Cached,
			}
			// On a cache hit no provider call is made; the cost figure is
			// what would have been spent — surfaced as "Saved" by the
			// verbose footer (see render/verbose.go).
			meta := render.Meta{
				Provider:     prov.Name(),
				Model:        model,
				Lang:         app.Lang.Code,
				Cached:       true,
				Timestamp:    entry.CreatedAt,
				Usage:        usage,
				Cost:         prov.Pricing(model).Cost(usage),
				Files:        parsed.FileCount(),
				LinesAdded:   parsed.AddedLines(),
				LinesRemoved: parsed.DeletedLines(),
				RulesLoaded:  loaded.Source != rules.SourceDefault,
			}
			// Parse Findings unless the entry was written in markdown-fallback
			// mode — in that case the original write already emitted the
			// stderr warning and we honour it silently on replay.
			var findings []render.Finding
			if entry.Result.Format != cache.FormatMarkdownFallback {
				findings, _ = render.ParseFindings(entry.Result.Content)
			}
			return renderResult(cmd, entry.Result.Content, outputLoaded.Content, findings, meta)
		}
	}

	start := time.Now()
	content, usage, format, err := tryStructuredReview(ctx, prov, provider.Request{
		Model:        model,
		SystemPrompt: p.System,
		UserPrompt:   p.User,
		Lang:         app.Lang.Code,
	})
	if err != nil {
		return fmt.Errorf("provider %s: %w", prov.Name(), err)
	}
	latency := time.Since(start)

	// Parse + warn happen here on a fresh call (the cache-hit path above
	// honours the cached Format and skips this warning to avoid repeats).
	var findings []render.Finding
	if format == cache.FormatJSON {
		findings, _ = render.ParseFindings(content)
	} else {
		fmt.Fprintln(cmd.ErrOrStderr(), app.Catalog.T("review.degraded"))
	}

	respModel := model
	meta := render.Meta{
		Provider:     prov.Name(),
		Model:        respModel,
		Lang:         app.Lang.Code,
		Usage:        usage,
		Cost:         prov.Pricing(respModel).Cost(usage),
		Latency:      latency,
		Timestamp:    time.Now().UTC(),
		Files:        parsed.FileCount(),
		LinesAdded:   parsed.AddedLines(),
		LinesRemoved: parsed.DeletedLines(),
		RulesLoaded:  loaded.Source != rules.SourceDefault,
	}

	if !global.noCache && cacheStore != nil {
		_ = cacheStore.Put(cacheKey, cache.Entry{
			Key: cache.KeyMeta{
				DiffHash:         "sha256:" + cacheKey[:16],
				SystemPromptHash: "",
				Provider:         prov.Name(),
				Model:            respModel,
				Lang:             app.Lang.Code,
			},
			Result: cache.Result{
				Content: content,
				Format:  format,
				Tokens: cache.Tokens{
					Input:  usage.InputTokens,
					Output: usage.OutputTokens,
					Cached: usage.CachedInputTokens,
				},
			},
		})
	}
	return renderResult(cmd, content, outputLoaded.Content, findings, meta)
}

// tryStructuredReview runs Review and, on parse failure, retries once.
// Returns (content, totalUsage, format, err). format is FormatJSON when
// either the first or retry response parses cleanly; FormatMarkdownFallback
// when both attempts fail (the caller emits the user warning and stores
// the marker in cache so replays stay silent).
//
// Token usage is summed across both attempts so the verbose footer / cost
// reflects what the user actually spent, even on a graceful degrade.
func tryStructuredReview(ctx context.Context, prov provider.Provider, req provider.Request) (string, provider.Usage, string, error) {
	resp, err := prov.Review(ctx, req)
	if err != nil {
		return "", provider.Usage{}, "", err
	}
	if _, parseErr := render.ParseFindings(resp.Content); parseErr == nil {
		return resp.Content, resp.Usage, cache.FormatJSON, nil
	}
	// First attempt unparseable — ADR-0014 §4 retry-once.
	resp2, err2 := prov.Review(ctx, req)
	if err2 != nil {
		// Network/auth failure on retry: surface the first response with
		// the fallback marker; the caller can still render via degrade.
		return resp.Content, resp.Usage, cache.FormatMarkdownFallback, nil
	}
	totalUsage := provider.Usage{
		InputTokens:       resp.Usage.InputTokens + resp2.Usage.InputTokens,
		OutputTokens:      resp.Usage.OutputTokens + resp2.Usage.OutputTokens,
		CachedInputTokens: resp.Usage.CachedInputTokens + resp2.Usage.CachedInputTokens,
	}
	if _, parseErr := render.ParseFindings(resp2.Content); parseErr == nil {
		return resp2.Content, totalUsage, cache.FormatJSON, nil
	}
	// Both attempts produced unparseable output — degrade with first
	// response cached as the canonical fallback content.
	return resp.Content, totalUsage, cache.FormatMarkdownFallback, nil
}

func fetchDiff(repo *git.DispatchRepo, scope reviewScopeFlags, cat *i18n.Catalog) (git.Diff, error) {
	switch {
	case scope.unstaged:
		return repo.UnstagedDiff()
	case scope.file != "":
		return repo.FileDiff(scope.file)
	case scope.commit != "":
		return repo.CommitDiff(scope.commit)
	case scope.pr != "":
		t, f, ok := splitThreeDot(scope.pr)
		if !ok {
			return git.Diff{}, errors.New(cat.T("review.pr_format", scope.pr))
		}
		return repo.RangeDiff(t, f)
	case scope.branch != "":
		return repo.BranchDiff(scope.branch)
	default:
		return repo.StagedDiff()
	}
}

func splitThreeDot(s string) (target, feature string, ok bool) {
	i := strings.Index(s, "...")
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+3:], true
}

func buildMatcher(repoRoot string) *ignore.Matcher {
	builtin := ignore.Builtin()
	if repoRoot == "" {
		return builtin
	}
	repoIgnore, _ := ignore.ParseFile(filepath.Join(repoRoot, ignore.Filename))
	return ignore.Compose(builtin, repoIgnore)
}

func openCache(repoRoot string) (*cache.Cache, error) {
	if repoRoot == "" {
		return nil, errors.New("no repo root")
	}
	return cache.Open(cache.Options{
		Dir:      filepath.Join(repoRoot, ".commitbrief", "cache"),
		RepoRoot: repoRoot,
	})
}

func renderResult(cmd *cobra.Command, content, outputTemplate string, findings []render.Finding, meta render.Meta) error {
	// Findings is pre-resolved by the caller — fresh-call retries and
	// cache-hit format markers are handled there so renderResult never
	// emits the malformed-JSON warning twice for the same review.
	payload := render.Payload{
		Content:        content,
		Findings:       findings,
		OutputTemplate: outputTemplate,
		Meta:           meta,
		Verbose:        global.verbose,
	}

	w, closer, err := openOutput(cmd)
	if err != nil {
		return err
	}
	defer closer()

	switch {
	case global.json:
		return render.JSON(w, payload)
	case global.markdown:
		return render.Markdown(w, payload)
	}
	if ui.ColorEnabled(w, ui.ParseColorMode(global.color)) {
		// Cards is the default rich TTY layout: glamour-rendered body
		// framed by a styled header, status line, and footer (Phase 11
		// Stage A). Terminal (glamour-only, no frame) stays exported for
		// callers that want a thinner render but is no longer the default.
		return render.Cards(w, payload)
	}
	return render.Markdown(w, payload)
}

func openOutput(cmd *cobra.Command) (io.Writer, func(), error) {
	if global.output == "" {
		return cmd.OutOrStdout(), func() {}, nil
	}
	f, err := os.Create(global.output)
	if err != nil {
		return nil, nil, fmt.Errorf("open --output: %w", err)
	}
	return f, func() { _ = f.Close() }, nil
}
