package prsuggest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path"
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
	defaultMinimaxEndpoint   = "https://api.minimax.io/anthropic"
	defaultMinimaxModel      = "MiniMax-M2.1"
	defaultAnthropicEndpoint = "https://api.anthropic.com/v1/messages"
	defaultAnthropicModel    = "claude-sonnet-4-5"
	defaultOpenAIEndpoint    = "https://api.openai.com/v1"
	defaultOpenAIModel       = "gpt-4o-mini"
)

const (
	ExitOK          = 0
	ExitRuntimeErr  = 1
	ExitInvalidArgs = 2
	ExitInterrupted = 130
)

type Options struct {
	BaseBranch string
	BaseGiven  bool
	Edit       bool
	Create     bool
	Debug      bool
}

type LLMProvider string

const (
	providerMinimaxAnthropic LLMProvider = "minimax_anthropic"
	providerAnthropic        LLMProvider = "anthropic"
	providerAnthropicCompat  LLMProvider = "anthropic_compat"
	providerOpenAICompat     LLMProvider = "openai_compat"
)

type Env struct {
	Provider LLMProvider
	APIKey   string
	Endpoint string
	Model    string
	Editor   string
	GHRepo   string
}

func Run(opts Options, in io.Reader, out io.Writer, errOut io.Writer) int {
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
		fmt.Fprintln(errOut, "GH_PR_SUGGEST_LLM_API_KEY is not set, so `gh ultrahope pr suggest` cannot run. Please set it:")
		fmt.Fprintln(errOut, "export GH_PR_SUGGEST_LLM_API_KEY=YOUR_LLM_API_KEY")
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

	var baseBranch string
	if opts.BaseGiven {
		baseBranch = opts.BaseBranch
	} else {
		detected, err := detectDefaultBaseBranch(ctx, env)
		if err != nil || strings.TrimSpace(detected) == "" {
			if opts.Debug && err != nil {
				fmt.Fprintf(errOut, "[debug] failed to detect default base branch: %s\n", err.Error())
			}
			fmt.Fprintln(errOut, "Error: Unable to determine the repository default base branch.")
			fmt.Fprintln(errOut, "Hint: Set GH_REPO=OWNER/REPO, or pass a base branch explicitly:")
			fmt.Fprintln(errOut, "  gh ultrahope pr suggest main")
			return ExitRuntimeErr
		}
		baseBranch = strings.TrimSpace(detected)
		if opts.Debug {
			fmt.Fprintf(errOut, "[debug] detected default base branch=%s\n", baseBranch)
		}
	}

	// Validate merge base exists.
	if err := validateMergeBase(ctx, baseBranch); err != nil {
		fmt.Fprintf(errOut, "Error: Cannot find merge base between '%s' and HEAD.\n", baseBranch)
		fmt.Fprintln(errOut, "Make sure the base branch/commit exists.")
		return ExitRuntimeErr
	}

	commitLog, err := getCommitLog(ctx, baseBranch, "HEAD")
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}
	if strings.TrimSpace(commitLog) == "" {
		fmt.Fprintln(errOut, "No commits found between base and HEAD.")
		fmt.Fprintln(errOut, "Make sure you have commits to create a pull request.")
		return ExitRuntimeErr
	}

	diffSummary, err := getDiffSummary(ctx, baseBranch)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}
	detailedDiff, err := getDetailedDiff(ctx, baseBranch)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}
	changedFiles, err := getChangedFiles(ctx, baseBranch)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	prompt := buildPrompt(commitLog, diffSummary, detailedDiff, changedFiles, currentBranch, baseBranch)

	suggested, err := callLLM(ctx, env, prompt, opts.Debug, &sp, errOut)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	outputText := suggested
	if opts.Edit {
		edited, err := editInEditor(outputText, env.Editor)
		if err != nil {
			fmt.Fprintln(errOut, err.Error())
			return ExitRuntimeErr
		}
		outputText = edited
	}

	fmt.Fprintln(out, outputText)

	if opts.Create {
		skipConfirm := loadedCfg.Config.Create.SkipConfirm != nil && *loadedCfg.Config.Create.SkipConfirm
		draft := loadedCfg.Config.Create.Draft != nil && *loadedCfg.Config.Create.Draft

		title, body := parseSuggestedOutput(outputText)
		if strings.TrimSpace(title) == "" {
			fmt.Fprintln(errOut, "Failed to parse TITLE from suggested output.")
			return ExitRuntimeErr
		}

		if !skipConfirm {
			if !confirm(in, errOut, "Create pull request with this title/body? [y/N] ") {
				return ExitOK
			}
		} else if opts.Debug {
			fmt.Fprintln(errOut, "[debug] create.skip_confirm=true; skipping confirmation prompt")
		}

		if err := createPullRequest(ctx, env, title, body, currentBranch, baseBranch, draft, opts.Debug, &sp, out, errOut); err != nil {
			fmt.Fprintln(errOut, err.Error())
			return ExitRuntimeErr
		}
	}

	return ExitOK
}

