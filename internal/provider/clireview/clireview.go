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
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/lang"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/tokens"
)

// versionTimeout caps how long the Backend will wait for the host
// CLI's version command. CLIs occasionally hang on first-run network
// lookups (license checks, telemetry handshakes) and we don't want
// DefaultModel — which is on the hot path for every cache key — to
// stall a review behind that. 5 seconds is comfortably above a happy
// path (sub-100ms in practice) and well under user-perceptible
// review latency.
const versionTimeout = 5 * time.Second

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
	//
	// When [UseStdin] is true the prompt argument is the empty string
	// and PromptArgs returns *only* the flag/positional combo that
	// makes the host CLI read its prompt from stdin (e.g. claude's
	// `-p -`). The Backend then pipes the combined prompt into the
	// subprocess's stdin instead of embedding it in argv.
	//
	// withContext is the --with-context signal (ADR-0017). When true the
	// adapter appends the minimal read-only capability flags its host CLI
	// needs to read project files beyond the diff (e.g. claude's
	// `--allowedTools Read,Grep,Glob`, gemini's `--approval-mode plan
	// --skip-trust`); codex already permits reads under its read-only
	// sandbox and adds nothing. When false the adapter returns exactly the
	// diff-only argv it always has, so existing behavior is unchanged.
	// Write/network-mutation capability is never granted either way.
	PromptArgs func(prompt string, withContext bool) []string

	// UseStdin selects the stdin transport for the prompt. UC-24 in
	// PATCH_ROADMAP: large prompts (mid-size diffs + rules) exceed the
	// platform ARG_MAX limit when embedded in argv, surfacing as
	// `argument list too long` on Linux/macOS at ~128KB. Piping the
	// prompt via stdin sidesteps the limit entirely.
	//
	// Adapter authors: only set this true when you've confirmed the
	// host CLI reliably reads from stdin in one-shot mode.
	UseStdin bool

	// CheckArgSize opts the provider into the pre-exec command-line size
	// preflight (see [ArgLimit]). Set it for adapters that embed the whole
	// prompt in argv and have no stdin transport (gemini-cli): an oversized
	// prompt is then rejected before the subprocess is started, with an
	// actionable, localized [ArgTooLongError] instead of the OS's opaque
	// `argument list too long`. It is ignored when UseStdin is true, since
	// the prompt is not on the command line then.
	CheckArgSize bool

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
//
// versionOnce + versionMemo cache the result of the host CLI's
// version query for the lifetime of the process. DefaultModel is on
// every cache-key computation, so paying the subprocess cost more
// than once per process startup would be wasteful — and the version
// can't change underneath us mid-run anyway.
type Backend struct {
	spec        Spec
	versionOnce sync.Once
	versionMemo string
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

// SetTimeout implements provider.TimeoutSetter, replacing the Spec's
// per-invocation cap with the user's --timeout / review.timeout value.
// Without it a host CLI chewing through a large diff would still be
// killed at the Spec default no matter how much time the user allowed,
// because a context deadline can only shorten the window. A
// non-positive d is ignored so callers can pass "unset" unconditionally.
func (b *Backend) SetTimeout(d time.Duration) {
	if d > 0 {
		b.spec.Timeout = d
	}
}

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

	// --with-context (ADR-0017): the CLI layer passes a ContextOptions via
	// ProviderOpts. Enabled grants the adapter's read-permission flags and
	// pins the subprocess to the repo root so relative reads resolve.
	// Absent or wrong-typed ProviderOpts → diff-only (today's behavior).
	var withContext bool
	var repoRoot string
	if opts, ok := req.ProviderOpts.(provider.ContextOptions); ok && opts.Enabled {
		withContext = true
		repoRoot = opts.RepoRoot
	}

	// UC-24: when the spec opted into stdin transport, build argv
	// without the prompt and pipe the combined prompt to the
	// subprocess's stdin. This sidesteps the platform ARG_MAX limit
	// (~128KB on Linux/macOS) that fires once diffs + rules exceed
	// the kitchen-sink size class.
	var args []string
	if b.spec.UseStdin {
		args = b.spec.PromptArgs("", withContext)
	} else {
		args = b.spec.PromptArgs(combined, withContext)
	}
	cmd := exec.CommandContext(cctx, b.spec.Binary, args...)
	// cmd.Err is set when the binary was not found on PATH; skip the preflight
	// then so the user sees the real problem, not "narrow the diff".
	if b.spec.CheckArgSize && !b.spec.UseStdin && cmd.Err == nil {
		// Preflight: refuse before exec when argv (+ env) cannot fit.
		if size, limit, kind := ArgLimit(runtime.GOOS, cmd.Args, cmd.Environ()); kind != ArgFits {
			return provider.Response{}, newArgTooLongError(b.spec.Name, req.Lang, size, limit, kind)
		}
	}
	if withContext && repoRoot != "" {
		// Run the agent in the repository root so its relative file reads
		// resolve there rather than against commitbrief's inherited cwd.
		cmd.Dir = repoRoot
	}
	if b.spec.UseStdin {
		cmd.Stdin = strings.NewReader(combined)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Distinguish timeout / context cancellation from a non-zero
		// exit so the caller can format a useful message. The wrapped
		// stderr usually carries the host CLI's own error text.
		msg := strings.TrimSpace(stderr.String())
		if isArgTooLong(err) {
			// The preflight is a conservative estimate; the OS has the final
			// word. Map its E2BIG to the same actionable message.
			// Our estimate may have said "fits" (kind == ArgFits): then there
			// is no measured limit to quote, and the message must not claim one.
			size, limit, kind := ArgLimit(runtime.GOOS, cmd.Args, cmd.Environ())
			if kind == ArgFits {
				kind, limit = ArgRejectedByOS, 0
			}
			return provider.Response{}, newArgTooLongError(b.spec.Name, req.Lang, size, limit, kind)
		}
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

// versionOrEmpty returns the host CLI's version string (first line of
// `<binary> --version` output, trimmed). The result is memoised — see
// the Backend doc-comment for why. UC-23 in PATCH_ROADMAP: each call
// runs under a [versionTimeout] context so a hung CLI doesn't stall
// DefaultModel (which feeds every cache-key computation). On error
// or timeout, the memo records "" so subsequent calls don't retry
// the same broken binary on every review.
func (b *Backend) versionOrEmpty() string {
	b.versionOnce.Do(func() {
		if len(b.spec.VersionArgs) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, b.spec.Binary, b.spec.VersionArgs...)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			return
		}
		first, _, _ := strings.Cut(stdout.String(), "\n")
		b.versionMemo = strings.TrimSpace(first)
	})
	return b.versionMemo
}

