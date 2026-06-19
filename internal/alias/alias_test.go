// SPDX-License-Identifier: GPL-3.0-or-later

package alias

import (
	"strings"
	"testing"
)

func TestIsValidName(t *testing.T) {
	valid := []string{"cbr", "cb_r", "cb-r", "_x", "Cbr2"}
	for _, n := range valid {
		if !IsValidName(n) {
			t.Errorf("IsValidName(%q) = false, want true", n)
		}
	}
	invalid := []string{"", "1cb", "cb r", "cb=x", "cb/x", "cb'x", "cb;rm", "-cb"}
	for _, n := range invalid {
		if IsValidName(n) {
			t.Errorf("IsValidName(%q) = true, want false", n)
		}
	}
}

func TestUpsertBlockAppendReplaceIdempotent(t *testing.T) {
	// Append into existing content.
	out, changed := upsertBlock("export PATH=/x\n", "alias cbr='commitbrief'")
	if !changed {
		t.Fatal("append should report changed")
	}
	if !strings.Contains(out, "export PATH=/x") {
		t.Error("upsert dropped pre-existing content")
	}
	if !strings.Contains(out, blockStart) || !strings.Contains(out, "alias cbr='commitbrief'") {
		t.Errorf("block missing:\n%s", out)
	}

	// Re-upsert identical body → no change.
	out2, changed2 := upsertBlock(out, "alias cbr='commitbrief'")
	if changed2 {
		t.Error("identical re-upsert should report no change")
	}
	if out2 != out {
		t.Error("identical re-upsert mutated content")
	}

	// Replace with a new body in place; old alias line must be gone.
	out3, changed3 := upsertBlock(out, "alias cb='commitbrief'")
	if !changed3 {
		t.Fatal("changed body should report changed")
	}
	if strings.Contains(out3, "alias cbr='commitbrief'") {
		t.Errorf("old alias survived replacement:\n%s", out3)
	}
	if strings.Count(out3, blockStart) != 1 {
		t.Errorf("expected exactly one managed block, got:\n%s", out3)
	}
}

func TestStripManagedBlock(t *testing.T) {
	content := "before\n" + blockStart + "\nalias cbr='commitbrief'\n" + blockEnd + "\nafter\n"
	got := stripManagedBlock(content)
	if strings.Contains(got, "cbr") {
		t.Errorf("strip left managed body: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Errorf("strip removed surrounding content: %q", got)
	}
}

func TestByNameUnknown(t *testing.T) {
	if _, ok := ByName("no-such-shell"); ok {
		t.Error("ByName on unknown shell should return false")
	}
}