func readEnv(fileCfg FileConfig) Env {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vim"
	}

	// Provider selection and shared overrides (avoid global env var namespace).
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("GH_PR_SUGGEST_LLM_PROVIDER")))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(fileCfg.LLM.Provider))
	}
	if provider == "" {
		provider = string(providerMinimaxAnthropic)
	}
	commonKey := strings.TrimSpace(os.Getenv("GH_PR_SUGGEST_LLM_API_KEY"))
	commonEndpoint := strings.TrimSpace(os.Getenv("GH_PR_SUGGEST_LLM_ENDPOINT"))
	commonModel := strings.TrimSpace(os.Getenv("GH_PR_SUGGEST_LLM_MODEL"))
	if commonEndpoint == "" {
		commonEndpoint = strings.TrimSpace(fileCfg.LLM.Endpoint)
	}
	if commonModel == "" {
		commonModel = strings.TrimSpace(fileCfg.LLM.Model)
	}

	var apiKey, endpoint, model string
	switch LLMProvider(provider) {
	case providerMinimaxAnthropic:
		apiKey = commonKey
		endpoint = firstNonEmpty(commonEndpoint, defaultMinimaxEndpoint)
		model = firstNonEmpty(commonModel, defaultMinimaxModel)
	case providerAnthropic, providerAnthropicCompat:
		apiKey = commonKey
		endpoint = firstNonEmpty(commonEndpoint, defaultAnthropicEndpoint)
		model = firstNonEmpty(commonModel, defaultAnthropicModel)
	case providerOpenAICompat:
		apiKey = commonKey
		endpoint = firstNonEmpty(commonEndpoint, defaultOpenAIEndpoint)
		model = firstNonEmpty(commonModel, defaultOpenAIModel)
	default:
		// Keep raw provider for validation error, but still populate defaults to reduce nil surprises.
		apiKey = commonKey
		endpoint = firstNonEmpty(commonEndpoint, defaultMinimaxEndpoint)
		model = firstNonEmpty(commonModel, defaultMinimaxModel)
	}

	endpoint = normalizeEndpoint(LLMProvider(provider), endpoint)

	return Env{
		Provider: LLMProvider(provider),
		APIKey:   strings.TrimSpace(apiKey),
		Endpoint: strings.TrimSpace(endpoint),
		Model:    strings.TrimSpace(model),
		Editor:   editor,
		GHRepo:   os.Getenv("GH_REPO"),
	}
}

func validateEnv(env Env) error {
	switch env.Provider {
	case providerMinimaxAnthropic:
		if strings.TrimSpace(env.APIKey) == "" {
			return fmt.Errorf("Missing GH_PR_SUGGEST_LLM_API_KEY env var (provider=%s).", env.Provider)
		}
	case providerAnthropic, providerAnthropicCompat, providerOpenAICompat:
		if strings.TrimSpace(env.APIKey) == "" {
			return fmt.Errorf("Missing GH_PR_SUGGEST_LLM_API_KEY env var (provider=%s).", env.Provider)
		}
	default:
		return fmt.Errorf("Invalid GH_PR_SUGGEST_LLM_PROVIDER=%q. Supported: %s, %s, %s, %s.",
			string(env.Provider),
			providerMinimaxAnthropic, providerAnthropic, providerAnthropicCompat, providerOpenAICompat,
		)
	}
	if looksLikePlaceholderKey(env.APIKey) {
		return fmt.Errorf("GH_PR_SUGGEST_LLM_API_KEY looks like a placeholder value. Please set the real API key.")
	}

	if strings.TrimSpace(env.Endpoint) == "" {
		return fmt.Errorf("LLM endpoint is empty (provider=%s). Set GH_PR_SUGGEST_LLM_ENDPOINT.", env.Provider)
	}
	if strings.TrimSpace(env.Model) == "" {
		return fmt.Errorf("LLM model is empty (provider=%s). Set GH_PR_SUGGEST_LLM_MODEL.", env.Provider)
	}
	return nil
}

