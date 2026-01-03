- Goal (incl. success criteria):
  - Maintain a Continuity Ledger in this workspace and follow `AGENTS.md` instructions.
  - Success:
    - `CONTINUITY.md` exists as the canonical session briefing (compaction-safe).
    - It is updated at the start of each assistant turn and immediately after any file edit.
    - Each assistant reply begins with a brief “Ledger Snapshot” (Goal + Now/Next + Open Questions).
  - Reimplement `spec.md` as a Go GitHub CLI extension for suggesting PR title/body.
  - Success (feature):
    - `gh pr-suggest [base-branch] [--edit|--create|--debug|--help]` works per `spec.md`.
    - Document alias so users can run `gh pr suggest ...` via `gh alias set "pr suggest" "pr-suggest"`.
    - `MINIMAX_CP_KEY` is required and never logged (even with `--debug`).
    - Spinner + SIGINT/SIGTERM handling exits with 130.

- Constraints/Assumptions:
  - Follow `AGENTS.md`: read/update `CONTINUITY.md` at the start of every assistant turn; update it immediately after every file edit.
  - Keep ledger short, factual, and compaction-safe; mark uncertainty as UNCONFIRMED.
  - Implement `spec.md` as source of truth; TypeScript sample is reference for API call behavior.
  - As an extension, executable is `gh-pr-suggest` and invocation is `gh pr-suggest ...`.

- Key decisions:
  - Create `CONTINUITY.md` to satisfy `AGENTS.md` and use it as the single canonical session briefing designed to survive context compaction.
  - `gh pr suggest` cannot be implemented as a true nested subcommand via extensions; provide an alias-based UX instead.

- State:
  - Repo: `/Users/satoshi/repo/toyamarinyon/gh-pr-suggest` (git repo).
  - Current branch: `master` (from initial snapshot).
  - Working tree (current): UNCONFIRMED (not recently re-checked via git status).
  - Local build cache dirs created: `.gocache/`, `.gotmp/` (user plans to adjust `.gitignore`).
  - Purpose of `CONTINUITY.md`: persist intent/constraints/decisions/state across context compaction (so the assistant does not rely on earlier chat text unless reflected here).
  - `main.go` now implements the `spec.md` flow end-to-end (git→prompt→LLM→parse/edit/create).

- Done:
  - Read `AGENTS.md`.
  - Created `CONTINUITY.md` (baseline ledger) because it did not exist; this is required to follow `AGENTS.md`.
  - Read `spec.md` and `EXTENSION_GUIDE.md`; confirmed extension invocation constraints.
  - Implemented initial `main.go` scaffold: `-e/-c/-d/-h` parsing + `MINIMAX_*`/editor env loading.
  - Implemented git command wrappers and data collection (branch, merge-base validation, commit log, diff summary, full diff, changed files).
  - Fixed `main.go` to compile cleanly (removed unused imports while incremental implementation proceeds).
  - Implemented prompt construction, topic extraction, MiniMax API call (anthropic-compatible), and spinner output to stderr.
  - Fixed spinner type definition (removed duplicate placeholder declaration).
  - Implemented output parsing (TITLE/BODY), `--edit` editor loop, confirmation prompt, and `--create` flow via `gh pr create`.
  - Added `README.md` with install/usage/env vars and alias instructions for `gh pr suggest`.
  - Fixed BODY parsing to avoid unsupported regexp lookahead in Go (RE2).
  - Verified `--help` output via `go run . --help`.
  - Confirmed release workflow uses `cli/gh-extension-precompile@v2` and follows `gh-pr-suggest-<os>-<arch>[.exe]` asset naming convention.

- Now:
  - Remaining housekeeping only (e.g. ignore local build/cache dirs as desired).

- Next:
  - Optionally add `.gocache/`, `.gotmp/` (and local build outputs) to `.gitignore` (user owned).

- Open questions (UNCONFIRMED if needed):
  - None.

- Working set (files/ids/commands):
  - `AGENTS.md`
  - `CONTINUITY.md`
