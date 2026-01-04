package prcreate

import "testing"

func TestParseRemoteRefTips(t *testing.T) {
	out := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa origin/feature/a\nbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb origin/HEAD\n"
	m := parseRemoteRefTips(out)
	if len(m) != 2 {
		t.Fatalf("expected 2 shas, got %d", len(m))
	}
	if got := m["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]; len(got) != 1 || got[0] != "origin/feature/a" {
		t.Fatalf("unexpected branches for aaaa: %#v", got)
	}
}

func TestParseRevListOutput(t *testing.T) {
	out := "c1\nc2\n\nc3\n"
	commits := parseRevListOutput(out)
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(commits))
	}
	if commits[0] != "c1" || commits[2] != "c3" {
		t.Fatalf("unexpected commits: %#v", commits)
	}
}

func TestSelectStackedBase_PicksNearestAncestorTip(t *testing.T) {
	commits := []string{"head", "p1", "p2", "p3"}
	shaToBranches := map[string][]string{
		"p3": {"origin/feature/a"},
	}
	exclude := map[string]struct{}{
		"origin/HEAD": {},
	}

	prBase, gitBase, ok := selectStackedBase(commits, shaToBranches, exclude)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if prBase != "feature/a" || gitBase != "origin/feature/a" {
		t.Fatalf("unexpected selection: prBase=%q gitBase=%q", prBase, gitBase)
	}
}

func TestSelectStackedBase_ExcludesUpstreamAndOriginHead(t *testing.T) {
	commits := []string{"head", "p1", "p2"}
	shaToBranches := map[string][]string{
		"p1": {"origin/HEAD", "origin/feature/b"},
		"p2": {"origin/feature/a"},
	}
	exclude := map[string]struct{}{
		"origin/HEAD":      {},
		"origin/feature/b": {},
	}

	prBase, gitBase, ok := selectStackedBase(commits, shaToBranches, exclude)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if prBase != "feature/a" || gitBase != "origin/feature/a" {
		t.Fatalf("unexpected selection: prBase=%q gitBase=%q", prBase, gitBase)
	}
}

func TestSelectStackedBase_PicksDeterministicBranchWhenMultiple(t *testing.T) {
	commits := []string{"head", "p1"}
	shaToBranches := map[string][]string{
		"p1": {"origin/zzz", "origin/aaa"},
	}
	exclude := map[string]struct{}{
		"origin/HEAD": {},
	}

	prBase, gitBase, ok := selectStackedBase(commits, shaToBranches, exclude)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if prBase != "aaa" || gitBase != "origin/aaa" {
		t.Fatalf("unexpected selection: prBase=%q gitBase=%q", prBase, gitBase)
	}
}

func TestSelectStackedBase_NotFound(t *testing.T) {
	commits := []string{"head", "p1"}
	shaToBranches := map[string][]string{}
	exclude := map[string]struct{}{
		"origin/HEAD": {},
	}

	_, _, ok := selectStackedBase(commits, shaToBranches, exclude)
	if ok {
		t.Fatalf("expected ok=false")
	}
}
