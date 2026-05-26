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
}

func runReview(ctx context.Context, scope reviewScopeFlags) error {
	app, err := resolveContext(true)
	if err != nil {
		return err
	}
	rawDiff, err := fetchDiff(app.Repo, scope)
	if err != nil {
		return err
	}
	parsed, err := diff.Parse(rawDiff)
	if err != nil {
		return err
	}
	matcher := buildMatcher(app.RepoRoot)
	parsed = diff.Filter(parsed, matcher)
	if parsed.Empty() {
		infof("No reviewable changes after filtering.")
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
	}

	if res, _ := guard.CheckDiffForLocalConfig(parsed, guard.Options{
		AssumeYes:      global.yes,
		NonInteractive: !ui.IsStdinTTY(os.Stdin),
	}); res == guard.Abort {
		return errors.New("aborted by pre-send guard")
	}

	p := prompt.Build(loaded, outputLoaded, app.Lang, parsed.String())

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
		infof("Cache disabled: %v", err)
	}

	if !global.noCache && cacheStore != nil {
		if entry, hit := cacheStore.Get(cacheKey); hit {
			meta := render.Meta{
				Provider:  prov.Name(),
				Model:     model,
				Lang:      app.Lang.Code,
				Cached:    true,
				Timestamp: entry.CreatedAt,
				Usage: provider.Usage{
					InputTokens:       entry.Result.Tokens.Input,
					OutputTokens:      entry.Result.Tokens.Output,
					CachedInputTokens: entry.Result.Tokens.Cached,
				},
			}
			return renderResult(entry.Result.Content, meta)
		}
	}

	start := time.Now()
	resp, err := prov.Review(ctx, provider.Request{
		Model:        model,
		SystemPrompt: p.System,
		UserPrompt:   p.User,
		Lang:         app.Lang.Code,
	})
	if err != nil {
		return fmt.Errorf("provider %s: %w", prov.Name(), err)
	}
	latency := time.Since(start)

	meta := render.Meta{
		Provider:  prov.Name(),
		Model:     resp.Model,
		Lang:      app.Lang.Code,
		Usage:     resp.Usage,
		Cost:      prov.Pricing(resp.Model).Cost(resp.Usage),
		Latency:   latency,
		Timestamp: time.Now().UTC(),
	}

	if !global.noCache && cacheStore != nil {
		_ = cacheStore.Put(cacheKey, cache.Entry{
			Key: cache.KeyMeta{
				DiffHash:         "sha256:" + cacheKey[:16],
				SystemPromptHash: "",
				Provider:         prov.Name(),
				Model:            resp.Model,
				Lang:             app.Lang.Code,
			},
			Result: cache.Result{
				Content: resp.Content,
				Tokens: cache.Tokens{
					Input:  resp.Usage.InputTokens,
					Output: resp.Usage.OutputTokens,
					Cached: resp.Usage.CachedInputTokens,
				},
			},
		})
	}
	return renderResult(resp.Content, meta)
}

func fetchDiff(repo *git.DispatchRepo, scope reviewScopeFlags) (git.Diff, error) {
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
			return git.Diff{}, fmt.Errorf("--pull-request expects target...feature, got %q", scope.pr)
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

func renderResult(content string, meta render.Meta) error {
	payload := render.Payload{
		Content: content,
		Meta:    meta,
		Verbose: global.verbose,
	}

	w, closer, err := openOutput()
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
		return render.Terminal(w, payload)
	}
	return render.Markdown(w, payload)
}

func openOutput() (io.Writer, func(), error) {
	if global.output == "" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.Create(global.output)
	if err != nil {
		return nil, nil, fmt.Errorf("open --output: %w", err)
	}
	return f, func() { _ = f.Close() }, nil
}
