package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/render"
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
`

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Print the command reference",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := render.Payload{Content: commandReference}
			if global.markdown || global.json || !ui.ColorEnabled(os.Stdout, ui.ParseColorMode(global.color)) {
				return render.Markdown(os.Stdout, payload)
			}
			return render.Terminal(os.Stdout, payload)
		},
	}
}
