// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/mcp"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/version"
)

// newMCPCmd is the `commitbrief mcp` entry point (ADR-0028): it runs a
// Model Context Protocol server over stdio so an AI agent/host can invoke
// CommitBrief as a tool — typically a self-review gate the agent calls
// before it submits code. The command is opt-in and additive; it changes
// nothing about the existing review surface.
//
// The server speaks JSON-RPC 2.0 over line-delimited stdin/stdout (the MCP
// stdio transport) and exposes one tool, `review`, which runs the exact same
// pipeline as `commitbrief --json` and returns the structured findings
// (JSON schema v1) plus a short text summary. Diagnostics go to stderr so the
// stdout JSON-RPC channel stays clean.
func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP (Model Context Protocol) server over stdio",
		Long: "Expose CommitBrief to an AI agent/host as an MCP tool. The server " +
			"speaks JSON-RPC 2.0 over stdio (the MCP stdio transport) and offers a " +
			"`review` tool that runs the standard review pipeline on the current " +
			"repo's diff and returns the structured findings (JSON schema v1).\n\n" +
			"Wire it into a host (e.g. Claude Desktop / an agent runtime) as a " +
			"stdio MCP server invoking `commitbrief mcp`. The host owns the lifecycle; " +
			"this process reads requests on stdin and writes responses on stdout until " +
			"the host closes the stream. Diagnostics are written to stderr.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCP(cmd)
		},
	}
}

// runMCP builds the server, registers the review tool, and serves the stdio
// transport until EOF. resolveContext is NOT called here — the server may be
// long-lived and each tool call resolves its own fresh context (config + repo)
// per invocation, exactly like a CLI run, so config edits between calls are
// picked up.
func runMCP(cmd *cobra.Command) error {
	app, err := resolveContext(false)
	if err != nil {
		return err
	}

	srv := mcp.New("commitbrief", version.Version)
	srv.Register(reviewToolDef(app), reviewToolHandler())
	if err := srv.Validate(); err != nil {
		return err
	}

	infof("%s", app.Catalog.T("mcp.serving"))
	// Serve reads stdin and writes stdout directly (the JSON-RPC channel),
	// independent of cobra's out/err sinks — those carry human diagnostics.
	if err := srv.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout()); err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	return nil
}

// reviewToolInputSchema is the JSON Schema (draft 2020-12 compatible) for the
// `review` tool's arguments. Every field is optional; with no arguments the
// tool reviews the staged diff, matching `commitbrief --staged`. Kept as a Go
// map so the schema lives next to the argument struct it validates.
func reviewToolInputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"staged": map[string]any{
				"type":        "boolean",
				"description": "Review the staged diff (default true). Set false together with unstaged=true to review the working tree.",
			},
			"unstaged": map[string]any{
				"type":        "boolean",
				"description": "Review the unstaged (working-tree) diff instead of the staged diff. Mutually exclusive with staged.",
			},
			"diff": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Arbitrary `git diff` arguments to review a range instead of staged/unstaged changes, e.g. [\"HEAD~3\",\"HEAD\"] or [\"main...feature\"]. Forwarded verbatim to git.",
			},
			"provider": map[string]any{
				"type":        "string",
				"description": "Override the configured provider for this review (e.g. \"anthropic\", \"openai\").",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Override the configured model for this review.",
			},
			"fail_on": map[string]any{
				"type":        "string",
				"enum":        []string{"critical", "high", "medium", "low", "info", "any", "none"},
				"description": "Report a gate failure (failed=true in the summary) when a finding meets/exceeds this severity. Findings are still returned regardless.",
			},
			"min_severity": map[string]any{
				"type":        "string",
				"enum":        []string{"critical", "high", "medium", "low", "info", "none"},
				"description": "Hide findings below this severity in the returned set (display filter; the gate still sees the full set).",
			},
			"no_flaky": map[string]any{
				"type":        "boolean",
				"description": "Skip the deterministic flaky-test detector (ADR-0022).",
			},
		},
		"additionalProperties": false,
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		// The schema is a static literal; a marshal failure is a programming
		// error, not a runtime condition. Panic surfaces it at startup.
		panic("mcp: marshal review input schema: " + err.Error())
	}
	return raw
}

