# googleads

A command-line client for the [Google Ads API](https://developers.google.com/google-ads/api),
built for agents and scripts: JSON in, JSON out, one static binary, no runtime dependencies. The
command tree is generated from Google's Discovery document for API v25, so every method of that
version is reachable as a command without hand-written glue. stdout carries the API response and
nothing else; every failure leaves one line of JSON on stderr.

It keeps the two decisions that cost money with a person: switching a campaign on, and how much a
budget may spend. Both need an explicit flag or a setting only a person changes; see
[Guardrails](#guardrails).

googleads is not affiliated with or endorsed by Google.

## Install

```sh
go install github.com/wir-drei-digital/google-ads-cli/cmd/googleads@latest
```

Or download a prebuilt binary from
[GitHub Releases](https://github.com/wir-drei-digital/google-ads-cli/releases):

| OS      | Architectures | Archive   |
| ------- | ------------- | --------- |
| Linux   | amd64, arm64  | `.tar.gz` |
| macOS   | amd64, arm64  | `.tar.gz` |
| Windows | amd64         | `.zip`    |

Each release also ships `checksums.txt`. Unpack the archive and put `googleads` on your `PATH`.

## Setup (once)

1. Use a Google Cloud project that is not on the Free Trial and has no suspended billing. Enable
   the Google Ads API in it.
2. Apply for API access on the project's Google Ads API page in the Cloud Console. Explorer access
   is enough for campaign work. Keyword Planner and the other planning services need Basic access,
   which needs brand verification of the project. Google may revoke access that goes unused for
   90 days.
3. Create a service account in the project. It needs no IAM roles. Create one JSON key per machine
   that will run googleads; a key can be downloaded only once.
4. In Google Ads, under Admin, Access and security, add the service account's email with Standard
   access.
5. On each machine, run `googleads init`. It asks for the key file, the customer ID and a budget
   cap, checks the key with two read-only calls and saves everything. Without a terminal, use the
   `config set` commands instead:

   ```sh
   googleads config set service-account-key < key.json
   googleads config set customer-id 123-456-7890
   googleads config set budget-cap 30
   ```

   Delete the downloaded key file afterwards.
6. In an account that already has campaigns, look for campaigns that lack the EU political
   advertising declaration before the first change. One undeclared campaign blocks every campaign
   change in the account:

   ```sh
   googleads search --query "SELECT campaign.id, campaign.name FROM campaign WHERE campaign.missing_eu_political_advertising_declaration = TRUE"
   ```

## Authentication

googleads authenticates with a Google Cloud service-account key. The first source found wins:

1. `GOOGLE_ADS_JSON_KEY_FILE_PATH`, the path to a key file (the name Google's own client libraries
   use);
2. the key stored in the config file with `googleads config set service-account-key < key.json`,
   which reads the key from stdin, never from the command line.

The key must be a service-account key (`"type": "service_account"`). An OAuth client file or a user
credential is refused with a message that says what the file is.

googleads exchanges the key for a one-hour access token at `https://oauth2.googleapis.com/token`
and caches the token in `<user cache dir>/googleads/token.json` (`0600`), reusing it while at least
60 seconds of validity remain. The `token_uri` inside the key file is ignored.

There is no developer token. Google sunset it on 2026-09-09; the access level now belongs to the
Cloud project. googleads never sends a `developer-token` header, and `googleads api` refuses to set
one.

`googleads auth status` prints the configuration as one line of JSON, offline, never a key or a
token:

```json
{"mode":"service_account","source":"config","client_email":"gads@proj.iam.gserviceaccount.com","private_key_id":"0a1b...","project_id":"proj","token_cached_until":"2026-09-25T14:03:11Z","customer_id":"1234567890","budget_cap":"30","read_only":false}
```

| Key | Meaning |
| --- | --- |
| `mode` | `service_account`, or `none` when no key is configured |
| `source` | `env` (`GOOGLE_ADS_JSON_KEY_FILE_PATH`) or `config` |
| `client_email`, `private_key_id`, `project_id` | from the key |
| `token_cached_until` | when the cached access token expires, if one is cached |
| `customer_id`, `login_customer_id` | the default customer and manager account |
| `budget_cap` | the budget cap in the account's currency, if one is set |
| `read_only` | whether read-only mode is on |
| `missing` | what blocks calls: `service_account_key`, `valid_service_account_key`, `customer_id` |
| `hint` | the commands that fix what is missing |

`googleads auth token` prints a valid access token to stdout, for piping into another tool. That is
a credential: do not log it.

A key is revoked only by deleting it in the Cloud Console (IAM & Admin, Service accounts, Keys).
`googleads config unset service-account-key` removes the local copy and nothing else. See
[SECURITY.md](SECURITY.md).

### Configuration

The config file lives at `<user config dir>/googleads/config.json`, written `0600` inside a `0700`
directory; `googleads config path` prints the exact location. `googleads config set <key>` and
`googleads config unset <key>` take these keys:

| Key | Value |
| --- | --- |
| `service-account-key` | the key JSON, from stdin only |
| `customer-id` | the customer to act on by default, `123-456-7890` or `1234567890` |
| `login-customer-id` | the manager account the access goes through |
| `read-only` | `true` or `false` |
| `budget-cap` | the highest daily budget googleads accepts, in the account's currency (`30`, `29.61`) |

Passing an environment variable name as the key (`GOOGLE_ADS_CUSTOMER_ID`) gets an error naming the
key it meant. Unsetting a key that is not set is a no-op success.

Environment variables, which win over the file: `GOOGLE_ADS_JSON_KEY_FILE_PATH`,
`GOOGLE_ADS_CUSTOMER_ID`, `GOOGLE_ADS_LOGIN_CUSTOMER_ID`, `GOOGLE_ADS_READ_ONLY`,
`GOOGLE_ADS_API_BASE`, `GOOGLE_ADS_TOKEN_URL`, `GOOGLE_ADS_ALLOW_CUSTOM_BASE`. Two exceptions:

- The budget cap has no environment variable and no flag. It lives in the config file only.
- `GOOGLE_ADS_READ_ONLY` (`1` or `true`) only switches read-only mode on. It cannot switch off a
  read-only mode stored in the config file; to turn that off, run `googleads config unset read-only`.

## Usage

The customer comes from `--customer-id` (dashes allowed), else `GOOGLE_ADS_CUSTOMER_ID`, else the
config file. `--login-customer-id` sets the manager account for one call.

Run a GAQL report, one page or all of them merged into one JSON array of rows:

```sh
googleads search --query "SELECT campaign.id, campaign.name, metrics.clicks, metrics.cost_micros FROM campaign WHERE segments.date DURING LAST_7_DAYS"
googleads search --all --query "SELECT campaign.id, campaign.name, metrics.clicks, metrics.cost_micros FROM campaign WHERE segments.date DURING LAST_7_DAYS"
```

`--query @report.gaql` reads the query from a file. Get keyword ideas (needs Basic access) with a
body from a file:

```sh
googleads generate-keyword-ideas --data @ideas.json
```

```json
{"language":"languageConstants/1001","geoTargetConstants":["geoTargetConstants/2756"],"keywordPlanNetwork":"GOOGLE_SEARCH","keywordSeed":{"keywords":["rapport automatisieren"]}}
```

Build a paused Search campaign in one request. Temporary resource names (negative ids) let later
operations reference earlier ones. `--validate-only` makes Google check the whole request and apply
nothing; send it again without the flag to create it:

```sh
googleads mutate --validate-only --data @campaign.json
googleads mutate --data @campaign.json
```

```json
{"mutateOperations":[
  {"campaignBudgetOperation":{"create":{"resourceName":"customers/1234567890/campaignBudgets/-1","name":"Brand","amountMicros":"20000000","deliveryMethod":"STANDARD","explicitlyShared":false}}},
  {"campaignOperation":{"create":{"resourceName":"customers/1234567890/campaigns/-2","name":"Brand","status":"PAUSED","advertisingChannelType":"SEARCH","campaignBudget":"customers/1234567890/campaignBudgets/-1","manualCpc":{"enhancedCpcEnabled":false},"networkSettings":{"targetGoogleSearch":true,"targetSearchNetwork":false,"targetContentNetwork":false,"targetPartnerSearchNetwork":false},"containsEuPoliticalAdvertising":"DOES_NOT_CONTAIN_EU_POLITICAL_ADVERTISING"}}},
  {"adGroupOperation":{"create":{"resourceName":"customers/1234567890/adGroups/-3","name":"Brand","campaign":"customers/1234567890/campaigns/-2","type":"SEARCH_STANDARD","status":"ENABLED","cpcBidMicros":"1000000"}}}
]}
```

A full campaign adds keywords (`adGroupCriterionOperation`), a responsive search ad
(`adGroupAdOperation`) and location targeting (`campaignCriterionOperation`) to the same list.

Switch the campaign on. That is a spend decision, so it needs `--force`:

```sh
googleads campaigns mutate --force --data '{"operations":[{"update":{"resourceName":"customers/1234567890/campaigns/123","status":"ENABLED"},"updateMask":"status"}]}'
```

Find your way around:

```sh
googleads schema Campaign             # fields of a resource or request type
googleads campaigns mutate --help     # fields, an example body and the risk class
googleads commands --json             # the machine-readable catalog of every command
googleads api GET v25/customers:listAccessibleCustomers   # raw request, guardrails still apply
```

Bodies come from `--data '{...}'`, `--data @file.json` or `--data -` (stdin), up to 20 MB, and are
sent as given, except that `--validate-only` and `--partial-failure` set `validateOnly` and
`partialFailure`. Responses are Google's camelCase JSON, untouched; `--output <file>` writes the body
to a file instead of stdout. int64 values (ids, `costMicros`, `amountMicros`, `clicks`) are strings.
Money is in micros: 1 unit of the account's currency is 1,000,000 micros.

`--all` follows page tokens and prints one JSON array of the merged rows; it always starts at the
first page, whatever page token `--data` carries. `--max-pages` (default 100) caps it.
`search --all` with `searchSettings.returnSummaryRow` is a usage error, because the merge would drop
the summary row.

Global flags: `--customer-id`, `--login-customer-id`, `--force`, `--output`, `--timeout` (per
attempt, default 60s) and `--verbose` (method, path, status and response size on stderr; never a
credential).

## Guardrails

### Risk classes

Every method has one reviewed class in `tools/genmanifest/classifications.json`, shown in its help
and in the catalog.

| Class | Meaning | Examples | Gate |
| --- | --- | --- | --- |
| `read` | reads or computes, changes nothing | `search`, `search-stream`, `generate-keyword-ideas`, `generate-keyword-forecast-metrics`, `google-ads-fields search`, `geo-target-constants suggest`, `list-accessible-customers`, `recommendations generate` | none; allowed in read-only mode |
| `write` | creates or edits campaign entities | most per-resource `mutate` methods, the generic `mutate`, `customer mutate`, `recommendations dismiss` | blocked in read-only mode |
| `delete` | removes something | any `remove` operation, `operations delete`, `remove-campaign-automatically-created-asset` | `--force` |
| `spend` | can start spending or change how much | switching a campaign on, changing a budget amount, `experiments promote-experiment`, `graduate-experiment` and `schedule-experiment`, `campaign-drafts promote` | `--force` |
| `admin` | outside campaign editing | users and invitations, billing setups and account budgets, account, data and product links, new client accounts, manager links, conversion and customer-data uploads, offline user data jobs, batch jobs, messages to Local Services leads, identity verification, incentives | `--force` |

Without `--force`, a `delete`, `spend` or `admin` call exits 2 with kind `usage` before any network
I/O, and the message names each operation that needs it and why.

### Payload check

One class per method is not enough, because one `mutate` call can do anything. Before a mutate is
sent (the generic `mutate`, every per-resource `mutate`, `batch-jobs add-operations`, and `api`
calls to those paths), googleads reads every operation in the body. The highest class found wins.
`batch-jobs add-operations` stays `admin` whatever it carries, but the refusals below, including the
budget cap, apply to its operations too.

1. A `remove` operation is `delete`.
2. A campaign `create` whose `status` is not exactly the string `"PAUSED"` is `spend`. That includes
   a missing status, because Google's default is `ENABLED`, and numeric enum values.
3. A campaign `update` that sets `status` to anything other than `"PAUSED"` is `spend`.
4. A campaign `update` that sets `campaignBudget` (moving the campaign to another budget) is
   `spend`.
5. A budget `create` or `update` that sets `amountMicros` is checked against the budget cap. Without
   a cap, or with an amount above it, the request is refused. `--force` and `--validate-only` do not
   change that.
6. A budget `update` that sets `amountMicros` within the cap is `spend`, whether it raises or lowers
   the amount. An amount cleared through `updateMask` alone counts as 0 and is `spend` too. Pausing
   the campaign is the way to stop spend without `--force`.
7. A budget that sets `totalAmountMicros`, or a `period` other than the string `"DAILY"`, is
   refused. The cap is a daily cap.
8. Every other operation takes the class of the method it belongs to. An operation type in the
   generic `mutate` takes the class of its per-resource method (`campaignOperation` takes the class
   of `campaigns mutate`); an unknown operation type is `admin`.

An operation that names more than one of `create`, `update` and `remove` is refused.

How the body is read:

- A field counts as set when it appears in the operation's object or in its `updateMask`, which
  must be a comma-separated string.
- Field names are matched in both spellings Google accepts (`amountMicros` and `amount_micros`).
  The body that is sent is not modified.
- A duplicate key, or both spellings of one field in the same object, refuses the request: the check
  has to see exactly what Google will see.
- Amounts are JSON numbers or decimal strings written as plain whole numbers of micros (digits only)
  and compared exactly. Anything else refuses the request.
- A mutate body that is not valid JSON is refused.

### Budget cap

- Set it with `googleads config set budget-cap <amount>`, in the account's currency (`30` or
  `29.61`, a dot as the decimal separator, at most six decimal places). It lives in the config file
  only: no environment variable, no flag.
- It applies to every daily budget amount in every request, with or without `--force` and
  `--validate-only`. Without a cap no budget amount can be set at all.
- It limits each budget, not the account: two campaigns with their own budgets can together spend
  twice the cap. Switching each of them on is a separate `--force` decision.
- Google may spend up to twice the daily budget on a single day and bills at most 30.4 times the
  daily budget in a month.

### Gates and modes

- **`--force`** is a per-call flag. It has no environment variable and no config setting, so every
  spend decision stays visible in the command that made it.
- **`--validate-only`** exists on the methods whose request supports it (`validate_only` in the
  catalog). Google checks the request completely and applies nothing. Such a call skips the
  `--force` gate and runs in read-only mode; the budget cap and the refusals above still apply. Only
  the flag counts: a `validateOnly` field inside `--data` relaxes nothing.
- **Read-only mode** (`GOOGLE_ADS_READ_ONLY=1` or `googleads config set read-only true`) allows
  `read`-class calls and `--validate-only` calls only.
- **`googleads api`** is not a bypass. A path that matches a known method, in any API version,
  inherits its class, the payload check, the budget cap and the block list. An unmatched POST, PUT,
  PATCH or DELETE is `admin`. In read-only mode it allows GET and `read`-class methods. It cannot
  set `Authorization`, a `developer-token` header, or the credential query parameters
  `access_token`, `oauth_token` and `key`, and it has no `--validate-only`. The path carries the
  customer, so `api` ignores `--customer-id`; it does honour `--login-customer-id`.
- **Pinned hosts.** Credentials go only to `oauth2.googleapis.com` and `googleads.googleapis.com`
  over HTTPS. `GOOGLE_ADS_API_BASE` and `GOOGLE_ADS_TOKEN_URL` pointing elsewhere are refused unless
  `GOOGLE_ADS_ALLOW_CUSTOM_BASE=1` is set too, and plain HTTP is allowed only on loopback. Redirects
  are never followed.

Not gated, by decision: bids and bidding strategies, enabling ad groups, ads and keywords, negative
keywords, conversion actions, and pausing anything. The budget bounds what those can cost, and
switching a campaign on is gated.

### What the CLI cannot do

It cannot stop a caller with shell access from passing `--force` or raising the cap in the config
file; it makes both explicit and visible. The other layers belong to the operator: the rules they
give their agents, and the access the service account holds in Google Ads (Standard access cannot
manage users).

## Blocked and skipped methods

Three methods can set a budget amount or a spend commitment that the request itself does not show,
so the budget cap could not hold for them. They stay in the command tree and in the catalog
(`blocked: true` with the reason), and are refused before any network I/O, as commands, as
operation types inside `mutate`, and through `api`, whatever the flags:

| Command | Reason |
| --- | --- |
| `recommendations apply` | applying a recommendation can set a budget to an amount Google chooses, which the budget cap cannot check; read recommendations with search and carry out a worthwhile one through mutate |
| `recommendation-subscriptions mutate-recommendation-subscription` | an auto-apply subscription lets Google change budgets later, outside any check |
| `reservations book-campaigns` | booking a reservation commits spend that the budget cap cannot check |

One method is not generated at all: `media.upload`. YouTube video upload uses Google's media upload
protocol, which this client does not implement.

Both lists live in `tools/genmanifest/overrides.json`, each entry with its reason.

## Errors and exit codes

Errors are one line of JSON on stderr, never on stdout (`details` shortened here):

```json
{"kind":"validation","error":"POST v25/customers/1234567890/googleAds:mutate: HTTP 400: fieldError.REQUIRED: The required field was not present. (mutate_operations[0].campaign_operation.create.contains_eu_political_advertising)","status":400,"details":{"error":{"code":400,"status":"INVALID_ARGUMENT"}},"request_id":"Ab12Cd34"}
```

`status` is the HTTP status and is omitted when there was no response. `details` is the parsed error
body (for Google Ads, the `GoogleAdsFailure` with all its errors) or `null`. `request_id` comes from
the `request-id` header or the failure and is omitted when unknown. The message leads with Google's
first error: its code, its message and the field path. Branch on `kind`:

| `kind` | Meaning |
| --- | --- |
| `auth` | HTTP 401, or the token endpoint refused the key (deleted or disabled key) |
| `forbidden` | HTTP 403 |
| `not_found` | HTTP 404 or 410 |
| `validation` | other 4xx; also a 3xx, which is refused rather than followed (the message names the `Location`) |
| `conflict` | HTTP 409, or Google's `databaseError.CONCURRENT_MODIFICATION`: the same resource changed concurrently. The request was rejected as a whole; retry once after a short wait |
| `rate_limited` | HTTP 429 beyond the retry budget |
| `server` | HTTP 5xx |
| `transport` | a network failure that is safe to retry: the request never reached Google, or it could not change anything (a read or a `--validate-only` call); also the token endpoint answering 429 or 5xx, and a call cancelled while waiting to retry |
| `outcome_unknown` | a change failed in flight and may or may not have been applied; check with `search` before retrying |
| `incomplete` | `--all` hit `--max-pages`; stdout holds the partial array |
| `partial_failure` | with `--partial-failure`, some operations failed; stdout holds the full response, including the ones that succeeded |
| `usage` | the command was wrong or a guardrail refused it; nothing was sent |

Hints are appended to the message for the errors people hit during setup:

- `CLOUD_PROJECT_NOT_APPROVED_FOR_PRODUCTION`: the Cloud project still has Test access, which
  reaches test accounts only; apply for Explorer or Basic access.
- A planning method (keyword ideas, forecasts, reach, audience insights) refused with HTTP 403:
  Explorer access blocks planning services; Basic access is required.
- `USER_PERMISSION_DENIED`: the service account has no access to this customer; add its email in
  Google Ads under Admin, Access and security, or set `login-customer-id` to the manager account it
  was added to.
- `EU_POLITICAL_ADVERTISING_DECLARATION_REQUIRED`: a campaign in the account lacks the declaration;
  the hint names the `search` that finds it.

| Exit code | Meaning |
| --- | --- |
| `0` | success |
| `1` | API or network error (any `kind` except `usage`) |
| `2` | usage error, which includes every guardrail refusal |

Retries happen inside the CLI; do not add a retry loop of your own. HTTP 429 is retried for every
call, waiting as long as Google asks (`retryDelay` in its quota error details, else `Retry-After`,
else exponential backoff with jitter), within a total budget of 60 seconds. When Google asks for
longer than the budget left (a daily quota is used up), the call fails at once with `rate_limited`
and the delay in the message. Network failures and HTTP 5xx are retried only for `read`-class and
`--validate-only` calls, which includes the POST-based `search`, up to three attempts. A change that
fails in flight is never replayed. Ctrl-C and SIGTERM cancel the request in flight and any retry
wait.

## API version policy

googleads is pinned to Google Ads API v25, which Google plans to sunset around August 2027. Moving to
a new version:

1. `make update-spec VERSION=vNN` downloads and validates the new Discovery document.
2. `make generate` rebuilds the embedded manifest. New methods are not guessed: the generator lists
   the unclassified ones and fails. `go run ./tools/genmanifest -write-proposals` proposes classes
   by rule; review every one, and every changed class, before they go into `classifications.json`.
3. Read the diff of the command list, then update the golden file with
   `UPDATE_GOLDEN=1 go test ./tools/genmanifest`. A renamed or removed command is a breaking change.
4. Release: a minor version when no command disappears or is renamed, a major one otherwise.

Google sunsets each API version about a year after its release, so an old release of googleads
stops working when its version is sunset. Upgrade before then.

## Development

```sh
make build        # go build -o bin/googleads ./cmd/googleads
make test         # go test ./...
make generate     # regenerate internal/manifest/manifest.json.gz from the vendored Discovery document
make update-spec  # re-fetch spec/googleads-v25.discovery.json (network; VERSION=vNN for another version)
make check        # go vet + manifest drift check + tests: what CI runs
```

`e2e/smoke_test.go` builds the real binary and drives it against a fake Google. `e2e/live_test.go`
runs against a real account with your real configuration and changes nothing: it lists accessible
customers, reads the account, sends a complete paused Search campaign with `--validate-only`, and
checks that the same campaign unpaused is refused locally. It is skipped unless you run it with:

```sh
GOOGLE_ADS_LIVE=1 GOOGLE_ADS_LIVE_FINAL_URL=https://your-site.example go test ./e2e -run TestLive -v
```

This project follows SemVer. The public API is the command paths, flags, exit codes, the stderr
error schema and the `googleads commands --json` schema (`schema_version`). New optional keys may
appear in the catalog under the same `schema_version`, so consumers must ignore keys they do not
know.

## License

MIT, see [LICENSE](LICENSE).
