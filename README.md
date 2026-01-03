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

If you omit `base-branch`, the tool will **auto-detect the repository’s default branch** via `gh repo view` (fallback: `origin/HEAD`). If detection fails, it falls back to `main`.

### Environment variables

- `MINIMAX_CP_KEY` (required): API key (**never printed**)
- `MINIMAX_ENDPOINT` (optional): default `https://api.minimax.io/anthropic/v1/messages`
- `MINIMAX_MODEL` (optional): default `MiniMax-M2.1`
- `EDITOR` / `VISUAL` (optional): editor used for `--edit` (default: `vim`)
- `GH_REPO` (optional): `owner/repo` (if set, passed to `gh pr create --repo`)

## Make it feel like `gh pr suggest ...`

GitHub CLI extensions cannot be true nested subcommands under `gh pr`, but you can get the UX with an alias:

```bash
gh alias set "pr suggest" "pr-suggest"
```

Then you can run:

```bash
gh pr suggest main --edit
```

