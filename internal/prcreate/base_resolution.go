package prcreate

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"
)

type resolvedBases struct {
	diffBaseLabel     string // branch name shown in output and used in prompt
	diffGitBase       string // git ref used for merge-base/log/diff
	prBase            string // branch name passed to `gh pr create --base`
	prBaseFallbackMsg string // printed to stderr if non-empty
}

type baseResolutionDeps struct {
	detectStackedBase  func(ctx context.Context, currentBranch string) (baseSelection, bool, error)
	detectDefaultBase  func(ctx context.Context, env Env) (string, error)
	githubBranchExists func(ctx context.Context, env Env, branch string) (bool, error)
	preferOriginRef    func(ctx context.Context, branch string) string
	validateMergeBase  func(ctx context.Context, base string) error
	gitMergeBase       func(ctx context.Context, base, head string) (string, error)
	gitRevParse        func(ctx context.Context, ref string) (string, error)
}

func defaultBaseDetectError() error {
	return fmt.Errorf("Error: Unable to determine the repository default base branch.\nHint: Set GH_REPO=OWNER/REPO, or pass a base branch explicitly:\n  gh ultrahope pr create main")
}

func resolveBases(ctx context.Context, env Env, opts Options, currentBranch string, errOut io.Writer) (resolvedBases, error) {
	deps := baseResolutionDeps{
		detectStackedBase:  detectStackedBaseBranch,
		detectDefaultBase:  detectDefaultBaseBranch,
		githubBranchExists: githubBranchExists,
		preferOriginRef:    preferOriginBranchRef,
		validateMergeBase:  validateMergeBase,
		gitMergeBase: func(ctx context.Context, base, head string) (string, error) {
			out, err := runGit(ctx, "merge-base", base, head)
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(out), nil
		},
		gitRevParse: func(ctx context.Context, ref string) (string, error) {
			out, err := runGit(ctx, "rev-parse", "--verify", ref)
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(out), nil
		},
	}
	return resolveBasesWithDeps(ctx, env, opts, currentBranch, errOut, deps)
}

func isPerfectAncestorBase(ctx context.Context, baseRef string, deps baseResolutionDeps) bool {
	baseRef = strings.TrimSpace(baseRef)
	if baseRef == "" {
		return false
	}
	if deps.gitRevParse == nil || deps.gitMergeBase == nil {
		return false
	}
	baseSHA, err := deps.gitRevParse(ctx, baseRef)
	if err != nil || strings.TrimSpace(baseSHA) == "" {
		return false
	}
	mb, err := deps.gitMergeBase(ctx, baseRef, "HEAD")
	if err != nil || strings.TrimSpace(mb) == "" {
		return false
	}
	// If merge-base(base, HEAD) == tip(base), then HEAD is strictly "on top of" base,
	// so base is the most sensible PR base.
	return strings.TrimSpace(mb) == strings.TrimSpace(baseSHA)
}