// reviewToolDef builds the advertised descriptor for the `review` tool. The
// description is i18n'd (output-language-independent host text uses the UI
// catalog resolved in app).
func reviewToolDef(app *appContext) mcp.Tool {
	return mcp.Tool{
		Name:        "review",
		Description: app.Catalog.T("mcp.tool.review.description"),
		InputSchema: reviewToolInputSchema(),
	}
}

// reviewToolArgs is the decoded shape of the `review` tool's arguments. All
// fields are pointers/zero-tolerant so "absent" is distinguishable from
// "false"/"" where it matters (staged defaults to true unless unstaged is set).
type reviewToolArgs struct {
	Staged      *bool    `json:"staged,omitempty"`
	Unstaged    bool     `json:"unstaged,omitempty"`
	Diff        []string `json:"diff,omitempty"`
	Provider    string   `json:"provider,omitempty"`
	Model       string   `json:"model,omitempty"`
	FailOn      string   `json:"fail_on,omitempty"`
	MinSeverity string   `json:"min_severity,omitempty"`
	NoFlaky     bool     `json:"no_flaky,omitempty"`
}

// reviewToolHandler returns the ToolHandler that runs the review pipeline. It
// closes over nothing mutable; each call re-derives state from arguments.
func reviewToolHandler() mcp.ToolHandler {
	return func(ctx context.Context, arguments json.RawMessage) (string, string, error) {
		var args reviewToolArgs
		if len(arguments) > 0 {
			// DisallowUnknownFields mirrors the schema's additionalProperties:false
			// so a host typo (e.g. "failon") is a clear error, not a silent no-op.
			dec := json.NewDecoder(bytes.NewReader(arguments))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&args); err != nil {
				return "", "", fmt.Errorf("invalid arguments: %w", err)
			}
		}
		if args.Unstaged && args.Staged != nil && *args.Staged {
			return "", "", fmt.Errorf("staged and unstaged are mutually exclusive")
		}
		return runReviewForMCP(ctx, args)
	}
}

// runReviewForMCP is the reuse seam: it drives the EXISTING runReview pipeline
// (internal/cli/review.go) rather than reimplementing any of it. It does so by
//
//  1. snapshotting and restoring the package-level global/reviewScope flag
//     state so an MCP review never leaks flags into a later call;
//  2. forcing --json + --quiet + --color=never so runReview's own renderer
//     emits the schema-v1 document into a buffer we capture; and
//  3. handing runReview a synthetic *cobra.Command whose stdout/stderr are
//     buffers and whose context is the host's, then translating the result.
//
// Everything downstream — diff fetch, three-layer filtering, the pre-send
// guard + secret scan, token/cost preflight, cache, the provider call, the
// flaky pre-pass, and signal control — runs exactly as it does for a terminal
// review. No pipeline is duplicated.
func runReviewForMCP(ctx context.Context, args reviewToolArgs) (string, string, error) {
	// Snapshot + restore package-global state. The CLI is single-invocation
	// per process for normal commands, but the MCP server is long-lived and
	// may handle many tool calls, so we MUST leave global/reviewScope exactly
	// as we found them.
	savedGlobal, savedScope := global, reviewScope
	defer func() { global, reviewScope = savedGlobal, savedScope }()

	// Start from a clean slate so a prior call's flags don't bleed in, then
	// apply the tool arguments.
	global = globalFlags{color: "never"}
	reviewScope = reviewScopeFlags{}

	// Machine-output mode: --json makes runReview's renderResult emit the
	// schema-v1 JSON document; --quiet silences info chatter on our captured
	// stderr; cache stays on (a repeated identical review is cheap and the
	// host may poll). The host is non-interactive, so the guard/secret/cost
	// preflights will abort-on-non-TTY if triggered — that abort surfaces as a
	// tool error, which is the correct, safe behavior.
	global.json = true
	global.quiet = true

	scope := reviewScopeFlags{}
	switch {
	case len(args.Diff) > 0:
		// Range review: scope flags are ignored when diffArgs is non-empty,
		// matching the `diff` subcommand.
	case args.Unstaged:
		scope.unstaged = true
	default:
		scope.staged = true
	}
	reviewScope = scope

	global.provider = strings.TrimSpace(args.Provider)
	global.model = strings.TrimSpace(args.Model)
	global.failOn = strings.TrimSpace(args.FailOn)
	global.minSeverity = strings.TrimSpace(args.MinSeverity)
	global.noFlaky = args.NoFlaky

	// Synthetic command: buffered sinks + the host context. runReview reads
	// cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr() — never os.Stdout
	// for its rendered output — so the JSON lands in outBuf.
	var outBuf, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetContext(ctx)

	reviewErr := runReview(cmd, scope, args.Diff)

	jsonOut := strings.TrimSpace(outBuf.String())
	// A review that produced a parseable schema-v1 document SUCCEEDED, even
	// when runReview returned a non-nil error: that error is the --fail-on
	// gate firing (the JSON is already rendered before applyFailOn runs). We
	// surface the gate in the summary, not as a tool failure, so the agent
	// gets the findings AND knows the gate tripped.
	doc, parseErr := parseReviewDoc(jsonOut)
	if parseErr != nil {
		// No usable JSON: a real failure (no staged changes, provider error,
		// aborted guard, etc.). Prefer runReview's error; fall back to any
		// captured stderr diagnostic so the host sees something actionable.
		if reviewErr != nil {
			return "", "", reviewErr
		}
		if diag := strings.TrimSpace(errBuf.String()); diag != "" {
			return "", "", fmt.Errorf("%s", diag)
		}
		return "", "", fmt.Errorf("review produced no output: %w", parseErr)
	}

	summary := reviewSummary(doc, reviewErr)
	return summary, jsonOut, nil
}

