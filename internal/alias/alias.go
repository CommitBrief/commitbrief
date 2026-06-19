// SPDX-License-Identifier: GPL-3.0-or-later

// Package alias installs a shell alias (e.g. `cbr` → `commitbrief`) into
// the user's shell startup file, cross-platform. It is the OS-level half of
// `commitbrief setup --alias`; the interactive orchestration lives in
// internal/setup.
//
// Each supported shell is an Installer. A managed block delimited by
// blockStart/blockEnd is written into the shell's startup file, so re-running
// updates the block in place (changing the alias name removes the old one)
// and lines outside the block are never touched. Conflict detection is
// best-effort: an executable of the same name on PATH (exec.LookPath) plus a
// scan of the startup file for an existing alias/function definition outside
// our block. Shell builtins, functions defined elsewhere, and aliases in a
// different shell's rc are out of scope (they cannot be detected without
// invoking the shell).
package alias

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// lookPath is a seam over exec.LookPath so tests can simulate an
// already-installed binary without depending on the host PATH.
var lookPath = exec.LookPath

const (
	// DefaultName is the alias installed when the user accepts the default.
	DefaultName = "cbr"

	// Command is what the alias expands to. A bare binary name (not an
	// absolute path) is deliberate: interactive shells have the install
	// directory on PATH, and a bare name survives `brew upgrade` /
	// reinstalls that would invalidate an embedded path. (install-hook
	// embeds an absolute path because GUI git clients run hooks with a
	// stripped PATH — that constraint does not apply to a user shell.)
	Command = "commitbrief"

	// blockStart / blockEnd delimit the commitbrief-managed region in a
	// shell startup file. `#` is a comment in every file-based shell we
	// target (bash/zsh/fish/PowerShell), so the markers are inert there.
	blockStart = "# >>> commitbrief alias >>>"
	blockEnd   = "# <<< commitbrief alias <<<"
)

// nameRe is the accepted alias-name shape: a shell identifier. We reject
// spaces, `=`, quotes, slashes and other metacharacters so the rendered
// `alias <name>='commitbrief'` line can never be broken or injected.
var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// IsValidName reports whether name is a safe shell alias identifier.
func IsValidName(name string) bool {
	return nameRe.MatchString(name)
}

// Installer knows how to write a managed alias into one shell's startup
// surface and how to detect a conflicting prior definition there.
type Installer interface {
	// Name is the machine id: "bash", "zsh", "fish", "powershell", "cmd".
	Name() string
	// Label is the human-facing name shown in the shell picker.
	Label() string
	// Target is the resolved destination (file path, or a short
	// description for the registry-backed cmd installer).
	Target() string
	// Conflict returns a non-empty human reason when `alias` is already
	// taken outside our managed block — an existing definition in the
	// startup surface, or an executable of that name on PATH. Empty means
	// the name is free.
	Conflict(alias string) (string, error)
	// Install writes/updates the managed alias and returns the shell
	// reload command the user should run (empty ⇒ "restart your terminal").
	// changed is false when the file already held the identical block.
	Install(alias string) (changed bool, reloadCmd string, err error)
}

// ByName returns the installer with the given machine name on the current
// OS, or false if it is unsupported here.
func ByName(name string) (Installer, bool) {
	for _, inst := range All() {
		if inst.Name() == name {
			return inst, true
		}
	}
	return nil, false
}

// fileInstaller is the shared Installer for shells whose alias lives in a
// plain startup file (bash/zsh/fish/PowerShell). render builds the managed
// block body for a given alias; detectors builds the regexes that spot a
// pre-existing definition of that alias outside our block. cmd.exe does not
// use this (it persists via the registry — see alias_windows.go).
type fileInstaller struct {
	name      string
	label     string
	path      string
	perm      os.FileMode
	render    func(alias string) string
	detectors func(alias string) []*regexp.Regexp
	reload    string // shell reload command; "" ⇒ caller suggests a restart
}

func (f fileInstaller) Name() string   { return f.name }
func (f fileInstaller) Label() string  { return f.label }
func (f fileInstaller) Target() string { return f.path }

func (f fileInstaller) Conflict(alias string) (string, error) {
	if p := pathConflict(alias); p != "" {
		return p, nil
	}
	if f.path == "" {
		return "", nil
	}
	hit, err := fileAliasConflict(f.path, f.detectors(alias))
	if err != nil {
		return "", err
	}
	if hit {
		return f.path, nil
	}
	return "", nil
}

