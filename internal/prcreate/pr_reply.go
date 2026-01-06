package prcreate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
)

type commentKind int

const (
	commentKindUnknown commentKind = iota
	commentKindReviewComment
	commentKindIssueComment
)

type prCommentTarget struct {
	Owner       string
	Repo        string
	PRNumber    int
	CommentID   int64
	Kind        commentKind
	OriginalURL string
}

var (
	reDiscussionR  = regexp.MustCompile(`(?i)^discussion_r(\d+)$`)
	reShortR       = regexp.MustCompile(`(?i)^r(\d+)$`)
	reIssueComment = regexp.MustCompile(`(?i)^issuecomment-(\d+)$`)
	reFindPullNum  = regexp.MustCompile(`^\d+$`)
)

const maxDiffForPrompt = 12000

func RunReply(opts Options, in io.Reader, out io.Writer, errOut io.Writer) int {
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
		loadedCfg, err = loadConfig(ctx, opts.Debug, errOut)
		if err != nil {
			fmt.Fprintln(errOut, err.Error())
			return ExitRuntimeErr
		}
	}

	env := readEnv(loadedCfg.Config)
	if strings.TrimSpace(env.APIKey) == "" {
		fmt.Fprintln(errOut, "ULTRAHOPE_LLM_API_KEY is not set, so `gh ultrahope pr reply` cannot run. Please set it:")
		fmt.Fprintln(errOut, "export ULTRAHOPE_LLM_API_KEY=YOUR_LLM_API_KEY")
		return ExitRuntimeErr
	}
	if err := validateEnv(env); err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	reader := bufio.NewReader(in)

	fmt.Fprintln(errOut, "")
	fmt.Fprint(errOut, "Comment URL: ")
	urlLine, err := readLine(reader)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	target, err := parsePRCommentURL(strings.TrimSpace(urlLine))
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	prTitle, prBody, err := fetchPRTitleBody(ctx, target.Owner, target.Repo, target.PRNumber)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	diff, err := fetchPRDiffViaGh(ctx, target.Owner, target.Repo, target.PRNumber)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	commentBody, err := fetchCommentBody(ctx, target)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}

	fmt.Fprintln(out, "Fetched PR title, body, diff, and comment body.")

	fmt.Fprintln(errOut, "")
	fmt.Fprintln(errOut, "Enter reply draft (end with an empty line):")
	draft, err := readUntilBlankLine(reader)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}
	if strings.TrimSpace(draft) == "" {
		fmt.Fprintln(errOut, "Draft reply is empty.")
		return ExitRuntimeErr
	}

	targetLang := inferReplyLanguage(prTitle, prBody, commentBody)
	prompt := buildReplyPrompt(prTitle, prBody, diff, commentBody, draft, target.OriginalURL, targetLang)
	suggested, err := callLLM(ctx, env, prompt, opts.Debug, &sp, errOut)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return ExitRuntimeErr
	}
	suggested = strings.TrimSpace(suggested)
	if suggested == "" {
		fmt.Fprintln(errOut, "LLM returned an empty reply.")
		return ExitRuntimeErr
	}
	// Fallback: if the model ignored our language instruction, re-translate to the target language.
	if !looksLikeLanguage(suggested, targetLang) {
		rePrompt := buildTranslatePrompt(suggested, targetLang)
		reSuggested, err := callLLM(ctx, env, rePrompt, opts.Debug, &sp, errOut)
		if err == nil && strings.TrimSpace(reSuggested) != "" {
			suggested = strings.TrimSpace(reSuggested)
		}
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Generated reply:")
	fmt.Fprintln(out, suggested)

	for {
		action := promptPostAction(reader, errOut)
		switch action {
		case "yes":
			if err := postReply(ctx, target, suggested); err != nil {
				fmt.Fprintln(errOut, err.Error())
				return ExitRuntimeErr
			}
			fmt.Fprintln(out, "Reply posted.")
			return ExitOK
		case "edit":
			edited, err := editInEditor(suggested, env.Editor)
			if err != nil {
				fmt.Fprintln(errOut, err.Error())
				return ExitRuntimeErr
			}
			suggested = strings.TrimSpace(edited)
			if suggested == "" {
				fmt.Fprintln(errOut, "Edited reply is empty.")
				return ExitRuntimeErr
			}
			fmt.Fprintln(out, "")
			fmt.Fprintln(out, "Edited reply:")
			fmt.Fprintln(out, suggested)
			continue
		default:
			return ExitOK
		}
	}

	// unreachable
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func readUntilBlankLine(r *bufio.Reader) (string, error) {
	var lines []string
	for {
		line, err := readLine(r)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func confirmWithReader(r *bufio.Reader, errOut io.Writer, prompt string) bool {
	fmt.Fprintln(errOut, "")
	fmt.Fprint(errOut, prompt)
	line, _ := readLine(r)
	resp := strings.TrimSpace(strings.ToLower(line))
	return resp == "y" || resp == "yes"
}

func promptPostAction(r *bufio.Reader, errOut io.Writer) string {
	fmt.Fprintln(errOut, "")
	fmt.Fprint(errOut, "Post this reply now? [y/N/e] ")
	line, _ := readLine(r)
	resp := strings.TrimSpace(strings.ToLower(line))
	switch resp {
	case "y", "yes":
		return "yes"
	case "e", "edit":
		return "edit"
	default:
		return "no"
	}
}

func parsePRCommentURL(raw string) (prCommentTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return prCommentTarget{}, fmt.Errorf("comment URL is empty")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return prCommentTarget{}, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return prCommentTarget{}, fmt.Errorf("invalid URL: missing scheme/host")
	}

	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) < 4 {
		return prCommentTarget{}, fmt.Errorf("invalid URL path: %q", u.Path)
	}

	owner := segments[0]
	repo := segments[1]

	// Find "/pull/<number>" anywhere in the path (tolerate suffix like /files).
	prNum := 0
	for i := 0; i+1 < len(segments); i++ {
		if segments[i] == "pull" && reFindPullNum.MatchString(segments[i+1]) {
			n, _ := strconv.Atoi(segments[i+1])
			prNum = n
			break
		}
	}
	if prNum <= 0 {
		return prCommentTarget{}, fmt.Errorf("failed to parse pull request number from URL: %q", raw)
	}

	frag := strings.TrimSpace(u.Fragment)
	if frag == "" {
		return prCommentTarget{}, fmt.Errorf("URL fragment is empty; expected #discussion_r<id> or #issuecomment-<id>")
	}

	kind := commentKindUnknown
	var commentID int64

	if m := reDiscussionR.FindStringSubmatch(frag); len(m) == 2 {
		kind = commentKindReviewComment
		commentID, _ = strconv.ParseInt(m[1], 10, 64)
	} else if m := reShortR.FindStringSubmatch(frag); len(m) == 2 {
		kind = commentKindReviewComment
		commentID, _ = strconv.ParseInt(m[1], 10, 64)
	} else if m := reIssueComment.FindStringSubmatch(frag); len(m) == 2 {
		kind = commentKindIssueComment
		commentID, _ = strconv.ParseInt(m[1], 10, 64)
	}

	if kind == commentKindUnknown || commentID <= 0 {
		return prCommentTarget{}, fmt.Errorf("unsupported URL fragment %q; expected #discussion_r<id> or #issuecomment-<id>", frag)
	}

	return prCommentTarget{
		Owner:       owner,
		Repo:        repo,
		PRNumber:    prNum,
		CommentID:   commentID,
		Kind:        kind,
		OriginalURL: raw,
	}, nil
}

func fetchPRTitleBody(ctx context.Context, owner, repo string, number int) (title string, body string, err error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" || number <= 0 {
		return "", "", fmt.Errorf("invalid repo/PR: %s/%s#%d", owner, repo, number)
	}
	client, err := api.DefaultRESTClient()
	if err != nil {
		return "", "", err
	}
	var resp struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number)
	if err := client.Get(path, &resp); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(resp.Title), strings.TrimSpace(resp.Body), nil
}

