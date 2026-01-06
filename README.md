# gh-ultrahope (GitHub CLI extension)

AI-powered GitHub workflow assistant.

This repository ships GitHub PR helpers:

- `ultrahope pr create`: create a pull request with an AI-generated title and body
- `ultrahope pr update`: update the pull request for the current branch (or propose creating one)
- `ultrahope pr reply`: draft and post a reply to a PR comment/review comment from a pasted URL

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
# With mise (recommended)
mise run install

# Or manually
go build -o gh-ultrahope .
gh extension install .
```

## Usage

```bash
gh ultrahope pr create [base-branch]
gh ultrahope pr create main --edit
gh ultrahope pr create main --dry-run
gh ultrahope pr create main --draft
gh ultrahope pr update [base-branch]
gh ultrahope pr update main --dry-run
gh ultrahope pr reply
gh ultrahope config
```

By default, `gh ultrahope pr create` prints the suggested title/body, then asks for confirmation and creates a pull request.
Use `--dry-run` to only print the suggestion (no confirmation prompt, no PR creation).

### Options

- `--edit`, `-e`: edit the generated output in `$EDITOR` before printing
- `--dry-run`, `-n`: print suggested title/body only (do not create/update PR)
- `--draft`: mark pull request as a draft (overrides config `create.draft`; use `--draft=false` to force non-draft)
- `--debug`, `-d`: print extra debug info (never prints secrets)
- `--help`, `-h`: show help

### `pr update` behavior

`gh ultrahope pr update` generates a new title/body (like `pr create`), then:

- If an **open pull request exists for the current branch**, it asks for confirmation and updates the PR title/body.
- If **no pull request exists**, it proposes creating a new PR (`Create one now? [y/N]`).
- If the existing PR is a **draft**, it offers publishing it (`Publish it now? [y/N]`).

Note: `--draft` is only used when `pr update` ends up creating a new PR.

### `pr reply` behavior

`gh ultrahope pr reply` is an interactive command:

- Paste a GitHub comment URL (see supported URL formats below)
- Ultrahope fetches PR title/body/diff and the comment body
- Enter a draft reply (end with an empty line)
- It generates a concise reply in the same language as the PR/comment, then asks:
  - `Post this reply now? [y/N/e]` (`e` opens `$EDITOR` to edit before posting)

Supported URL formats:

- Review comments: `.../pull/<number>#discussion_r<id>` (or `#r<id>`)
- Issue comments on PR: `.../pull/<number>#issuecomment-<id>`

### Base branch default behavior

If you omit `base-branch`, `gh ultrahope pr create` will auto-detect the base branch using a two-step process:

1. **Stacked branch detection (preferred):** Walks up to 200 commits from HEAD to find the first commit that has a remote tracking branch (`origin/*`) pointing to it. This enables seamless workflows with stacked PRs—if you're on `feature-b` which branches off `feature-a`, it will automatically detect `feature-a` as the base, not the repo default branch.
2. **Repository default branch (fallback):** If no stacked base is found, queries the GitHub REST API for the repository's default branch (fallback: `origin/HEAD` symbolic ref).

If both detection methods fail, the command exits with an error.

Hints if detection fails:

- Set `GH_REPO=OWNER/REPO`
- Or pass a base branch explicitly: `gh ultrahope pr create main`

### Environment variables

- `ULTRAHOPE_LLM_API_KEY` (required): LLM API key (**never printed**)
- `ULTRAHOPE_CONFIG` (optional): config file path override (`/path/to/config.yml`)
- `EDITOR` / `VISUAL` (optional): editor used for `--edit` (default: `vim`)
- `GH_REPO` (optional): `owner/repo` (if set, passed to `gh pr create --repo`)

### Configuration files (non-secret defaults)

This tool keeps the **API key required via env var** (`ULTRAHOPE_LLM_API_KEY`), but supports YAML config files for non-secret defaults such as **provider / endpoint / model** and PR creation defaults.

**Search locations (later wins among config files):**

- Global:
  - If `XDG_CONFIG_HOME` is set: `$XDG_CONFIG_HOME/ultrahope/config.yml`
  - Else: `$HOME/.config/ultrahope/config.yml`
- Repo: `.github/ultrahope.yml` (or `.yaml`), or `.ultrahope.yml` (or `.yaml`)
- Optional override: set `ULTRAHOPE_CONFIG=/path/to/config.yml`

**Precedence:**

- For non-secret settings (provider/model/endpoint, create options): `repo config > global config > built-in defaults`
- `ULTRAHOPE_CONFIG` can override which config file is loaded
- The API key is **env only**: `ULTRAHOPE_LLM_API_KEY`

**Example (`config.yml`):**

```yaml
llm:
  provider: minimax_anthropic # or: anthropic / anthropic_compat / openai_compat
  model: MiniMax-M2.1         # defaults exist per provider; override as needed
  # endpoint is optional; defaults per provider; set a base URL for compat providers
  # endpoint: https://api.minimax.io/anthropic

create:
  draft: true
  skip_confirm: false
```

### Auto-init wizard (TTY only)

If no config file is found and `stdin` is a TTY, `gh ultrahope pr create` will start an **English interactive wizard** and create the global config file for you.

### Examples

#### Default (MiniMax Anthropic-compatible)

```bash
export ULTRAHOPE_LLM_API_KEY="..."
gh ultrahope pr create
```

#### Update PR for current branch

```bash
export ULTRAHOPE_LLM_API_KEY="..."
gh ultrahope pr update
```

#### Reply to a PR comment

```bash
export ULTRAHOPE_LLM_API_KEY="..."
gh ultrahope pr reply
# paste comment URL when prompted
```

#### Claude (Anthropic Messages API)

```bash
cat > ./ultrahope.yml <<'YAML'
llm:
  provider: anthropic
  model: claude-sonnet-4-5
YAML

export ULTRAHOPE_CONFIG="$PWD/ultrahope.yml"
export ULTRAHOPE_LLM_API_KEY="$ANTHROPIC_API_KEY"
gh ultrahope pr create
```

#### Claude-compatible (Anthropic Messages compatible)

```bash
cat > ./ultrahope.yml <<'YAML'
llm:
  provider: anthropic_compat
  endpoint: https://your-llm.example # base URL (the tool uses BASE_URL + /v1/messages)
  model: ...
YAML

export ULTRAHOPE_CONFIG="$PWD/ultrahope.yml"
export ULTRAHOPE_LLM_API_KEY="..."
gh ultrahope pr create
```

#### OpenAI-compatible (Chat Completions)

```bash
cat > ./ultrahope.yml <<'YAML'
llm:
  provider: openai_compat
  endpoint: https://api.openai.com/v1 # base URL including /v1 (the tool uses BASE_URL + /chat/completions)
  model: gpt-4o-mini
YAML

export ULTRAHOPE_CONFIG="$PWD/ultrahope.yml"
export ULTRAHOPE_LLM_API_KEY="..."
gh ultrahope pr create
```
