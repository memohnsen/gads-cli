---
name: finish-work
description: Finish a gads-cli change once its code works: run the focused checks, open the pull request, rebase, write the risk list, run the merge gates, then merge (and release) when every gate passes. Use when a task's code works, before opening or updating its pull request, and whenever deciding whether a pull request may merge.
---

# Finish Work

The steps after the code works, in order. A **documentation-only change** (Markdown files, including
`AGENTS.md` and `SKILL.md`, and symlinks to them, with no Go, workflow, config or test changes)
skips steps 1 and 4: rebase, open the pull request with "Documentation-only: no checks" as its test
plan, wait for CI (it still runs; it must be green), and merge it.

## 1. Verify Locally

Run the smallest commands that cover what you changed, so an obvious failure never costs a CI run:

```sh
gofmt -l .                       # must print nothing
go vet ./...
go test ./internal/<pkg>/...     # the packages you touched; ./e2e when the binary's behavior changed
./scripts/check-secrets.sh       # after `git add`, since it scans tracked files
go run ./tools/genmanifest -check   # only when spec/, tools/genmanifest/ or the manifest changed
```

`make check` runs vet, the manifest drift check and every test; use it when the change is broad.
Never run the live test (`GOOGLE_ADS_LIVE=1 go test ./e2e -run TestLive`) or any command against a
real Google Ads account unless the user approved that named command in the current task.

## 2. Open The Pull Request

Branch off `main` (never commit to `main`), push, and open it with `gh pr create`. Every pull
request a task opens joins one stack off `main`, each based on the one below it, side fixes
included. Title in imperative sentence case naming the outcome, no type prefix (for example "Accept
user OAuth credentials"). The body has three sections:

- `## Summary`: what changed and why, and any safety gate the change touches (see gate 2).
- `## Test plan`: the CI run and its result, and anything you ran by hand.
- `## Merge Gates`: each gate's result and its evidence, kept current with `gh pr edit` until the
  merge.

## 3. Rebase

`main` moves while you work, so rebase onto it before you merge, every time:
`git fetch origin && git rebase origin/main`, then `git push --force-with-lease`. Resolve conflicts
by keeping main's change and reapplying yours on top. The push starts a new CI run; gate 1 needs it
green on the head commit as it will land.

## 4. Write The Risk List

A bulleted list of what makes the pull request risky to merge right now: checks skipped or
failing, behavior verified only by unit tests or fakes rather than against Google, credential or
auth changes, anything that touches a safety gate, the public interface (command paths, flags,
exit codes, the stderr schema, the `commands --json` schema), and anything you know is unfinished.
Write "None found" only when you checked each of these. The list feeds gate 4 and goes in your final
message.

## 5. Merge When Every Gate Passes

Run the [Merge Gates](#merge-gates). When all of them pass, merge your own pull request with the
`merge-and-deploy` skill, which also cuts the GitHub release. When one fails, do the work it
names, go back through the steps it reopens, and run the gates again. Merge a pull request whose
gates have not all passed only when the user explicitly asks in the current task. End with the
merge commit, the release (if any), the risk list and the confidence score.

## 6. Clean Up

Remove worktrees you created (`git worktree remove`, then `git worktree prune`) and anything you
built outside `bin/` and `dist/`. Delete credential files, OAuth client files or test configs you
made, and never leave one in the tree.

## Merge Gates

The agent that wrote a pull request may merge it, and cut the release, once every gate below passes
on the branch as it will land: after the last rebase, with the pull request's `## Merge Gates`
section recording each result and its evidence. Each gate passes or fails; none is waived, and a
change after a gate passed reopens it. The approval covers only your own pull requests, never
another agent's, and a "don't merge" or hold from the user still stands.

1. **CI green on the head commit.** Every job of the `CI` workflow (`.github/workflows/ci.yml`) for
   the pull request's head commit concluded `success`: `gh pr checks <number>`, or
   `gh run list --workflow ci.yml --commit <sha> --json status,conclusion`. In a stack, each layer
   merges on its own green run after its rebase. A job GitHub never started is not green; the
   `remote-gate` skill says how to handle it.
2. **Safety gates unchanged.** The diff does not loosen any of them, and no test that pins one was
   deleted or relaxed (`internal/guard`, `internal/cli/gates_test.go`, `e2e/smoke_test.go`):
   - every write is a `validate_only` dry run unless `--apply`;
   - creates of campaigns, ad groups, ads and keywords must be `PAUSED`;
   - enabling, publishing, budgets, bids and bid strategies, billing, admin and deletes need
     `--apply --confirm-spend` (`tools/genmanifest/classifications.json` and `internal/guard`);
   - the audit log records every write attempt and a write whose log cannot be opened is refused;
   - the budget cap bounds each call and the account total, with no flag or environment override;
   - read-only mode is on by default, only the config file turns it off, and dry runs use the reader
     credential by default;
   - nothing prints, logs or audits a token, client secret, refresh token or private key.

   A change that must alter one of these needs the user's explicit approval in the current task,
   recorded in the pull request.
3. **No secrets, no personal values.** `./scripts/check-secrets.sh` passes on the final tree, and
   `git diff origin/main...HEAD` adds no credential, token, key file, audit log, real customer ID,
   real 1Password vault or item name, or personal email. The repository is public: use placeholders
   such as `123-456-7890` and `op://<vault>/<item>/<field>`. No live Google Ads call was made
   without the user's approval for that named command.
4. **Confident above 85%.** With gates 1 to 3 passed, score from 0 to 100% how confident you are
   that the change is correct and complete, and name what keeps it from 100%. Every item on the risk
   list lowers the score. Above 85% passes; otherwise fix what lowers it and run the gates it
   reopens again.
