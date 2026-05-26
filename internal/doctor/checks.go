package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
	"github.com/CommitBrief/commitbrief/internal/setup"
)

// connectionTimeout caps each per-provider TestConnection. The doctor
// command should fail fast — a hung provider must not leave the user
// staring at a blank terminal for minutes.
const connectionTimeout = 5 * time.Second

// RunAll executes every check in display order and returns the flat
// result slice. Per-provider connection checks are fanned out
// concurrently with [connectionTimeout]; everything else runs serially
// (the I/O is local + fast).
func (r *Runner) RunAll(ctx context.Context) []Result {
	out := []Result{
		r.checkGit(),
		r.checkConfig(),
		r.checkRules(),
		r.checkOutput(),
		r.checkProviderConfigured(),
		r.checkCache(),
		r.checkGitignore(),
	}
	out = append(out, r.checkProviderConnections(ctx)...)
	return out
}

// checkGit verifies a `git` binary is on PATH. Several pipeline paths
// (CLI git fallback in `internal/git`, smoke-test scripts, the dry-run
// helpers) shell out to git, so a missing binary is always a Fail.
func (r *Runner) checkGit() Result {
	name := r.t("doctor.check.git")
	path, err := exec.LookPath("git")
	if err != nil {
		return Result{Name: name, Status: StatusFail, Detail: r.t("doctor.detail.git_missing")}
	}
	return Result{Name: name, Status: StatusOK, Detail: path}
}

// checkConfig confirms the merged config we were handed at least
// parses and has a non-empty Provider field. The deeper "is the picked
// provider sane?" question lives in checkProviderConfigured.
func (r *Runner) checkConfig() Result {
	name := r.t("doctor.check.config")
	if r.Config == nil {
		return Result{Name: name, Status: StatusFail, Detail: r.t("doctor.detail.config_nil")}
	}
	if r.Config.Provider == "" {
		return Result{Name: name, Status: StatusFail, Detail: r.t("doctor.detail.config_no_provider")}
	}
	return Result{Name: name, Status: StatusOK}
}

// checkRules surfaces where COMMITBRIEF.md is coming from — repo file
// or the embedded default. Default is a soft Warn (not Fail) so the
// "I'm running outside any prepared repo" case still passes if every-
// thing else is healthy; the user just hasn't customised rules yet.
func (r *Runner) checkRules() Result {
	name := r.t("doctor.check.rules")
	if r.RepoRoot == "" {
		return Result{Name: name, Status: StatusOK, Detail: r.t("doctor.detail.default")}
	}
	loaded, err := rules.Load(r.RepoRoot)
	if err != nil {
		return Result{Name: name, Status: StatusFail, Detail: err.Error()}
	}
	if loaded.Source == rules.SourceFile {
		return Result{Name: name, Status: StatusOK, Detail: loaded.Path}
	}
	return Result{Name: name, Status: StatusOK, Detail: r.t("doctor.detail.default")}
}

// checkOutput validates the OUTPUT.md template if the user has one.
// The embedded default is presumed-valid (release-check.sh enforces it)
// so a non-default template that fails validation is a Fail — same
// guard the runReview path applies pre-send.
func (r *Runner) checkOutput() Result {
	name := r.t("doctor.check.output")
	loaded, err := rules.LoadOutput(r.RepoRoot, r.Home)
	if err != nil {
		return Result{Name: name, Status: StatusFail, Detail: err.Error()}
	}
	if loaded.Source == rules.SourceDefault {
		return Result{Name: name, Status: StatusOK, Detail: r.t("doctor.detail.default")}
	}
	if err := render.ValidateOutputTemplate(loaded.Content); err != nil {
		return Result{Name: name, Status: StatusFail, Detail: err.Error()}
	}
	return Result{Name: name, Status: StatusOK, Detail: loaded.Path}
}

