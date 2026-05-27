// SPDX-License-Identifier: GPL-3.0-or-later

package version

import (
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	cases := map[string]string{
		"Version": Version,
		"Commit":  Commit,
		"Date":    Date,
	}
	for name, got := range cases {
		if got == "" {
			t.Errorf("%s is empty; ldflags-injected vars must default to a non-empty placeholder", name)
		}
	}
	if Version != "dev" {
		t.Errorf("Version = %q, want %q (changed defaults break `go run` and tests)", Version, "dev")
	}
}

func TestInfo(t *testing.T) {
	got := Info()
	for _, want := range []string{"commitbrief", Version, Commit, Date} {
		if !strings.Contains(got, want) {
			t.Errorf("Info() = %q, want substring %q", got, want)
		}
	}
}

func TestResolveLeavesLdflagsAlone(t *testing.T) {
	// Simulate a goreleaser/`make build` invocation: ldflags injected
	// concrete values. Resolve() must not touch any of them, even when
	// BuildInfo would offer different data.
	origV, origC, origD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = origV, origC, origD })

	Version = "1.2.3"
	Commit = "deadbeef"
	Date = "2026-05-26T12:00:00Z"

	Resolve()

	if Version != "1.2.3" || Commit != "deadbeef" || Date != "2026-05-26T12:00:00Z" {
		t.Errorf("Resolve overwrote ldflags-injected values: %q %q %q",
			Version, Commit, Date)
	}
}

func TestResolveIdempotent(t *testing.T) {
	// Calling Resolve() twice must produce the same result as calling
	// it once — no accumulation of suffixes, no second-call surprises.
	origV, origC, origD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = origV, origC, origD })

	Resolve()
	v1, c1, d1 := Version, Commit, Date
	Resolve()
	v2, c2, d2 := Version, Commit, Date

	if v1 != v2 || c1 != c2 || d1 != d2 {
		t.Errorf("Resolve not idempotent: first call gave (%q %q %q), second gave (%q %q %q)",
			v1, c1, d1, v2, c2, d2)
	}
}
