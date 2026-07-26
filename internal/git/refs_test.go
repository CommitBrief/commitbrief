// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"context"
	"testing"
)

func TestBranchTopologyReportsAheadBehind(t *testing.T) {
	r := newFilterRepo(t)
	branches, err := BranchTopology(context.Background(), r.dir, "main")
	if err != nil {
		t.Fatalf("BranchTopology: %v", err)
	}
	byName := map[string]Branch{}
	for _, b := range branches {
		byName[b.Name] = b
	}

	base, ok := byName["main"]
	if !ok {
		t.Fatalf("main missing from %v", branchNames(branches))
	}
	if !base.IsBase || base.Ahead != 0 || base.Behind != 0 {
		t.Errorf("base branch must report itself as base with zero counts: %#v", base)
	}

	// payments/stripe forked off HEAD~1 and added one commit, while main went
	// on to add one of its own.
	feat, ok := byName["payments/stripe"]
	if !ok {
		t.Fatalf("payments/stripe missing from %v", branchNames(branches))
	}
	if feat.Ahead != 1 {
		t.Errorf("ahead = %d, want 1", feat.Ahead)
	}
	if feat.Behind != 2 {
		t.Errorf("behind = %d, want 2", feat.Behind)
	}
	if feat.Subject != "chore: bump sdk" {
		t.Errorf("subject = %q, want the tip commit's subject", feat.Subject)
	}
	if feat.Author != "Bob" {
		t.Errorf("author = %q, want Bob", feat.Author)
	}
	if feat.Date.IsZero() {
		t.Error("tip date should be populated")
	}
}

func TestBranchTopologyOrdersBaseFirst(t *testing.T) {
	r := newFilterRepo(t)
	r.git(t, nil, "branch", "aaa-early", "HEAD")
	branches, err := BranchTopology(context.Background(), r.dir, "main")
	if err != nil {
		t.Fatalf("BranchTopology: %v", err)
	}
	if len(branches) == 0 || !branches[0].IsBase {
		t.Fatalf("base branch must sort first, got %v", branchNames(branches))
	}
}

func TestBranchTopologySkipsRemoteHEADAlias(t *testing.T) {
	r := newFilterRepo(t)
	branches, err := BranchTopology(context.Background(), r.dir, "main")
	if err != nil {
		t.Fatalf("BranchTopology: %v", err)
	}
	for _, b := range branches {
		if b.Name == "origin/HEAD" {
			t.Fatalf("origin/HEAD is a symbolic alias and must not be listed")
		}
	}
}

func TestDefaultBranchFallsBackToMain(t *testing.T) {
	r := newFilterRepo(t)
	if got := DefaultBranch(context.Background(), r.dir); got != "main" {
		t.Fatalf("DefaultBranch = %q, want main", got)
	}
}

func branchNames(branches []Branch) []string {
	out := make([]string, 0, len(branches))
	for _, b := range branches {
		out = append(out, b.Name)
	}
	return out
}
