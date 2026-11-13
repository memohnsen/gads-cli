---
name: release-cli
description: Cut a tagged gads-cli GitHub release with built binaries: pick the SemVer version, tag the merged commit on main, let the GoReleaser workflow publish, and verify the assets. Use after merging a change to the binary (merge-and-deploy step 4), or when asked to cut, publish or bump a release.
---

# Release The CLI

Releases are built in CI: pushing a `v*` tag runs `.github/workflows/release.yml`, which tests the
tree, runs the secret scan and then GoReleaser (`.goreleaser.yaml`). GoReleaser builds `googleads`
for linux and darwin on amd64 and arm64 with the version stamped in, and publishes
`gads-cli_<version>_<os>_<arch>.tar.gz` archives plus `checksums.txt` to a GitHub release with a
changelog generated from the commits. Nothing is published from a local machine.

## 1. Preconditions

- The commit to release is on `main`, and the `CI` run for it is green
  (`gh run list --workflow ci.yml --branch main --limit 1`).
- `git fetch origin --tags` and look at what exists: `git tag -l 'v*' --sort=-v:refname | head`,
  `gh release list --limit 5`. Never reuse or move a tag that has a release.

## 2. Pick The Version

SemVer on the public interface (command paths, flags, exit codes, the stderr schema and the
`commands --json` schema), as the README's "API version and upgrades" section says:

- **major** when a command or flag disappears or is renamed, or an exit code or schema changes
  incompatibly;
- **minor** when commands, flags, credential types or behavior are only added;
- **patch** for fixes only.

`-rc.N` tags (for example `v0.4.0-rc.1`) publish as pre-releases.

## 3. Tag And Push

```sh
git tag -a vX.Y.Z -m "gads-cli vX.Y.Z" <merge commit sha>
git push origin vX.Y.Z
```

## 4. Wait For The Workflow

```sh
gh run list --workflow release.yml --limit 1 --json databaseId,headBranch,status,conclusion
```

Wait until the run for `vX.Y.Z` concludes. On failure read `gh run view <id> --log-failed`:

- **No release was created** (the run failed in tests, the secret scan or before GoReleaser
  published): fix the cause through `finish-work`, then delete the tag
  (`git push --delete origin vX.Y.Z && git tag -d vX.Y.Z`) and tag the fixed commit.
- **A release exists:** never re-tag. Fix forward and release the next patch version.
- **The workflow itself is broken** (for example a GoReleaser config error): fix
  `.goreleaser.yaml` or `release.yml` in a pull request through `finish-work`. To test a config
  change before tagging, run `goreleaser check` and `goreleaser release --snapshot --clean` locally
  if GoReleaser is installed; the snapshot writes to `dist/` and publishes nothing.

## 5. Verify The Release

```sh
gh release view vX.Y.Z --json tagName,isDraft,isPrerelease,url,assets -q '.url, (.assets[].name)'
```

It must list the four archives (`linux_amd64`, `linux_arm64`, `darwin_amd64`, `darwin_arm64`) and
`checksums.txt`. Then download the archive for the platform you are on into a scratch directory,
check it against `checksums.txt` (`sha256sum -c --ignore-missing checksums.txt`, or
`shasum -a 256 -c` on macOS), unpack it, and run `./googleads version`. It must print
`googleads X.Y.Z (Google Ads API vNN, ...)`: the tag without its leading `v`, and the API version
the README pins. Running `version` makes no Google Ads call.

## 6. Report

The tag, the release URL, the asset list and the `version` output.
