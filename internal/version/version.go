// SPDX-License-Identifier: GPL-3.0-or-later

package version

import (
	"fmt"
	"runtime/debug"
)

// Version, Commit, and Date are set at link time by goreleaser/`make build`
// via -ldflags. When the binary is built without that injection — most
// importantly `go install github.com/CommitBrief/commitbrief/cmd/commitbrief@vX.Y.Z`
// — Resolve() backfills them from the Go module BuildInfo (see Resolve doc).
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Resolve fills Version/Commit/Date from runtime/debug.BuildInfo when
// they still hold their compile-time defaults. ldflags-injected values
// always win, so production binaries (brew, scoop, GitHub Releases
// tarballs) are unaffected. `go install …@vX.Y.Z` benefits: Main.Version
// becomes "vX.Y.Z" and the VCS settings carry the upstream commit and
// build timestamp, so --version stops reporting "dev (commit none,
// built unknown)".
//
// Not auto-called via init(): existing tests assert the bare defaults,
// and main() is the single right place to opt in.
func Resolve() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if Version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if Commit == "none" && s.Value != "" {
				Commit = s.Value
			}
		case "vcs.time":
			if Date == "unknown" && s.Value != "" {
				Date = s.Value
			}
		}
	}
}

func Info() string {
	return fmt.Sprintf("commitbrief %s (commit %s, built %s)", Version, Commit, Date)
}