func looksLikePlaceholderKey(key string) bool {
	k := strings.TrimSpace(key)
	if k == "" {
		return true
	}
	upper := strings.ToUpper(k)
	if strings.Contains(upper, "YOUR_LLM_API_KEY") || strings.Contains(upper, "YOUR_API_KEY") {
		return true
	}
	// Common placeholder patterns.
	if k == "..." || strings.Contains(k, "REPLACE_ME") || strings.Contains(k, "CHANGEME") {
		return true
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func normalizeEndpoint(provider LLMProvider, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	suffix := ""
	switch provider {
	case providerAnthropicCompat:
		// For anthropic-compatible endpoints, treat the user value as BASE_URL and
		// always build BASE_URL + "/v1/messages" to avoid ambiguity.
		return forceBaseSuffix(raw, "/v1/messages")
	case providerOpenAICompat:
		// For OpenAI-compatible chat endpoints, treat the user value as BASE_URL and
		// always build BASE_URL + "/chat/completions" to avoid ambiguity.
		// Convention: BASE_URL includes "/v1" (e.g. https://api.provider.com/v1).
		return forceOpenAICompatBase(raw)
	case providerMinimaxAnthropic, providerAnthropic:
		suffix = "/v1/messages"
	default:
		return raw
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		// If it's not a normal URL, don't try to rewrite it.
		return raw
	}

	trimmedPath := strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(trimmedPath, suffix) {
		return raw
	}
	// If the user passed a base URL like "https://api.minimax.io/anthropic" or
	// "https://api.anthropic.com", append the provider-specific path.
	// Heuristic: if it doesn't include "/v1/", treat it as a base path.
	if !strings.Contains(trimmedPath, "/v1/") {
		u.Path = path.Join(u.Path, suffix)
		return u.String()
	}
	return raw
}

func forceBaseSuffix(raw string, suffix string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}

	trimmedPath := strings.TrimRight(u.Path, "/")

	// If the user provided a full URL ending with the suffix, strip it to get BASE_URL.
	if strings.HasSuffix(trimmedPath, suffix) {
		trimmedPath = strings.TrimSuffix(trimmedPath, suffix)
		trimmedPath = strings.TrimRight(trimmedPath, "/")
	}

	// If the path contains /v1/, assume they accidentally provided a versioned path;
	// keep only the portion before /v1/ as the BASE_URL path.
	if idx := strings.Index(trimmedPath, "/v1/"); idx >= 0 {
		trimmedPath = strings.TrimRight(trimmedPath[:idx], "/")
	}

	u.Path = path.Join(trimmedPath, suffix)
	return u.String()
}

func forceOpenAICompatBase(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}

	trimmedPath := strings.TrimRight(u.Path, "/")

	// If a full endpoint was provided, normalize back to BASE_URL.
	if strings.HasSuffix(trimmedPath, "/v1/chat/completions") {
		trimmedPath = strings.TrimSuffix(trimmedPath, "/v1/chat/completions")
		trimmedPath = strings.TrimRight(trimmedPath, "/")
	}
	if strings.HasSuffix(trimmedPath, "/chat/completions") {
		trimmedPath = strings.TrimSuffix(trimmedPath, "/chat/completions")
		trimmedPath = strings.TrimRight(trimmedPath, "/")
	}

	// Enforce the convention that BASE_URL contains "/v1".
	if trimmedPath == "" {
		trimmedPath = "/v1"
	} else if !strings.HasSuffix(trimmedPath, "/v1") {
		// If user passed something like https://api.provider.com, make it .../v1
		// so we consistently build /v1/chat/completions.
		trimmedPath = path.Join(trimmedPath, "/v1")
	}

	u.Path = path.Join(trimmedPath, "/chat/completions")
	return u.String()
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
		os.Exit(ExitInterrupted)
	}()
}

// ---- prompt + LLM ----

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
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
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
	switch env.Provider {
	case providerMinimaxAnthropic, providerAnthropic, providerAnthropicCompat:
		return callAnthropicMessages(ctx, env, prompt, debug, spinner, errOut)
	case providerOpenAICompat:
		return callOpenAIChatCompletions(ctx, env, prompt, debug, spinner, errOut)
	default:
		return "", fmt.Errorf("unsupported provider: %s", env.Provider)
	}
}