// reviewDoc is the minimal projection of the schema-v1 JSON we read back to
// build the text summary. We re-parse our own rendered output (rather than
// thread a typed result out of runReview) so the MCP tool stays a thin
// consumer of the locked JSON contract — the same contract external tools
// rely on — instead of reaching into pipeline internals.
type reviewDoc struct {
	Schema   int              `json:"schema"`
	Findings []render.Finding `json:"findings"`
	Meta     struct {
		Provider   string `json:"provider"`
		Model      string `json:"model"`
		Cached     bool   `json:"cached"`
		Baselined  int    `json:"baselined"`
		Suppressed int    `json:"suppressed"`
	} `json:"meta"`
}

// parseReviewDoc decodes a rendered schema-v1 document. It requires schema==1
// and a non-nil findings array so a stray non-JSON line (which can't happen on
// the happy path, but defends the contract) is treated as "no usable output".
func parseReviewDoc(s string) (reviewDoc, error) {
	if s == "" {
		return reviewDoc{}, fmt.Errorf("empty output")
	}
	var doc reviewDoc
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return reviewDoc{}, err
	}
	if doc.Schema != render.SchemaVersion {
		return reviewDoc{}, fmt.Errorf("unexpected schema %d", doc.Schema)
	}
	return doc, nil
}

// reviewSummary builds the short human-readable text block that precedes the
// structured JSON in the tool result. It reports the finding count by severity
// and whether the --fail-on gate tripped. Deliberately English + compact: it
// is a model-facing orientation line, not localized UI.
func reviewSummary(doc reviewDoc, reviewErr error) string {
	var b strings.Builder
	n := len(doc.Findings)
	if n == 0 {
		b.WriteString("CommitBrief review: no findings.")
	} else {
		counts := map[render.Severity]int{}
		for _, f := range doc.Findings {
			counts[f.Severity]++
		}
		parts := make([]string, 0, 5)
		for _, sev := range []render.Severity{
			render.SeverityCritical, render.SeverityHigh, render.SeverityMedium,
			render.SeverityLow, render.SeverityInfo,
		} {
			if c := counts[sev]; c > 0 {
				parts = append(parts, strconv.Itoa(c)+" "+string(sev))
			}
		}
		b.WriteString("CommitBrief review: ")
		b.WriteString(strconv.Itoa(n))
		b.WriteString(" finding(s) (")
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString(").")
	}
	if doc.Meta.Provider != "" {
		b.WriteString(" Provider: ")
		b.WriteString(doc.Meta.Provider)
		if doc.Meta.Model != "" {
			b.WriteString("/")
			b.WriteString(doc.Meta.Model)
		}
		b.WriteString(".")
	}
	if doc.Meta.Cached {
		b.WriteString(" (cached)")
	}
	// reviewErr is non-nil here only because the --fail-on gate fired (a real
	// error would have been returned before this point). Report the gate so
	// the agent treats the submission as blocked.
	if reviewErr != nil {
		b.WriteString(" GATE FAILED: ")
		b.WriteString(reviewErr.Error())
	}
	return b.String()
}
