# gads-cli

`googleads` (alias `gads`): a Go CLI for the Google Ads API, built for agents, with hard gates on
anything that can spend money. One static binary; the command tree is generated from Google's
Discovery document (`spec/`) into an embedded manifest. This file is for agents changing the code.
Agents *using* the CLI read [docs/agents.md](docs/agents.md).

## Hard Rules

- **The repository is public.** Commit no secret: no service-account key, OAuth client file,
  client secret, refresh or access token, audit log or token cache. Commit no personal value
  either: no real customer ID, 1Password vault or item name, or personal email. Use placeholders
  (`123-456-7890`, `op://<vault>/<item>/<field>`). `./scripts/check-secrets.sh` must pass.
- **No live Google Ads calls without approval.** Never run a command against a real account,
  including `GOOGLE_ADS_LIVE=1 go test ./e2e -run TestLive`, unless the user approved that named
  command in the current task. Tests use fakes (`e2e/helpers_test.go`, `internal/cli` test servers).
- **Keep every safety gate.** Writes are `validate_only` dry runs unless `--apply`; creates are
  `PAUSED`; enable, publish, budget, bid and bid-strategy, billing, admin and delete calls need
  `--apply --confirm-spend`; every write attempt is audited; the budget cap bounds each call and the
  account total; read-only mode is on by default and dry runs use the reader credential. Loosening
  any of them needs the user's explicit approval in the current task.
- **Never print a secret.** No output, log, error or audit entry may hold a token, client secret,
  refresh token or private key. Tests check this; keep them.
- Never commit to `main`; work on a branch and merge through a pull request.

## Workflow

1. Branch off an up-to-date `main` (`git fetch origin && git switch -c <branch> origin/main`).
2. Change the code with tests next to it. Go standard tooling: `go test`, `go vet`, `gofmt`.
3. Finish with the [`finish-work`](.codex/skills/finish-work/SKILL.md) skill: focused checks, the
   pull request, rebase, risk list and merge gates. When every gate passes you may merge your own
   pull request and cut the release without asking:
   [`merge-and-deploy`](.codex/skills/merge-and-deploy/SKILL.md) merges and hands off to
   [`release-cli`](.codex/skills/release-cli/SKILL.md), which tags `main` and verifies the
   GoReleaser build. [`remote-gate`](.codex/skills/remote-gate/SKILL.md) explains the CI gate.

## Verify

```sh
gofmt -l .                          # prints nothing
go vet ./...
go test ./...                       # includes the e2e smoke tests against a fake Google
./scripts/check-secrets.sh          # scans tracked files: `git add` first
go run ./tools/genmanifest -check   # manifest drift, when spec/ or tools/genmanifest changed
make check                          # vet, drift check and all tests
```

## Layout

| Path | What it holds |
| --- | --- |
| `cmd/googleads` | entry point |
| `internal/cli` | the command tree, wrappers, `auth`, `config`, `init`, gates and the audit log |
| `internal/guard` | risk classes and the spend, PAUSED and budget checks |
| `internal/auth` | credentials (service-account keys, user OAuth), `auth login`, token cache, 1Password |
| `internal/config` | config file, environment overlay, credential sources |
| `internal/api` | HTTP client, host pinning, retries, error shape |
| `internal/manifest` | the embedded, generated method manifest |
| `tools/genmanifest`, `tools/fetchspec` | manifest generator and Discovery fetcher |
| `e2e` | tests that drive the built binary |

## Skills

Skills live in `.codex/skills/<name>/SKILL.md`; each `.claude/skills/<name>` is a symlink to
`../../.codex/skills/<name>`. Edit only the `.codex` copy. They were adapted from the Atlas
repository's skills of the same purpose.

| Skill | Use |
| --- | --- |
| `finish-work` | after the code works: checks, pull request, rebase, risk list, merge gates |
| `merge-and-deploy` | merge your own pull request once the gates pass, then release |
| `release-cli` | tag `main` and publish the GoReleaser build as a GitHub release |
| `remote-gate` | how the CI workflow gates pull requests, and what to do when it is red |
