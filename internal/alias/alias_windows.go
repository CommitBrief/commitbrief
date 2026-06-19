// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package alias

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// commandProcessorKey is the per-user key whose AutoRun value runs on every
// new cmd.exe session — the only persistence hook cmd.exe offers for DOSKEY
// macros (there is no startup file equivalent).
const commandProcessorKey = `Software\Microsoft\Command Processor`

// All returns the shells we can install an alias for on Windows: PowerShell
// (a $PROFILE function) and cmd.exe (a DOSKEY macro loaded via AutoRun).
func All() []Installer {
	return []Installer{
		newPowerShellInstaller(powershellProfile()),
		cmdInstaller{},
	}
}

// Detect defaults to PowerShell, the modern Windows shell. cmd.exe vs.
// PowerShell cannot be told apart reliably from a child process, so the
// caller offers the picker when the default is not wanted.
func Detect() (Installer, bool) {
	return ByName("powershell")
}

// newPowerShellInstaller writes a `function <name> { commitbrief @args }`
// into the user's PowerShell profile. A function (not Set-Alias) is used so
// the alias forwards arguments cleanly and stays future-proof.
func newPowerShellInstaller(profile string) Installer {
	return fileInstaller{
		name:  "powershell",
		label: fmt.Sprintf("PowerShell (%s)", displayPath(profile)),
		path:  profile,
		perm:  os.FileMode(0o644),
		render: func(alias string) string {
			return fmt.Sprintf("function %s { %s @args }", alias, Command)
		},
		detectors: func(alias string) []*regexp.Regexp {
			q := regexp.QuoteMeta(alias)
			return []*regexp.Regexp{
				regexp.MustCompile(`(?mi)^[ \t]*function[ \t]+` + q + `\b`),
				regexp.MustCompile(`(?mi)^[ \t]*Set-Alias\b[^\n]*\b` + q + `\b`),
			}
		},
		reload: "", // PowerShell re-reads $PROFILE on a new session
	}
}

func powershellProfile() string {
	docs := windowsDocuments()
	if docs == "" {
		return ""
	}
	// PowerShell 7+ (pwsh) default. Windows PowerShell 5.1 uses
	// Documents\WindowsPowerShell\… — users on the legacy host can point
	// setup at it via the picker label, or copy the block.
	return filepath.Join(docs, "PowerShell", "Microsoft.PowerShell_profile.ps1")
}

// windowsDocuments resolves the user's Documents folder, honouring Known
// Folder redirection. On many Windows 10/11 machines Documents is redirected
// into OneDrive, so a hardcoded ~\Documents would write the PowerShell
// profile to a file PowerShell never loads. The authoritative source is the
// per-user "Personal" shell-folder registry value, which already reflects any
// redirection (OneDrive or custom); we expand its %VARS%. Falls back to
// %OneDrive%\Documents, then ~\Documents.
func windowsDocuments() string {
	if k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\User Shell Folders`,
		registry.QUERY_VALUE); err == nil {
		defer k.Close()
		if v, _, err := k.GetStringValue("Personal"); err == nil && v != "" {
			if expanded, err := registry.ExpandString(v); err == nil && expanded != "" {
				return expanded
			}
			return v
		}
	}
	if od := os.Getenv("OneDrive"); od != "" {
		p := filepath.Join(od, "Documents")
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	if h := homeDir(); h != "" {
		return filepath.Join(h, "Documents")
	}
	return ""
}

// cmdInstaller persists a DOSKEY macro for cmd.exe. The macro lives in a
// commitbrief-owned macrofile, and the per-user Command Processor AutoRun
// value is augmented with `doskey /macrofile=…` so the macro loads in every
// new cmd session. AutoRun is merged, never clobbered.
type cmdInstaller struct{}

func (cmdInstaller) Name() string  { return "cmd" }
func (cmdInstaller) Label() string { return "Command Prompt (cmd.exe, via DOSKEY)" }
func (cmdInstaller) Target() string {
	return cmdMacrofile()
}

func (cmdInstaller) Conflict(alias string) (string, error) {
	// We cannot enumerate live DOSKEY macros from here; fall back to the
	// PATH check, which still catches a same-named executable.
	if p := pathConflict(alias); p != "" {
		return p, nil
	}
	return "", nil
}

func (c cmdInstaller) Install(alias string) (bool, string, error) {
	macrofile := cmdMacrofile()
	if macrofile == "" {
		return false, "", fmt.Errorf("alias: could not resolve cmd macrofile path")
	}
	// $* forwards all arguments to the macro target.
	body := fmt.Sprintf("%s=%s $*\r\n", alias, Command)
	macroChanged := true
	if existing, err := os.ReadFile(macrofile); err == nil {
		macroChanged = string(existing) != body
	}
	if macroChanged {
		if err := writeFileAtomic(macrofile, []byte(body), 0o644); err != nil {
			return false, "", err
		}
	}
	autoRunChanged, err := ensureAutoRun(macrofile)
	if err != nil {
		return false, "", err
	}
	// Report a real change so a no-op re-run prints "already up to date",
	// consistent with the file installers.
	return macroChanged || autoRunChanged, "", nil
}

func cmdMacrofile() string {
	if h := homeDir(); h != "" {
		return filepath.Join(h, ".commitbrief", "cmd-aliases.doskey")
	}
	return ""
}

// ensureAutoRun makes the Command Processor AutoRun value run
// `doskey /macrofile="<macrofile>"`. An existing AutoRun (e.g. another
// tool's) is preserved and our command appended with `&`; if our command is
// already present nothing changes. Returns whether the value was modified.
func ensureAutoRun(macrofile string) (bool, error) {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, commandProcessorKey,
		registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, fmt.Errorf("alias: open registry %s: %w", commandProcessorKey, err)
	}
	defer key.Close()

	doskey := fmt.Sprintf(`doskey /macrofile="%s"`, macrofile)

	existing, _, err := key.GetStringValue("AutoRun")
	if err != nil && err != registry.ErrNotExist {
		return false, fmt.Errorf("alias: read AutoRun: %w", err)
	}
	if strings.Contains(existing, macrofile) {
		return false, nil // already wired up
	}

	value := doskey
	if strings.TrimSpace(existing) != "" {
		value = existing + " & " + doskey
	}
	if err := key.SetStringValue("AutoRun", value); err != nil {
		return false, fmt.Errorf("alias: write AutoRun: %w", err)
	}
	return true, nil
}
