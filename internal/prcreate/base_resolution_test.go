package prcreate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestResolveBasesWithDeps_PRBaseFallbackWhenDetectedBaseMissingOnGitHub(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := Env{GHRepo: "owner/repo"}
	opts := Options{BaseGiven: false, Debug: false}

	calls := struct {
		defaultDetect int
	}{}

	deps := baseResolutionDeps{
		detectStackedBase: func(ctx context.Context, currentBranch string) (baseSelection, bool, error) {
			return baseSelection{prBase: "stsh/20260105-154600", gitBase: "origin/stsh/20260105-154600"}, true, nil
		},
		detectDefaultBase: func(ctx context.Context, env Env) (string, error) {
			calls.defaultDetect++
			return "main", nil
		},
		githubBranchExists: func(ctx context.Context, env Env, branch string) (bool, error) {
			if branch != "stsh/20260105-154600" {
				t.Fatalf("unexpected branch: %q", branch)
			}
			return false, nil
		},
		preferOriginRef: func(ctx context.Context, branch string) string {
			if branch == "main" {
				return "origin/main"
			}
			return branch
		},
		validateMergeBase: func(ctx context.Context, base string) error {
			// Stacked base is still valid for diff.
			return nil
		},
	}

	got, err := resolveBasesWithDeps(ctx, env, opts, "feature", nil, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.diffBaseLabel != "stsh/20260105-154600" {
		t.Fatalf("diffBaseLabel: got %q", got.diffBaseLabel)
	}
	if got.diffGitBase != "origin/stsh/20260105-154600" {
		t.Fatalf("diffGitBase: got %q", got.diffGitBase)
	}
	if got.prBase != "main" {
		t.Fatalf("prBase: got %q", got.prBase)
	}
	if strings.TrimSpace(got.prBaseFallbackMsg) == "" {
		t.Fatalf("expected prBaseFallbackMsg to be non-empty")
	}
	if calls.defaultDetect != 1 {
		t.Fatalf("expected default base detection once, got %d", calls.defaultDetect)
	}
}

func TestResolveBasesWithDeps_DiffBaseFallsBackWhenMergeBaseFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := Env{GHRepo: "owner/repo"}
	opts := Options{BaseGiven: false, Debug: false}

	deps := baseResolutionDeps{
		detectStackedBase: func(ctx context.Context, currentBranch string) (baseSelection, bool, error) {
			return baseSelection{prBase: "topic/base", gitBase: "origin/topic/base"}, true, nil
		},
		detectDefaultBase: func(ctx context.Context, env Env) (string, error) {
			return "main", nil
		},
		githubBranchExists: func(ctx context.Context, env Env, branch string) (bool, error) {
			t.Fatalf("githubBranchExists should not be called when diff base already fell back")
			return false, nil
		},
		preferOriginRef: func(ctx context.Context, branch string) string {
			if branch == "main" {
				return "origin/main"
			}
			return branch
		},
		validateMergeBase: func(ctx context.Context, base string) error {
			if base == "origin/topic/base" {
				return errors.New("no merge base")
			}
			return nil
		},
	}

	got, err := resolveBasesWithDeps(ctx, env, opts, "feature", nil, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.diffBaseLabel != "main" {
		t.Fatalf("diffBaseLabel: got %q", got.diffBaseLabel)
	}
	if got.diffGitBase != "origin/main" {
		t.Fatalf("diffGitBase: got %q", got.diffGitBase)
	}
	if got.prBase != "main" {
		t.Fatalf("prBase: got %q", got.prBase)
	}
	if strings.TrimSpace(got.prBaseFallbackMsg) != "" {
		t.Fatalf("expected prBaseFallbackMsg to be empty, got %q", got.prBaseFallbackMsg)
	}
}

func TestResolveBasesWithDeps_ExplicitBasePrefersOriginForDiffRef(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := Env{GHRepo: "owner/repo"}
	opts := Options{BaseGiven: true, BaseBranch: "main", Debug: false}

	deps := baseResolutionDeps{
		detectStackedBase: func(ctx context.Context, currentBranch string) (baseSelection, bool, error) {
			t.Fatalf("detectStackedBase should not be called when base is explicit")
			return baseSelection{}, false, nil
		},
		detectDefaultBase: func(ctx context.Context, env Env) (string, error) {
			t.Fatalf("detectDefaultBase should not be called when base is explicit")
			return "", nil
		},
		githubBranchExists: func(ctx context.Context, env Env, branch string) (bool, error) {
			t.Fatalf("githubBranchExists should not be called when base is explicit")
			return false, nil
		},
		preferOriginRef: func(ctx context.Context, branch string) string {
			if branch == "main" {
				return "origin/main"
			}
			return branch
		},
		validateMergeBase: func(ctx context.Context, base string) error {
			return nil
		},
	}

	got, err := resolveBasesWithDeps(ctx, env, opts, "feature", nil, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.diffBaseLabel != "main" {
		t.Fatalf("diffBaseLabel: got %q", got.diffBaseLabel)
	}
	if got.diffGitBase != "origin/main" {
		t.Fatalf("diffGitBase: got %q", got.diffGitBase)
	}
	if got.prBase != "main" {
		t.Fatalf("prBase: got %q", got.prBase)
	}
}