func fetchPRDiffViaGh(ctx context.Context, owner, repo string, number int) (string, error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" || number <= 0 {
		return "", fmt.Errorf("invalid repo/PR: %s/%s#%d", owner, repo, number)
	}
	args := []string{"pr", "diff", fmt.Sprintf("%d", number), "--repo", fmt.Sprintf("%s/%s", owner, repo)}
	out, err := execGh(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func fetchCommentBody(ctx context.Context, target prCommentTarget) (string, error) {
	client, err := api.DefaultRESTClient()
	if err != nil {
		return "", err
	}
	var resp struct {
		Body string `json:"body"`
	}

	var path string
	switch target.Kind {
	case commentKindReviewComment:
		path = fmt.Sprintf("repos/%s/%s/pulls/comments/%d", target.Owner, target.Repo, target.CommentID)
	case commentKindIssueComment:
		path = fmt.Sprintf("repos/%s/%s/issues/comments/%d", target.Owner, target.Repo, target.CommentID)
	default:
		return "", fmt.Errorf("unsupported comment kind")
	}

	if err := client.Get(path, &resp); err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Body), nil
}

func postReply(ctx context.Context, target prCommentTarget, reply string) error {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return fmt.Errorf("reply is empty")
	}
	client, err := api.DefaultRESTClient()
	if err != nil {
		return err
	}

	switch target.Kind {
	case commentKindReviewComment:
		path := fmt.Sprintf("repos/%s/%s/pulls/comments/%d/replies", target.Owner, target.Repo, target.CommentID)
		body := map[string]string{"body": reply}
		b, _ := json.Marshal(body)
		return client.Post(path, bytes.NewReader(b), nil)
	case commentKindIssueComment:
		path := fmt.Sprintf("repos/%s/%s/issues/%d/comments", target.Owner, target.Repo, target.PRNumber)
		body := map[string]string{
			"body": fmt.Sprintf("Replying to: %s\n\n%s", target.OriginalURL, reply),
		}
		b, _ := json.Marshal(body)
		return client.Post(path, bytes.NewReader(b), nil)
	default:
		return fmt.Errorf("unsupported comment kind")
	}
}

