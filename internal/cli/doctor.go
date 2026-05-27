// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/doctor"
)

func newDoctorCmd() *cobra.Command {
	var quiet bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run a health check across the configured pipeline",
		Long: `Diagnoses the CommitBrief pipeline without running an actual review.
Checks git binary availability, config validity, provider reachability,
cache writability, COMMITBRIEF.md / OUTPUT.md sources, and the repo
.gitignore. Exits non-zero if any check fails — useful in CI for "is my
config still valid?" smoke tests.

With --quiet, only warning and failed lines are printed; an all-green
run produces no output.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := resolveContext(false)
			if err != nil {
				return err
			}
			runner := &doctor.Runner{
				RepoRoot: app.RepoRoot,
				Home:     userHome(),
				Config:   app.Config,
				Catalog:  app.Catalog,
			}
			results := runner.RunAll(cmd.Context())
			summary := doctor.Summarize(results)

			w := cmd.OutOrStdout()
			if err := writeDoctorReport(w, app.Catalog.T("doctor.heading", summary.Total), results, summary, quiet, app.Catalog); err != nil {
				return err
			}
			if summary.Failed > 0 {
				return errors.New(app.Catalog.T("doctor.aggregate_failed", summary.Failed))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "only print warning and failed checks")
	return cmd
}

// writeDoctorReport formats the result table and summary line. Layout
// is a single-line-per-check table; columns auto-size to the widest
// name so the detail column lines up. --quiet hides StatusOK rows but
// always prints the summary so the user knows the run happened.
func writeDoctorReport(w io.Writer, heading string, results []doctor.Result, summary doctor.Summary, quiet bool, cat catalog) error {
	if !quiet {
		if _, err := fmt.Fprintln(w, heading); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	maxName := 0
	for _, r := range results {
		if !quiet || r.Status != doctor.StatusOK {
			if n := lipgloss.Width(r.Name); n > maxName {
				maxName = n
			}
		}
	}

	for _, r := range results {
		if quiet && r.Status == doctor.StatusOK {
			continue
		}
		icon := doctorIcon(r.Status)
		pad := strings.Repeat(" ", maxName-lipgloss.Width(r.Name))
		line := icon + " " + r.Name + pad
		if r.Detail != "" {
			line += "  " + r.Detail
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	summaryLine := cat.T("doctor.summary", summary.Total, summary.OK, summary.Warnings, summary.Failed)
	if _, err := fmt.Fprintln(w, summaryLine); err != nil {
		return err
	}
	return nil
}

// catalog is the subset of i18n.Catalog the report needs; an interface
// lets tests inject a no-op without dragging the full catalog.
type catalog interface {
	T(key string, args ...any) string
}

// doctorIcon maps a Status to its colored Unicode glyph. Kept in this
// file rather than the doctor package so the renderer choice (lipgloss
// colors that match the rest of the CLI) stays a presentation concern.
func doctorIcon(s doctor.Status) string {
	switch s {
	case doctor.StatusOK:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render("✓")
	case doctor.StatusWarn:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true).Render("⚠")
	case doctor.StatusFail:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Render("✗")
	}
	return "?"
}