func callAnthropicMessages(ctx context.Context, env Env, prompt string, debug bool, spinner *Spinner, errOut io.Writer) (string, error) {
	if debug {
		fmt.Fprintf(errOut, "[debug] provider=%s endpoint=%s model=%s api_key_present=%t api_key_len=%d\n",
			env.Provider, env.Endpoint, env.Model, strings.TrimSpace(env.APIKey) != "", len(strings.TrimSpace(env.APIKey)))
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
	// Anthropic Messages API shape uses `x-api-key`. (MiniMax's Anthropic-compatible
	// endpoint also accepts `x-api-key` per local verification.)
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

type openAIChatRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens,omitempty"`
	Messages  []llmMessage `json:"messages"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message *struct {
			Content string `json:"content"`
		} `json:"message"`
		Text string `json:"text"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func callOpenAIChatCompletions(ctx context.Context, env Env, prompt string, debug bool, spinner *Spinner, errOut io.Writer) (string, error) {
	if debug {
		fmt.Fprintf(errOut, "[debug] provider=%s endpoint=%s model=%s api_key_present=%t api_key_len=%d\n",
			env.Provider, env.Endpoint, env.Model, strings.TrimSpace(env.APIKey) != "", len(strings.TrimSpace(env.APIKey)))
	}

	reqBody := openAIChatRequest{
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
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

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
		// Prefer message from JSON error if present.
		var parsed openAIChatResponse
		if err := json.Unmarshal(bodyBytes, &parsed); err == nil {
			if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
				return "", fmt.Errorf("API error: %s", strings.TrimSpace(parsed.Error.Message))
			}
		}
		msg := safeSnippet(string(bodyBytes), 800)
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("API error: %s", msg)
	}

	var parsed openAIChatResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err == nil {
		if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			return "", fmt.Errorf("API error: %s", strings.TrimSpace(parsed.Error.Message))
		}
		for _, c := range parsed.Choices {
			if c.Message != nil && strings.TrimSpace(c.Message.Content) != "" {
				return c.Message.Content, nil
			}
			if strings.TrimSpace(c.Text) != "" {
				return c.Text, nil
			}
		}
	}

	// Fallback for minor schema differences without guessing too much.
	var anyObj map[string]any
	if err := json.Unmarshal(bodyBytes, &anyObj); err == nil {
		if arr, ok := anyObj["choices"].([]any); ok && len(arr) > 0 {
			if m, ok := arr[0].(map[string]any); ok {
				if msg, ok := m["message"].(map[string]any); ok {
					if s, _ := msg["content"].(string); strings.TrimSpace(s) != "" {
						return s, nil
					}
				}
				if s, _ := m["text"].(string); strings.TrimSpace(s) != "" {
					return s, nil
				}
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

func isTTY(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

const wizardDocURL = "https://github.com/toyamarinyon/gh-ultrahope#configuration-files-non-secret-defaults"

func runInitWizard(in io.Reader, errOut io.Writer) error {
	r := bufio.NewReader(in)

	fmt.Fprintln(errOut, "We'll start configuring the ultrahope PR suggest extension.")
	fmt.Fprintln(errOut, "")
	fmt.Fprintln(errOut, "Select LLM API:")
	fmt.Fprintln(errOut, "  1) OpenAI")
	fmt.Fprintln(errOut, "  2) OpenAI Compatible (Chat)")
	fmt.Fprintln(errOut, "  3) Anthropic")
	fmt.Fprintln(errOut, "  4) Anthropic Compatible")

	var provider string
	needsEndpoint := false

	for {
		fmt.Fprint(errOut, "Enter selection [1-4]: ")
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		sel := strings.TrimSpace(line)
		switch sel {
		case "1":
			provider = string(providerOpenAICompat)
		case "2":
			provider = string(providerOpenAICompat)
			needsEndpoint = true
		case "3":
			provider = string(providerAnthropic)
		case "4":
			provider = string(providerAnthropicCompat)
			needsEndpoint = true
		default:
			if err == io.EOF {
				return fmt.Errorf("input aborted")
			}
			continue
		}
		break
	}

	endpoint := ""
	if needsEndpoint {
		for {
			fmt.Fprint(errOut, "Enter API base URL: ")
			line, err := r.ReadString('\n')
			if err != nil && err != io.EOF {
				return err
			}
			endpoint = strings.TrimSpace(line)
			if endpoint == "" {
				if err == io.EOF {
					return fmt.Errorf("input aborted")
				}
				continue
			}
			break
		}
	}

	model := ""
	for {
		fmt.Fprint(errOut, "Enter model: ")
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		model = strings.TrimSpace(line)
		if model == "" {
			if err == io.EOF {
				return fmt.Errorf("input aborted")
			}
			continue
		}
		break
	}

	path, err := configCreationPath()
	if err != nil {
		return err
	}

	cfg := FileConfig{
		LLM: LLMConfig{
			Provider: provider,
			Endpoint: endpoint,
			Model:    model,
		},
	}
	if err := writeYAMLConfigFile(path, cfg); err != nil {
		return err
	}

	fmt.Fprintln(errOut, "")
	fmt.Fprintf(errOut, "Created config file at %s. For other settings, see %s.\n", path, wizardDocURL)
	return nil
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

func createPullRequest(ctx context.Context, env Env, title, body, head, base string, draft bool, debug bool, spinner *Spinner, out io.Writer, errOut io.Writer) error {
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
	if draft {
		args = append(args, "--draft")
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