func (f fileInstaller) Install(alias string) (bool, string, error) {
	if f.path == "" {
		return false, "", fmt.Errorf("alias: could not resolve %s startup file path", f.name)
	}
	changed, err := writeManagedBlock(f.path, f.render(alias), f.perm)
	if err != nil {
		return false, "", err
	}
	return changed, f.reload, nil
}

// homeDir returns the user's home directory or "" when it cannot be
// resolved (installers degrade to an empty path that Install rejects).
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

// displayPath shortens a path under the home directory to ~/… for friendlier
// labels; paths outside home (or an empty path) are returned as-is.
func displayPath(p string) string {
	if p == "" {
		return "unknown"
	}
	if h := homeDir(); h != "" {
		if rel, err := filepath.Rel(h, p); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return p
}

// pathConflict reports a non-empty reason when an executable named `alias`
// is already resolvable on PATH (so the alias would shadow it). Cross
// platform: on Windows LookPath honours PATHEXT (.exe/.cmd/...).
func pathConflict(alias string) string {
	if p, err := lookPath(alias); err == nil && p != "" {
		return p
	}
	return ""
}

// stripManagedBlock removes the commitbrief-managed region from content so a
// conflict scan never matches the alias we ourselves installed.
func stripManagedBlock(content string) string {
	si := strings.Index(content, blockStart)
	if si < 0 {
		return content
	}
	rest := content[si+len(blockStart):]
	ei := strings.Index(rest, blockEnd)
	if ei < 0 {
		return content[:si]
	}
	return content[:si] + rest[ei+len(blockEnd):]
}

// upsertBlock returns content with the managed block set to body (no
// surrounding markers — they are added here). An existing block is replaced
// in place; otherwise the block is appended after a blank-line separator.
// The bool reports whether content actually changed.
func upsertBlock(content, body string) (string, bool) {
	block := blockStart + "\n" + body + "\n" + blockEnd
	si := strings.Index(content, blockStart)
	if si >= 0 {
		rest := content[si+len(blockStart):]
		if ei := strings.Index(rest, blockEnd); ei >= 0 {
			tail := rest[ei+len(blockEnd):]
			updated := content[:si] + block + tail
			return updated, updated != content
		}
	}
	var b strings.Builder
	b.WriteString(content)
	if content != "" && !strings.HasSuffix(content, "\n") {
		b.WriteByte('\n')
	}
	if content != "" {
		b.WriteByte('\n')
	}
	b.WriteString(block)
	b.WriteByte('\n')
	return b.String(), true
}

// writeManagedBlock reads path (creating parent dirs), upserts body into the
// managed block, and writes it back atomically with the given mode. It
// returns whether the file changed.
func writeManagedBlock(path, body string, perm os.FileMode) (bool, error) {
	if path == "" {
		return false, fmt.Errorf("alias: empty target path")
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("alias: read %s: %w", path, err)
	}
	updated, changed := upsertBlock(string(existing), body)
	if !changed {
		return false, nil
	}
	if err := writeFileAtomic(path, []byte(updated), perm); err != nil {
		return false, err
	}
	return true, nil
}

// writeFileAtomic writes data to path via a temp file in the same directory
// followed by a rename, creating parent directories as needed. Two
// dotfile-friendly behaviours matter here:
//
//   - Symlinks are followed. Shell rc files are frequently symlinks managed
//     by GNU Stow / chezmoi / yadm — exactly the power users who install
//     aliases. A naive rename over the link would replace it with a
//     standalone file and silently detach it from the dotfile repo, so we
//     resolve the link and land the rename on the *real* file, preserving
//     the symlink.
//   - The existing file's permission bits are preserved. perm is used only
//     when the file does not yet exist, so a user who ran `chmod 600
//     ~/.zshrc` does not get it widened to 0644 on every install.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	target := path
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			target = resolved
		}
		// A broken/dangling link falls through with target == path: the
		// rename then replaces the dead link, which is the best we can do.
	}
	if fi, err := os.Stat(target); err == nil {
		perm = fi.Mode().Perm()
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("alias: mkdir %s: %w", dir, err)
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("alias: write %s: %w", tmp, err)
	}
	// os.WriteFile applies the umask, so set the exact bits explicitly to
	// preserve a stricter mode read above.
	if err := os.Chmod(tmp, perm); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("alias: chmod %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("alias: rename %s: %w", target, err)
	}
	return nil
}

// fileAliasConflict reports whether path already defines `alias` (matched by
// any of patterns) outside our managed block. A missing file is not a
// conflict.
func fileAliasConflict(path string, patterns []*regexp.Regexp) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("alias: read %s: %w", path, err)
	}
	content := stripManagedBlock(string(data))
	for _, re := range patterns {
		if re.MatchString(content) {
			return true, nil
		}
	}
	return false, nil
}
