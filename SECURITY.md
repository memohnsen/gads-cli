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

## What a user OAuth credential is

Where service-account keys are blocked, `googleads auth login` creates an `authorized_user`
credential: an OAuth client ID, its client secret and a refresh token. Whoever has all three can act
as the person who signed in, with that person's full Google Ads access, until the grant is revoked
at <https://myaccount.google.com/permissions> (or, for an External consent screen still in Testing,
until it expires after 7 days). Treat it like a key:

- It is stored only where keys are stored (below): 1Password, or a `0600` file. A credential file
  or OAuth client file readable by group or others is refused.
- The login requests only the Google Ads scope, binds its redirect listener to `127.0.0.1`, checks
  the `state` parameter and uses PKCE (S256), so an intercepted authorization code is useless on its
  own. Endpoints are pinned to Google; the `auth_uri` and `token_uri` in a client file are ignored.
- It prints neither the client secret nor the refresh token, and passes the credential to `op` on
  stdin, never as an argument. `op`'s output, which echoes the item, is discarded, and its errors
  are redacted.
- The person's own access is not split into reader and writer the way two service accounts are.
  Keep read-only mode on until a person decides to write, or sign the reader in as a second Google
  user with Read only access.

## Where keys live

Never in the config file, the git tree, a command line, or a chat. The same goes for user OAuth
credentials, which load from the same places. For each role, in order of preference:

1. **1Password**, read on demand with `op read --no-newline <ref>` (stdin closed, 60 s timeout).
   The key goes from op's stdout to memory and is never written to disk by gads-cli.
2. **A `chmod 600` file** named by `GOOGLE_ADS_JSON_KEY_FILE_PATH` (reader) or
   `GOOGLE_ADS_WRITE_JSON_KEY_FILE_PATH` (writer). A file readable by group or others is refused.
3. **A stored file**, `<user config dir>/gads/credentials/<role>.json`, mode `0600` in a `0700`
   directory, written by `googleads config set service-account-key [--role writer] < key.json` from
   stdin (delete the downloaded copy afterwards) or by `googleads auth login`.

Access tokens are cached for at most one hour in `<user cache dir>/gads/token.json` (`0600`); a
user credential's entry is keyed by a hash of its refresh token, never the token itself. No command
prints a token, a client secret or a key; `auth status` shows credential types, client emails,
projects, the last characters of key ids and the start of OAuth client IDs only.

## What the CLI does not do

- It does not send credentials anywhere but `oauth2.googleapis.com` (the signed key assertion, or a
  user credential's refresh token and client secret) and `googleads.googleapis.com` (the access
  token). `auth login` also sends the browser to `accounts.google.com`, with the client ID only.
  Other hosts need `GOOGLE_ADS_ALLOW_CUSTOM_BASE=1`, which exists for tests and proxies, and plain
  HTTP is refused except on loopback. The `token_uri` inside a key file is ignored.
- It does not follow redirects.
- It does not take a key or token from argv. `googleads api` cannot set `Authorization`, a
  `developer-token` header, the method-override headers or the credential query parameters
  `access_token`, `oauth_token` and `key`.
- It does not send a body with GET or DELETE, and it refuses `api` paths whose spelling could dodge
  the match against known methods. A call to an unknown path is admin-class, cannot be dry-run, and
  has every budget amount in its body held to the cap.
- It does not log credentials. `--verbose` logs method, path, status and response size; the audit
  log holds a redacted operation summary and a body hash, never the body, a token, a client secret or
  a key.
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