func buildReplyPrompt(prTitle, prBody, prDiff, commentBody, userDraft, commentURL string, targetLang replyLanguage) string {
	diffForPrompt := strings.TrimSpace(prDiff)
	truncNote := ""
	if len(diffForPrompt) > maxDiffForPrompt {
		diffForPrompt = diffForPrompt[:maxDiffForPrompt]
		truncNote = fmt.Sprintf("\n\nNOTE: Diff was truncated to %d characters for the prompt.", maxDiffForPrompt)
	}

	return fmt.Sprintf(`You are a helpful assistant replying to a GitHub pull request comment.

Requirements:
- Write the reply in the target language.
- The user draft may be in a different language; you MUST translate it into the target language.
- Do NOT mix languages unless the PR/comment already does.
- Keep it concise, clear, and directly addressing the comment.
- Be polite and constructive.
- Output ONLY the reply text (no preamble, no quotes, no markdown fences).

Target language:
%s

Comment URL:
%s

Pull request title:
%s

Pull request body:
%s

Comment body:
%s

User draft reply (may be incomplete):
%s

Pull request diff:
%s%s
`, string(targetLang), strings.TrimSpace(commentURL), nonEmptyOr(strings.TrimSpace(prTitle), "(empty)"), nonEmptyOr(strings.TrimSpace(prBody), "(empty)"), nonEmptyOr(strings.TrimSpace(commentBody), "(empty)"), strings.TrimSpace(userDraft), nonEmptyOr(diffForPrompt, "(empty)"), truncNote)
}

type replyLanguage string

const (
	replyLangEnglish  replyLanguage = "English"
	replyLangJapanese replyLanguage = "Japanese"
)

func inferReplyLanguage(prTitle, prBody, commentBody string) replyLanguage {
	// Infer language from PR title/body and comment body (ignore user draft).
	// If Japanese-like text dominates, choose Japanese; otherwise default to English.
	ctx := strings.TrimSpace(prTitle) + "\n" + strings.TrimSpace(prBody) + "\n" + strings.TrimSpace(commentBody)
	jp, latin := languageScores(ctx)
	if jp > 0 && jp*4 >= latin { // tolerate some ASCII in Japanese text
		return replyLangJapanese
	}
	return replyLangEnglish
}

func looksLikeLanguage(s string, lang replyLanguage) bool {
	switch lang {
	case replyLangJapanese:
		// Expect at least some Japanese script in output.
		return containsJapaneseRunes(s)
	case replyLangEnglish:
		// English should not contain Japanese scripts.
		return !containsJapaneseRunes(s)
	default:
		return true
	}
}

func buildTranslatePrompt(text string, lang replyLanguage) string {
	return fmt.Sprintf(`You are a translation assistant.

Task:
- Translate the text into %s.
- Keep it concise and clear.
- Preserve technical terms/paths like "settings/:path" and "manage/:path" as-is.
- Output ONLY the translated text (no preamble, no quotes, no markdown fences).

Text:
%s
`, string(lang), strings.TrimSpace(text))
}

func languageScores(s string) (japanese int, latin int) {
	for _, r := range s {
		switch {
		case isJapaneseRune(r):
			japanese++
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
			latin++
		}
	}
	return japanese, latin
}

func containsJapaneseRunes(s string) bool {
	for _, r := range s {
		if isJapaneseRune(r) {
			return true
		}
	}
	return false
}

func isJapaneseRune(r rune) bool {
	// Hiragana, Katakana, CJK Unified Ideographs (common Japanese scripts).
	if r >= 0x3040 && r <= 0x309F { // Hiragana
		return true
	}
	if r >= 0x30A0 && r <= 0x30FF { // Katakana
		return true
	}
	if r >= 0x4E00 && r <= 0x9FFF { // CJK Unified Ideographs
		return true
	}
	return false
}
