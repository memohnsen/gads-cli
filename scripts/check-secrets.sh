#!/usr/bin/env bash
# Fails when a tracked file looks like it carries a credential: a PEM private
# key, a service-account key body, a Google access or refresh token, an OAuth
# client secret or an API key. Tests build their keys at runtime, so nothing
# in the tree should ever match. Run by CI on every push and pull request.
set -euo pipefail
pattern='BEGIN [A-Z ]*PRIVATE KEY|"private_key"[[:space:]]*:[[:space:]]*"-----|ya29\.[0-9A-Za-z_-]{20,}|GOCSPX-[0-9A-Za-z_-]{10,}|AIza[0-9A-Za-z_-]{35}|"refresh_token"[[:space:]]*:[[:space:]]*"1//'
if git grep -nIE "$pattern" -- . ':!spec/*.discovery.json' ':!scripts/check-secrets.sh'; then
  echo "possible credential in a tracked file (above); remove it and rotate it" >&2
  exit 1
fi
# Key files and audit logs must never be tracked.
if git ls-files | grep -E '(^|/)(credentials/|.*-key\.json$|.*\.jsonl$|token\.json$)'; then
  echo "a key, token cache or audit log is tracked (above)" >&2
  exit 1
fi
echo "no credentials found"
