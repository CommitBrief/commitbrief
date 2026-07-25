// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in    string
		ok    bool
		major int
		minor int
		patch int
		pre   string
	}{
		{"v1.13.0", true, 1, 13, 0, ""},
		{"1.13.0", true, 1, 13, 0, ""},
		{"v1.0.0-rc.1", true, 1, 0, 0, "rc.1"},
		{"v2.0.0-beta.10", true, 2, 0, 0, "beta.10"},
		{"dev", false, 0, 0, 0, ""},
		{"", false, 0, 0, 0, ""},
		{"v1.2", false, 0, 0, 0, ""},
		{"v1.2.x", false, 0, 0, 0, ""},
	}
	for _, c := range cases {
		got, ok := ParseVersion(c.in)
		if ok != c.ok {
			t.Fatalf("ParseVersion(%q) ok = %v, want %v", c.in, ok, c.ok)
		}
		if !ok {
			continue
		}
		if got.Major != c.major || got.Minor != c.minor || got.Patch != c.patch || got.Pre != c.pre {
			t.Fatalf("ParseVersion(%q) = %+v, want %d.%d.%d-%q", c.in, got, c.major, c.minor, c.patch, c.pre)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a    string
		b    string
		want int
	}{
		{"v1.13.0", "v1.13.0", 0},
		{"v1.13.0", "v1.14.0", -1},
		{"v1.14.0", "v1.13.0", 1},
		{"v1.13.0", "v2.0.0", -1},
		{"v1.13.0", "v1.13.1", -1},
		// a release outranks its own prerelease
		{"v1.14.0-rc.1", "v1.14.0", -1},
		{"v1.14.0", "v1.14.0-rc.1", 1},
		// numeric prerelease identifiers compare numerically, not lexically
		{"v1.14.0-rc.2", "v1.14.0-rc.10", -1},
		{"v1.14.0-rc.1", "v1.14.0-rc.1", 0},
		// fewer identifiers sort lower when the prefix matches
		{"v1.14.0-rc", "v1.14.0-rc.1", -1},
		// numeric identifiers sort below alphanumeric ones
		{"v1.14.0-1", "v1.14.0-alpha", -1},
	}
	for _, c := range cases {
		va, ok := ParseVersion(c.a)
		if !ok {
			t.Fatalf("ParseVersion(%q) failed", c.a)
		}
		vb, ok := ParseVersion(c.b)
		if !ok {
			t.Fatalf("ParseVersion(%q) failed", c.b)
		}
		if got := va.Compare(vb); got != c.want {
			t.Fatalf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestVersionString(t *testing.T) {
	v, _ := ParseVersion("1.2.3-rc.4")
	if got := v.String(); got != "v1.2.3-rc.4" {
		t.Fatalf("String() = %q, want %q", got, "v1.2.3-rc.4")
	}
}
