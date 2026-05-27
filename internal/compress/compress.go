// SPDX-License-Identifier: GPL-3.0-or-later

package compress

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

type Level int

const (
	LevelBalanced Level = iota
	LevelLight
	LevelAggressive
)

func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "balanced":
		return LevelBalanced, nil
	case "light":
		return LevelLight, nil
	case "aggressive":
		return LevelAggressive, nil
	default:
		return 0, fmt.Errorf("compress: unknown level %q (want: light, balanced, aggressive)", s)
	}
}

func (l Level) String() string {
	switch l {
	case LevelLight:
		return "light"
	case LevelAggressive:
		return "aggressive"
	default:
		return "balanced"
	}
}

//go:embed prompts/light.md
var promptLight string

//go:embed prompts/balanced.md
var promptBalanced string

//go:embed prompts/aggressive.md
var promptAggressive string

func systemPrompt(l Level) string {
	switch l {
	case LevelLight:
		return promptLight
	case LevelAggressive:
		return promptAggressive
	default:
		return promptBalanced
	}
}

type Request struct {
	Level    Level
	Original string // raw COMMITBRIEF.md content
	Model    string
}

type Result struct {
	OriginalContent   string
	CompressedContent string
	OriginalChars     int
	CompressedChars   int
	OriginalTokens    int
	CompressedTokens  int
	Usage             provider.Usage
	Aborted           bool   // true if the compressed output is not smaller; original unchanged
	AbortReason       string // why we refused to apply
}

// Run sends the compression request to the provider, post-processes the
// response (strips common preambles and code-fence wrapping), and reports
// the savings. The caller is responsible for displaying the diff, asking
// for confirmation, and persisting the result via Apply.
func Run(ctx context.Context, p provider.Provider, req Request) (Result, error) {
	if req.Original == "" {
		return Result{}, errors.New("compress: empty original content")
	}
	if p == nil {
		return Result{}, errors.New("compress: nil provider")
	}

	system := systemPrompt(req.Level)
	user := wrapForCompression(req.Original)

	resp, err := p.Review(ctx, provider.Request{
		Model:        req.Model,
		SystemPrompt: system,
		UserPrompt:   user,
	})
	if err != nil {
		return Result{}, fmt.Errorf("compress: provider call: %w", err)
	}

	compressed := postProcess(resp.Content)

	result := Result{
		OriginalContent:   req.Original,
		CompressedContent: compressed,
		OriginalChars:     len(req.Original),
		CompressedChars:   len(compressed),
		OriginalTokens:    estimateTokens(req.Original),
		CompressedTokens:  estimateTokens(compressed),
		Usage:             resp.Usage,
	}
	if result.CompressedChars >= result.OriginalChars {
		result.Aborted = true
		result.AbortReason = fmt.Sprintf("compressed output (%d chars) is not smaller than original (%d chars)",
			result.CompressedChars, result.OriginalChars)
	}
	return result, nil
}

// Savings returns a per-review token reduction percentage and the absolute
// token delta. A 1000→400 compression yields (60.0, 600).
func (r Result) Savings() (percent float64, deltaTokens int) {
	if r.OriginalTokens == 0 {
		return 0, 0
	}
	delta := r.OriginalTokens - r.CompressedTokens
	return float64(delta) * 100 / float64(r.OriginalTokens), delta
}

// wrapForCompression mirrors ADR-0004's prompt-injection defense for the
// review path: the rules content goes inside an XML-style block named
// <user_rules>; the compression system prompt instructs the model to
// treat that block as immutable text rather than instructions.
func wrapForCompression(content string) string {
	var sb strings.Builder
	sb.WriteString("<user_rules>\n")
	sb.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("</user_rules>")
	return sb.String()
}

// postProcess trims preambles like "Here is the compressed version:" and
// peels off triple-backtick fences if the model wrapped the whole output.
// Conservative: only strips obvious wrappers, never edits the file body.
func postProcess(out string) string {
	out = strings.TrimSpace(out)

	// Strip a leading single-line preamble that the model sometimes adds
	// despite explicit instructions ("Here is the compressed file:", etc.).
	if idx := strings.Index(out, "\n"); idx > 0 && idx < 120 {
		first := strings.ToLower(out[:idx])
		if strings.Contains(first, "compressed") || strings.Contains(first, "here is") {
			out = strings.TrimSpace(out[idx+1:])
		}
	}

	// Strip surrounding markdown code fence if the entire body is wrapped.
	if strings.HasPrefix(out, "```") {
		// Find the end of the opening fence line
		nl := strings.Index(out, "\n")
		if nl > 0 {
			out = out[nl+1:]
		}
		if idx := strings.LastIndex(out, "```"); idx >= 0 {
			out = strings.TrimRight(out[:idx], "\n ")
		}
	}
	return strings.TrimSpace(out) + "\n"
}

// Apply writes the compressed content to <repoRoot>/COMMITBRIEF.md after
// backing the original up to <repoRoot>/.commitbrief/backups/<timestamp>.md.
// The backup directory is gitignored by the .commitbrief/ rule
// `commitbrief setup --local` adds.
func Apply(repoRoot string, r Result, backupDir, timestamp string) (rulesPath, backupPath string, err error) {
	if r.Aborted {
		return "", "", fmt.Errorf("compress: not applying aborted result: %s", r.AbortReason)
	}
	if repoRoot == "" {
		return "", "", errors.New("compress: empty repoRoot")
	}
	rulesPath = filepath.Join(repoRoot, rules.Filename)
	if backupDir == "" {
		backupDir = filepath.Join(repoRoot, rules.LocalSubdir, "backups")
	}
	backupPath = filepath.Join(backupDir, fmt.Sprintf("COMMITBRIEF-%s.md", timestamp))

	if err := writeBackupAndApply(rulesPath, backupPath, r.OriginalContent, r.CompressedContent); err != nil {
		return "", "", err
	}
	return rulesPath, backupPath, nil
}

// estimateTokens is the chars/4 heuristic shared with internal/diff. Kept
// inline so compress doesn't drag an import cycle through diff.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}