func resolveBasesWithDeps(ctx context.Context, env Env, opts Options, currentBranch string, errOut io.Writer, deps baseResolutionDeps) (resolvedBases, error) {
	// Explicit base: treat it as both PR base and diff base.
	if opts.BaseGiven {
		base := strings.TrimSpace(opts.BaseBranch)
		return resolvedBases{
			diffBaseLabel: base,
			diffGitBase:   deps.preferOriginRef(ctx, base),
			prBase:        base,
		}, nil
	}

	// Prefer `main` if it is a perfect ancestor base (merge-base(main, HEAD) == main tip).
	// This handles repos where stacked-base heuristics might pick another long-lived branch
	// (e.g. preview) even though the branch was cut from main.
	if isPerfectAncestorBase(ctx, deps.preferOriginRef(ctx, "main"), deps) || isPerfectAncestorBase(ctx, "main", deps) {
		// Best-effort safety check: only abort main-preference if GitHub clearly says `main` doesn't exist.
		if exists, err := deps.githubBranchExists(ctx, env, "main"); err == nil && !exists {
			// Fall through to normal resolution.
		} else {
			return resolvedBases{
				diffBaseLabel: "main",
				diffGitBase:   deps.preferOriginRef(ctx, "main"),
				prBase:        "main",
			}, nil
		}
	}

	defaultBase := ""
	ensureDefaultBase := func() (string, error) {
		if strings.TrimSpace(defaultBase) != "" {
			return defaultBase, nil
		}
		detected, err := deps.detectDefaultBase(ctx, env)
		if err != nil || strings.TrimSpace(detected) == "" {
			if opts.Debug && errOut != nil && err != nil {
				fmt.Fprintf(errOut, "[debug] failed to detect default base branch: %s\n", err.Error())
			}
			return "", defaultBaseDetectError()
		}
		defaultBase = strings.TrimSpace(detected)
		if opts.Debug && errOut != nil {
			fmt.Fprintf(errOut, "[debug] detected default base branch=%s\n", defaultBase)
		}
		return defaultBase, nil
	}

	// 1) Pick a stacked base candidate (best-effort).
	var stacked baseSelection
	stackedOK := false
	if detected, ok, err := deps.detectStackedBase(ctx, currentBranch); err != nil {
		if opts.Debug && errOut != nil {
			fmt.Fprintf(errOut, "[debug] stacked base detection failed: %s\n", err.Error())
		}
	} else if ok {
		stacked = detected
		stackedOK = strings.TrimSpace(stacked.prBase) != "" && strings.TrimSpace(stacked.gitBase) != ""
		if stackedOK && opts.Debug && errOut != nil {
			fmt.Fprintf(errOut, "[debug] detected stacked base prBase=%s gitBase=%s\n", strings.TrimSpace(stacked.prBase), strings.TrimSpace(stacked.gitBase))
		}
	}

	// 2) Decide diff base first.
	r := resolvedBases{}
	if stackedOK {
		r.diffBaseLabel = strings.TrimSpace(stacked.prBase)
		r.diffGitBase = strings.TrimSpace(stacked.gitBase) // typically origin/<branch>
	} else {
		b, err := ensureDefaultBase()
		if err != nil {
			return resolvedBases{}, err
		}
		r.diffBaseLabel = b
		r.diffGitBase = deps.preferOriginRef(ctx, b)
	}

	// Ensure merge-base works for the diff base. If not, fall back diff base to default.
	if err := deps.validateMergeBase(ctx, r.diffGitBase); err != nil {
		if opts.Debug && errOut != nil {
			fmt.Fprintf(errOut, "[debug] merge-base failed for diff base %q: %s\n", r.diffGitBase, err.Error())
		}
		b, derr := ensureDefaultBase()
		if derr != nil {
			return resolvedBases{}, derr
		}
		r.diffBaseLabel = b
		r.diffGitBase = deps.preferOriginRef(ctx, b)
	}

	// 3) Decide PR base (may differ from diff base).
	r.prBase = r.diffBaseLabel
	if stackedOK && r.diffBaseLabel == strings.TrimSpace(stacked.prBase) {
		exists, err := deps.githubBranchExists(ctx, env, strings.TrimSpace(stacked.prBase))
		if err != nil {
			if opts.Debug && errOut != nil {
				fmt.Fprintf(errOut, "[debug] failed to check GitHub branch existence for %q: %s\n", strings.TrimSpace(stacked.prBase), err.Error())
			}
		} else if !exists {
			b, derr := ensureDefaultBase()
			if derr != nil {
				return resolvedBases{}, derr
			}
			r.prBase = b
			r.prBaseFallbackMsg = fmt.Sprintf("[info] PR base branch fell back: %q -> %q (detected base missing on GitHub)", strings.TrimSpace(stacked.prBase), r.prBase)
		}
	}

	return r, nil
}

func githubBranchExists(ctx context.Context, env Env, branch string) (bool, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return false, fmt.Errorf("branch is empty")
	}

	repoStr := strings.TrimSpace(env.GHRepo)
	var repo repository.Repository
	var err error
	if repoStr != "" {
		repo, err = repository.Parse(repoStr)
	} else {
		repo, err = repository.Current()
	}
	if err != nil {
		return false, err
	}

	client, err := api.DefaultRESTClient()
	if err != nil {
		return false, err
	}

	path := fmt.Sprintf("repos/%s/%s/branches/%s", repo.Owner, repo.Name, url.PathEscape(branch))
	var resp any
	if err := client.Get(path, &resp); err != nil {
		// go-gh doesn't guarantee a stable exported error type across versions,
		// so use conservative detection for "not found".
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "404") && strings.Contains(msg, "not found") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func preferOriginBranchRef(ctx context.Context, branch string) string {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return ""
	}
	// If the caller already provided an origin/* ref, keep it.
	if strings.HasPrefix(branch, "origin/") {
		return branch
	}
	// If origin/<branch> exists locally, prefer it for git log/diff/merge-base.
	if _, err := runGit(ctx, "rev-parse", "--verify", "origin/"+branch); err == nil {
		return "origin/" + branch
	}
	return branch
}
