package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

const commandReference = `# CommitBrief Commands

## Review (default)

` + "```" + `
commitbrief                       # review staged changes (= --staged)
commitbrief --unstaged            # review unstaged working-tree changes
commitbrief --file <path>         # review changes in a single file
commitbrief --commit <hash>       # review a specific commit
commitbrief --pull-request a...b  # review a PR-style three-dot diff
commitbrief --branch <target>     # review current branch vs target ref
` + "```" + `

## Setup and rules

` + "```" + `
commitbrief setup [--local]       # provider + API key wizard
commitbrief init                  # write COMMITBRIEF.md to the repo
` + "```" + `

## Inspection

` + "```" + `
commitbrief dry-run               # build prompt and report; no API call
commitbrief list                  # this reference
` + "```" + `

## Maintenance

` + "```" + `
commitbrief compress              # shrink COMMITBRIEF.md losslessly
commitbrief cache clear           # remove cached LLM responses for this repo
` + "```" + `

## Global flags

- ` + "`--json`" + ` — machine-readable JSON output
- ` + "`--markdown`" + ` — plain markdown, no ANSI
- ` + "`-o, --output <file>`" + ` — write to file instead of stdout
- ` + "`--no-cache`" + ` — bypass cache read and write
- ` + "`-y, --yes`" + ` — auto-confirm prompts
- ` + "`-v, --verbose`" + ` — show token/cost/latency footer
- ` + "`-q, --quiet`" + ` — suppress info messages on stderr
- ` + "`--lang <code>`" + ` — override output language
- ` + "`--provider <name>`" + ` — override configured provider
- ` + "`--model <model>`" + ` — override configured model
- ` + "`--color <mode>`" + ` — auto | always | never

## Filtering

Three layers, applied in order. Later layers win, so a ` + "`!pattern`" + ` in
` + "`.commitbriefignore`" + ` can revert a built-in exclusion:

1. **Built-in defaults** — binaries, lock files, ` + "`vendor/**`" + `,
   ` + "`node_modules/**`" + `, generated code, build artifacts, IDE/OS noise,
   ` + "`.commitbrief/cache/**`" + `.
2. **` + "`.commitbriefignore`" + `** (repo root, team-shared) — gitignore syntax. Use
   ` + "`!path`" + ` to un-ignore a file the built-in layer caught.
3. **` + "`COMMITBRIEF.md`" + ` semantic filter** — interpreted by the LLM
   (file-level decisions happen above; this is the natural-language layer).

Example ` + "`.commitbriefignore`" + `:

` + "```" + `gitignore
# generated migrations
db/migrations/*.sql

# vendored docs
docs/vendor/**

# but please review go.sum despite the built-in default
!go.sum
` + "```" + `

` + "`commitbrief dry-run --staged`" + ` reports how many files each layer
removed.
`

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Print the command reference",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// resolveContext(false) → tolerate non-repo invocation; the
			// reference itself doesn't need a repo, only the summary does.
			app, err := resolveContext(false)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			content := commandReference + configSummary(app)
			payload := render.Payload{Content: content}
			if global.markdown || global.json || !ui.ColorEnabled(w, ui.ParseColorMode(global.color)) {
				return render.Markdown(w, payload)
			}
			return render.Terminal(w, payload)
		},
	}
}

// configSummary appends a "Current configuration" markdown section to the
// command reference. Surfaces what the binary would actually do *now*
// (active provider/model + which rules + cache footprint) so a user can
// answer "where do I stand?" with one command. API keys are never shown
// here — see `commitbrief providers list` for that.
func configSummary(app *appContext) string {
	var sb strings.Builder
	sb.WriteString("\n## Current configuration\n\n")

	provider := app.Config.Provider
	model := app.Config.Providers[provider].Model
	if model == "" {
		model = "(provider default)"
	}
	fmt.Fprintf(&sb, "- **Active provider:** `%s` / `%s`\n", provider, model)

	// COMMITBRIEF.md — rules.Load requires a repo path; outside a repo we
	// fall through to the built-in default with an explanatory tag.
	if app.RepoRoot == "" {
		sb.WriteString("- **Rules file (COMMITBRIEF.md):** built-in default (not in a repo)\n")
	} else if loaded, err := rules.Load(app.RepoRoot); err != nil {
		fmt.Fprintf(&sb, "- **Rules file (COMMITBRIEF.md):** load error: %v\n", err)
	} else if loaded.Source == rules.SourceFile {
		fmt.Fprintf(&sb, "- **Rules file (COMMITBRIEF.md):** `%s`\n", loaded.Path)
	} else {
		sb.WriteString("- **Rules file (COMMITBRIEF.md):** built-in default\n")
	}

	// OUTPUT.md — works without a repo (user-level path can still apply).
	output, err := rules.LoadOutput(app.RepoRoot, userHome())
	switch {
	case err != nil:
		fmt.Fprintf(&sb, "- **Output template (OUTPUT.md):** load error: %v\n", err)
	case output.Source == rules.SourceFile:
		fmt.Fprintf(&sb, "- **Output template (OUTPUT.md):** `%s` (repo override)\n", output.Path)
	case output.Source == rules.SourceUserFile:
		fmt.Fprintf(&sb, "- **Output template (OUTPUT.md):** `%s` (user-level)\n", output.Path)
	default:
		sb.WriteString("- **Output template (OUTPUT.md):** built-in default\n")
	}

	if app.RepoRoot == "" {
		sb.WriteString("- **Cache:** unavailable (not in a repo)\n")
	} else {
		count, bytes := cacheStats(filepath.Join(app.RepoRoot, ".commitbrief", "cache"))
		fmt.Fprintf(&sb, "- **Cache:** %s, %s\n", entriesLabel(count), humanBytes(bytes))
	}

	return sb.String()
}

// cacheStats walks the cache directory and returns the count + total byte
// size of .json entry files. Missing directory → 0/0 (a fresh repo has no
// cache yet; that's not an error worth surfacing in `list`). Sub-dirs and
// non-json files (like the auto-generated .gitignore) are ignored.
func cacheStats(dir string) (count int, totalBytes int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		count++
		totalBytes += info.Size()
	}
	return count, totalBytes
}

func entriesLabel(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}

func humanBytes(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < k*k:
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	}
}
