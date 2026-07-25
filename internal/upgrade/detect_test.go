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