// checkProviderConfigured asserts at least one provider entry has the
// credentials it needs to make a request. "Configured" means:
//   - an API key is set, OR
//   - this is the active provider AND it's ollama AND the base_url is set
//
// The ollama-only-when-active rule prevents the default base_url
// (`http://localhost:11434`) from inflating the check into a false
// positive when the user has no provider configured at all — the
// default value shouldn't count as user intent.
func (r *Runner) checkProviderConfigured() Result {
	name := r.t("doctor.check.provider_configured")
	for n, pc := range r.Config.Providers {
		if pc.APIKey != "" {
			return Result{Name: name, Status: StatusOK, Detail: n}
		}
		if n == "ollama" && n == r.Config.Provider && pc.BaseURL != "" {
			return Result{Name: name, Status: StatusOK, Detail: n}
		}
	}
	return Result{Name: name, Status: StatusFail, Detail: r.t("doctor.detail.no_provider")}
}

// checkProviderConnections pings every provider that looks configured
// and reports per-provider Results. Each ping runs in its own goroutine
// with a [connectionTimeout] context; failures Warn (not Fail) because
// having one broken provider out of three is recoverable — the user
// can `providers use` a different one.
//
// Same ollama-only-when-active rule as checkProviderConfigured: the
// default base_url shouldn't trigger a network probe the user never
// asked for.
func (r *Runner) checkProviderConnections(ctx context.Context) []Result {
	configured := make([]string, 0, len(r.Config.Providers))
	for n, pc := range r.Config.Providers {
		if pc.APIKey != "" {
			configured = append(configured, n)
			continue
		}
		if n == "ollama" && n == r.Config.Provider && pc.BaseURL != "" {
			configured = append(configured, n)
		}
	}
	if len(configured) == 0 {
		return nil
	}
	sort.Strings(configured)

	results := make([]Result, len(configured))
	var wg sync.WaitGroup
	for i, providerName := range configured {
		wg.Add(1)
		go func(i int, providerName string) {
			defer wg.Done()
			ctx2, cancel := context.WithTimeout(ctx, connectionTimeout)
			defer cancel()

			pc := r.Config.Providers[providerName]
			label := r.t("doctor.check.provider_connection", providerName)
			start := time.Now()
			err := setup.TestConnection(ctx2, providerName, pc)
			elapsed := time.Since(start).Round(time.Millisecond)
			if err != nil {
				results[i] = Result{Name: label, Status: StatusWarn, Detail: err.Error()}
				return
			}
			results[i] = Result{Name: label, Status: StatusOK, Detail: fmt.Sprintf("ok (%s)", elapsed)}
		}(i, providerName)
	}
	wg.Wait()
	return results
}

// checkCache asserts the local cache directory can be created and
// written to. Permission issues here silently degrade `--no-cache`
// behavior at every review; surface them up-front. Outside a repo
// there is no expected cache dir, so the check is a no-op OK.
func (r *Runner) checkCache() Result {
	name := r.t("doctor.check.cache")
	if r.RepoRoot == "" {
		return Result{Name: name, Status: StatusOK, Detail: r.t("doctor.detail.no_repo_skip")}
	}
	dir := filepath.Join(r.RepoRoot, ".commitbrief", "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{Name: name, Status: StatusFail, Detail: err.Error()}
	}
	probe, err := os.CreateTemp(dir, "doctor-probe-*.tmp")
	if err != nil {
		return Result{Name: name, Status: StatusFail, Detail: err.Error()}
	}
	path := probe.Name()
	_ = probe.Close()
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Result{Name: name, Status: StatusWarn, Detail: err.Error()}
	}
	return Result{Name: name, Status: StatusOK, Detail: dir}
}

// checkGitignore reports whether the repo's .gitignore includes the
// `.commitbrief/` directory. Missing it isn't catastrophic (the cache
// + repo-local config could still work) but a user who runs `git add .`
// could accidentally commit their API key, so it warns hard.
func (r *Runner) checkGitignore() Result {
	name := r.t("doctor.check.gitignore")
	if r.RepoRoot == "" {
		return Result{Name: name, Status: StatusOK, Detail: r.t("doctor.detail.no_repo_skip")}
	}
	path := filepath.Join(r.RepoRoot, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Result{Name: name, Status: StatusWarn, Detail: r.t("doctor.detail.gitignore_missing")}
		}
		return Result{Name: name, Status: StatusFail, Detail: err.Error()}
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == ".commitbrief/" || line == ".commitbrief" || line == "/.commitbrief/" {
			return Result{Name: name, Status: StatusOK, Detail: path}
		}
	}
	return Result{Name: name, Status: StatusWarn, Detail: r.t("doctor.detail.gitignore_no_entry")}
}
