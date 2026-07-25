// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import "testing"

func TestAssetName(t *testing.T) {
	cases := []struct {
		version string
		goos    string
		goarch  string
		want    string
	}{
		{"v1.15.0", "darwin", "arm64", "commitbrief_1.15.0_darwin_arm64.tar.gz"},
		{"1.15.0", "darwin", "amd64", "commitbrief_1.15.0_darwin_x86_64.tar.gz"},
		{"v1.15.0", "linux", "amd64", "commitbrief_1.15.0_linux_x86_64.tar.gz"},
		{"v1.15.0", "linux", "arm64", "commitbrief_1.15.0_linux_arm64.tar.gz"},
		{"v1.15.0", "windows", "amd64", "commitbrief_1.15.0_windows_x86_64.zip"},
		{"v2.0.0-rc.1", "linux", "arm64", "commitbrief_2.0.0-rc.1_linux_arm64.tar.gz"},
	}
	for _, c := range cases {
		if got := AssetName(c.version, c.goos, c.goarch); got != c.want {
			t.Fatalf("AssetName(%q,%q,%q) = %q, want %q", c.version, c.goos, c.goarch, got, c.want)
		}
	}
}

func TestBinaryEntryName(t *testing.T) {
	if got := BinaryEntryName("linux"); got != "commitbrief" {
		t.Fatalf("BinaryEntryName(linux) = %q", got)
	}
	if got := BinaryEntryName("windows"); got != "commitbrief.exe" {
		t.Fatalf("BinaryEntryName(windows) = %q", got)
	}
}

func TestParseChecksums(t *testing.T) {
	data := []byte(
		"abc123  commitbrief_1.15.0_darwin_arm64.tar.gz\n" +
			"def456  commitbrief_1.15.0_linux_x86_64.tar.gz\n" +
			"\n" +
			"789fed *commitbrief_1.15.0_windows_x86_64.zip\n")
	sums := ParseChecksums(data)
	if got := sums["commitbrief_1.15.0_darwin_arm64.tar.gz"]; got != "abc123" {
		t.Fatalf("darwin sum = %q, want abc123", got)
	}
	if got := sums["commitbrief_1.15.0_linux_x86_64.tar.gz"]; got != "def456" {
		t.Fatalf("linux sum = %q, want def456", got)
	}
	// goreleaser writes binary-mode entries with a leading '*'; the
	// star belongs to the format, not to the file name.
	if got := sums["commitbrief_1.15.0_windows_x86_64.zip"]; got != "789fed" {
		t.Fatalf("windows sum = %q, want 789fed", got)
	}
	if len(sums) != 3 {
		t.Fatalf("len(sums) = %d, want 3", len(sums))
	}
}
