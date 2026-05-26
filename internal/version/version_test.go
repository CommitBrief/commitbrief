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
