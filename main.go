package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	gh "github.com/cli/go-gh/v2"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"
)

const (
	defaultBaseBranch = "main"
	defaultEndpoint   = "https://api.minimax.io/anthropic/v1/messages"
	defaultModel      = "MiniMax-M2.1"

	exitOK          = 0
	exitRuntimeErr  = 1
	exitInvalidArgs = 2
	exitInterrupted = 130
)

type Config struct {
	BaseBranch string
	BaseGiven  bool
	Edit       bool
	Create     bool
	Debug      bool
	Help       bool
}

type Env struct {
	APIKey   string
	Endpoint string
	Model    string
	Editor   string
	GHRepo   string
}

func main() {
	code := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	os.Exit(code)
}

func run(argv []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	cfg, code := parseArgs(argv, errOut)
	if code != exitOK {
		return code
	}

	if cfg.Help {
		showHelp(out)
		return exitOK
	}

	env := readEnv()
	if env.APIKey == "" {
		fmt.Fprintln(errOut, "Missing MINIMAX_CP_KEY env var.")
		return exitRuntimeErr
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var sp Spinner
	setupSignalHandlers(ctx, cancel, &sp, errOut)

	currentBranch, err := getCurrentBranch(ctx)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return exitRuntimeErr
	}

	baseBranch := cfg.BaseBranch
	if !cfg.BaseGiven {
		if detected, err := detectDefaultBaseBranch(ctx, env); err == nil && strings.TrimSpace(detected) != "" {
			baseBranch = strings.TrimSpace(detected)
			if cfg.Debug {
				fmt.Fprintf(errOut, "[debug] detected default base branch=%s\n", baseBranch)
			}
		} else if cfg.Debug && err != nil {
			fmt.Fprintf(errOut, "[debug] failed to detect default base branch: %s\n", err.Error())
		}
	}

	// Validate merge base exists.
	if err := validateMergeBase(ctx, baseBranch); err != nil {
		fmt.Fprintf(errOut, "Error: Cannot find merge base between '%s' and HEAD.\n", baseBranch)
		fmt.Fprintln(errOut, "Make sure the base branch/commit exists.")
		return exitRuntimeErr
	}

	commitLog, err := getCommitLog(ctx, baseBranch, "HEAD")
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return exitRuntimeErr
	}
	if strings.TrimSpace(commitLog) == "" {
		fmt.Fprintln(errOut, "No commits found between base and HEAD.")
		fmt.Fprintln(errOut, "Make sure you have commits to create a pull request.")
		return exitRuntimeErr
	}

	diffSummary, err := getDiffSummary(ctx, baseBranch)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return exitRuntimeErr
	}
	detailedDiff, err := getDetailedDiff(ctx, baseBranch)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return exitRuntimeErr
	}
	changedFiles, err := getChangedFiles(ctx, baseBranch)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return exitRuntimeErr
	}

	prompt := buildPrompt(commitLog, diffSummary, detailedDiff, changedFiles, currentBranch, baseBranch)

	suggested, err := callLLM(ctx, env, prompt, cfg.Debug, &sp, errOut)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return exitRuntimeErr
	}

	outputText := suggested
	if cfg.Edit {
		edited, err := editInEditor(outputText, env.Editor)
		if err != nil {
			fmt.Fprintln(errOut, err.Error())
			return exitRuntimeErr
		}
		outputText = edited
	}

	fmt.Fprintln(out, outputText)

	if cfg.Create {
		title, body := parseSuggestedOutput(outputText)
		if strings.TrimSpace(title) == "" {
			fmt.Fprintln(errOut, "Failed to parse TITLE from suggested output.")
			return exitRuntimeErr
		}

		if !confirm(in, errOut, "Create pull request with this title/body? [y/N] ") {
			return exitOK
		}

		if err := createPullRequest(ctx, env, title, body, currentBranch, baseBranch, cfg.Debug, &sp, out, errOut); err != nil {
			fmt.Fprintln(errOut, err.Error())
			return exitRuntimeErr
		}
	}

	return exitOK
}

func parseArgs(argv []string, errOut io.Writer) (Config, int) {
	cfg := Config{
		BaseBranch: defaultBaseBranch,
	}

	var positional []string

	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch arg {
		case "--edit", "-e":
			cfg.Edit = true
		case "--create", "-c":
			cfg.Create = true
		case "--debug", "-d":
			cfg.Debug = true
		case "--help", "-h":
			cfg.Help = true
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(errOut, "Unknown argument: %s\n", arg)
				return Config{}, exitInvalidArgs
			}
			positional = append(positional, arg)
		}
	}

	if len(positional) > 1 {
		fmt.Fprintln(errOut, "Too many arguments.")
		return Config{}, exitInvalidArgs
	}
	if len(positional) == 1 {
		cfg.BaseBranch = positional[0]
		cfg.BaseGiven = true
	}
	return cfg, exitOK
}

