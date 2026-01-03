- Goal (incl. success criteria):
  - Maintain this Continuity Ledger per `AGENTS.md` (update at start of each assistant turn and immediately after each file edit).
  - Refactor the GitHub CLI extension from `gh-pr-suggest` into a Cobra-based suite invoked as `gh ultrahope pr suggest [base-branch]`.
  - Success criteria:
    - `go build` produces `gh-ultrahope`.
    - `gh ultrahope pr suggest` preserves existing behavior (flags, env vars, config loading, base branch auto-detection, debug/secret-safe logging, LLM providers, PR creation flow).
    - CLI parsing uses `spf13/cobra` with command tree: `ultrahope` → `pr` → `suggest`.

- Constraints/Assumptions:
  - Do not rewrite business logic; change primarily CLI structure/command parsing/naming.
  - Use `spf13/cobra` for parsing and help/usage output.
  - No backward-compat wrapper binary (`gh-pr-suggest`) will be kept (user choice).
  - Rename Go module path to `github.com/toyamarinyon/gh-ultrahope` (user choice).

- Key decisions:
  - Switch env var prefix from `GH_PR_SUGGEST_*` to `ULTRAHOPE_*` (brand unification). No backward compatibility.
  - Extract existing `run()` logic into `internal/prsuggest` so Cobra handlers are thin glue.
  - Map CLI errors to existing exit codes (0/1/2/130) as closely as practical with Cobra.
  - Release workflow will NOT set `go_binary_name` (keep relying on default behavior); user may adjust later if needed.

- State:
  - Repo: `/Users/satoshi/repo/toyamarinyon/gh-pr-suggest` (git repo; will become ultrahope naming).
  - Current implementation: single `main.go` with manual parsing and rich logic (LLM/config/PR create).
  - Command target: `gh ultrahope pr suggest [base-branch]` implemented via Cobra.
  - Go module path updated to `github.com/toyamarinyon/gh-ultrahope`.
  - Added Cobra dependency (`github.com/spf13/cobra`) via `go get`; `go.mod`/`go.sum` updated.
  - Extracted existing implementation into `internal/prsuggest/prsuggest.go` (new package entrypoint: `prsuggest.Run(opts, in, out, errOut)`).
  - Moved config loader/schema into `internal/prsuggest/config.go` (preserves current file locations/precedence).
  - Env vars use `ULTRAHOPE_*` only.
  - Added Cobra root command scaffold in `cmd/root.go` with exit-code mapping.
  - Disabled Cobra default `completion` subcommand to keep help output minimal/GH-like.
  - Added `ultrahope pr` namespace command in `cmd/pr.go`.
  - Added `ultrahope pr suggest` Cobra command in `cmd/pr_suggest.go`, wiring flags/args into `internal/prsuggest`.
  - Replaced root `main.go` with a Cobra bootstrap that exits with `cmd.Execute()` return code.
  - Removed obsolete root `config.go` (moved to `internal/prsuggest/config.go`).
  - README updated to new binary name and command path (`gh ultrahope pr suggest`).
  - `go mod tidy` completed; direct deps now include `cobra` and `yaml.v3` (tidy’d).
  - Release workflow: unchanged (no `go_binary_name`).

- Done:
  - Existing `gh-pr-suggest` implementation is functional; refactor to `ultrahope` is the active task.
  - Ran `gofmt` over `main.go`, `cmd/*.go`, `internal/prsuggest/*.go`.
  - Switched LLM env var names in `internal/prsuggest/prsuggest.go` to `ULTRAHOPE_*` only (no legacy fallback).
  - Updated `README.md` to document `ULTRAHOPE_*` env vars (and `ULTRAHOPE_CONFIG`).

- Now:
  - Refactor completed; only optional workflow/repo-rename polish remains outside this session.

- Next:
  - Optional (user-owned): rename GitHub repo to `gh-ultrahope` and verify release workflow artifact naming still matches GitHub CLI extension requirements.

- Open questions (UNCONFIRMED if needed):
  - None.

- Working set (files/ids/commands):
  - `main.go`, `config.go`
  - `cmd/root.go`, `cmd/pr.go`, `cmd/pr_suggest.go`
  - `internal/prsuggest/*`
