// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
		case errors.Is(err, upgrade.ErrBadResponse):
			// The server was reached and answered; the payload just
			// didn't parse. That is distinct from a network failure and
			// must not be reported as one.
			return errors.New(cat.T("upgrade.err_bad_response", err))
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
		// Surface the same permission gate a real run would hit, so a
		// root-owned or read-only manual install is reported honestly
		// instead of promising an install that would actually be
		// refused. Never reached when --json implied checkOnly: that
		// path already returned above, and --json prints only the
		// report object. A warning only — --check always exits 0.
		if method == upgrade.MethodManual {
			if err := upgrade.PreflightWritable(exe); err != nil {
				_, _ = fmt.Fprintln(msg, cat.T("upgrade.err_not_writable", exe))
				_, _ = fmt.Fprintln(msg, cat.T("upgrade.hint_manual"))
				_, _ = fmt.Fprintf(msg, "  sudo %s upgrade\n", exe)
				_, _ = fmt.Fprintf(msg, "  %s\n", upgrade.ReleasesPage)
			}
		}
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
		verifyReplacement(cmd, cat, msg, exe, latest)
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

// verifyReplacement re-runs the binary we just swapped and checks two
// independent things, each with its own warning: whether it reports the
// expected version, and whether some other "commitbrief" resolves
// earlier on PATH than the file that was just upgraded. Neither implies
// the other — a version mismatch after execing the resolved `exe`
// directly cannot be explained by PATH shadowing, since exec bypasses
// PATH entirely; the shadow check exists to catch the separate, real
// problem that the command the user types next may still resolve to
// the old binary even though this exact file was upgraded correctly.
//
// Never fatal — the upgrade already happened, and neither a failure to
// re-exec (a sandbox, a hardened mount) nor a shadowing PATH entry is a
// reason to report failure. Applies to the manual path only; a package
// manager may relocate its binary, so `exe` is not necessarily the new
// file after delegation.
func verifyReplacement(cmd *cobra.Command, cat catalog, msg io.Writer, exe string, latest upgrade.Version) {
	out, err := exec.CommandContext(cmd.Context(), exe, "--version").Output()
	if err != nil {
		_, _ = fmt.Fprintln(msg, cat.T("upgrade.verify_failed", err))
	} else if reported := strings.TrimSpace(string(out)); !reportsVersion(reported, latest) {
		_, _ = fmt.Fprintln(msg, cat.T("upgrade.verify_mismatch", reported, latest.String()))
	}

	if shadow, ok := shadowingPath(exe); ok {
		_, _ = fmt.Fprintln(msg, cat.T("upgrade.verify_shadowed", exe, shadow))
	}
}

// shadowingPath reports whatever "commitbrief" the user's shell would
// actually resolve on PATH, if it differs from exe (the binary just
// upgraded). ok is false when PATH has no commitbrief, or it resolves
// to exe itself — neither is worth reporting, and a LookPath failure is
// not an error in its own right, just the common case of a manual
// install that was never put on PATH.
func shadowingPath(exe string) (shadow string, ok bool) {
	found, err := exec.LookPath("commitbrief")
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(found)
	if err != nil {
		// A broken or unreadable link is not fatal to the check —
		// compare what LookPath found, unresolved.
		resolved = found
	}
	if resolved == exe {
		return "", false
	}
	return resolved, true
}

// reportsVersion checks whether --version output names the expected
// release. The leading "v" is trimmed because goreleaser injects the tag
// without it (`-X …version.Version={{.Version}}`), so a released binary
// prints "commitbrief 1.15.0 (…)" while the tag reads "v1.15.0".
// Comparing them verbatim would warn on every successful upgrade.
func reportsVersion(output string, v upgrade.Version) bool {
	return strings.Contains(output, strings.TrimPrefix(v.String(), "v"))
}
