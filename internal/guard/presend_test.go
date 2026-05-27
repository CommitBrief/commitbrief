// SPDX-License-Identifier: GPL-3.0-or-later

package guard

import (
	"bytes"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/diff"
)

func diffWith(paths ...string) diff.Diff {
	files := make([]diff.FileDiff, 0, len(paths))
	for _, p := range paths {
		files = append(files, diff.FileDiff{Path: p, Mode: diff.ModeModified})
	}
	return diff.Diff{Files: files}
}

func TestNoTriggerCleanDiff(t *testing.T) {
	d := diffWith("internal/auth/login.go", "README.md")
	res, err := CheckDiffForLocalConfig(d, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res != Continue {
		t.Errorf("Result = %v, want Continue", res)
	}
}

func TestTriggerOnLocalConfig(t *testing.T) {
	d := diffWith("internal/auth/login.go", ".commitbrief/config.yml")
	var w bytes.Buffer
	res, err := CheckDiffForLocalConfig(d, Options{
		NonInteractive: true,
		Writer:         &w,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res != Abort {
		t.Errorf("Result = %v, want Abort (non-interactive)", res)
	}
	out := w.String()
	if !strings.Contains(out, ".commitbrief/config.yml") {
		t.Errorf("warning missing triggered path; got:\n%s", out)
	}
	if !strings.Contains(out, "non-interactive") {
		t.Errorf("non-interactive abort message missing; got:\n%s", out)
	}
}

func TestRootCommitbriefMdDoesNotTrigger(t *testing.T) {
	// Root-level shared files must NOT trigger; only the directory does.
	d := diffWith("COMMITBRIEF.md", ".commitbriefignore", "README.md")
	res, err := CheckDiffForLocalConfig(d, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res != Continue {
		t.Errorf("Result = %v; root .commitbriefignore/COMMITBRIEF.md should not trigger", res)
	}
}

func TestAssumeYesSkipsPrompt(t *testing.T) {
	d := diffWith(".commitbrief/config.yml")
	var w bytes.Buffer
	res, err := CheckDiffForLocalConfig(d, Options{
		AssumeYes: true,
		Writer:    &w,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res != Continue {
		t.Errorf("Result = %v, want Continue (AssumeYes)", res)
	}
	if w.Len() != 0 {
		t.Errorf("AssumeYes should not print warning; got:\n%s", w.String())
	}
}

func TestNonInteractiveAborts(t *testing.T) {
	d := diffWith(".commitbrief/config.yml")
	var w bytes.Buffer
	res, _ := CheckDiffForLocalConfig(d, Options{
		NonInteractive: true,
		Writer:         &w,
	})
	if res != Abort {
		t.Errorf("Result = %v, want Abort", res)
	}
}

func TestPromptYesContinues(t *testing.T) {
	d := diffWith(".commitbrief/config.yml")
	for _, ans := range []string{"y", "Y", "yes", "YES", "  yes  "} {
		t.Run("ans="+ans, func(t *testing.T) {
			var w bytes.Buffer
			res, err := CheckDiffForLocalConfig(d, Options{
				Writer: &w,
				Reader: strings.NewReader(ans + "\n"),
			})
			if err != nil {
				t.Fatal(err)
			}
			if res != Continue {
				t.Errorf("answer %q: Result = %v, want Continue", ans, res)
			}
		})
	}
}

func TestPromptDefaultsToAbort(t *testing.T) {
	d := diffWith(".commitbrief/config.yml")
	for _, ans := range []string{"", "n", "no", "anything-else", "yep"} {
		t.Run("ans="+ans, func(t *testing.T) {
			var w bytes.Buffer
			res, err := CheckDiffForLocalConfig(d, Options{
				Writer: &w,
				Reader: strings.NewReader(ans + "\n"),
			})
			if err != nil {
				t.Fatal(err)
			}
			if res != Abort {
				t.Errorf("answer %q: Result = %v, want Abort", ans, res)
			}
		})
	}
}

func TestPromptEOFAborts(t *testing.T) {
	d := diffWith(".commitbrief/config.yml")
	var w bytes.Buffer
	res, err := CheckDiffForLocalConfig(d, Options{
		Writer: &w,
		Reader: strings.NewReader(""), // immediate EOF
	})
	if err != nil {
		t.Fatal(err)
	}
	if res != Abort {
		t.Errorf("EOF should abort; got %v", res)
	}
}

func TestTriggersIncludesOldPathForDeletions(t *testing.T) {
	d := diff.Diff{Files: []diff.FileDiff{
		{Path: "", OldPath: ".commitbrief/old.yml", Mode: diff.ModeDeleted},
	}}
	got := Triggers(d)
	if len(got) != 1 {
		t.Fatalf("Triggers = %v, want one entry for OldPath", got)
	}
	if got[0] != ".commitbrief/old.yml" {
		t.Errorf("Trigger path = %q, want OldPath fallback", got[0])
	}
}

func TestTriggersNestedPath(t *testing.T) {
	d := diffWith(".commitbrief/cache/abc.json")
	if len(Triggers(d)) != 1 {
		t.Error("nested .commitbrief/cache/* should also trigger")
	}
}

func TestTriggersDoesNotMatchSubstring(t *testing.T) {
	// Files named .commitbrief-something/ (no slash separator) should not trigger.
	d := diffWith(".commitbrief-backup/x.yml", "src/.commitbrief/foo.yml")
	if len(Triggers(d)) != 0 {
		t.Errorf("substring/non-root .commitbrief should not trigger; got: %v", Triggers(d))
	}
}

func TestResultString(t *testing.T) {
	if Continue.String() != "continue" {
		t.Error("Continue.String wrong")
	}
	if Abort.String() != "abort" {
		t.Error("Abort.String wrong")
	}
}

func TestWarningWriterDefaultsToStderr(t *testing.T) {
	// Smoke test: when Writer is nil, the call must not panic. We pipe an
	// empty Reader to ensure it returns quickly with Abort.
	d := diffWith(".commitbrief/config.yml")
	res, err := CheckDiffForLocalConfig(d, Options{
		NonInteractive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res != Abort {
		t.Errorf("Result = %v, want Abort", res)
	}
}
