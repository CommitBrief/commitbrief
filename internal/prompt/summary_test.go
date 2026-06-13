// SPDX-License-Identifier: GPL-3.0-or-later

package prompt

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/lang"
)

func TestBuildSummaryWithManifest(t *testing.T) {
	manifest := "a1b2c3d  fix: invoice rounding\n    files: internal/invoice/calc.go"
	p := BuildSummary("--- a/x.go\n+++ b/x.go\n", manifest, lang.Resolution{Code: "tr", Name: "Turkish"}, false)

	for _, want := range []string{
		"COMMIT MANIFEST",   // manifest is advertised in the system prompt
		"short commit hash", // attribution instruction present
		"(ISO tr)",          // language directive carries the resolved code
		"Turkish",           // ...and the human name
	} {
		if !strings.Contains(p.System, want) {
			t.Errorf("system prompt missing %q\n---\n%s", want, p.System)
		}
	}
	if !strings.Contains(p.User, "<manifest>") || !strings.Contains(p.User, manifest) {
		t.Errorf("user prompt should fence the manifest:\n%s", p.User)
	}
	if !strings.Contains(p.User, "<diff>") {
		t.Errorf("user prompt should fence the diff:\n%s", p.User)
	}
}

func TestBuildSummaryWithoutManifest(t *testing.T) {
	p := BuildSummary("--- a/x.go\n+++ b/x.go\n", "", lang.Resolution{Code: "en", Name: "English"}, false)

	if strings.Contains(p.System, "COMMIT MANIFEST") {
		t.Errorf("no manifest: system prompt must not mention a manifest\n%s", p.System)
	}
	if !strings.Contains(p.System, "do NOT append any parenthesised attribution") {
		t.Errorf("no manifest: system prompt should suppress attribution\n%s", p.System)
	}
	if strings.Contains(p.User, "<manifest>") {
		t.Errorf("no manifest: user prompt must not contain a manifest block\n%s", p.User)
	}
}

func TestBuildSummaryWithContext(t *testing.T) {
	en := lang.Resolution{Code: "en", Name: "English"}
	off := BuildSummary("diff", "", en, false)
	if strings.Contains(off.System, "PROJECT CONTEXT ACCESS") {
		t.Errorf("withContext=false must not append the context section\n%s", off.System)
	}
	on := BuildSummary("diff", "", en, true)
	if !strings.Contains(on.System, "PROJECT CONTEXT ACCESS") {
		t.Errorf("withContext=true must append the context section\n%s", on.System)
	}
}