func showHelp(out io.Writer) {
	fmt.Fprintln(out, `gh pr-suggest

Suggest a pull request title and body from git commits and diffs.

USAGE:
  gh pr-suggest [base-branch]
  gh pr-suggest main --edit
  gh pr-suggest main --create

OPTIONS:
  base-branch  Base branch/commit (default: auto-detect repo default; fallback: main)
  --edit, -e   Edit the output before printing
  --create, -c Create PR with suggested title/body (with confirmation)
  --debug, -d  Print extra debug info
  --help, -h   Show help`)
}

func readEnv() Env {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vim"
	}

	endpoint := os.Getenv("MINIMAX_ENDPOINT")
	if endpoint == "" {
		endpoint = defaultEndpoint
	}

	model := os.Getenv("MINIMAX_MODEL")
	if model == "" {
		model = defaultModel
	}

	return Env{
		APIKey:   os.Getenv("MINIMAX_CP_KEY"),
		Endpoint: endpoint,
		Model:    model,
		Editor:   editor,
		GHRepo:   os.Getenv("GH_REPO"),
	}
}

// ---- git wrappers ----

func runGit(ctx context.Context, args ...string) (string, error) {
	stdout, stderr, err := runCmd(ctx, "git", args, nil)
	if err != nil {
		if strings.TrimSpace(stderr) == "" {
			return "", fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(stderr))
	}
	return strings.TrimRight(stdout, "\n"), nil
}

