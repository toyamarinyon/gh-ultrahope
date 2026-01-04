# gh-ultrahope (GitHub CLI extension)

AI-powered GitHub workflow assistant.

This repository currently ships the `ultrahope pr suggest` command, which suggests a pull request title and body from git commits and diffs using an LLM API.

This repository is a **GitHub CLI extension**. The executable is `gh-ultrahope` and it is invoked as:

```bash
gh ultrahope ...
```

## Install

### From GitHub

```bash
gh extension install toyamarinyon/gh-ultrahope
```

### From local directory (development)

```bash
go build -o gh-ultrahope .
gh extension install .
```

## Usage

```bash
gh ultrahope pr suggest [base-branch]
gh ultrahope pr suggest main --edit
gh ultrahope pr suggest main --create
gh ultrahope config
```

### Options

- `--edit`, `-e`: edit the suggested output in `$EDITOR` before printing
- `--create`, `-c`: create a PR with the suggested title/body (with confirmation)
- `--debug`, `-d`: print extra debug info (never prints secrets)
- `--help`, `-h`: show help

### Base branch default behavior

If you omit `base-branch`, `gh ultrahope pr suggest` will **auto-detect the repository’s default branch** via the GitHub REST API (fallback: `origin/HEAD`). If detection fails, it exits with an error.

Hints if detection fails:

- Set `GH_REPO=OWNER/REPO`
- Or pass a base branch explicitly: `gh ultrahope pr suggest main`

### Environment variables

- `ULTRAHOPE_LLM_PROVIDER` (optional): LLM API type (default: `minimax_anthropic`)
  - `minimax_anthropic`: Anthropic Messages compatible (MiniMax endpoint by default)
  - `anthropic`: Anthropic Messages compatible (Claude)
  - `anthropic_compat`: Anthropic Messages compatible (custom endpoint)
  - `openai_compat`: OpenAI Chat Completions compatible (custom endpoint)
- `ULTRAHOPE_LLM_API_KEY` (required): API key (**never printed**)
- `ULTRAHOPE_LLM_ENDPOINT` (optional): API endpoint override
  - For `anthropic_compat`, set a **BASE URL** (the tool uses `BASE_URL + /v1/messages`)
  - For `openai_compat`, set a **BASE URL that includes `/v1`** (the tool uses `BASE_URL + /chat/completions`)
- `ULTRAHOPE_LLM_MODEL` (optional): model override
- `EDITOR` / `VISUAL` (optional): editor used for `--edit` (default: `vim`)
- `GH_REPO` (optional): `owner/repo` (if set, passed to `gh pr create --repo`)

### Configuration files (non-secret defaults)

This tool keeps the **API key required via env var** (`ULTRAHOPE_LLM_API_KEY`), but supports YAML config files for non-secret defaults.

**Search locations (later wins among config files):**

- Global:
  - If `XDG_CONFIG_HOME` is set: `$XDG_CONFIG_HOME/ultrahope/config.yml`
  - Else: `$HOME/.config/ultrahope/config.yml`
- Repo: `.github/gh-pr-suggest.yml` (or `.yaml`), or `.gh-pr-suggest.yml` (or `.yaml`)
- Optional override: set `ULTRAHOPE_CONFIG=/path/to/config.yml`

**Precedence:**

`env > repo config > global config > built-in defaults`

**Example (`config.yml`):**

```yaml
llm:
  provider: anthropic
  model: claude-sonnet-4-5
  # endpoint is optional; defaults per provider
  # endpoint: https://api.anthropic.com/v1/messages

create:
  draft: true
  skip_confirm: false
```

### Auto-init wizard (TTY only)

If no config file is found and `stdin` is a TTY, `gh ultrahope pr suggest` will start an **English interactive wizard** and create the global config file for you.

### Examples

#### Default (MiniMax Anthropic-compatible)

```bash
export ULTRAHOPE_LLM_API_KEY="..."
gh ultrahope pr suggest --debug
```

#### Claude (Anthropic Messages API)

```bash
export ULTRAHOPE_LLM_PROVIDER="anthropic"
export ULTRAHOPE_LLM_API_KEY="$ANTHROPIC_API_KEY"
export ULTRAHOPE_LLM_MODEL="claude-sonnet-4-5"
# optional:
# export ULTRAHOPE_LLM_ENDPOINT="https://api.anthropic.com/v1/messages"
gh ultrahope pr suggest
```

#### Claude-compatible (Anthropic Messages compatible)

```bash
export ULTRAHOPE_LLM_PROVIDER="anthropic_compat"
export ULTRAHOPE_LLM_API_KEY="..."
export ULTRAHOPE_LLM_ENDPOINT="https://your-llm.example/v1/messages"
export ULTRAHOPE_LLM_MODEL="..."
gh ultrahope pr suggest
```

#### OpenAI-compatible (Chat Completions)

```bash
export ULTRAHOPE_LLM_PROVIDER="openai_compat"
export ULTRAHOPE_LLM_API_KEY="..."
# optional:
# export ULTRAHOPE_LLM_ENDPOINT="https://api.openai.com/v1/chat/completions"
export ULTRAHOPE_LLM_MODEL="gpt-4o-mini"
gh ultrahope pr suggest
```

