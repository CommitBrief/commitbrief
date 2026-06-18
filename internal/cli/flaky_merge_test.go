// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"testing"

	"github.com/CommitBrief/commitbrief/internal/render"
)

func TestMergeFlaky(t *testing.T) {
	llm := []render.Finding{
		{Severity: render.SeverityHigh, File: "a.go", Line: 10, Title: "llm-1"},
		{Severity: render.SeverityLow, File: "b.go", Line: 20, Title: "llm-2"},
	}
	flaky := []render.Finding{
		// same file:line as llm-1 → dropped (the model already covered the line)
		{Severity: render.SeverityMedium, File: "a.go", Line: 10, Title: "flaky-dup"},
		// unique line → kept
		{Severity: render.SeverityMedium, File: "c.go", Line: 5, Title: "flaky-new"},
	}

	got := mergeFlaky(llm, flaky)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (2 llm + 1 unique flaky): %+v", len(got), got)
	}
	// Every LLM finding is preserved, in order, ahead of the flaky ones.
	if got[0].Title != "llm-1" || got[1].Title != "llm-2" {
		t.Errorf("LLM findings not preserved in order: %+v", got[:2])
	}
	for _, f := range got {
		if f.Title == "flaky-dup" {
			t.Errorf("flaky finding at an LLM-occupied line should be dropped: %+v", f)
		}
	}
	if got[2].Title != "flaky-new" {
		t.Errorf("unique flaky finding should be appended last; got %+v", got[2])
	}
}

func TestMergeFlaky_Empty(t *testing.T) {
	llm := []render.Finding{{Severity: render.SeverityInfo, File: "a.go", Line: 1, Title: "x"}}
	// No flaky findings → llm returned unchanged (the common + plain-text path).
	if got := mergeFlaky(llm, nil); len(got) != 1 || got[0].Title != "x" {
		t.Errorf("mergeFlaky(llm, nil) should return llm unchanged; got %+v", got)
	}
	// No LLM findings (clean review) + flaky present → flaky surfaced.
	flaky := []render.Finding{{Severity: render.SeverityMedium, File: "t_test.go", Line: 3, Title: "f"}}
	if got := mergeFlaky(nil, flaky); len(got) != 1 || got[0].Title != "f" {
		t.Errorf("mergeFlaky(nil, flaky) should return flaky; got %+v", got)
	}
}
