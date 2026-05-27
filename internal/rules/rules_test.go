// SPDX-License-Identifier: GPL-3.0-or-later

package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/lang"
)

func TestLoadFileWhenPresent(t *testing.T) {
	dir := t.TempDir()
	content := "# Custom\n\nReview rules for this repo.\n"
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceFile {
		t.Errorf("Source = %v, want SourceFile", got.Source)
	}
	if got.Content != content {
		t.Errorf("Content mismatch:\n%q\nvs\n%q", got.Content, content)
	}
	if got.Path != filepath.Join(dir, Filename) {
		t.Errorf("Path = %q, want %q", got.Path, filepath.Join(dir, Filename))
	}
	if got.Hash == "" {
		t.Error("Hash empty")
	}
}

func TestLoadFallsBackToDefaultWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceDefault {
		t.Errorf("Source = %v, want SourceDefault", got.Source)
	}
	if got.Path != "" {
		t.Errorf("Path = %q, want empty for default", got.Path)
	}
	if !strings.Contains(got.Content, "CommitBrief") {
		t.Error("default content does not contain 'CommitBrief'; embed broken")
	}
}

func TestLoadEmptyRepoRootUsesDefault(t *testing.T) {
	got, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceDefault {
		t.Errorf("Source = %v, want SourceDefault (empty repoRoot)", got.Source)
	}
}

func TestLoadUnreadableErrors(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, Filename)
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Skip("could not create directory in place of file:", err)
	}
	_, err := Load(dir)
	if err == nil {
		t.Error("expected error when COMMITBRIEF.md is a directory, got nil")
	}
}

func TestHashDeterministic(t *testing.T) {
	a := Default()
	b := Default()
	if a.Hash != b.Hash {
		t.Errorf("Default Hash not deterministic: %s vs %s", a.Hash, b.Hash)
	}
	if len(a.Hash) != 64 {
		t.Errorf("Hash length = %d, want 64 (hex sha256)", len(a.Hash))
	}
}

func TestHashChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte("rules A"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := Load(dir)
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte("rules B"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := Load(dir)
	if a.Hash == b.Hash {
		t.Error("hashes equal for different content; sha256 broken")
	}
}

func TestDefaultHasNoTBDPlaceholder(t *testing.T) {
	if strings.Contains(defaultContent, "<!-- TBD:") {
		t.Error("internal/rules/default.md still contains a '<!-- TBD:' placeholder; release-check.sh would block release")
	}
}

func TestBuildXMLWrap(t *testing.T) {
	rulesLoaded := Loaded{Content: "rule one\nrule two"}
	res := lang.Resolution{Code: "tr", Name: "Türkçe", Source: lang.SourceRepoConfig}
	system, userTpl := Build(rulesLoaded, res)

	wantTags := []string{
		"<project_rules>", "</project_rules>",
		"<severity_rubric>", "</severity_rubric>",
		"<response_format>", "</response_format>",
	}
	for _, tag := range wantTags {
		if !strings.Contains(system, tag) {
			t.Errorf("system prompt missing %q\n%s", tag, system)
		}
	}
	if !strings.Contains(system, "rule one") || !strings.Contains(system, "rule two") {
		t.Error("rules content not embedded in system prompt")
	}
	if strings.Contains(system, "<output_format>") {
		t.Errorf("system prompt should not include <output_format> after ADR-0014:\n%s", system)
	}
	for _, sev := range []string{"critical", "high", "medium", "low", "info"} {
		if !strings.Contains(system, sev) {
			t.Errorf("severity rubric missing level %q\n%s", sev, system)
		}
	}
	if !strings.Contains(system, `"findings"`) {
		t.Errorf("response_format missing JSON schema sentinel:\n%s", system)
	}
	if !strings.Contains(system, "Türkçe") || !strings.Contains(system, "ISO tr") {
		t.Errorf("lang directive missing or wrong:\n%s", system)
	}
	if !strings.Contains(system, "immutable") {
		t.Error("prompt-injection guard line missing")
	}
	if !strings.Contains(userTpl, "%s") {
		t.Errorf("userTpl is not a format string (missing %%s placeholder for diff)")
	}
	if !strings.Contains(userTpl, "```diff") {
		t.Error("userTpl missing diff fence")
	}
}

func TestBuildPreservesTrailingNewline(t *testing.T) {
	withNL := Loaded{Content: "rules\n"}
	withoutNL := Loaded{Content: "rules"}
	res := lang.Resolution{Code: "en", Name: "English"}
	a, _ := Build(withNL, res)
	b, _ := Build(withoutNL, res)
	if !strings.Contains(a, "rules\n</project_rules>") {
		t.Error("trailing newline content broken")
	}
	if !strings.Contains(b, "rules\n</project_rules>") {
		t.Error("missing-newline content did not get newline injected before close tag")
	}
}

func TestDefaultOutputIsTemplate(t *testing.T) {
	got := DefaultOutput()
	if got.Source != SourceDefault {
		t.Errorf("Source = %v, want SourceDefault", got.Source)
	}
	if got.Content == "" {
		t.Error("DefaultOutput content empty; embed broken")
	}
	for _, marker := range []string{".Findings", "groupBySeverity", "{{"} {
		if !strings.Contains(got.Content, marker) {
			t.Errorf("DefaultOutput missing template marker %q — output.md may not be a Go text/template", marker)
		}
	}
	if got.Hash == "" {
		t.Error("DefaultOutput Hash empty")
	}
}

func TestLoadOutputFallsBackToDefault(t *testing.T) {
	got, err := LoadOutput("", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceDefault {
		t.Errorf("Source = %v, want SourceDefault when both layers absent", got.Source)
	}
}

func TestLoadOutputPrefersRepoLevel(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(repo, LocalSubdir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, LocalSubdir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, LocalSubdir, OutputFilename), []byte("REPO OVERRIDE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, LocalSubdir, OutputFilename), []byte("USER OVERRIDE"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadOutput(repo, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "REPO OVERRIDE" {
		t.Errorf("Content = %q, want REPO OVERRIDE (repo wins over user)", got.Content)
	}
	if got.Source != SourceFile {
		t.Errorf("Source = %v, want SourceFile for repo-level hit", got.Source)
	}
}

func TestLoadOutputFallsThroughToUserHome(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(home, LocalSubdir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, LocalSubdir, OutputFilename), []byte("USER PERSONAL"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadOutput(repo, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "USER PERSONAL" {
		t.Errorf("Content = %q, want USER PERSONAL", got.Content)
	}
	if got.Source != SourceUserFile {
		t.Errorf("Source = %v, want SourceUserFile", got.Source)
	}
}

func TestLoadOutputEmptyUserHomeSkipsLayer(t *testing.T) {
	got, err := LoadOutput(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceDefault {
		t.Errorf("Source = %v, want SourceDefault when both repo and user-home absent", got.Source)
	}
}

func TestLoadOutputUnreadableErrors(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, LocalSubdir), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, LocalSubdir, OutputFilename)
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Skip("could not create directory in place of file:", err)
	}
	_, err := LoadOutput(repo, "")
	if err == nil {
		t.Error("expected error when OUTPUT.md is a directory, got nil")
	}
}
