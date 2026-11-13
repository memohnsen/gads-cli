# Security

## What a service-account key is

gads-cli authenticates with Google Cloud service-account keys: JSON files holding a private key.
Whoever has the file can act as the service account, with the access it holds in Google Ads, from
any machine, until the key is deleted. Keys do not expire on their own.

Limit what each key can do:

- Use two service accounts. The **reader** gets Google Ads **Read only** access and handles every
  report and dry run. The **writer** gets **Standard** access and is used only for `--apply`. Never
  grant Admin.
- Create one key per machine, so one machine can be cut off without touching the others.
- Delete a key in the Google Cloud Console (IAM & Admin, Service accounts, Keys) as soon as it is
  no longer needed. That is the only revocation:
  `googleads config unset service-account-key [--role writer]` removes the local copy and nothing
  else.

## Where keys live

Never in the config file, the git tree, a command line, or a chat. For each role, in order of
preference:

1. **1Password**, read on demand with `op read --no-newline <ref>` (stdin closed, 60 s timeout).
   The key goes from op's stdout to memory and is never written to disk by gads-cli.
2. **A `chmod 600` file** named by `GOOGLE_ADS_JSON_KEY_FILE_PATH` (reader) or
   `GOOGLE_ADS_WRITE_JSON_KEY_FILE_PATH` (writer). A file readable by group or others is refused.
3. **A stored file**, `<user config dir>/gads/credentials/<role>.json`, mode `0600` in a `0700`
   directory, written by `googleads config set service-account-key [--role writer] < key.json` from
   stdin. Delete the downloaded copy afterwards.

Access tokens are cached for at most one hour in `<user cache dir>/gads/token.json` (`0600`). No
command prints a token or a key; `auth status` shows client emails, projects and the last
characters of key ids only.

## What the CLI does not do

- It does not send credentials anywhere but `oauth2.googleapis.com` (the signed key assertion) and
  `googleads.googleapis.com` (the access token). Other hosts need `GOOGLE_ADS_ALLOW_CUSTOM_BASE=1`,
  which exists for tests and proxies, and plain HTTP is refused except on loopback. The `token_uri`
  inside a key file is ignored.
- It does not follow redirects.
- It does not take a key or token from argv. `googleads api` cannot set `Authorization`, a
  `developer-token` header, the method-override headers or the credential query parameters
  `access_token`, `oauth_token` and `key`.
- It does not send a body with GET or DELETE, and it refuses `api` paths whose spelling could dodge
  the match against known methods. A call to an unknown path is admin-class, cannot be dry-run, and
  has every budget amount in its body held to the cap.
- It does not log credentials. `--verbose` logs method, path, status and response size; the audit
  log holds a redacted operation summary and a body hash, never the body, a token or a key.
- It does not phone home. The catalog is compiled into the binary.

## Spending money

Writes are dry runs unless `--apply`; spend, delete and admin need `--apply --confirm-spend`;
read-only mode is on by default and only the config file turns it off; the budget cap bounds both
each call and the account's total daily budget. See the README's Safety model.

These are the CLI's part. They cannot stop a caller with shell access from passing
`--confirm-spend` or editing the config file; they make those decisions explicit, visible in the
command, and recorded in the audit log. Give agents rules of their own, give them the reader key
only where writing is not their job, and keep `GOOGLE_ADS_READ_ONLY=1` in their environment.

## Reporting a vulnerability

Report security issues privately through GitHub Security Advisories on this repository (Security,
Report a vulnerability). Please do not open a public issue. Include `googleads version`, the steps
to reproduce and the impact you see.
