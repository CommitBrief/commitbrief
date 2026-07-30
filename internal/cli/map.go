// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/graph"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

// `commitbrief map` (ADR-0037) — a deterministic view of the commit graph.
//
// No provider call, no cache, no cost. Two jobs:
//
//  1. Show how commits and branches actually relate, which `dry-run`'s counts
//     cannot.
//  2. Make the ADR-0035 commit filters *visible*. `dry-run` reports that 12
//     commits matched; `map --author alice` shows WHICH twelve and what they
//     sat between, with the rest dimmed as context.
//
// It is a viewer, never a gate: a successful render always exits 0.

type mapFlags struct {
	branches bool
	all      bool
}

// mapDefaultMaxCommits bounds the default DAG height. A graph taller than a
// few screens stops being a visualisation, and the walk itself costs a `git
// log` over that many commits.
const mapDefaultMaxCommits = 200

func newMapCmd() *cobra.Command {
	var f mapFlags

	cmd := &cobra.Command{
		Use:   "map [<git range>...]",
		Short: "Draw the commit graph, highlighting what a filter selected",
		Long: "Render the commit DAG for a range (or HEAD's history) as a lane graph: " +
			"one row per commit with its branch/tag labels, subject, author and age.\n\n" +
			"The commit filters apply on top, and that is the point: with " +
			"--author / --start-date / --end-date / --text set, matching commits are " +
			"highlighted and the rest are drawn as dimmed context, so you can see " +
			"exactly what a filter selects before spending a review on it.\n\n" +
			"--branches switches to a branch topology summary — where each branch " +
			"sits relative to the base, and how far ahead/behind.\n\n" +
			"Read-only and deterministic: no provider call, no cache, no cost.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMap(cmd, f, args)
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&f.branches, "branches", false, "show a branch topology summary instead of the commit graph")
	flags.BoolVar(&f.all, "all", false, "walk every branch, not just the given range (commit graph only)")
	return cmd
}

func runMap(cmd *cobra.Command, f mapFlags, args []string) error {
	app, err := resolveContext(true)
	if err != nil {
		return err
	}

	ctx, cancel := app.withTimeout(cmd.Context())
	defer cancel()
	cmd.SetContext(ctx)

	// map draws a graph, not findings. Rejecting the findings-oriented output
	// flags up front beats emitting something that isn't what the flag
	// promised. A graph JSON would be a new semver-locked schema; that is
	// deferred (ADR-0037), not silently approximated.
	if global.json || global.markdown {
		return errors.New(app.Catalog.T("map.flag_conflict_format"))
	}
	if global.failOn != "" || global.minSeverity != "" || global.suggestCommit {
		return errors.New(app.Catalog.T("map.flag_conflict_review"))
	}

	w, closer, err := openOutput(cmd)
	if err != nil {
		return err
	}
	defer closer()

	// Colour and box-drawing travel together: a terminal that refused ANSI is
	// also the one most likely to mangle U+2502, so both fall back at once.
	styled := ui.ColorEnabled(w, ui.ParseColorMode(global.color))
	opts := render.GraphOptions{
		Color:   styled,
		Unicode: styled,
		Width:   ui.TerminalWidth(w),
	}

	if f.branches {
		return runMapBranches(cmd.Context(), app, w, opts)
	}
	return runMapGraph(cmd.Context(), cmd, app, f, args, w, opts)
}

// runMapGraph is the default DAG view.
func runMapGraph(ctx context.Context, cmd *cobra.Command, app *appContext, f mapFlags, args []string, w io.Writer, opts render.GraphOptions) error {
	// The commit filter does double duty: its rev range bounds the walk, and
	// its predicate decides which rows are highlighted.
	// buildWalkFilter, not buildCommitFilter: map always walks history, so
	// `map --max-commits 20` is an ordinary bound rather than a modifier with
	// nothing to modify.
	filter, err := buildWalkFilter(app.Catalog, args)
	if err != nil {
		return err
	}
	filtered := filter.Active()

	// Two walks when a filter is active. The first drops the *predicate* but
	// keeps the range, so the graph still shows the surrounding commits and
	// the lanes stay topologically correct — a graph of only the matches would
	// be a list, not a graph. The second is the real filter, and reusing
	// SelectCommits for it means the highlight is exactly what a review would
	// pick up, never an approximation of it.
	topology := filter
	topology.Authors = nil
	topology.Committers = nil
	topology.Text = ""
	topology.Since = time.Time{}
	topology.Until = time.Time{}
	topology.MaxCommits = mapCommitLimit()
	if f.all {
		topology.Rev = []string{"--all"}
	}
	// Merges are structure, not noise, in a graph: hiding them would leave
	// lanes that fork and never rejoin.
	topology.Merges = true

	all, err := git.SelectCommits(ctx, app.RepoRoot, topology)
	if err != nil {
		return err
	}
	if len(all.Commits) == 0 {
		infof("%s", app.Catalog.T("map.no_commits"))
		return nil
	}

	var matched map[string]bool
	if filtered {
		sel, sErr := git.SelectCommits(ctx, app.RepoRoot, filter)
		if sErr != nil {
			return sErr
		}
		matched = make(map[string]bool, len(sel.Commits))
		for _, c := range sel.Commits {
			matched[c.Hash] = true
		}
		infof("%s", app.Catalog.T("filter.commit.selected", len(sel.Commits), all.Walked))
	}
	opts.Filtered = filtered

	// Ref labels are decoration; a repo that cannot enumerate them still gets
	// its graph.
	refs, _ := git.RefsByCommit(ctx, app.RepoRoot)

	nodes := make([]graph.Commit, 0, len(all.Commits))
	for _, c := range all.Commits {
		nodes = append(nodes, graph.Commit{Hash: c.Hash, Parents: c.Parents})
	}
	laid := graph.Layout(nodes, matched)

	rows := make([]render.GraphRow, 0, len(laid))
	for i, row := range laid {
		rows = append(rows, render.GraphRow{
			Row:    row,
			Commit: all.Commits[i],
			Refs:   refs[all.Commits[i].Hash],
		})
	}
	if rErr := render.Graph(w, rows, opts); rErr != nil {
		return rErr
	}
	// The truncation caveat is about the view, not part of it, so it goes to
	// stderr and a redirected stdout stays a clean graph.
	if all.Truncated {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), app.Catalog.T("map.truncated", mapCommitLimit()))
	}
	return nil
}

// runMapBranches is the `--branches` topology view.
func runMapBranches(ctx context.Context, app *appContext, w io.Writer, opts render.GraphOptions) error {
	// The commit filters select commits; a branch list has none to select.
	// Rejecting beats rendering an identical view that ignored the flag.
	if commitFiltersRequested() {
		return errors.New(app.Catalog.T("map.branches_flag_conflict", commitFilterFlags))
	}
	branches, err := git.BranchTopology(ctx, app.RepoRoot, "")
	if err != nil {
		return err
	}
	if len(branches) == 0 {
		infof("%s", app.Catalog.T("map.no_branches"))
		return nil
	}
	return render.BranchTopology(w, branches, opts)
}

// mapCommitLimit resolves the row cap from the shared --max-commits flag,
// falling back to the built-in default.
func mapCommitLimit() int {
	if global.maxCommits > 0 {
		return global.maxCommits
	}
	return mapDefaultMaxCommits
}
