---
name: merge-and-deploy
description: Merge your own gads-cli pull request once every merge gate passes, confirm main is green, then cut the tagged GitHub release with the release-cli skill. Use when a pull request passes every gate in the finish-work skill, or when the user asks in the current task to merge.
---

# Merge And Deploy

For gads-cli, "deploy" means publishing a tagged GitHub release with built binaries; nothing else
is deployed. Merge only a pull request that passes every
[merge gate](../finish-work/SKILL.md#merge-gates) (your own), a documentation-only one, or one the
user asked in the current task to merge.

## 1. Check Before Merging

- The pull request's `## Merge Gates` section records every gate as passed, with evidence, for the
  current head commit (`gh pr view <number> --json headRefOid,mergeable,mergeStateStatus`).
- CI is green on that commit (`gh pr checks <number>`).
- The branch is rebased on the current `origin/main`. If `main` moved since, rebase, push and
  wait for CI again (finish-work step 3).

## 2. Merge In Order

Merge a stack bottom-up. For each pull request:

```sh
gh pr merge <number> --squash --delete-branch \
  --subject "<title> (#<number>)" --body "<one-paragraph summary>"
```

Before merging the next layer, change its base to `main` (`gh pr edit <next> --base main`),
rebase it onto `origin/main`, push, and wait for its CI run to go green.

## 3. Confirm Main

- `git fetch origin` and check the merge commit is `origin/main`'s head.
- The `CI` run for the merge commit on `main` concludes `success`
  (`gh run list --workflow ci.yml --branch main --limit 1`). A red `main` is fixed first, in a new
  pull request through `finish-work`, before any release.

## 4. Release

When the merge changed the binary (anything under `cmd/`, `internal/`, `spec/`, `go.mod` or
`go.sum`), cut a release from the merge commit with the `release-cli` skill, unless the user asked
to hold it. A change only to docs, skills, tests or CI needs no release. When several pull requests
land together, release once, from the last merge commit.

## 5. Report

The merge commits, the release tag and its URL (or why there is none), the risk list and the
confidence score from `finish-work`.
