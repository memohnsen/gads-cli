# Security

## What a service-account key is

googleads authenticates with a Google Cloud service-account key: a JSON file holding a private key.
Whoever has the file can act as the service account, with the access it holds in Google Ads, from
any machine, until the key is deleted. The key does not expire on its own.

Limit what it can do:

- Add the service account in Google Ads with Standard access, not Admin. Standard access cannot
  manage users.
- Create one key per machine, so one machine can be cut off without touching the others.
- Delete a key in the Google Cloud Console (IAM & Admin, Service accounts, Keys) as soon as a machine
  no longer needs it. That is the only revocation: `googleads config unset service-account-key`
  removes the local copy and nothing else.

## Where the key lives

Either in the config file, stored with `googleads config set service-account-key < key.json` (read
from stdin, never from argv; the file is written `0600` inside a `0700` directory, and
`googleads config path` prints where), or in a file you point `GOOGLE_ADS_JSON_KEY_FILE_PATH` at,
which then wins. After storing the key, delete the downloaded copy. On Windows and on shared
machines the config file gets only the user-scoped protection of `%AppData%`.

Access tokens are cached for at most one hour in `<user cache dir>/googleads/token.json` (`0600`).

## What the CLI does not do

- It does not send credentials anywhere but `oauth2.googleapis.com` (the signed key assertion) and
  `googleads.googleapis.com` (the access token). Other hosts need `GOOGLE_ADS_ALLOW_CUSTOM_BASE=1`,
  which exists for tests and proxies, and plain HTTP is refused except on loopback. The `token_uri`
  inside a key file is ignored.
- It does not follow redirects, on either host.
- It does not take a key or token from argv, and `googleads api` cannot set `Authorization`, a
  `developer-token` header, the method-override headers `X-HTTP-Method-Override`, `X-HTTP-Method`
  and `X-Method-Override`, or the credential query parameters `access_token`, `oauth_token` and
  `key`.
- It does not send a body with GET or DELETE, or any other method without a risk class the guard
  assigned. `googleads api` refuses a path with an empty, `.` or `..` segment, or with `%`,
  whitespace or a backslash, before it matches the path against the known methods, so a budget
  change cannot slip past the budget cap under another spelling.
- It does not log credentials. `--verbose` logs method, path, status and response size.
- It does not phone home. The catalog is compiled into the binary.
- It prints a credential in exactly one place, on purpose: `googleads auth token`.

## Spending money

The guardrails in the README (payload check, budget cap, `--force`, read-only mode) are the CLI's
part. They cannot stop a caller with shell access from passing `--force` or editing the config file;
they make those decisions explicit and visible. Give agents rules of their own and keep the service
account's Google Ads access as narrow as the work allows.

## Reporting a vulnerability

Report security issues privately through GitHub Security Advisories on this repository (Security,
Report a vulnerability). Please do not open a public issue. Include `googleads version`, the steps
to reproduce and the impact you see.
