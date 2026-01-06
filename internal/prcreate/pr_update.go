package prcreate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type pullRequestInfo struct {
	Number  int    `json:"number"`
	IsDraft bool   `json:"isDraft"`
	URL     string `json:"url"`
	Title   string `json:"title"`
}

func RunUpdate(opts Options, in io.Reader, out io.Writer, errOut io.Writer) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var sp Spinner
	setupSignalHandlers(ctx, cancel, &sp, errOut)

	loadedCfg, err := loadConfig(ctx, opts.Debug, errOut)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	if len(loadedCfg.Sources) == 0 && isTTY(in) {
		if err := runInitWizard(in, errOut); err != nil {
			fmt.Fprintln(errOut, err.Error())
			return ExitRuntimeErr
		}
		// Reload after creating config.
		loadedCfg, err = loadConfig(ctx, opts.Debug, errOut)
		if err != nil {
			fmt.Fprintln(errOut, err.Error())
			return ExitRuntimeErr
		}
	}

	env := readEnv(loadedCfg.Config)
	if strings.TrimSpace(env.APIKey) == "" {
		fmt.Fprintln(errOut, "ULTRAHOPE_LLM_API_KEY is not set, so `gh ultrahope pr update` cannot run. Please set it:")
		fmt.Fprintln(errOut, "export ULTRAHOPE_LLM_API_KEY=YOUR_LLM_API_KEY")
		return ExitRuntimeErr
	}
	if err := validateEnv(env); err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	currentBranch, err := getCurrentBranch(ctx)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	bases, err := resolveBases(ctx, env, opts, currentBranch, errOut)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	// Validate merge base exists for the diff base.
	if err := validateMergeBase(ctx, bases.diffGitBase); err != nil {
		fmt.Fprintf(errOut, "Error: Cannot find merge base between '%s' and HEAD.\n", bases.diffGitBase)
		fmt.Fprintln(errOut, "Make sure the base branch/commit exists.")
		return ExitRuntimeErr
	}

	commitLog, err := getCommitLog(ctx, bases.diffGitBase, "HEAD")
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}
	if strings.TrimSpace(commitLog) == "" {
		fmt.Fprintln(errOut, "No commits found between base and HEAD.")
		fmt.Fprintln(errOut, "Make sure you have commits to update a pull request.")
		return ExitRuntimeErr
	}

	diffSummary, err := getDiffSummary(ctx, bases.diffGitBase)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}
	detailedDiff, err := getDetailedDiff(ctx, bases.diffGitBase)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}
	changedFiles, err := getChangedFiles(ctx, bases.diffGitBase)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	prompt := buildPrompt(commitLog, diffSummary, detailedDiff, changedFiles, currentBranch, bases.diffBaseLabel)
	suggested, err := callLLM(ctx, env, prompt, opts.Debug, &sp, errOut)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	outputText := fmt.Sprintf("Base branch: %s\n\n%s", strings.TrimSpace(bases.diffBaseLabel), suggested)
	if opts.Edit {
		edited, err := editInEditor(outputText, env.Editor)
		if err != nil {
			fmt.Fprintln(errOut, err.Error())
			return ExitRuntimeErr
		}
		outputText = edited
	}

	fmt.Fprintln(out, outputText)
	if strings.TrimSpace(bases.prBaseFallbackMsg) != "" {
		fmt.Fprintln(errOut, bases.prBaseFallbackMsg)
	}

	if opts.DryRun {
		return ExitOK
	}

	title, body := parseSuggestedOutput(outputText)
	if strings.TrimSpace(title) == "" {
		fmt.Fprintln(errOut, "Failed to parse TITLE from generated output.")
		return ExitRuntimeErr
	}

	// 1) Find PR for current branch.
	pr, err := findPullRequestForHead(ctx, env, currentBranch)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	// 2) If no PR exists, propose creating one.
	draft := resolveDraft(opts, loadedCfg.Config)
	if opts.Debug && opts.DraftGiven {
		fmt.Fprintf(errOut, "[debug] --draft explicitly set to %t (overrides config)\n", opts.Draft)
	}

	skipConfirm := loadedCfg.Config.Create.SkipConfirm != nil && *loadedCfg.Config.Create.SkipConfirm

	if pr == nil {
		if !skipConfirm {
			msg := fmt.Sprintf("No pull request found for branch %q. Create one now? [y/N] ", currentBranch)
			if !confirm(in, errOut, msg) {
				return ExitOK
			}
		} else if opts.Debug {
			fmt.Fprintln(errOut, "[debug] create.skip_confirm=true; skipping PR-create prompt")
		}

		if err := createPullRequest(ctx, env, title, body, currentBranch, bases.prBase, draft, opts.Debug, &sp, out, errOut); err != nil {
			fmt.Fprintln(errOut, err.Error())
			return ExitRuntimeErr
		}
		return ExitOK
	}

	// 3) Update title/body of the existing PR.
	if !skipConfirm {
		if !confirm(in, errOut, fmt.Sprintf("Update pull request #%d with this title/body? [y/N] ", pr.Number)) {
			return ExitOK
		}
	} else if opts.Debug {
		fmt.Fprintln(errOut, "[debug] create.skip_confirm=true; skipping PR-update prompt")
	}

	if err := updatePullRequest(ctx, env, pr.Number, title, body, opts.Debug, &sp, errOut); err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}
	if strings.TrimSpace(pr.URL) != "" {
		fmt.Fprintf(out, "\nPR updated: %s\n", strings.TrimSpace(pr.URL))
	} else {
		fmt.Fprintf(out, "\nPR updated: #%d\n", pr.Number)
	}

	// 4) If draft, offer publishing.
	if pr.IsDraft {
		if confirm(in, errOut, "Pull request is draft. Publish it now? [y/N] ") {
			if err := publishPullRequest(ctx, env, pr.Number, opts.Debug, &sp, errOut); err != nil {
				fmt.Fprintln(errOut, err.Error())
				return ExitRuntimeErr
			}
			fmt.Fprintln(out, "PR published (marked ready for review).")
		}
	}

	return ExitOK
}

