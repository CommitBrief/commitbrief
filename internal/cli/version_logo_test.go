// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import "testing"

// versionFlagRequested gates whether Execute prints the branding logo:
// `commitbrief --version` must emit a single parseable line (no logo),
// while every other invocation — including --help — keeps the logo.
func TestVersionFlagRequested(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"bare", nil, false},
		{"version only", []string{"--version"}, true},
		{"version after subcommand", []string{"diff", "--version"}, true},
		{"version with other flags", []string{"--json", "--version"}, true},
		{"help is not version", []string{"--help"}, false},
		{"staged review", []string{"--staged"}, false},
		// A "--" terminator hands the rest to positional args (e.g. a git
		// pathspec for `diff`), so a later --version is not our flag.
		{"version after terminator", []string{"diff", "--", "--version"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionFlagRequested(tc.args); got != tc.want {
				t.Errorf("versionFlagRequested(%q) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