func getCurrentBranch(ctx context.Context) (string, error) {
	out, err := runGit(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func validateMergeBase(ctx context.Context, base string) error {
	_, err := runGit(ctx, "merge-base", base, "HEAD")
	return err
}

func getCommitLog(ctx context.Context, base, head string) (string, error) {
	out, err := runGit(ctx, "log", "--oneline", fmt.Sprintf("%s..%s", base, head))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func getDiffSummary(ctx context.Context, base string) (string, error) {
	out, err := runGit(ctx, "diff", fmt.Sprintf("%s...HEAD", base), "--stat")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func getDetailedDiff(ctx context.Context, base string) (string, error) {
	out, err := runGit(ctx, "diff", fmt.Sprintf("%s...HEAD", base))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func getChangedFiles(ctx context.Context, base string) ([]string, error) {
	out, err := runGit(ctx, "diff", fmt.Sprintf("%s...HEAD", base), "--name-only")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var files []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

func pickPushRemote(ctx context.Context) (string, error) {
	// Prefer origin if it exists; otherwise pick the first remote.
	out, err := runGit(ctx, "remote")
	if err != nil {
		return "", err
	}
	var remotes []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			remotes = append(remotes, l)
		}
	}
	if len(remotes) == 0 {
		return "", fmt.Errorf("no git remotes configured")
	}
	for _, r := range remotes {
		if r == "origin" {
			return "origin", nil
		}
	}
	return remotes[0], nil
}

func ensureBranchPushed(ctx context.Context, branch string, debug bool, spinner *Spinner, errOut io.Writer) error {
	// If upstream isn't set, push with -u to establish it. If upstream exists and
	// we're ahead, push to avoid gh pr create failing in non-interactive mode.
	upstream, err := runGit(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		remote, rerr := pickPushRemote(ctx)
		if rerr != nil {
			return rerr
		}
		if debug {
			fmt.Fprintf(errOut, "[debug] no upstream for %s; pushing to %s with -u\n", branch, remote)
		}
		fmt.Fprintln(errOut, "")
		if spinner != nil {
			spinner.Start(fmt.Sprintf(" Pushing branch %s to %s (set upstream)...", branch, remote), errOut)
		}
		_, perr := runGit(ctx, "push", "-u", remote, "HEAD")
		if spinner != nil {
			spinner.Stop()
		}
		if perr == nil {
			fmt.Fprintln(errOut, "Push completed.")
		}
		return perr
	}

	upstream = strings.TrimSpace(upstream)
	if upstream == "" {
		return fmt.Errorf("upstream branch is empty")
	}

	counts, err := runGit(ctx, "rev-list", "--left-right", "--count", fmt.Sprintf("%s...HEAD", upstream))
	if err != nil {
		return err
	}
	// Output format: "<behind>\t<ahead>"
	fields := strings.Fields(strings.ReplaceAll(counts, "\t", " "))
	if len(fields) != 2 {
		return fmt.Errorf("unexpected rev-list count output: %q", counts)
	}
	ahead := fields[1]
	if ahead != "0" {
		if debug {
			fmt.Fprintf(errOut, "[debug] local branch ahead of %s by %s commits; pushing\n", upstream, ahead)
		}
		fmt.Fprintln(errOut, "")
		if spinner != nil {
			spinner.Start(fmt.Sprintf(" Pushing branch %s to %s (%s commits ahead)...", branch, upstream, ahead), errOut)
		}
		_, perr := runGit(ctx, "push")
		if spinner != nil {
			spinner.Stop()
		}
		if perr == nil {
			fmt.Fprintln(errOut, "Push completed.")
		}
		return perr
	}
	fmt.Fprintf(errOut, "\nBranch is already up-to-date with %s (no push needed).\n", upstream)
	return nil
}

func runCmd(ctx context.Context, bin string, args []string, stdin io.Reader) (stdout string, stderr string, err error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if stdin != nil {
		cmd.Stdin = stdin
	}
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// ---- signal/spinner ----

type spinnerState struct {
	frames  []string
	idx     int
	message string
	w       io.Writer
	stopCh  chan struct{}
	doneCh  chan struct{}
	mu      sync.Mutex
	running bool
}

type Spinner struct {
	st spinnerState
}

func (s *Spinner) Start(message string, w io.Writer) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if s.st.running {
		return
	}
	s.st.frames = []string{"⠾", "⠽", "⠻", "⠧", "⠟", "⠯", "⠷"}
	s.st.idx = 0
	s.st.message = message
	s.st.w = w
	s.st.stopCh = make(chan struct{})
	s.st.doneCh = make(chan struct{})
	s.st.running = true

	go func() {
		defer close(s.st.doneCh)
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				frame := s.st.frames[s.st.idx]
				s.st.idx = (s.st.idx + 1) % len(s.st.frames)
				fmt.Fprintf(s.st.w, "\r%s%s", frame, s.st.message)
			case <-s.st.stopCh:
				clearSpinnerLine(s.st.w, s.st.message)
				return
			}
		}
	}()
}

func (s *Spinner) Stop() {
	s.st.mu.Lock()
	if !s.st.running {
		s.st.mu.Unlock()
		return
	}
	close(s.st.stopCh)
	done := s.st.doneCh
	s.st.running = false
	s.st.mu.Unlock()
	<-done
}

func clearSpinnerLine(w io.Writer, message string) {
	// Use rune count to better match terminal width.
	n := utf8.RuneCountInString(message) + 8
	if n < 40 {
		n = 40
	}
	fmt.Fprintf(w, "\r%s\r", strings.Repeat(" ", n))
}

func setupSignalHandlers(ctx context.Context, cancel context.CancelFunc, spinner *Spinner, errOut io.Writer) {
	_ = ctx
	_ = errOut
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		if spinner != nil {
			spinner.Stop()
		}
		cancel()
		os.Exit(exitInterrupted)
	}()
}

// ---- prompt + LLM (MiniMax anthropic-compatible) ----

var (
	reConventionalWithScope = regexp.MustCompile(`^(\w+)\s*\(([^)]+)\):`)
)

func extractTopics(commitLog string) []string {
	topics := map[string]struct{}{}
	lines := strings.Split(commitLog, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		// `git log --oneline` starts with "<sha> <subject>"
		parts := strings.SplitN(l, " ", 2)
		subject := l
		if len(parts) == 2 {
			subject = strings.TrimSpace(parts[1])
		}
		m := reConventionalWithScope.FindStringSubmatch(subject)
		if len(m) == 3 {
			scope := strings.TrimSpace(m[2])
			if scope != "" {
				topics[scope] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(topics))
	for t := range topics {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func buildPrompt(commitLog, diffSummary, detailedDiff string, changedFiles []string, currentBranch, baseBranch string) string {
	topics := extractTopics(commitLog)
	topicsBlock := "None detected"
	if len(topics) > 0 {
		topicsBlock = strings.Join(topics, ", ")
	}
	filesBlock := "(none)"
	if len(changedFiles) > 0 {
		var b strings.Builder
		for _, f := range changedFiles {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		filesBlock = strings.TrimRight(b.String(), "\n")
	}

	return fmt.Sprintf(`You are a helpful assistant for writing pull request descriptions.

Task:
Generate a concise and informative pull request title and body based on the git commits and changes.

Output format:
TITLE: <title>

BODY:
## Summary
<2-3 sentence summary>

## Changes
- <key change 1>
- <key change 2>
- <key change 3>

## Testing
<how tested, or "Not applicable">

---

Current branch: %s
Base branch: %s

Commit log (%s → HEAD):
%s

Changed files:
%s

Topics detected:
%s

Diff summary:
%s

Full diff:
%s`, currentBranch, baseBranch, baseBranch, nonEmptyOr(commitLog, "(no commits)"), filesBlock, topicsBlock, diffSummary, detailedDiff)
}

func nonEmptyOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

type llmRequest struct {
	Model     string      `json:"model"`
	MaxTokens int         `json:"max_tokens"`
	Messages  []llmMessage `json:"messages"`
}

type llmMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type llmResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func callLLM(ctx context.Context, env Env, prompt string, debug bool, spinner *Spinner, errOut io.Writer) (string, error) {
	if debug {
		fmt.Fprintf(errOut, "[debug] endpoint=%s model=%s\n", env.Endpoint, env.Model)
	}

	reqBody := llmRequest{
		Model:     env.Model,
		MaxTokens: 2048,
		Messages: []llmMessage{
			{Role: "user", Content: prompt},
		},
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.Endpoint, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", env.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{}
	if spinner != nil {
		spinner.Start(" Calling API...", errOut)
	}
	resp, err := client.Do(req)
	if spinner != nil {
		spinner.Stop()
	}
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := safeSnippet(string(bodyBytes), 800)
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("API error: %s", msg)
	}

	var parsed llmResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err == nil {
		if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			return "", fmt.Errorf("API error: %s", strings.TrimSpace(parsed.Error.Message))
		}
		var text strings.Builder
		for _, c := range parsed.Content {
			if c.Type == "text" && c.Text != "" {
				text.WriteString(c.Text)
			}
		}
		if strings.TrimSpace(text.String()) != "" {
			return text.String(), nil
		}
	}

	// Fallback: tolerate minor schema differences without guessing too much.
	var anyObj map[string]any
	if err := json.Unmarshal(bodyBytes, &anyObj); err == nil {
		if c, ok := anyObj["content"].(string); ok && strings.TrimSpace(c) != "" {
			return c, nil
		}
		if arr, ok := anyObj["content"].([]any); ok {
			var text strings.Builder
			for _, v := range arr {
				m, ok := v.(map[string]any)
				if !ok {
					continue
				}
				if t, _ := m["type"].(string); t != "text" {
					continue
				}
				if s, _ := m["text"].(string); s != "" {
					text.WriteString(s)
				}
			}
			if strings.TrimSpace(text.String()) != "" {
				return text.String(), nil
			}
		}
	}

	if debug {
		fmt.Fprintf(errOut, "[debug] response_body=%s\n", safeSnippet(string(bodyBytes), 1200))
	}
	return "", fmt.Errorf("API response did not include text content")
}

func safeSnippet(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// ---- output parsing, editor, PR creation ----

var (
	reTitle = regexp.MustCompile(`(?m)^TITLE:\s*(.+)$`)
	reURL   = regexp.MustCompile(`https?://\S+`)
)

func parseSuggestedOutput(output string) (title string, body string) {
	titleMatch := reTitle.FindStringSubmatch(output)
	if len(titleMatch) == 2 {
		title = strings.TrimSpace(titleMatch[1])
	}

	// Go's regexp is RE2 (no lookahead), so parse BODY with simple string operations.
	// Spec: body is everything after "BODY:\n" until "\n---\n" or end.
	idx := strings.Index(output, "BODY:")
	if idx >= 0 {
		rest := output[idx+len("BODY:"):]
		rest = strings.TrimPrefix(rest, "\r\n")
		rest = strings.TrimPrefix(rest, "\n")
		if sep := strings.Index(rest, "\n---\n"); sep >= 0 {
			body = strings.TrimSpace(rest[:sep])
		} else {
			body = strings.TrimSpace(rest)
		}
	} else {
		// Fallback: treat full output as body (mirrors TS behavior).
		body = strings.TrimSpace(output)
	}
	return title, body
}

func editInEditor(content string, editor string) (string, error) {
	tmpDir := os.TempDir()
	f, err := os.CreateTemp(tmpDir, "gh-suggest-pr-*.txt")
	if err != nil {
		return "", err
	}
	tmpPath := f.Name()
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	_ = f.Close()

	defer os.Remove(tmpPath)

	// `EDITOR` may include arguments; interpret it similarly to shell splitting.
	editorArgs := splitCommand(editor)
	if len(editorArgs) == 0 {
		return "", fmt.Errorf("invalid editor command")
	}
	bin := editorArgs[0]
	args := append(editorArgs[1:], tmpPath)

	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}

	b, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func splitCommand(cmd string) []string {
	// Minimal split: respect simple quoting? For now, space-split is acceptable for typical $EDITOR values.
	// Users can set EDITOR to an absolute binary path without spaces to avoid ambiguity.
	fields := strings.Fields(cmd)
	return fields
}

func confirm(in io.Reader, errOut io.Writer, prompt string) bool {
	fmt.Fprintln(errOut, "")
	fmt.Fprint(errOut, prompt)

	r := bufio.NewReader(in)
	line, _ := r.ReadString('\n')
	resp := strings.TrimSpace(strings.ToLower(line))
	return resp == "y" || resp == "yes"
}

func execGh(ctx context.Context, args ...string) (string, error) {
	stdout, stderr, err := gh.ExecContext(ctx, args...)
	if err != nil {
		if strings.TrimSpace(stderr.String()) == "" {
			return "", fmt.Errorf("gh %s failed: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("gh %s failed: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

func detectDefaultBaseBranch(ctx context.Context, env Env) (string, error) {
	// Prefer GitHub's notion of the repo default branch via the REST API.
	// This avoids guessing between main/master and matches what gh itself uses.
	var apiErr error
	repoStr := strings.TrimSpace(env.GHRepo)

	var repo repository.Repository
	var err error
	if repoStr != "" {
		repo, err = repository.Parse(repoStr)
	} else {
		repo, err = repository.Current()
	}
	if err == nil {
		client, err := api.DefaultRESTClient()
		if err == nil {
			var resp struct {
				DefaultBranch string `json:"default_branch"`
			}
			path := fmt.Sprintf("repos/%s/%s", repo.Owner, repo.Name)
			if err := client.Get(path, &resp); err == nil {
				if s := strings.TrimSpace(resp.DefaultBranch); s != "" {
					return s, nil
				}
				apiErr = fmt.Errorf("default_branch is empty")
			} else {
				apiErr = err
			}
		} else {
			apiErr = err
		}
	} else {
		apiErr = err
	}

	// Fallback to git's remote HEAD symbolic ref if available.
	ref, err := runGit(ctx, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		if apiErr != nil {
			return "", fmt.Errorf("failed to detect default branch via API: %w; fallback failed: %w", apiErr, err)
		}
		return "", err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		if apiErr != nil {
			return "", fmt.Errorf("failed to detect default branch via API: %w; fallback failed: origin/HEAD is empty", apiErr)
		}
		return "", fmt.Errorf("origin/HEAD is empty")
	}
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1], nil
}

func createPullRequest(ctx context.Context, env Env, title, body, head, base string, debug bool, spinner *Spinner, out io.Writer, errOut io.Writer) error {
	// Ensure the current branch is pushed so `gh pr create` can run non-interactively
	// without prompting where to push.
	if err := ensureBranchPushed(ctx, head, debug, spinner, errOut); err != nil {
		return fmt.Errorf("failed to push current branch: %w", err)
	}

	tmpDir := os.TempDir()
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("gh-pr-body-%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(tmpPath, []byte(body), 0o600); err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	args := []string{
		"pr", "create",
		"--title", title,
		"--body-file", tmpPath,
		"--base", base,
	}
	if strings.TrimSpace(env.GHRepo) != "" {
		args = append(args, "--repo", strings.TrimSpace(env.GHRepo))
	}

	fmt.Fprintln(errOut, "")
	if spinner != nil {
		spinner.Start(fmt.Sprintf(" Creating pull request (%s → %s)...", head, base), errOut)
	}
	output, err := execGh(ctx, args...)
	if spinner != nil {
		spinner.Stop()
	}
	if err != nil {
		return err
	}

	if m := reURL.FindString(output); m != "" {
		fmt.Fprintf(out, "\nPR created: %s\n", m)
		return nil
	}

	// Fallback: print raw output.
	fmt.Fprintln(out, "\nPR created successfully")
	if strings.TrimSpace(output) != "" {
		fmt.Fprintln(out, output)
	}
	return nil
}
