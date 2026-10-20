// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// leakSarifEnv returns a repo whose HEAD commit carries leakKey in conf.yml.
func leakSarifEnv(t *testing.T) *cliEnv {
	t.Helper()
	e := newCLIEnv(t)
	writeFile(t, filepath.Join(e.repoRoot, "conf.yml"), "key: "+leakKey+"\n")
	gitCmd(t, e.repoRoot, "add", "conf.yml")
	gitCmd(t, e.repoRoot, "commit", "-q", "-m", "add conf")
	return e
}

func TestLeaksSarifWorktreeHit(t *testing.T) {
	e := leakSarifEnv(t)
	out := filepath.Join(t.TempDir(), "leaks.sarif")

	// The default --fail-on any still fails the run after the file is written.
	if err := e.run("leaks", "--no-history", "--sarif", "-o", out); err == nil {
		t.Fatal("a found credential must still fail the run")
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("SARIF must be written before the gate fails: %v", err)
	}
	if got := sarifCategory(t, data); got != "commitbrief/leaks/" {
		t.Errorf("category = %q, want commitbrief/leaks/", got)
	}
	results := sarifResults(t, data)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1\n%s", len(results), data)
	}
	r := results[0]
	if r["ruleId"] != "sec.hardcoded-secret" {
		t.Errorf("ruleId = %v, want sec.hardcoded-secret", r["ruleId"])
	}
	if !strings.Contains(string(data), "conf.yml") {
		t.Errorf("location should name conf.yml:\n%s", data)
	}
	props, _ := r["properties"].(map[string]any)
	patterns, _ := props["commitbrief/patterns"].([]any)
	if len(patterns) == 0 || patterns[0] != "AWS Access Key" {
		t.Errorf("properties[commitbrief/patterns] = %v, want [AWS Access Key]", props["commitbrief/patterns"])
	}
	if _, has := props["commitbrief/commit"]; has {
		t.Errorf("a working-tree hit must not carry a commit: %v", props)
	}
	if strings.Contains(string(data), leakKey) {
		t.Fatalf("the matched secret leaked into SARIF:\n%s", data)
	}
	if strings.Contains(e.out.String(), "conf.yml") {
		t.Errorf("with -o the human report must not land on stdout:\n%s", e.out.String())
	}
}

func TestLeaksSarifHistoryHitCarriesCommit(t *testing.T) {
	e := leakSarifEnv(t)
	leakCommit := gitHead(t, e.repoRoot)
	writeFile(t, filepath.Join(e.repoRoot, "conf.yml"), "key: REDACTED\n")
	gitCmd(t, e.repoRoot, "add", "conf.yml")
	gitCmd(t, e.repoRoot, "commit", "-q", "-m", "scrub")
	out := filepath.Join(t.TempDir(), "leaks.sarif")

	if err := e.run("leaks", "--no-worktree", "--sarif", "-o", out); err == nil {
		t.Fatal("the history hit must fail the run")
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	results := sarifResults(t, data)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1\n%s", len(results), data)
	}
	props, _ := results[0]["properties"].(map[string]any)
	commit, _ := props["commitbrief/commit"].(string)
	if len(commit) < 7 || !strings.HasPrefix(leakCommit, commit) {
		t.Errorf("properties[commitbrief/commit] = %q, want a short hash of %q", commit, leakCommit)
	}
	if strings.Contains(string(data), leakKey) {
		t.Fatalf("the matched secret leaked into SARIF:\n%s", data)
	}
	// The author is personal data the alert does not need.
	if strings.Contains(string(data), "smoke@test") {
		t.Errorf("the commit author must not be written:\n%s", data)
	}
}

func TestLeaksSarifCleanRepoWritesEmptyResults(t *testing.T) {
	e := newCLIEnv(t)
	out := filepath.Join(t.TempDir(), "leaks.sarif")

	if err := e.run("leaks", "--sarif", "-o", out); err != nil {
		t.Fatalf("a clean repo must exit 0: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("a clean scan must still write the file: %v", err)
	}
	if n := len(sarifResults(t, data)); n != 0 {
		t.Errorf("results = %d, want 0", n)
	}
	if got := sarifCategory(t, data); got != "commitbrief/leaks/" {
		t.Errorf("category = %q", got)
	}
}

func TestLeaksSarifToStdoutIsOneDocument(t *testing.T) {
	e := leakSarifEnv(t)

	if err := e.run("leaks", "--no-history", "--sarif", "--fail-on", "none"); err != nil {
		t.Fatalf("--fail-on none must report without failing: %v", err)
	}
	if n := len(sarifResults(t, e.out.Bytes())); n != 1 {
		t.Errorf("results = %d, want 1", n)
	}
	// The human report (coverage line, findings list) moves to stderr.
	if !strings.Contains(e.errOut.String(), "conf.yml") {
		t.Errorf("human report should be on stderr:\n%s", e.errOut.String())
	}
	if strings.Contains(e.out.String()+e.errOut.String(), leakKey) {
		t.Fatal("the matched secret leaked")
	}
}

func TestLeaksSarifQuietSilencesCoverageLine(t *testing.T) {
	loud := newCLIEnv(t)
	if err := loud.run("leaks", "--sarif"); err != nil {
		t.Fatalf("leaks: %v", err)
	}
	coverage := strings.SplitN(loud.errOut.String(), "\n", 2)[0]
	if !strings.Contains(coverage, "canned") {
		t.Fatalf("expected a coverage line on stderr; got:\n%s", loud.errOut.String())
	}

	e := newCLIEnv(t)
	if err := e.run("leaks", "--sarif", "--quiet"); err != nil {
		t.Fatalf("leaks: %v", err)
	}
	if strings.Contains(e.errOut.String(), coverage) {
		t.Errorf("--quiet must silence the coverage line:\n%s", e.errOut.String())
	}
	if n := len(sarifResults(t, e.out.Bytes())); n != 0 {
		t.Errorf("results = %d, want 0", n)
	}
}

func TestLeaksSarifConflicts(t *testing.T) {
	cases := map[string][]string{
		"json":     {"leaks", "--sarif", "--json"},
		"patterns": {"leaks", "--sarif", "--patterns"},
		"markdown": {"leaks", "--sarif", "--markdown"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			e := newCLIEnv(t)
			if err := e.run(args...); err == nil {
				t.Fatalf("%v must be refused", args)
			}
			if strings.TrimSpace(e.out.String()) != "" {
				t.Errorf("a refused invocation must write nothing to stdout:\n%s", e.out.String())
			}
		})
	}
}
