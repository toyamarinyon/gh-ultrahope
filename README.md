# gh-pr-suggest (GitHub CLI extension)

Suggest a pull request title and body from git commits and diffs using an LLM API.

This repository is a **GitHub CLI extension**. The executable is `gh-pr-suggest` and it is invoked as:

```bash
gh pr-suggest ...
```

## Install

### From GitHub

```bash
gh extension install toyamarinyon/gh-pr-suggest
```

### From local directory (development)

```bash
go build -o gh-pr-suggest .
gh extension install .
```

## Usage

```bash
gh pr-suggest [base-branch]
gh pr-suggest main --edit
gh pr-suggest main --create
```

### Options

- `--edit`, `-e`: edit the suggested output in `$EDITOR` before printing
- `--create`, `-c`: create a PR with the suggested title/body (with confirmation)
- `--debug`, `-d`: print extra debug info (never prints secrets)
- `--help`, `-h`: show help

### Base branch default behavior

If you omit `base-branch`, the tool will **auto-detect the repository’s default branch** via the GitHub REST API (fallback: `origin/HEAD`). If detection fails, it falls back to `main`.

### Environment variables

- `GH_PR_SUGGEST_LLM_PROVIDER` (optional): LLM API type (default: `minimax_anthropic`)
  - `minimax_anthropic`: Anthropic Messages compatible (MiniMax endpoint by default)
  - `anthropic`: Anthropic Messages compatible (Claude)
  - `anthropic_compat`: Anthropic Messages compatible (custom endpoint)
  - `openai_compat`: OpenAI Chat Completions compatible (custom endpoint)
- `GH_PR_SUGGEST_LLM_API_KEY` (required): API key (**never printed**)
- `GH_PR_SUGGEST_LLM_ENDPOINT` (optional): API endpoint override
- `GH_PR_SUGGEST_LLM_MODEL` (optional): model override
- `EDITOR` / `VISUAL` (optional): editor used for `--edit` (default: `vim`)
- `GH_REPO` (optional): `owner/repo` (if set, passed to `gh pr create --repo`)

### Examples

#### Default (MiniMax Anthropic-compatible)

```bash
export GH_PR_SUGGEST_LLM_API_KEY="..."
gh pr-suggest --debug
```

#### Claude (Anthropic Messages API)

```bash
export GH_PR_SUGGEST_LLM_PROVIDER="anthropic"
export GH_PR_SUGGEST_LLM_API_KEY="$ANTHROPIC_API_KEY"
export GH_PR_SUGGEST_LLM_MODEL="claude-sonnet-4-5"
# optional:
# export GH_PR_SUGGEST_LLM_ENDPOINT="https://api.anthropic.com/v1/messages"
gh pr-suggest
```

#### Claude-compatible (Anthropic Messages compatible)

```bash
export GH_PR_SUGGEST_LLM_PROVIDER="anthropic_compat"
export GH_PR_SUGGEST_LLM_API_KEY="..."
export GH_PR_SUGGEST_LLM_ENDPOINT="https://your-llm.example/v1/messages"
export GH_PR_SUGGEST_LLM_MODEL="..."
gh pr-suggest
```

#### OpenAI-compatible (Chat Completions)

```bash
export GH_PR_SUGGEST_LLM_PROVIDER="openai_compat"
export GH_PR_SUGGEST_LLM_API_KEY="..."
# optional:
# export GH_PR_SUGGEST_LLM_ENDPOINT="https://api.openai.com/v1/chat/completions"
export GH_PR_SUGGEST_LLM_MODEL="gpt-4o-mini"
gh pr-suggest
```

## Make it feel like `gh pr suggest ...`

GitHub CLI extensions cannot be true nested subcommands under `gh pr`, but you can get the UX with an alias:

```bash
gh alias set "pr suggest" "pr-suggest"
```

Then you can run:

```bash
gh pr suggest main --edit
```

