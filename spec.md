# gh-pr-suggest CLI Specification

## Overview

`gh-pr-suggest` is a CLI tool that suggests pull request title and body from git commits and diffs using an LLM API. It is written in TypeScript (Deno) and being reimplemented in Go.

## Command Line Interface

### Usage

```bash
gh-pr-suggest [base-branch] [OPTIONS]
gh-pr-suggest main --edit
gh-pr-suggest main --create
```

### Arguments

| Argument | Description | Default |
|----------|-------------|---------|
| `base-branch` | Base branch/commit to compare against | `main` |

### Options

| Option | Short | Description |
|--------|-------|-------------|
| `--edit` | `-e` | Edit the suggested output in $EDITOR before printing |
| `--create` | `-c` | Create a PR with the suggested title/body (with confirmation) |
| `--debug` | `-d` | Print extra debug information (API endpoint, model, etc.) |
| `--help` | `-h` | Show help message |

### Environment Variables

| Variable | Required | Description | Default |
|----------|----------|-------------|---------|
| `MINIMAX_CP_KEY` | Yes | API key for the LLM service | - |
| `MINIMAX_ENDPOINT` | No | API endpoint URL | `https://api.minimax.io/anthropic/v1/messages` |
| `MINIMAX_MODEL` | No | LLM model name | `MiniMax-M2.1` |
| `EDITOR` | No* | Editor to use for editing (--edit mode) | `vim` |
| `VISUAL` | No* | Alternative editor (fallback) | `vim` |
| `GH_REPO` | No* | Repository in `owner/repo` format (auto-detected by `gh`) | auto-detected |

*Required only when using the respective features.

## Core Features

### 1. Git Integration

The tool retrieves the following information from git:

- **Current Branch**: Using `git rev-parse --abbrev-ref HEAD`
- **Commit Log**: Using `git log --oneline ${base}..HEAD`
- **Diff Summary**: Using `git diff ${base}...HEAD --stat`
- **Detailed Diff**: Using `git diff ${base}...HEAD`
- **Changed Files**: Using `git diff ${base}...HEAD --name-only`
- **Merge Base Validation**: Verifies that a merge base exists between base and HEAD

### 2. Commit Message Analysis

The tool extracts topics from commit messages using pattern matching:

```typescript
// Detects patterns like:
// "feat(auth): add login" -> topic: "auth"
// "fix: resolve bug" -> topic: null (no scope)
// "docs: update README" -> topic: null (no scope)
```

Pattern: `^(\w+)\s*\(([^)]+)\):` or `^(\w+):\s+(.+)`

### 3. LLM API Integration

#### Request Format

```json
{
  "model": "MiniMax-M2.1",
  "max_tokens": 2048,
  "messages": [
    {
      "role": "user",
      "content": "<constructed prompt>"
    }
  ]
}
```

#### Request Headers

```
Content-Type: application/json
x-api-key: <MINIMAX_CP_KEY>
anthropic-version: 2023-06-01
```

#### Prompt Structure

The tool constructs a detailed prompt with:

1. **Task Description**: Generate PR title and body
2. **Output Format**:
   ```
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
   ```
3. **Context**: Current branch, base branch, commit log, changed files, topics, diff summary, full diff

### 4. Spinner Indicator

During API calls, an animated spinner is displayed:

```typescript
const frames = ["⠾", "⠽", "⠻", "⠧", "⠟", "⠯", "⠷"];
```

**Technical Details:**
- Uses `AbortController` for clean cancellation
- Spins at ~10fps (100ms interval)
- Writes to stderr to avoid polluting stdout
- Clears itself on completion or interruption
- Spin on SIGINT/SIGTERM for graceful exit

**Implementation Pattern:**
```typescript
// Start spinner
const spinner = new AbortController();
const signal = spinner.signal;

(async () => {
  while (!signal.aborted) {
    await new Promise(resolve => setTimeout(resolve, 100));
    if (!signal.aborted) {
      Deno.stderr.writeSync(new TextEncoder().encode(`\r${frames[i]}${message}`));
      i = (i + 1) % frames.length;
    }
  }
})();

// Stop spinner
spinner.abort();
Deno.stderr.writeSync(new TextEncoder().encode(`\r${" ".repeat(40)}\r`));
```

### 5. Output Parsing

The suggested output is parsed using regex:

```typescript
// Extract title
const titleMatch = output.match(/^TITLE:\s*(.+)$/m);

// Extract body (everything after BODY: until --- or end)
const bodyMatch = output.match(/^BODY:\s*\n([\s\S]*?)(?=\n---\n|$)/m);
```

### 6. Editor Integration (--edit mode)

When `--edit` flag is used:

1. Content is written to a temporary file (`/tmp/gh-suggest-pr-${timestamp}.txt`)
2. `$EDITOR` or `$VISUAL` or `vim` is launched with the file
3. After editor closes, the file is read and the content is returned
4. Temporary file is cleaned up

### 7. PR Creation (--create mode)

When `--create` flag is used:

1. User is prompted for confirmation: `Create pull request with this title/body? [y/N] `
2. If confirmed, body is written to a temporary file (`/tmp/gh-pr-body-${timestamp}.txt`)
3. `gh pr create` is called with:
   - `--title <title>`
   - `--body-file <tmp-path>`
   - `--head <current-branch>`
   - `--base <base-branch>`
   - `--repo <GH_REPO>` (if set)
4. PR URL is extracted from output and displayed
5. Temporary file is cleaned up

### 8. Signal Handling

The tool registers handlers for `SIGINT` and `SIGTERM` to:

- Stop the spinner gracefully
- Exit with code 130 (standard for interrupted processes)

