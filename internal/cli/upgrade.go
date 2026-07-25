// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/ui"
	"github.com/CommitBrief/commitbrief/internal/upgrade"
	"github.com/CommitBrief/commitbrief/internal/version"
)

// upgradeReport is the --json payload. It is intentionally NOT review
// schema v1 — it describes an installation, not a review, and nothing
// in the findings contract changes because of it.
type upgradeReport struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	Method          string `json:"method"`
	UpdateAvailable bool   `json:"update_available"`
	Action          string `json:"action"`
}

func writeUpgradeJSON(w io.Writer, rep upgradeReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

func newUpgradeCmd() *cobra.Command {
	var checkOnly bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Check for a newer CommitBrief and install it",
		Long: `Checks GitHub Releases for a newer CommitBrief and installs it.

How the binary was installed decides what happens. A Homebrew, Scoop or
'go install' install is upgraded by its own package manager, because
overwriting a manager-owned binary desynchronizes its metadata. Only a
manually installed binary (a GitHub Releases tarball) is downloaded,
SHA-256 verified against the release checksums, and replaced in place.

Nothing is installed without confirmation, and nothing is downloaded if
the target cannot be written — CommitBrief never invokes sudo itself.

--check reports what would happen and installs nothing. --json implies
--check: it prints a single report object and exits.

This is the only network request CommitBrief makes on its own behalf,
and only when you run this command. There is no automatic update check
and no telemetry.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgrade(cmd, checkOnly || global.json)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only report whether a newer version exists; install nothing")
	return cmd
}

func runUpgrade(cmd *cobra.Command, checkOnly bool) error {
	app, err := resolveContext(false)
	if err != nil {
		return err
	}
	cat := app.Catalog
	out := cmd.OutOrStdout()
	msg := cmd.ErrOrStderr() // human chatter goes to stderr so --json stdout stays clean

	exe, err := upgrade.ResolveExe()
	if err != nil {
		return err
	}
	// Sweep the moved-aside binary a previous Windows upgrade could not
	// delete while it was still executing.
	upgrade.CleanupStale(exe)

	method := upgrade.Detect(upgrade.CurrentEnv(exe))

	current, ok := upgrade.ParseVersion(version.Version)
	if !ok {
		// --json promises a report object on every non-error run. A dev
		// build must therefore still emit one: returning nil with empty
		// stdout would hand a parsing script silence and exit 0, with no
		// way to tell "no update" from "the command did nothing".
		if global.json {
			return writeUpgradeJSON(out, upgradeReport{
				Current:         version.Version,
				Method:          string(method),
				UpdateAvailable: false,
			})
		}
		if checkOnly {
			_, _ = fmt.Fprintln(msg, cat.T("upgrade.dev_build", version.Version))
			return nil
		}
		return errors.New(cat.T("upgrade.dev_build", version.Version))
	}

	client := upgrade.NewClient(version.Version)
	rel, err := client.Latest(cmd.Context())
	if err != nil {
		switch {
		case errors.Is(err, upgrade.ErrRateLimited):
			return errors.New(cat.T("upgrade.err_rate_limited"))
		case errors.Is(err, upgrade.ErrNoRelease):
			return errors.New(cat.T("upgrade.err_no_release"))
		default:
			return errors.New(cat.T("upgrade.err_network", err))
		}
	}

	latest, ok := upgrade.ParseVersion(rel.TagName)
	if !ok {
		return errors.New(cat.T("upgrade.err_no_release"))
	}

	argv := upgrade.Command(method)
	action := strings.Join(argv, " ")
	if method == upgrade.MethodManual {
		action = upgrade.AssetName(rel.TagName, runtime.GOOS, runtime.GOARCH)
	}

	rep := upgradeReport{
		Current:         current.String(),
		Latest:          latest.String(),
		Method:          string(method),
		UpdateAvailable: current.Compare(latest) < 0,
		Action:          action,
	}

	if !rep.UpdateAvailable {
		if global.json {
			rep.Action = ""
			return writeUpgradeJSON(out, rep)
		}
		_, _ = fmt.Fprintln(msg, cat.T("upgrade.up_to_date", current.String()))
		return nil
	}

	if global.json {
		return writeUpgradeJSON(out, rep)
	}

	_, _ = fmt.Fprintln(msg, cat.T("upgrade.available", current.String(), latest.String()))
	_, _ = fmt.Fprintln(msg, cat.T("upgrade.method", string(method)))
	if method == upgrade.MethodManual {
		_, _ = fmt.Fprintln(msg, cat.T("upgrade.action_manual", action))
	} else {
		_, _ = fmt.Fprintln(msg, cat.T("upgrade.action_managed", action))
	}

	if checkOnly {
		return nil
	}

	// NonInteractive mirrors internal/cli/cache.go: without a TTY and
	// without --yes the answer is a deterministic "no", so an unattended
	// run aborts instead of hanging or half-consuming stdin.
	confirmed, err := ui.Confirm(cmd.InOrStdin(), msg, cat.T("upgrade.confirm"), ui.AskOptions{
		AssumeYes:      global.yes,
		Interactive:    ui.IsStdinTTY(os.Stdin),
		NonInteractive: !ui.IsStdinTTY(os.Stdin),
		Catalog:        app.Catalog,
	})
	if err != nil {
		return err
	}
	if !confirmed {
		_, _ = fmt.Fprintln(msg, cat.T("upgrade.cancelled"))
		return nil
	}

	if method == upgrade.MethodManual {
		if err := upgrade.PreflightWritable(exe); err != nil {
			_, _ = fmt.Fprintln(msg, cat.T("upgrade.hint_manual"))
			_, _ = fmt.Fprintf(msg, "  sudo %s upgrade\n", exe)
			_, _ = fmt.Fprintf(msg, "  %s\n", upgrade.ReleasesPage)
			return errors.New(cat.T("upgrade.err_not_writable", exe))
		}
		if err := upgrade.InstallManual(cmd.Context(), upgrade.ManualOptions{
			Client:  client,
			Release: rel,
			Target:  exe,
			GOOS:    runtime.GOOS,
			GOARCH:  runtime.GOARCH,
		}); err != nil {
			switch {
			case errors.Is(err, upgrade.ErrChecksumMismatch):
				return errors.New(cat.T("upgrade.err_checksum", action))
			case errors.Is(err, upgrade.ErrAssetMissing):
				return errors.New(cat.T("upgrade.err_asset_missing", runtime.GOOS, runtime.GOARCH, upgrade.ReleasesPage))
			case errors.Is(err, upgrade.ErrNotWritable):
				return errors.New(cat.T("upgrade.err_not_writable", exe))
			default:
				return err
			}
		}
	} else {
		if err := upgrade.Run(cmd.Context(), argv, msg, cmd.ErrOrStderr()); err != nil {
			if errors.Is(err, upgrade.ErrToolMissing) {
				return errors.New(cat.T("upgrade.err_tool_missing", string(method), argv[0]))
			}
			return err
		}
	}

	_, _ = fmt.Fprintln(msg, cat.T("upgrade.success", latest.String()))
	return nil
}
