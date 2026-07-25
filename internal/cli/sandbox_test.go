// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/flaky"
)

func TestParseSandboxCommand_Empty(t *testing.T) {
	tmpls, needsTest, err := parseSandboxCommand(nil)
	if err != nil {
		t.Fatalf("parseSandboxCommand(nil) errored: %v", err)
	}
	if tmpls != nil || needsTest {
		t.Fatalf("nil spec = (%v, %v); want (nil, false)", tmpls, needsTest)
	}
}

func TestParseSandboxCommand_DetectsTestPlaceholder(t *testing.T) {
	_, needsTest, err := parseSandboxCommand([]string{"go", "test", "-run", "^{{.Test}}$"})
	if err != nil {
		t.Fatalf("parseSandboxCommand errored: %v", err)
	}
	if !needsTest {
		t.Error("needsTest = false; want true when the template references .Test")
	}

	_, needsTest, err = parseSandboxCommand([]string{"go", "test", "{{.File}}"})
	if err != nil {
		t.Fatalf("parseSandboxCommand errored: %v", err)
	}
	if needsTest {
		t.Error("needsTest = true; want false when no element references .Test")
	}
}

func TestParseSandboxCommand_InvalidTemplate(t *testing.T) {
	// A malformed template must surface at parse time — before any provider
	// call — naming the offending element, not silently do nothing.
	_, _, err := parseSandboxCommand([]string{"go", "test", "{{.Test"})
	if err == nil {
		t.Fatal("parseSandboxCommand accepted an unterminated action")
	}
	if !strings.Contains(err.Error(), "sandbox_command[2]") {
		t.Errorf("error = %v; want it to name element index 2", err)
	}
}

func TestRenderSandboxArgv(t *testing.T) {
	tmpls, _, err := parseSandboxCommand([]string{"go", "test", "-run", "^{{.Test}}$", "./{{.File}}"})
	if err != nil {
		t.Fatalf("parseSandboxCommand errored: %v", err)
	}
	argv, err := renderSandboxArgv(tmpls, flaky.Target{File: "internal/auth", Line: 42, Test: "TestLogin"})
	if err != nil {
		t.Fatalf("renderSandboxArgv errored: %v", err)
	}
	want := []string{"go", "test", "-run", "^TestLogin$", "./internal/auth"}
	if strings.Join(argv, "|") != strings.Join(want, "|") {
		t.Errorf("argv = %v; want %v", argv, want)
	}
}

func TestRenderSandboxArgv_EmptyElementIsAnError(t *testing.T) {
	// An element that renders to nothing would shift the argv and run a
	// different command than the user declared.
	tmpls, _, err := parseSandboxCommand([]string{"go", "{{.Test}}"})
	if err != nil {
		t.Fatalf("parseSandboxCommand errored: %v", err)
	}
	if _, err := renderSandboxArgv(tmpls, flaky.Target{File: "a_test.go", Line: 1}); err == nil {
		t.Fatal("renderSandboxArgv accepted an element that rendered empty")
	}
}

func TestSandboxExecutor_TimeoutIsAnErrorNotAFail(t *testing.T) {
	// A per-attempt timeout means the attempt was never observed — it must
	// come back as an error, never as (false, nil). Getting this backwards
	// makes a hung/slow test look like a deterministic failure: classify()
	// would tally it as a Fail, and a campaign of nothing-but-timeouts would
	// render as VerdictRealFailure ("fix it, don't quarantine it") instead of
	// VerdictInconclusive. ADR-0033 §5 requires the former to be impossible.
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell-free sleep binary")
	}
	prev := sandboxRerunTimeout
	sandboxRerunTimeout = 100 * time.Millisecond
	t.Cleanup(func() { sandboxRerunTimeout = prev })

	tmpls, _, err := parseSandboxCommand([]string{"sleep", "5"})
	if err != nil {
		t.Fatalf("parseSandboxCommand errored: %v", err)
	}
	exec := newSandboxExecutor(t.TempDir(), tmpls)
	passed, err := exec(context.Background(), flaky.Target{File: "a_test.go", Line: 1, Test: "TestX"})
	if passed {
		t.Error("passed = true; want false on a timed-out attempt")
	}
	if err == nil {
		t.Fatal("err = nil; want a non-nil error — a timed-out attempt was never observed")
	}
}

func TestBuildSandboxRunner_EmptyConfigIsInert(t *testing.T) {
	// review.sandbox_command defaults to empty: no runner, no error. This is
	// what keeps --sandbox-rerun a no-op until a user also sets a command.
	app := sandboxTestApp(t)
	runner, err := buildSandboxRunner(app)
	if err != nil {
		t.Fatalf("buildSandboxRunner errored: %v", err)
	}
	if runner != nil {
		t.Fatalf("runner = %+v; want nil when sandbox_command is empty", runner)
	}
}

func TestBuildSandboxRunner_BuildsFromConfig(t *testing.T) {
	app := sandboxTestApp(t)
	app.RepoRoot = t.TempDir()
	app.Config.Review.SandboxCommand = []string{"go", "test", "-run", "^{{.Test}}$"}

	runner, err := buildSandboxRunner(app)
	if err != nil {
		t.Fatalf("buildSandboxRunner errored: %v", err)
	}
	if runner == nil {
		t.Fatal("runner = nil; want a bound runner")
		return
	}
	if !runner.needsTest {
		t.Error("needsTest = false; want true, the template references .Test")
	}
	if runner.display != "go test -run ^{{.Test}}$" {
		t.Errorf("display = %q", runner.display)
	}
	if runner.exec == nil {
		t.Error("exec = nil; want a bound executor")
	}
}

func TestBuildSandboxRunner_InvalidTemplateErrors(t *testing.T) {
	app := sandboxTestApp(t)
	app.Config.Review.SandboxCommand = []string{"go", "{{.Test"}
	if _, err := buildSandboxRunner(app); err == nil {
		t.Fatal("buildSandboxRunner accepted an invalid template")
	}
}

func TestSandboxExecutor_ExitCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell-free true/false binary")
	}
	cases := []struct {
		name       string
		argv       []string
		wantPassed bool
		wantErr    bool
	}{
		{"exit zero is a pass", []string{"true"}, true, false},
		{"non-zero exit is a fail, not an error", []string{"false"}, false, false},
		{"unspawnable binary is an error", []string{"commitbrief-no-such-binary-xyz"}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpls, _, err := parseSandboxCommand(tc.argv)
			if err != nil {
				t.Fatalf("parseSandboxCommand errored: %v", err)
			}
			exec := newSandboxExecutor(t.TempDir(), tmpls)
			passed, err := exec(context.Background(), flaky.Target{File: "a_test.go", Line: 1, Test: "TestX"})
			if passed != tc.wantPassed {
				t.Errorf("passed = %v; want %v", passed, tc.wantPassed)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v; wantErr %v", err, tc.wantErr)
			}
		})
	}
}
