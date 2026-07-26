// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Method is how the running binary was installed. It decides whether
// `upgrade` delegates to a package manager or replaces the file itself
// (ADR-0034 §D1).
type Method string

const (
	MethodHomebrew  Method = "homebrew"
	MethodScoop     Method = "scoop"
	MethodGoInstall Method = "go-install"
	MethodManual    Method = "manual"
)

// Env is every piece of ambient state Detect reads. It is passed in
// rather than read from os.Getenv/runtime inside Detect so a macOS host
// can exercise the Windows and Scoop branches in a unit test.
type Env struct {
	ExePath     string // resolved (symlinks evaluated) path of the running binary
	GOOS        string
	Scoop       string // $SCOOP
	UserProfile string // %USERPROFILE%
	GOBIN       string
	GOPATH      string
	Home        string
}

// ResolveExe returns the running binary's path with symlinks resolved.
// Resolution is mandatory, not cosmetic: Homebrew installs the binary
// into the Cellar and links it from <prefix>/bin, so an unresolved path
// hides the one marker that identifies a brew install.
func ResolveExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// A broken or unreadable link is not fatal — fall back to the
		// unresolved path and let detection do what it can.
		return exe, nil
	}
	return resolved, nil
}

// CurrentEnv builds an Env from the live process environment.
func CurrentEnv(exePath string) Env {
	home, _ := os.UserHomeDir()
	return Env{
		ExePath:     exePath,
		GOOS:        runtime.GOOS,
		Scoop:       os.Getenv("SCOOP"),
		UserProfile: os.Getenv("USERPROFILE"),
		GOBIN:       os.Getenv("GOBIN"),
		GOPATH:      os.Getenv("GOPATH"),
		Home:        home,
	}
}

// Detect classifies the installation, most specific marker first.
// Anything unrecognized is MethodManual — including distro packages we
// do not publish (nix, AUR, apt). That is safe by construction: those
// live in read-only or root-owned locations, so the write-permission
// gate aborts before a single byte is downloaded.
func Detect(env Env) Method {
	p := normalizePath(env.ExePath, env.GOOS)

	if strings.Contains(p, "/cellar/commitbrief/") {
		return MethodHomebrew
	}

	if scoop := normalizePath(env.Scoop, env.GOOS); scoop != "" &&
		strings.HasPrefix(p, strings.TrimSuffix(scoop, "/")+"/apps/commitbrief/") {
		return MethodScoop
	}
	if strings.Contains(p, "/scoop/apps/commitbrief/") {
		return MethodScoop
	}

	dir := pathDir(p)
	for _, bin := range goBinDirs(env) {
		if b := normalizePath(bin, env.GOOS); b != "" && strings.TrimSuffix(b, "/") == dir {
			return MethodGoInstall
		}
	}

	return MethodManual
}

// goBinDirs lists the directories `go install` could have written to,
// in the same precedence order the go command uses.
func goBinDirs(env Env) []string {
	var dirs []string
	if env.GOBIN != "" {
		dirs = append(dirs, env.GOBIN)
	}
	if env.GOPATH != "" {
		// GOPATH may be a list; only the first entry receives binaries.
		first := strings.Split(env.GOPATH, string(os.PathListSeparator))[0]
		if first != "" {
			dirs = append(dirs, filepath.Join(first, "bin"))
		}
	}
	if env.Home != "" {
		dirs = append(dirs, filepath.Join(env.Home, "go", "bin"))
	}
	return dirs
}

// SamePath reports whether a and b name the same filesystem location,
// under the platform rules normalizePath already applies for the
// marker comparisons above: case-insensitive and separator-normalized
// on Windows (whose filesystem is not case-sensitive), exact bytes
// everywhere else. goos is a parameter rather than read from runtime
// for the same reason Env.GOOS is: it lets a non-Windows host exercise
// the Windows comparison rules in a test.
//
// Exported for internal/cli's shadowingPath, which compares a
// PATH-resolved binary against the one just upgraded. Without this, it
// would warn about a shadowing commitbrief on Windows purely from a
// letter-case or `\`-vs-`/` difference that filepath.EvalSymlinks does
// not normalize away — a false positive, not a real shadow.
func SamePath(a, b, goos string) bool {
	return normalizePath(a, goos) == normalizePath(b, goos)
}

// normalizePath lowercases on Windows (its paths are case-insensitive)
// and converts separators to forward slashes so the marker checks above
// can be written once instead of per-OS.
func normalizePath(p, goos string) string {
	if p == "" {
		return ""
	}
	// On Windows, backslash is the path separator; convert to forward slash.
	// filepath.ToSlash doesn't work for testing Windows paths on Unix hosts.
	if goos == "windows" {
		p = strings.ReplaceAll(p, "\\", "/")
		p = strings.ToLower(p)
	} else {
		p = filepath.ToSlash(p)
		// Marker comparisons are lowercase; on case-sensitive systems
		// only the fixed markers are folded, never the user's path.
		p = strings.Replace(p, "/Cellar/", "/cellar/", 1)
	}
	return p
}

// pathDir returns the parent directory of an already-normalized
// (forward-slash) path, without a trailing slash.
func pathDir(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return p
	}
	return p[:i]
}
