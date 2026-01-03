# GitHub CLI Extension Guide (Go / precompiled) for `gh-pr-suggest`

This repository is intended to become a **GitHub CLI extension** implemented in **Go** and distributed as **precompiled binaries**.

Primary references:
- GitHub Docs: `https://docs.github.com/ja/github-cli/github-cli/creating-github-cli-extensions`
- GitHub Docs: `https://docs.github.com/ja/github-cli/github-cli/using-github-cli-extensions`

---

## What “GitHub CLI extension” means

- The extension repository **must** be named with the `gh-` prefix.
  - This repo is `gh-pr-suggest`.
- The command users run is the repo name **minus** `gh-`.
  - Users will run: `gh pr-suggest ...`

In other words, the CLI UX described in `spec.md` should be understood as:

```bash
# spec.md shows: gh-pr-suggest ...
# as an extension it is invoked as:
gh pr-suggest [base-branch] [OPTIONS]
```

---

## Extension requirements (repo + binaries)

### Repository naming

- The repository name **must start with** `gh-`.
- The “extension name” is whatever comes after `gh-`.

### Precompiled release assets (most important detail)

When distributing a precompiled extension, you attach binaries to a GitHub Release.

- **Each asset filename must include an OS/architecture suffix**:
  - `gh-EXTENSION-NAME-<os>-<arch>[.exe]`
- **Windows assets must end in** `.exe`. Other OSes do not require an extension.

Examples (from the official docs’ naming convention):

```text
gh-pr-suggest-windows-amd64.exe
gh-pr-suggest-linux-amd64
gh-pr-suggest-darwin-amd64
```

---

## Recommended scaffolding (for new extensions)

If you were starting from scratch, GitHub CLI can scaffold a Go precompiled extension:

```bash
gh extension create --precompiled=go EXTENSION-NAME
```

Notes:
- `EXTENSION-NAME` is the extension command name (without `gh-`), e.g. `pr-suggest`.
- The scaffold includes Go project files and workflow scaffolding.

This repository already exists, so treat that command as the reference for how GitHub CLI expects a Go precompiled extension to be shaped.

---

## Local development workflow (build → install → run)

### Repository state notes (this repo)

- This repository currently contains a compiled binary at `./gh-pr-suggest` (e.g. a macOS Mach-O build).
  - Treat it as a **local build artifact**.
  - **Do not commit** compiled binaries to version control; publish them as GitHub Release assets instead.
  - Recommended: add `gh-pr-suggest` (and any `gh-pr-suggest-*` build outputs) to `.gitignore`.
- The module already depends on `github.com/cli/go-gh/v2` (recommended for Go-based extensions).

### 1) Build a local binary

From the repository root:

```bash
go build -o gh-pr-suggest .
```

That produces a local executable named `gh-pr-suggest` (matching the repository name), which GitHub CLI can treat as the extension executable.

### 2) Install the extension from the local directory

```bash
gh extension install .
```

### 3) Run it

```bash
gh pr-suggest --help
gh pr-suggest main --edit
gh pr-suggest main --create
```

### 4) Manage extensions

```bash
gh extension list
gh extension upgrade pr-suggest
gh extension upgrade --all
gh extension remove pr-suggest
```

---

## Project-specific implementation contract (from `spec.md`)

Implement the following user-facing interface (invoked as `gh pr-suggest ...`):

### Arguments
- `base-branch`: base branch/commit to compare against (default: `main`)

### Options
- `--edit` / `-e`: open the suggested output in `$EDITOR` before printing
- `--create` / `-c`: create a PR using `gh pr create` after confirmation
- `--debug` / `-d`: print extra debug information (but never secrets)
- `--help` / `-h`: show help

### Environment variables
- `MINIMAX_CP_KEY` (required): LLM API key (**must never be logged**)
- `MINIMAX_ENDPOINT` (optional): default `https://api.minimax.io/anthropic/v1/messages`
- `MINIMAX_MODEL` (optional): default `MiniMax-M2.1`
- `EDITOR` / `VISUAL` (optional): editor for `--edit`
- `GH_REPO` (optional): repository in `owner/repo` format (if not set, rely on `gh` auto-detection)

### Exit codes
- `0`: success
- `1`: runtime error (git/API/etc.)
- `2`: invalid arguments
- `130`: interrupted (SIGINT/SIGTERM)

---

## Using GitHub CLI features from Go

For Go-based extensions, prefer using the official `go-gh` library when it helps:
- It exposes pieces of `gh` functionality for use in extensions (auth, API clients, etc.).
- This repo already depends on `github.com/cli/go-gh/v2`.

Use cases:
- Calling GitHub REST/GraphQL APIs using the same authentication context as `gh`
- Getting repo/host context in a way consistent with `gh`

---

## Non-interactive behavior (avoid prompts)

Some `gh` commands can prompt for user input. In an extension, prompts are often undesirable.

Guideline:
- When invoking `gh` core commands (e.g. `gh pr create`), **pass required fields explicitly** so the command can run non-interactively.

For this project’s `--create` mode, that typically means:
- Always provide `--title ...`
- Provide PR body via `--body-file ...` (recommended for multi-line)
- Provide `--head ...` and `--base ...`
- Provide `--repo ...` if `GH_REPO` is set or auto-detection is ambiguous

---

## Release strategy (manual)

Create a Release with assets named per the required convention.

Example flow:

```bash
# tag + push
git tag v1.0.0
git push origin v1.0.0

# cross-compile assets (adjust OS/ARCH as needed)
GOOS=windows GOARCH=amd64 go build -o gh-pr-suggest-windows-amd64.exe .
GOOS=linux   GOARCH=amd64 go build -o gh-pr-suggest-linux-amd64 .
GOOS=darwin  GOARCH=amd64 go build -o gh-pr-suggest-darwin-amd64 .

# create GitHub Release with attached assets
gh release create v1.0.0 ./*amd64*
```

Important:
- Do **not** commit build artifacts to git.
- If you support Windows, ensure the Windows asset ends with `.exe`.

---

## Release automation (recommended)

The official docs recommend considering the GitHub Action:
- `https://github.com/cli/gh-extension-precompile`

It can:
- Automatically produce cross-compiled Go binaries for your extension
- Provide build scaffolding for non-Go precompiled extensions

Recommended approach for this repo:
- Add a workflow that runs on tags (e.g. `v*`)
- Produces the required release asset names automatically

---

## Practical checklist (before publishing)

- [ ] Repo name starts with `gh-` (this repo: `gh-pr-suggest`)
- [ ] Running command is `gh pr-suggest ...`
- [ ] `--help` output and flag behavior match `spec.md`
- [ ] `MINIMAX_CP_KEY` is required and never printed (even in `--debug`)
- [ ] `--create` runs without unexpected prompts (explicit `gh pr create` args)
- [ ] Release assets are attached and named `gh-pr-suggest-<os>-<arch>[.exe]`