// Command-line size budgets used by [ArgLimit]. They are deliberately
// conservative: rejecting a prompt that would have squeaked through is a
// better failure than an opaque E2BIG, and the message names the real fixes.
const (
	// posixMaxArgBytes caps one argv string. Linux enforces MAX_ARG_STRLEN =
	// 131072 bytes per string (NUL included); the whole prompt travels as a
	// single `-p <prompt>` argument, so this is the limit that binds first.
	// 120 KiB leaves headroom for the terminator and rounding. macOS has no
	// per-string cap, but the same value keeps behaviour uniform.
	posixMaxArgBytes = 120 * 1024

	// posixMaxTotalBytes caps argv + environment together (each string
	// counted with its NUL and a pointer slot). macOS ARG_MAX is 1 MiB for
	// both combined; Linux is typically 2 MiB (a quarter of the stack
	// rlimit). 900 000 stays under the smaller of the two.
	posixMaxTotalBytes = 900_000

	// windowsMaxCmdLine caps the CreateProcess command line, documented as
	// 32 767 characters. Go builds that line by quoting and escaping argv,
	// so the estimate in ArgLimit counts escape overhead worst-case; 32 000
	// leaves headroom.
	windowsMaxCmdLine = 32_000

	// ptrBytes is the per-string pointer slot counted against the POSIX total.
	ptrBytes = 8
)

// ArgKind says which command-line budget a prompt broke.
type ArgKind int

const (
	// ArgFits: within every budget.
	ArgFits ArgKind = iota
	// ArgSingleTooLong: one argument (the prompt) is over the per-argument
	// cap, or on Windows the whole command line is over its cap.
	ArgSingleTooLong
	// ArgTotalTooLong: no single argument is over the cap but argv plus the
	// environment together are over the POSIX total budget — the environment
	// is part of the problem, so narrowing the diff may not be enough.
	ArgTotalTooLong
	// ArgRejectedByOS: exec failed with E2BIG although the estimate said the
	// command line fits (unusual stack rlimit, large environment). No limit is
	// known, so none is quoted.
	ArgRejectedByOS
)

