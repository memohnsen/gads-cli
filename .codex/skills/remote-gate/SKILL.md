---
name: remote-gate
description: How gads-cli pull requests are gated by the CI workflow in GitHub Actions, how to read a run, reproduce a failure locally, and handle runs GitHub never started. Use when checking whether a pull request's checks pass, or when a CI run is red.
---

# The CI Gate

Every pull request and every push to `main` runs `.github/workflows/ci.yml`. A pull request
merges only when every job of that workflow is green on its head commit
([finish-work gate 1](../finish-work/SKILL.md#merge-gates)). Do not use local runs as a substitute
for it; run locally only to find a failure faster.

## What It Runs

| Job | Steps | Reproduce locally |
| --- | --- | --- |
| `test (ubuntu-latest)`, `test (macos-latest)` | `go vet ./...`, `go test ./...` (includes the e2e smoke test against a fake Google; never the live test) | `go vet ./... && go test ./...` |
| `checks` | secret scan, gofmt, manifest drift, cross-compile for linux/darwin amd64/arm64 | `./scripts/check-secrets.sh`, `gofmt -l .`, `go run ./tools/genmanifest -check`, `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null ./cmd/googleads` |

## Reading A Run

```sh
gh pr checks <number>
gh run list --workflow ci.yml --commit <sha> --json databaseId,status,conclusion
gh run view <run id> --log-failed
```

- **A test or check failed:** reproduce it with the command above, fix it on the branch, push, and
  wait for the new run. A failure that also happens on a clean `origin/main` is fixed in its own
  pull request at the bottom of the stack.
- **A job was cancelled without starting** (no steps, no runner; annotations such as "The job was
  not acquired by Runner of type hosted" or macOS capacity notices): that is GitHub capacity, not
  the code. Re-run it with `gh run rerun <run id> --failed`. It is still not green until it
  passes; never merge on it.
- **Flaky-looking failure:** read the log before re-running. Re-run once at most; a second failure
  is a real failure.

Say in the pull request's test plan which run is the evidence (its URL or id) and anything you ran
by hand.
