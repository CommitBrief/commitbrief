// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import "testing"

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		env  Env
		want Method
	}{
		{
			name: "homebrew macos arm",
			env:  Env{ExePath: "/opt/homebrew/Cellar/commitbrief/1.13.0/bin/commitbrief", GOOS: "darwin"},
			want: MethodHomebrew,
		},
		{
			name: "homebrew intel macos",
			env:  Env{ExePath: "/usr/local/Cellar/commitbrief/1.13.0/bin/commitbrief", GOOS: "darwin"},
			want: MethodHomebrew,
		},
		{
			name: "linuxbrew",
			env:  Env{ExePath: "/home/linuxbrew/.linuxbrew/Cellar/commitbrief/1.13.0/bin/commitbrief", GOOS: "linux"},
			want: MethodHomebrew,
		},
		{
			name: "scoop default location",
			env:  Env{ExePath: `C:\Users\ada\scoop\apps\commitbrief\current\commitbrief.exe`, GOOS: "windows", UserProfile: `C:\Users\ada`},
			want: MethodScoop,
		},
		{
			name: "scoop custom SCOOP dir",
			env:  Env{ExePath: `D:\tools\sc\apps\commitbrief\1.13.0\commitbrief.exe`, GOOS: "windows", Scoop: `D:\tools\sc`},
			want: MethodScoop,
		},
		{
			name: "go install via GOBIN",
			env:  Env{ExePath: "/home/ada/dev/bin/commitbrief", GOOS: "linux", GOBIN: "/home/ada/dev/bin"},
			want: MethodGoInstall,
		},
		{
			name: "go install via GOPATH",
			env:  Env{ExePath: "/home/ada/go/bin/commitbrief", GOOS: "linux", GOPATH: "/home/ada/go"},
			want: MethodGoInstall,
		},
		{
			name: "go install via default GOPATH under home",
			env:  Env{ExePath: "/home/ada/go/bin/commitbrief", GOOS: "linux", Home: "/home/ada"},
			want: MethodGoInstall,
		},
		{
			name: "manual tarball in usr local bin",
			env:  Env{ExePath: "/usr/local/bin/commitbrief", GOOS: "linux"},
			want: MethodManual,
		},
		{
			name: "manual tarball in home bin",
			env:  Env{ExePath: "/home/ada/bin/commitbrief", GOOS: "linux", Home: "/home/ada"},
			want: MethodManual,
		},
		{
			// A distro package is classified manual; the write-permission
			// gate in Task 6 is what actually protects it.
			name: "distro package looks manual",
			env:  Env{ExePath: "/usr/bin/commitbrief", GOOS: "linux"},
			want: MethodManual,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Detect(c.env); got != c.want {
				t.Fatalf("Detect() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDetectWindowsIsCaseInsensitive(t *testing.T) {
	env := Env{ExePath: `C:\Users\Ada\Scoop\Apps\CommitBrief\current\commitbrief.exe`, GOOS: "windows", UserProfile: `C:\Users\Ada`}
	if got := Detect(env); got != MethodScoop {
		t.Fatalf("Detect() = %q, want %q", got, MethodScoop)
	}
}

// TestSamePath pins the comparison internal/cli's shadowingPath relies
// on: exact-byte on any non-Windows GOOS (unchanged from a plain ==),
// but case- and separator-insensitive on Windows, since neither
// os.Executable nor exec.LookPath is guaranteed to return byte-identical
// casing/separators for the same file, and EvalSymlinks normalizes
// neither. goos is passed explicitly so this runs the Windows rules on
// any host, the same way TestDetect exercises them.
func TestSamePath(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		goos string
		want bool
	}{
		{
			name: "windows case difference is the same path",
			a:    `C:\Users\ada\bin\commitbrief.exe`,
			b:    `C:\USERS\ada\BIN\CommitBrief.EXE`,
			goos: "windows",
			want: true,
		},
		{
			name: "windows separator difference is the same path",
			a:    `C:\Users\ada\bin\commitbrief.exe`,
			b:    `C:/Users/ada/bin/commitbrief.exe`,
			goos: "windows",
			want: true,
		},
		{
			name: "windows genuinely different paths do not match",
			a:    `C:\Users\ada\bin\commitbrief.exe`,
			b:    `C:\Program Files\CommitBrief\commitbrief.exe`,
			goos: "windows",
			want: false,
		},
		{
			name: "unix comparison stays case-sensitive",
			a:    "/usr/local/bin/commitbrief",
			b:    "/usr/local/bin/CommitBrief",
			goos: "linux",
			want: false,
		},
		{
			name: "unix identical paths match",
			a:    "/usr/local/bin/commitbrief",
			b:    "/usr/local/bin/commitbrief",
			goos: "darwin",
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SamePath(c.a, c.b, c.goos); got != c.want {
				t.Fatalf("SamePath(%q, %q, %q) = %v, want %v", c.a, c.b, c.goos, got, c.want)
			}
		})
	}
}