func findPullRequestForHead(ctx context.Context, env Env, headBranch string) (*pullRequestInfo, error) {
	headBranch = strings.TrimSpace(headBranch)
	if headBranch == "" {
		return nil, fmt.Errorf("current branch is empty")
	}

	args := []string{
		"pr", "list",
		"--head", headBranch,
		"--state", "open",
		"--limit", "1",
		"--json", "number,isDraft,url,title",
	}
	if strings.TrimSpace(env.GHRepo) != "" {
		args = append(args, "--repo", strings.TrimSpace(env.GHRepo))
	}

	out, err := execGh(ctx, args...)
	if err != nil {
		return nil, err
	}

	var prs []pullRequestInfo
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return nil, fmt.Errorf("failed to parse `gh pr list` JSON: %w", err)
	}
	if len(prs) == 0 {
		return nil, nil
	}
	return &prs[0], nil
}

func updatePullRequest(ctx context.Context, env Env, number int, title string, body string, debug bool, spinner *Spinner, errOut io.Writer) error {
	if number <= 0 {
		return fmt.Errorf("invalid pull request number: %d", number)
	}
	tmpDir := os.TempDir()
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("gh-pr-body-%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(tmpPath, []byte(body), 0o600); err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	args := []string{
		"pr", "edit", fmt.Sprintf("%d", number),
		"--title", strings.TrimSpace(title),
		"--body-file", tmpPath,
	}
	if strings.TrimSpace(env.GHRepo) != "" {
		args = append(args, "--repo", strings.TrimSpace(env.GHRepo))
	}

	fmt.Fprintln(errOut, "")
	if spinner != nil {
		spinner.Start(fmt.Sprintf(" Updating pull request #%d...", number), errOut)
	}
	_, err := execGh(ctx, args...)
	if spinner != nil {
		spinner.Stop()
	}
	if err != nil {
		return err
	}
	if debug {
		fmt.Fprintf(errOut, "[debug] updated PR #%d\n", number)
	}
	return nil
}

func publishPullRequest(ctx context.Context, env Env, number int, debug bool, spinner *Spinner, errOut io.Writer) error {
	if number <= 0 {
		return fmt.Errorf("invalid pull request number: %d", number)
	}
	args := []string{
		"pr", "ready", fmt.Sprintf("%d", number),
	}
	if strings.TrimSpace(env.GHRepo) != "" {
		args = append(args, "--repo", strings.TrimSpace(env.GHRepo))
	}

	fmt.Fprintln(errOut, "")
	if spinner != nil {
		spinner.Start(fmt.Sprintf(" Publishing pull request #%d...", number), errOut)
	}
	_, err := execGh(ctx, args...)
	if spinner != nil {
		spinner.Stop()
	}
	if err != nil {
		return err
	}
	if debug {
		fmt.Fprintf(errOut, "[debug] published PR #%d\n", number)
	}
	return nil
}
