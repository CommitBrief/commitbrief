// SPDX-License-Identifier: GPL-3.0-or-later

// Package clireview is the shared implementation of CLI-tool-backed
// review providers. The concrete providers (claude-cli, gemini-cli,
// codex-cli) live in their own packages under internal/provider/ —
// they only declare a Spec and call clireview.New to get a working
// Backend that implements provider.Provider plus the
// provider.PlainTextEmitter marker.
//
// Why a shared package: the three CLIs differ only in their binary
// name and how they accept a one-shot prompt on the command line.
// Everything else — registering with the provider registry, running
// the subprocess, surfacing PATH-not-found and exit-non-zero as
// translatable errors, returning a provider.Response with the raw
// CLI output as Content — is identical.
//
// CLI providers deliberately have NO API key, NO model selection
// (the host CLI manages that), and NO per-call cost estimate (the
// host CLI bills against the user's existing subscription). The
// review pipeline branches on provider.PlainTextEmitter to skip the
// JSON contract path, retry-once, and the cards/markdown renderer.
package clireview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/tokens"
)

// Spec describes one CLI backend. Each concrete provider package
// fills this in once and passes it to New.
type Spec struct {
	// Name is the provider name as it appears in the registry,
	// `commitbrief providers list`, and the `--provider` flag —
	// e.g. "claude-cli", "gemini-cli". The user-facing `--cli`
	// shorthand maps "claude" → "claude-cli" by appending "-cli".
	Name string

	// Binary is the executable name expected on PATH, e.g. "claude",
	// "gemini", "codex". `exec.LookPath` resolves it.
	Binary string

	// PromptArgs returns the argv slice (excluding the binary itself)
	// for a one-shot, non-interactive invocation that prints the
	// model's response to stdout and exits. Implementations should
	// disable colors and tool use where possible so the output stays
	// machine-friendly.
	PromptArgs func(prompt string) []string

	// VersionArgs prints the CLI version and exits. Used by
	// TestConnection to confirm the binary works and by the cache
	// key to invalidate cached entries across CLI upgrades. Optional;
	// if nil, version is "" and cache keys can't differentiate
	// across upgrades.
	VersionArgs []string

	// Timeout caps a single CLI invocation. CLIs sometimes hang on
	// rate limits or network issues; we'd rather surface a clear
	// error than block the user indefinitely. Zero means no timeout.
	Timeout time.Duration
}

// Backend is a provider.Provider + provider.PlainTextEmitter built
// around a single Spec. Construct via New; callers don't need to
// touch the struct directly.
type Backend struct {
	spec Spec
}

// New returns a Backend ready to register with the provider registry.
// Validation of the Spec is deferred to actual use (Review,
// TestConnection) so a missing binary doesn't crash init().
func New(spec Spec) *Backend {
	return &Backend{spec: spec}
}

// EmitsPlainText implements provider.PlainTextEmitter — the marker
// interface review.go uses to branch into the CLI-output path.
func (b *Backend) EmitsPlainText() {}

func (b *Backend) Name() string { return b.spec.Name }

// DefaultModel returns a stable identifier for the cache key. CLI
// tools don't expose model selection to us, so we tag entries with
// the binary name + detected version. Across CLI upgrades the
// version changes and cached entries cleanly invalidate.
func (b *Backend) DefaultModel() string {
	if v := b.versionOrEmpty(); v != "" {
		return b.spec.Binary + " " + v
	}
	return b.spec.Binary
}

// ContextWindow is informational for CLI providers — we don't gate
// the request on it because the host CLI handles its own context
// management. We return a generous default so the dry-run footer
// doesn't read "0".
func (b *Backend) ContextWindow(string) int { return 200_000 }

// EstimateTokens uses the shared chars/4 heuristic. CLI tools
// usually don't expose tokenizers we can call from outside their
// process, so the approximation has to do for the cost preflight
// and context-window gate.
func (b *Backend) EstimateTokens(s string) int { return tokens.Estimate(s) }

// Pricing is zero for CLI providers — the user pays through their
// host-CLI subscription (Claude Pro, Gemini Advanced, ChatGPT Plus,
// …), not per-token to us. The cost preflight short-circuits on
// zero pricing, and the verbose footer shows "—" instead of a
// token count.
func (b *Backend) Pricing(string) provider.Pricing { return provider.Pricing{} }

// TestConnection verifies the binary is on PATH and runs its
// version command (when defined). A failed lookup or non-zero exit
// from the version command surface as the catalog-translatable
// errors at the CLI layer.
func (b *Backend) TestConnection(ctx context.Context) error {
	if _, err := exec.LookPath(b.spec.Binary); err != nil {
		return fmt.Errorf("%s: binary %q not found on PATH (%w)",
			b.spec.Name, b.spec.Binary, err)
	}
	if len(b.spec.VersionArgs) == 0 {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, b.spec.Binary, b.spec.VersionArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s: version check failed: %s", b.spec.Name, msg)
	}
	return nil
}

// Review invokes the host CLI with the combined system + user
// prompt, captures stdout, and returns it verbatim as the Response
// Content. Token usage is zeroed (the CLI doesn't expose it) and
// Model is set to the resolved DefaultModel so the cache key
// invalidates on CLI upgrades.
//
// The review pipeline checks provider.PlainTextEmitter before
// calling, so this is only invoked when the caller is committed to
// the CLI path — no JSON parsing follows.
func (b *Backend) Review(ctx context.Context, req provider.Request) (provider.Response, error) {
	if b.spec.PromptArgs == nil {
		return provider.Response{}, fmt.Errorf("%s: PromptArgs is required in Spec", b.spec.Name)
	}
	// CLIs take a single combined prompt — concatenate system and
	// user halves with a paragraph break so the model sees them as
	// distinct sections without losing either.
	combined := req.SystemPrompt
	if combined != "" && req.UserPrompt != "" {
		combined += "\n\n"
	}
	combined += req.UserPrompt

	cctx := ctx
	if b.spec.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, b.spec.Timeout)
		defer cancel()
	}

	args := b.spec.PromptArgs(combined)
	cmd := exec.CommandContext(cctx, b.spec.Binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Distinguish timeout / context cancellation from a non-zero
		// exit so the caller can format a useful message. The wrapped
		// stderr usually carries the host CLI's own error text.
		msg := strings.TrimSpace(stderr.String())
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			return provider.Response{}, fmt.Errorf("%s: timed out after %s (last stderr: %s)",
				b.spec.Name, b.spec.Timeout, msg)
		}
		if msg == "" {
			msg = err.Error()
		}
		return provider.Response{}, fmt.Errorf("%s: invocation failed: %s", b.spec.Name, msg)
	}

	content := strings.TrimSpace(stdout.String())
	if content == "" {
		return provider.Response{}, fmt.Errorf("%s: empty output from %s", b.spec.Name, b.spec.Binary)
	}

	return provider.Response{
		Content: content,
		Model:   b.DefaultModel(),
		Usage:   provider.Usage{},
	}, nil
}

// versionOrEmpty runs the version command and returns the first line
// of output. Failures are swallowed — the cache key gracefully
// degrades to "binary-name without version".
func (b *Backend) versionOrEmpty() string {
	if len(b.spec.VersionArgs) == 0 {
		return ""
	}
	cmd := exec.Command(b.spec.Binary, b.spec.VersionArgs...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	first, _, _ := strings.Cut(stdout.String(), "\n")
	return strings.TrimSpace(first)
}