// ArgLimit estimates the OS command-line footprint of args (argv including
// argv[0]) plus env and reports which conservative platform budget, if any,
// it breaks. goos is a parameter (callers pass runtime.GOOS) so every
// platform's arithmetic is unit-testable on any host. size and limit are in
// bytes (characters on Windows). For ArgSingleTooLong on POSIX, size is the
// largest argument against the per-argument cap; for ArgTotalTooLong it is
// the whole argv + environment footprint against the total budget; for
// ArgFits they are the total footprint and total budget.
func ArgLimit(goos string, args, env []string) (size, limit int, kind ArgKind) {
	if goos == "windows" {
		// Windows passes one command-line string; the environment block is
		// separate and does not count. Worst case every `"` and `\` is
		// escaped with an extra backslash; +3 covers the quotes and space.
		for _, a := range args {
			size += len(a) + strings.Count(a, `"`) + strings.Count(a, `\`) + 3
		}
		if size > windowsMaxCmdLine {
			return size, windowsMaxCmdLine, ArgSingleTooLong
		}
		return size, windowsMaxCmdLine, ArgFits
	}
	largest := 0
	for _, a := range args {
		size += len(a) + 1 + ptrBytes
		if len(a) > largest {
			largest = len(a)
		}
	}
	for _, e := range env {
		size += len(e) + 1 + ptrBytes
	}
	switch {
	case largest > posixMaxArgBytes:
		return largest, posixMaxArgBytes, ArgSingleTooLong
	case size > posixMaxTotalBytes:
		return size, posixMaxTotalBytes, ArgTotalTooLong
	}
	return size, posixMaxTotalBytes, ArgFits
}

// ErrArgTooLong is matched (errors.Is) by every [ArgTooLongError].
var ErrArgTooLong = errors.New("clireview: command line exceeds the platform argument limit")

// ArgTooLongError reports a prompt that cannot be passed on the command line,
// either caught by the preflight or reported by the OS as E2BIG. Error()
// already carries the localized, user-facing text.
type ArgTooLongError struct {
	Provider string
	Kind     ArgKind
	Size     int // bytes (characters on Windows) measured
	Limit    int // the budget that was exceeded; 0 when the OS rejected it unpredicted
	msg      string
}

func (e *ArgTooLongError) Error() string { return e.msg }
func (e *ArgTooLongError) Unwrap() error { return ErrArgTooLong }

// newArgTooLongError builds the localized error. The catalog follows the
// request language (the same code the CLI resolves its interface language
// from); an unknown language or a load failure falls back to English, and a
// catalog-less last resort keeps the message non-empty. The i18n key depends
// on what was exceeded so the text never mislabels the number it quotes.
func newArgTooLongError(name, reqLang string, size, limit int, kind ArgKind) *ArgTooLongError {
	kb := func(n int) int { return (n + 1023) / 1024 }
	e := &ArgTooLongError{Provider: name, Kind: kind, Size: size, Limit: limit}
	key := "provider.cli.arg_too_long"
	args := []any{name, kb(size), limit / 1024}
	switch kind {
	case ArgTotalTooLong:
		key = "provider.cli.arg_total_too_long"
	case ArgRejectedByOS:
		key = "provider.cli.arg_rejected_by_os"
		args = []any{name, kb(size)}
	}
	cat, err := i18n.Load(lang.UICatalogFor(reqLang))
	if err != nil {
		e.msg = fmt.Sprintf("%s: the command line is too long for this platform; narrow the diff with --file/--dir or use claude-cli", name)
		return e
	}
	e.msg = cat.T(key, args...)
	return e
}

// isArgTooLong recognizes the OS's "argument list too long" failure from
// exec: E2BIG on POSIX, and Windows' "filename or extension is too long"
// (ERROR_FILENAME_EXCED_RANGE) when CreateProcess rejects the command line.
func isArgTooLong(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.E2BIG) {
		return true
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "argument list too long") ||
		strings.Contains(m, "filename or extension is too long")
}