```typescript
const handler = () => {
  stopSpinner();
  Deno.exit(130);
};

Deno.addSignalListener("SIGINT", handler);
Deno.addSignalListener("SIGTERM", handler);
```

## Error Handling

| Error Type | Handling |
|------------|----------|
| Missing API key | `Missing MINIMAX_CP_KEY env var.` → exit(1) |
| API error | Print error message → exit(1) |
| Unknown argument | `Unknown argument: <arg>` → exit(2) |
| No merge base | Error message → exit(1) |
| No commits found | Error message → exit(1) |
| Git command failure | `git <cmd> failed: <stderr>` → throw |

## Output Format

### Help Output

```
gh-suggest-pull-title-and-body

Suggest a pull request title and body from git commits and diffs.

USAGE:
  gh-suggest-pull-title-and-body [base-branch]
  gh-suggest-pull-title-and-body main --edit
  gh-suggest-pull-title-and-body main --create

OPTIONS:
  base-branch  Base branch/commit (default: main)
  --edit       Edit the output before printing
  --create     Create PR with suggested title/body (with confirmation)
  --debug      Print extra debug info
  --help       Show help
```

### Suggested Output Example

```
TITLE: feat(auth): Implement OAuth2 login flow

BODY:
## Summary
This PR adds OAuth2 authentication support allowing users to sign in with Google and GitHub. The implementation includes a new auth service, OAuth provider abstraction, and updated user sessions.

## Changes
- Add OAuth2Service with Google and GitHub providers
- Implement OAuth callback handler and token exchange
- Create new session management with secure cookies
- Update user model to store OAuth provider info
- Add configuration for OAuth client IDs/secrets

## Testing
- Unit tests for OAuthService (85% coverage)
- Integration tests with mock OAuth providers
- Manual testing with test Google/GitHub accounts
```

## Go Implementation Considerations

### Dependencies

- Standard library only (preferred) or minimal external deps
- For spinner: `github.com/buger/goterm` or custom implementation
- For API calls: `net/http`, `encoding/json`
- For git commands: `os/exec`
- For editor: `os/exec` with temp file
- For signal handling: `os/signal`, `context`

### Key Structures

```go
type Config struct {
    BaseBranch string
    Edit       bool
    Create     bool
    Debug      bool
    Help       bool
}

type GitInfo struct {
    CurrentBranch  string
    BaseBranch     string
    CommitLog      string
    DiffSummary    string
    DetailedDiff   string
    ChangedFiles   []string
    Topics         []string
}

type LLMResponse struct {
    Title string
    Body  string
}
```

### Spinner Implementation (Go)

```go
type Spinner struct {
    frames []string
    index  int
    stopCh chan struct{}
    w      io.Writer
}

func (s *Spinner) Start(message string) {
    s.stopCh = make(chan struct{})
    go func() {
        ticker := time.NewTicker(100 * time.Millisecond)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                fmt.Fprintf(s.w, "\r%s%s", s.frames[s.index], message)
                s.index = (s.index + 1) % len(s.frames)
            case <-s.stopCh:
                fmt.Fprintf(s.w, "\r%s\r", strings.Repeat(" ", 40))
                return
            }
        }
    }()
}

func (s *Spinner) Stop() {
    close(s.stopCh)
}
```

### Signal Handling (Go)

```go
func setupSignalHandlers(spinner *Spinner) {
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
    
    go func() {
        <-sigCh
        spinner.Stop()
        os.Exit(130)
    }()
}
```

### Command Parsing (Go)

Use `flag` package or `github.com/urfave/cli/v2` for option parsing:

```go
var (
    baseBranch string
    edit       bool
    create     bool
    debug      bool
    help       bool
)

flag.StringVar(&baseBranch, "base", "main", "Base branch/commit")
flag.BoolVar(&edit, "edit", false, "Edit output before printing")
flag.BoolVar(&create, "create", false, "Create PR with suggested title/body")
flag.BoolVar(&debug, "debug", false, "Print debug info")
flag.BoolVar(&help, "help", false, "Show help")

flag.Parse()
```

### API Call (Go)

```go
func callLLM(prompt string, debug bool) (string, error) {
    endpoint := os.Getenv("MINIMAX_ENDPOINT")
    if endpoint == "" {
        endpoint = "https://api.minimax.io/anthropic/v1/messages"
    }
    model := os.Getenv("MINIMAX_MODEL")
    if model == "" {
        model = "MiniMax-M2.1"
    }
    apiKey := os.Getenv("MINIMAX_CP_KEY")
    if apiKey == "" {
        return "", fmt.Errorf("missing MINIMAX_CP_KEY env var")
    }

    payload := map[string]interface{}{
        "model":      model,
        "max_tokens": 2048,
        "messages": []map[string]string{
            {"role": "user", "content": prompt},
        },
    }

    body, err := json.Marshal(payload)
    if err != nil {
        return "", err
    }

    req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("x-api-key", apiKey)
    req.Header.Set("anthropic-version", "2023-06-01")

    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    var result map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&result)

    // Extract text from response...
    return "", nil
}
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Runtime error (API, git, etc.) |
| 2 | Invalid arguments |
| 130 | Interrupted (SIGINT/SIGTERM) |

## File Structure (Go)

```
gh-pr-suggest/
├── main.go           # Entry point, CLI parsing
├── cmd/
│   └── root.go       # Cobra/urfave CLI root command
├── git/
│   └── git.go        # Git command wrappers
├── llm/
│   └── client.go     # LLM API client
├── spinner/
│   └── spinner.go    # Spinner implementation
├── editor/
│   └── editor.go     # Editor integration
├── pr/
│   └── create.go     # PR creation logic
└── config/
    └── config.go     # Configuration handling
