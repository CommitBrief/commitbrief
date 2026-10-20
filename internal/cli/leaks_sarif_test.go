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

// leakSarifWorktreeEnv returns a repo whose credential is staged but never
// committed, so only the working-tree half of the scan can see it and a
// full (un-narrowed) scan stays free of history hits.
func leakSarifWorktreeEnv(t *testing.T) *cliEnv {
	t.Helper()
	e := newCLIEnv(t)
	stageSecretIntoRepo(t, e.repoRoot, "conf.yml", "key: "+leakKey+"\n")
	return e
}

func TestLeaksSarifWorktreeHit(t *testing.T) {
	e := leakSarifWorktreeEnv(t)
	out := filepath.Join(t.TempDir(), "leaks.sarif")

	// The default --fail-on any still fails the run after the file is written.
	if err := e.run("leaks", "--sarif", "-o", out); err == nil {
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

	if err := e.run("leaks", "--sarif", "-o", out); err == nil {
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
	e := leakSarifWorktreeEnv(t)

	if err := e.run("leaks", "--sarif", "--fail-on", "none"); err != nil {
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

// A narrowed scan would publish a partial result set under the single
// commitbrief/leaks/ category, and Code Scanning closes every alert missing
// from a category as fixed. Each narrowing flag is refused up front.
func TestLeaksSarifRejectsNarrowedScans(t *testing.T) {
	cases := map[string][]string{
		"no-history":   {"--no-history"},
		"no-worktree":  {"--no-worktree"},
		"file":         {"--file", "*.yml"},
		"dir":          {"--dir", "src"},
		"exclude-file": {"--exclude-file", "*.md"},
		"exclude-dir":  {"--exclude-dir", "vendor"},
		"author":       {"--author", "someone"},
		"committer":    {"--committer", "someone"},
		"start-date":   {"--start-date", "2020-01-01"},
		"end-date":     {"--end-date", "2030-01-01"},
		"text":         {"--text", "scrub"},
		"range":        {"main"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			e := leakSarifEnv(t)
			out := filepath.Join(t.TempDir(), "leaks.sarif")
			args := append([]string{"leaks", "--sarif", "-o", out}, extra...)

			if err := e.run(args...); err == nil {
				t.Fatalf("%v must be refused", args)
			}
			if _, statErr := os.Stat(out); statErr == nil {
				t.Error("a refused narrowed scan must not write a SARIF file")
			}
			if strings.TrimSpace(e.out.String()) != "" {
				t.Errorf("a refused invocation must write nothing to stdout:\n%s", e.out.String())
			}
		})
	}
}

// --max-commits is a window size, not a narrowing filter: a scan that fits
// the window still writes SARIF, while one the window cuts short fails
// closed with a hint to enlarge it.
func TestLeaksSarifTruncatedHistoryFailsClosed(t *testing.T) {
	e := leakSarifEnv(t)
	for i := 0; i < 3; i++ {
		writeFile(t, filepath.Join(e.repoRoot, "extra.txt"), strings.Repeat("x\n", i+1))
		gitCmd(t, e.repoRoot, "add", "extra.txt")
		gitCmd(t, e.repoRoot, "commit", "-q", "-m", "more")
	}
	out := filepath.Join(t.TempDir(), "leaks.sarif")

	err := e.run("leaks", "--sarif", "-o", out, "--max-commits", "2")
	if err == nil {
		t.Fatal("a truncated history scan must fail the run")
	}
	if !strings.Contains(err.Error(), "--max-commits") {
		t.Errorf("the error should hint at --max-commits, got: %v", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("no SARIF file may be written for a truncated scan")
	}

	// A window large enough for the whole history writes the log again.
	e2 := leakSarifEnv(t)
	out2 := filepath.Join(t.TempDir(), "leaks.sarif")
	if err := e2.run("leaks", "--sarif", "-o", out2, "--max-commits", "50"); err == nil {
		t.Fatal("the credential must still fail the gate")
	}
	if _, statErr := os.Stat(out2); statErr != nil {
		t.Errorf("a complete scan must write SARIF: %v", statErr)
	}
}
