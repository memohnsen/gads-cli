# Google Ads CLI: Design

**Date:** 2026-09-25
**Status:** Draft for review (design approved in conversation on 2026-09-25)

## Goal

A single-binary CLI, written in Go, that lets AI agents and humans read and manage Google Ads
accounts through the Google Ads API from the shell: run GAQL reports, pull keyword ideas and
forecasts, and build and edit campaigns. The two decisions that cost money stay with a person:
switching a campaign on, and how much a budget may spend. Runs on Linux, macOS and Windows with no
runtime dependencies. Public open-source project under the MIT license.

- Binary: `googleads` (`gads` is taken by an older, unmaintained CLI).
- Module: `github.com/wir-drei-digital/google-ads-cli`.
- Not affiliated with or endorsed by Google; the README says so.

It is a sibling of `bexio-cli`, `klara-cli`, `cash-ctrl-cli` and `grav-cms-cli` and keeps their
stack and contracts on purpose, so an agent that knows one of them knows this one.

## What was verified before designing this

Checked on 2026-09-25 against Google's pages (most show "Last updated 2026-09-23") and the live
Discovery endpoint. Nothing below was tested with credentials yet; see *Open questions*.

| Question | Result |
| --- | --- |
| Current API version | v25 (v25.2 released 2026-09-23). v22 sunsets on 2026-10-07, v25 in August 2027. v26 is planned for October 2026 |
| Developer token | Sunset on 2026-09-09. The access level now belongs to the Google Cloud project that owns the credential. The `developer-token` header is optional and ignored, and a future major version will reject it |
| Manager account | No longer required. Needed only to manage several accounts through one login |
| Access levels | Test: test accounts only. Explorer: production accounts, 2,880 operations per day, **planning services blocked** (KeywordPlanIdeaService and others), as are account creation, user management and billing. Basic: 15,000 operations per day, requires brand verification of the Cloud project. Standard: unlimited |
| Service accounts | Can be added directly as a user in Google Ads (Admin, Access and security). No Workspace, no domain-wide delegation, no consent screen, no refresh token. Google recommends them for accounts you already have access to. One service-account email reaches up to 20 accounts; beyond that, through a manager account |
| REST surface | Same RPCs as gRPC. Discovery document at `https://googleads.googleapis.com/$discovery/rest?version=v25`: 3.28 MB, revision 20260923, 175 methods (12 GET, 162 POST, 1 DELETE), 1,489 schemas, 78 per-resource `mutate` methods, a generic `googleAds:mutate` with 64 operation types, 91 methods whose request carries `validateOnly`, 38 methods without `{+customerId}` in the path |
| JSON mapping | lowerCamelCase field names; parsers also accept the proto (snake_case) names. int64 values are decimal strings on output; numbers or strings are accepted on input. Enums are names on output; parsers also accept their numeric values (proto3 JSON) |
| Campaign default | Discovery, `Campaign.status`: "When a new campaign is added, the status defaults to ENABLED." |
| Budget fields | `amountMicros` is the average daily amount (period `DAILY`, the default). `totalAmountMicros` applies only to `CUSTOM_PERIOD`. `explicitlyShared` defaults to true on create |
| Paging | `googleAds:search` returns fixed pages of 10,000 rows with `nextPageToken`; `pageSize` is rejected. `searchStream` returns one JSON array of batches |
| EU political declaration | `containsEuPoliticalAdvertising` is required on campaign create. Since 2026-04-01 one undeclared campaign blocks every campaign mutate in the account (`EU_POLITICAL_ADVERTISING_DECLARATION_REQUIRED`) |
| Official MCP server | `googleads/google-ads-mcp` v0.0.3 (2026-09-10) is read-only: `search`, `get_resource_metadata`, `list_accessible_customers` |
| Go client | No official library. The community `shenzhencenter/google-ads-pb` is gRPC-only |

Sources: [sunset dates](https://developers.google.com/google-ads/api/docs/sunset-dates),
[developer token](https://developers.google.com/google-ads/api/docs/api-policy/developer-token),
[access levels](https://developers.google.com/google-ads/api/docs/api-policy/access-levels),
[service accounts](https://developers.google.com/google-ads/api/docs/oauth/service-accounts),
[REST search](https://developers.google.com/google-ads/api/rest/common/search),
[REST mutate](https://developers.google.com/google-ads/api/rest/common/mutate),
[JSON mappings](https://developers.google.com/google-ads/api/rest/design/json-mappings),
[proto3 JSON](https://protobuf.dev/programming-guides/json/),
[EU political advertising](https://developers.google.com/google-ads/api/docs/api-policy/eu-par),
[Search campaign guide](https://developers.google.com/google-ads/api/docs/campaigns/search-campaigns/getting-started),
[google-ads-mcp](https://github.com/googleads/google-ads-mcp),
[client libraries](https://developers.google.com/google-ads/api/docs/client-libs).

## Requirements

- **Full coverage, generated.** Every method of the pinned API version is a command, generated from
  the vendored Discovery document. New methods come from regenerating, not from hand-written
  commands.
- **Service-account auth only.** No browser, no refresh token, no developer token.
- **Agent-first UX.** JSON in, JSON out, one line of JSON per failure, stable exit codes,
  self-describing help and a machine-readable catalog.
- **Spend stays with a person.** Payload-aware risk classes, `--force`, a hard budget cap,
  server-side validation without effect, and a read-only mode.
- **Public OSS.** README, SECURITY.md, `docs/agents.md`, CI, versioned multi-platform releases.

## What is different from bexio-cli

| Concern | bexio-cli | google-ads-cli |
| --- | --- | --- |
| Command source | OpenAPI extracted from the docs page | Google's Discovery document for one API version, vendored |
| Command names | from `operationId` | from the Discovery method id (see *Command naming*) |
| Credential | PAT or OAuth login | Service-account key, exchanged for one-hour access tokens |
| Account selection | one company per token | `--customer-id` per call, default from config; `login-customer-id` for access through a manager |
| Risk | per operation, from a reviewed table | per method from a reviewed table, **plus a check of every operation in a mutate body**, plus a budget cap |
| Dry run | none | `--validate-only`: Google validates the request fully and applies nothing |
| Paging | offset or page envelope | page tokens; `--all` merges the result rows |
| API version | unversioned paths | pinned to `v25`; a new version is a spec refresh plus review |
| Error kinds | ten | the same ten plus `partial_failure` and `conflict` |

Everything else is carried over deliberately: Go and cobra, a single static binary
(`CGO_ENABLED=0`), stdout reserved for the response, one line of JSON on stderr per failure, exit
codes 0/1/2, `commands --json`, the `api` escape hatch, `--data` from an argument, a file or stdin,
`--output`, `--all` with `--max-pages`, refusal to follow redirects, pinned hosts with an explicit
opt-in for others, secrets never from argv, the config file conventions and goreleaser releases.

## Architecture

```
google-ads-cli/
├── cmd/googleads/          # main: hands argv to internal/cli
├── internal/cli/           # command tree, flags, output, auth/config/init/schema commands
├── internal/api/           # HTTP client: headers, host pinning, retries, errors, paging
├── internal/auth/          # service-account key parsing, JWT exchange, token cache
├── internal/guard/         # risk resolution: method class, payload check, budget cap
├── internal/config/        # config file and environment resolution
├── internal/manifest/      # manifest types + embedded manifest.json.gz
├── tools/fetchspec/        # downloads and validates a Discovery document
├── tools/genmanifest/      # Discovery document -> manifest (naming, overrides, classifications)
├── spec/googleads-v25.discovery.json
├── spec/PROVENANCE.json
├── e2e/                    # smoke test against a fake server; manual live test
└── docs/agents.md
```

Code is copied from `bexio-cli` and adapted where the behaviour is the same: the API client
skeleton, the retry loop, error output, config file handling, and the CLI scaffolding for `--data`,
`--all`, `commands`, `api` and `config`. The sibling CLIs share no module. `internal/guard`, the
service-account auth and the Discovery generator are new.

### Build-time pipeline

1. **fetchspec** (`make update-spec VERSION=v25`) downloads
   `https://googleads.googleapis.com/$discovery/rest?version=<version>`, validates it (`kind` is
   `discovery#restDescription`, `version` matches, at least 150 methods, schemas present) and writes
   `spec/googleads-<version>.discovery.json` and `spec/PROVENANCE.json` (URL, retrieval time,
   SHA-256, Discovery revision). It writes nothing when validation fails.
2. **genmanifest** reads the vendored document and writes `internal/manifest/manifest.json.gz`.
   Per method it records the command path, the Discovery method id, the HTTP method, the URL
   template (built from `path`, which uses `{+customerId}`, never from `flatPath`), path and query
   parameters, a body summary and example skeleton, whether the request supports `validateOnly`
   and `partialFailure`, the paging fields, whether the path needs a customer, the risk class and
   the help text. It also stores a compact summary of every schema (field name, type or referenced
   schema, repeated, read-only, and the "Required." and "Immutable." markers Google puts at the
   start of descriptions) and, for mutate methods, which resource each operation type creates or
   updates. Discovery's global parameters (`alt`, `fields`, `key`, `access_token`, `oauth_token`,
   `quotaUser`, `$.xgafv` and the like) are not exposed.
3. The generator **fails** on a method without a reviewed risk class, a classification entry for a
   method that no longer exists, a command-name collision, a generic mutate operation type it
   cannot map to a resource, or a skiplist entry without a reason or without a matching method.

The vendored document and the manifest are both committed, so `go build` works offline. CI
regenerates the manifest and fails on drift.

### Command naming

The method id is Discovery's `id` without its `googleads.` prefix (`googleads.customers.campaigns.mutate`
becomes `customers.campaigns.mutate`); it contains no version, so ids and commands survive a
version change. Drop a leading `customers` segment and kebab-case what remains:

| Discovery method id | Command |
| --- | --- |
| `customers.campaigns.mutate` | `googleads campaigns mutate` |
| `customers.adGroupCriteria.mutate` | `googleads ad-group-criteria mutate` |
| `customers.generateKeywordIdeas` | `googleads generate-keyword-ideas` |
| `customers.listAccessibleCustomers` | `googleads list-accessible-customers` |
| `googleAdsFields.search` | `googleads google-ads-fields search` |
| `geoTargetConstants.suggest` | `googleads geo-target-constants suggest` |
| `listPlannableLocations` | `googleads list-plannable-locations` |

A checked-in override table puts the three methods almost all work goes through at the top, and
resolves the one collision the rule produces on v25:

| Discovery method id | Command |
| --- | --- |
| `customers.googleAds.search` | `googleads search` |
| `customers.googleAds.searchStream` | `googleads search-stream` |
| `customers.googleAds.mutate` | `googleads mutate` |
| `customers.mutate` (the account's own settings) | `googleads customer mutate` |

The generated command list is a golden file; a rename is a breaking change and must appear in the
release notes.

Skiplist in v1: `media.upload` (YouTube video upload through Google's media upload protocol, which
the client does not implement). The skiplist lives in `tools/genmanifest/overrides.json` with a
mandatory reason, and the README lists it.

## CLI contract

- **Shape:** `googleads <resource> <verb> [positional path params] [flags]`, or a single verb for
  methods that sit directly on a customer.
- **Customer:** the `customerId` path parameter is never positional. It comes from `--customer-id`,
  then `GOOGLE_ADS_CUSTOMER_ID`, then the config file. `123-456-7890` and `1234567890` are both
  accepted and normalised to ten digits; anything else is a usage error, and so is a missing
  customer on a method that needs one. Methods without a customer in their path ignore it.
- **Manager access:** `--login-customer-id`, `GOOGLE_ADS_LOGIN_CUSTOMER_ID` or the config file sets
  the `login-customer-id` header, normalised the same way. It is sent only when set.
- **Other path parameters** (for example the resource name of `google-ads-fields get`) are
  positional, in path order. Query parameters are flags.
- **Bodies:** `--data '{...}'`, `--data @file.json` or `--data -`, capped at 20 MB and sent as
  given, except for the fields the CLI owns: `--validate-only` sets `validateOnly: true` and
  `--partial-failure` sets `partialFailure: true`, overriding the body.
- **Reports:** `search` and `search-stream` also take `--query <GAQL>` or `--query @file.gaql`,
  which builds `{"query": ...}`. `--query` together with `--data` is a usage error.
- **Output:** the API response, untouched, on stdout. int64 values arrive as strings, as Google
  sends them. `--output <file>` writes the body to a file instead.
- **`--all`:** available on methods whose request has `pageToken` and whose response has
  `nextPageToken` (`search`, `generate-keyword-ideas`, `google-ads-fields search` and others). It
  follows the tokens and prints one JSON array of the merged result rows, the only transformation
  the CLI performs. The generator records which repeated field of each paged response holds the
  rows (`results` for the methods seen so far) and fails when it cannot tell. `--max-pages`
  (default 100) caps it; hitting the cap with data remaining prints
  the partial array and exits 1 with kind `incomplete`. `search --all` together with
  `returnSummaryRow` is a usage error, because the summary row would be lost in the merge.
- **Discovery:** `googleads commands --json` prints the catalog (`schema_version` 1, plus
  `api_version` and `discovery_revision`): per command the method id, HTTP method, path, risk
  class, whether it needs a customer, parameters, body summary, `validate_only`, `partial_failure`
  and paging. `googleads schema <Name>` prints the field table of any request or resource type, so
  nested types are reachable. It takes the short name, which is Google's schema name without the
  `GoogleAdsGoogleadsV25<Package>` prefix and the underscores after it (`Campaign`,
  `Campaign_NetworkSettings`, `ResponsiveSearchAdInfo`), lists the candidates when a short name is
  ambiguous, and also accepts the full name. `--help` on a per-resource mutate command shows the resource's fields with their
  markers and an example skeleton.
- **Escape hatch:** `googleads api <METHOD> <path>` with `--data`, `--query k=v` and
  `--header k:v`, for a path relative to `https://googleads.googleapis.com/` (for example
  `v25/customers/1234567890/googleAds:search`). It cannot set `Authorization` or the credential
  query parameters `access_token`, `oauth_token` and `key`, and it has no `--validate-only`.
  Guardrails apply as described below.
- **Misc:** `googleads version` prints the CLI version, the API version and the Discovery revision.
  Shell completion comes from cobra.

## Authentication

- **Credential:** a Google Cloud service-account key in JSON. The first source found wins:
  1. `GOOGLE_ADS_JSON_KEY_FILE_PATH`, a path to the key file (the variable name Google's own client
     libraries use);
  2. the key stored in the config file by `googleads config set service-account-key`, which reads
     the JSON from stdin only.
- **Validation:** `type` must be `service_account`, with `client_email`, `private_key_id` and a
  parseable RSA `private_key`. An OAuth client file (`installed`, `web`) or user credentials
  (`authorized_user`) are refused with a message saying what the file is.
- **Token:** JWT bearer grant (RFC 7523) through `golang.org/x/oauth2/jwt`: issuer `client_email`,
  scope `https://www.googleapis.com/auth/adwords`, no subject, one-hour lifetime, exchanged at
  `https://oauth2.googleapis.com/token`. The key file's own `token_uri` is ignored; the token URL is
  pinned and can only be changed with `GOOGLE_ADS_TOKEN_URL` together with
  `GOOGLE_ADS_ALLOW_CUSTOM_BASE=1` (for tests).
- **Cache:** the access token is cached in `<user cache dir>/googleads/token.json` (file `0600`,
  directory `0700`, written through a temp file and rename), keyed by client email, key id and
  token URL, and reused while at least 60 seconds of validity remain. There is no lock: two
  processes that mint at the same time both receive valid tokens. An unreadable cache is ignored.
- **Headers:** `Authorization: Bearer <token>` and, when set, `login-customer-id`. No
  `developer-token`.
- **`googleads auth status`:** offline, one line of JSON: `mode` (`service_account` or `none`),
  `source` (`env` or `config`), `client_email`, `private_key_id`, `project_id`,
  `token_cached_until`, `customer_id`, `login_customer_id`, `budget_cap`, `read_only`, and
  `missing` plus `hint` when the configuration cannot make a call. Never a key or a token.
- **`googleads auth token`:** prints a valid access token to stdout for piping into another tool.
  Its help says that it writes a credential.
- **Revocation** is deleting the key in the Cloud Console. `googleads config unset
  service-account-key` removes the local copy only, and says so.

## Configuration

- File: `<user config dir>/googleads/config.json`, `0600` inside a `0700` directory, written
  through a temp file and rename. `googleads config path` prints the location.
- Keys for `config set` and `config unset`: `service-account-key` (stdin only), `customer-id`,
  `login-customer-id`, `read-only` (`true` or `false`), `budget-cap`. Passing an environment
  variable name or an unknown key is an error that names the valid keys.
- Environment variables, which win over the file: `GOOGLE_ADS_JSON_KEY_FILE_PATH`,
  `GOOGLE_ADS_CUSTOMER_ID`, `GOOGLE_ADS_LOGIN_CUSTOMER_ID`, `GOOGLE_ADS_READ_ONLY`,
  `GOOGLE_ADS_API_BASE`, `GOOGLE_ADS_TOKEN_URL`, `GOOGLE_ADS_ALLOW_CUSTOM_BASE`.
- **The budget cap has no environment variable and no flag.** It lives in the config file only.
- `GOOGLE_ADS_READ_ONLY` only switches read-only mode on; it can never switch off a read-only mode
  set in the config file. To turn that off, run `googleads config unset read-only`.
- **`googleads init`** needs a terminal and refuses without one. It asks for the path of a key file,
  validates and stores the key, asks for the customer ID, then verifies with
  `list-accessible-customers` and a `search` for `customer.descriptive_name`,
  `customer.currency_code` and `customer.time_zone`. It shows the account name and currency, asks
  for the budget cap in that currency, and saves. When verification fails it prints the error with
  its hint and saves nothing; the non-interactive `config set` path stays available. Once the key
  is stored it reminds the user to delete the downloaded key file.

## Guardrails

### Risk classes

Every method has exactly one reviewed class in `tools/genmanifest/classifications.json`, keyed by
Discovery method id. `go run ./tools/genmanifest -write-proposals` proposes classes by rule for new
methods; a person reviews them before the manifest can be generated.

| Class | Meaning | Examples | Gate |
| --- | --- | --- | --- |
| `read` | reads or computes, changes nothing | `search`, `search-stream`, `generate-keyword-ideas`, `generate-keyword-forecast-metrics`, `google-ads-fields search`, `geo-target-constants suggest`, `list-accessible-customers`, `recommendations generate` | none; allowed in read-only mode |
| `write` | creates or edits campaign entities | most per-resource `mutate` methods, the generic `mutate`, `customer mutate`, `recommendations dismiss` | blocked in read-only mode |
| `delete` | removes something | any `remove` operation, `operations delete`, removing automatically created assets | `--force` |
| `spend` | can start spending or change how much | switching a campaign on, changing a budget amount, promoting, graduating or scheduling experiments, `campaign-drafts promote` | `--force` |
| `admin` | outside campaign editing | users and invitations, billing setups and account budgets, account, data and product links, new client accounts, manager links, conversion and customer-data uploads, offline user data jobs, batch jobs (they run later, as a whole), messages to Local Services leads, identity verification, incentives | `--force` |

A call with `--validate-only` skips the `--force` gate and is allowed in read-only mode; see
*Gates and modes*.

**Blocked in v1.** Three methods can set a budget amount or a spend commitment that the request
itself does not show, so the budget cap could not hold for them: `recommendations apply` (a budget
recommendation applies Google's amount), `recommendation-subscriptions
mutate-recommendation-subscription` (auto-apply) and `reservations book-campaigns`. They are
refused before any network I/O, as commands and through `api`, whatever the flags. They stay in
the command tree and in the catalog (`blocked: true` with the reason) so a caller learns why. Each
is listed with its reason in `tools/genmanifest/overrides.json` and in the README. An agent can still read
recommendations with `search` and carry out a worthwhile one through `mutate`, where the checks
apply.

### Payload check

One class per method is not enough, because one `mutate` call can do anything. Before a mutate is
sent (the generic `mutate`, every per-resource `mutate`, `batch-jobs add-operations`, whose body
carries the same `mutateOperations`, and `api` calls to those paths), `internal/guard` reads every
operation in the body. The highest class found wins, with `read < write < delete, spend, admin`;
`batch-jobs add-operations` stays `admin` whatever it carries, but the refusals below, including
the budget cap, apply to its operations too.

1. A `remove` operation is `delete`.
2. A campaign `create` whose `status` is not exactly the string `"PAUSED"` is `spend`. That
   includes a missing status, because Google's default is `ENABLED`, and numeric enum values.
3. A campaign `update` that sets `status` to anything other than `"PAUSED"` is `spend`.
4. A campaign `update` that sets `campaignBudget` (moving the campaign to another budget) is
   `spend`.
5. A budget `create` or `update` that sets `amountMicros` is checked against the cap. Without a
   configured cap, or with an amount above it, the request is refused with kind `usage`. `--force`
   and `--validate-only` do not change that.
6. A budget `update` that sets `amountMicros` within the cap is `spend`, whether it raises or lowers
   the amount: the CLI does not look up the current value. Pausing the campaign is the way to stop
   spend without `--force`.
7. A budget that sets `totalAmountMicros` or `period: CUSTOM_PERIOD` is refused in v1. The cap is a
   daily cap.
8. Every other operation takes the class of the method it belongs to. An operation type in the
   generic `mutate` takes the class of its per-resource method (`campaignOperation` takes the class
   of `customers.campaigns.mutate`); an unknown operation type is `admin`.

How the body is read:

- A field counts as set when it appears in the operation's object **or** in its `updateMask`.
- Field names are matched in both spellings Google accepts (`amountMicros` and `amount_micros`,
  `campaignOperation` and `campaign_operation`). The body that is sent is not modified.
- A body with a duplicate key, or with both spellings of one field in the same object, is refused:
  the check has to see exactly what Google will see.
- Amounts are accepted as JSON numbers or decimal strings and compared as exact integers. A value
  that is not a non-negative integer refuses the request.
- A mutate body that is not valid JSON is refused. The CLI does not send what it cannot inspect.
- `api` calls to a `:mutate` path in another API version get the same checks; the resource is taken
  from the path segment (`campaigns:mutate`, `campaignBudgets:mutate`, `googleAds:mutate`).

### Budget cap

- Set with `googleads config set budget-cap <amount>`, in the account's currency (for example `30`
  or `29.61`, at most six decimal places), stored as integer micros. Config file only.
- Applies to every daily budget amount in every request, independent of `--force` and
  `--validate-only`.
- Without a cap no budget amount can be set at all.
- It limits each budget, not the account total: two campaigns with their own budgets can together
  spend twice the cap. Switching each of them on is a separate `--force` decision.
- Google may spend up to twice the daily budget on a single day and bills at most 30.4 times the
  daily budget in a month. The README states this next to the cap.

### Gates and modes

- **`--force`** is a per-call flag with no environment or config equivalent. Without it, a `delete`,
  `spend` or `admin` call exits 2 with kind `usage` before any network I/O, and the message names
  each operation that needs it and why (for example "operation 2: campaign created without status
  PAUSED; Google's default is ENABLED").
- **`--validate-only`** exists on the 91 methods whose request supports it. Google validates the
  request completely and applies nothing. Such a call skips the `--force` gate and is allowed in
  read-only mode; the budget cap and the refusals above still apply. Only the flag counts: a
  `validateOnly` field inside `--data` does not relax any gate.
- **Read-only mode** (`GOOGLE_ADS_READ_ONLY=1` or `config set read-only true`) allows `read`-class
  calls and `--validate-only` calls only. Under it, `api` allows GET and calls that match a
  `read`-class method.
- **`api` escape hatch:** a path that matches a manifest method inherits its class and the payload
  check. An unmatched POST, PATCH or DELETE is `admin`.
- **Not gated**, by decision: bids and bidding strategies, enabling ad groups, ads and keywords,
  negative keywords, conversion actions, and pausing anything. The budget bounds what those can
  cost, and switching a campaign on is gated.

### What the CLI cannot do

It cannot stop a caller with shell access from passing `--force` or changing the cap in the config
file. It makes both explicit and visible. The other layers belong to the operator: the rules they
give their agents, and the access level the service account holds in Google Ads (Standard access
cannot manage users). Integration into an operator's own workspace, such as agent rules and
pointers to where the key lives, happens in that workspace and is not part of this repository.

## Errors, exit codes, retries

- **Exit codes:** `0` success, `1` API or network error, `2` usage error (including every guardrail
  refusal).
- **Error line** on stderr: `{"kind","error","status","details","request_id"}`. `status` is the HTTP
  status when a response exists. `details` is the parsed error body (for Google Ads, the
  `GoogleAdsFailure` with all its errors). `request_id` comes from the `request-id` response header
  or the failure and is omitted when absent.
- **Kinds:** bexio's ten (`auth`, `forbidden`, `not_found`, `validation`, `rate_limited`, `server`,
  `transport`, `outcome_unknown`, `incomplete`, `usage`) plus:
  - `partial_failure`: with `--partial-failure`, a 200 response carrying `partialFailureError`
    prints the full response on stdout and exits 1;
  - `conflict`: HTTP 409, or a failure whose error is `databaseError.CONCURRENT_MODIFICATION`.
    Without `--partial-failure` the request was rejected as a whole, and Google documents this
    error as retryable after a short wait.
- **Mapping:** 400 to `validation`, 401 to `auth`, 403 to `forbidden`, 404 to `not_found`, 409 to
  `conflict`, 429 to `rate_limited`, 5xx to `server`, 3xx to `validation` (redirects are refused and
  the message names the `Location`). A concurrent-modification failure is `conflict` whatever its
  status.
- **Message:** it leads with Google's first error rather than the generic status text: the error
  code as `category.VALUE`, its message, and the field path when present, for example
  `POST v25/customers/1234567890/googleAds:mutate: HTTP 400: fieldError.REQUIRED: The required field was not present. (mutate_operations[0].campaign_operation.create.contains_eu_political_advertising)`.
- **Hints** appended to the message for errors expected during setup:
  - `CLOUD_PROJECT_NOT_APPROVED_FOR_PRODUCTION`: the Cloud project still has Test access; apply for
    Explorer or Basic.
  - a planning method (keyword ideas, historical metrics, forecasts, reach, audience insights)
    refused with `forbidden`: Explorer access blocks planning services; Basic is required.
  - `USER_PERMISSION_DENIED`: the service account has no access to this customer; add it in Google
    Ads under Admin, Access and security, or set `login-customer-id` to the manager account it was
    added to.
  - `EU_POLITICAL_ADVERTISING_DECLARATION_REQUIRED`: a campaign in the account lacks the
    declaration; the hint names the `search` that finds it.
- **Retries:** 429 is retried for every call, waiting for the `retryDelay` in Google's
  `QuotaErrorDetails` when present and otherwise with exponential backoff and jitter, within a total
  budget of 60 seconds. When the requested delay exceeds the remaining budget (a daily quota is used
  up), the call fails at once with `rate_limited` and the delay in the message. Transport errors and
  5xx are retried only for `read`-class calls, which includes the POST-based `search`, up to three
  attempts. A mutation that fails in flight is never replayed and ends as `outcome_unknown`.
- **Deadlines:** `--timeout` per attempt, default 60 seconds. Ctrl-C and SIGTERM cancel the request
  and any retry wait.

## Testing

- **Generator**, against the vendored document: every method classified or skiplisted with a
  reason, no command collisions, every generic mutate operation type mapped to a resource, golden
  command list with risk classes. CI regenerates the manifest and fails on drift.
- **Guard**, table-driven: each rule above; both spellings; duplicate keys and mixed spellings;
  numbers, strings, exponents and fractions as amounts; a request with several operations where the
  highest class wins; exactly at the cap and one micro above; no cap; `totalAmountMicros`;
  changes carried only by `updateMask`; `--validate-only`; read-only mode; `api` paths in another
  version and unmatched paths; `batch-jobs add-operations` bodies; the blocked methods, as commands
  and through `api`.
- **Auth**, against an `httptest` token endpoint: the JWT is decoded and its issuer, scope,
  audience, expiry and RS256 signature are verified against the test key; cache reuse, expiry and a
  corrupt cache; rejection of OAuth client files and user credentials; token URL pinning.
- **API client**, against a fake Google Ads server: headers (no `developer-token`,
  `login-customer-id` only when set), flag-owned body fields, error mapping with hints and
  `request_id`, retries (429 with `retryDelay`, an exhausted budget, retries of the POST-based
  `search`, no replay of mutations), `--all` and `--max-pages`, partial failure, refused redirects,
  the custom-base opt-in.
- **CLI:** `config set`, `unset` and `path` semantics, the stdin-only key, `init` refusing without a
  terminal, `auth status` output, the `commands --json` schema, `schema <Name>`.
- **End to end:** the built binary against the fake server.
- **Live, manual, not in CI** (`GOOGLE_ADS_LIVE=1` with a configured key and customer):
  1. `list-accessible-customers` lists the customer;
  2. `search` returns the account name, currency and time zone;
  3. `mutate --validate-only` of a complete minimal Search campaign built with temporary resource
     names (a budget with `explicitlyShared: false`, a paused campaign with
     `containsEuPoliticalAdvertising` and Google Search as the only network, an ad group, one exact
     and one phrase keyword, one responsive search ad, one location criterion) returns 200 and
     creates nothing. It carries no language criterion: Google announced that Search campaigns
     stop accepting them through the API from late September 2026;
  4. the same payload without `--validate-only` and without `status: PAUSED` is refused locally.

  A create-and-remove round trip runs only against a Google test account.

## CI and release

- GitHub Actions on Ubuntu, macOS and Windows: `go vet`, tests and the manifest drift check
  (`make check`).
- goreleaser on tag: darwin amd64/arm64, linux amd64/arm64, windows amd64, `checksums.txt`, GitHub
  Release. `go install github.com/wir-drei-digital/google-ads-cli/cmd/googleads@latest` works too.
- SemVer. The public API is the command paths, flags, exit codes, the stderr error schema and the
  `commands --json` schema. Additive optional fields are allowed under the same `schema_version`.
- First release `v0.1.0` after the live checks pass.
- **API version policy:** one pinned version. Moving to a new one is `make update-spec
  VERSION=vNN`, `make generate`, a review of new methods and changed classes, and a minor release
  when no command disappears or is renamed, a major one otherwise. Google sunsets a version about a
  year after its release, so an old release of this CLI stops working; the README says so.

## Documentation

- **README:** install, operator setup, authentication, usage, guardrails, error contract, skiplist,
  API version policy, and that the project is not affiliated with Google.
- **SECURITY.md:** what a service-account key is (no expiry, works from any machine that has it,
  revoked only by deleting it in the Cloud Console), one key per machine, storing it in the config
  file versus pointing `GOOGLE_ADS_JSON_KEY_FILE_PATH` at it, the cached access token (one hour at
  most), and what the CLI never does (other hosts, redirects, logging credentials, secrets from
  argv).
- **docs/agents.md:** the paste-in agent reference: the catalog and `schema`, `search` with GAQL,
  micros (one unit of currency is 1,000,000 micros), int64 values as strings, temporary resource
  names for building a campaign in one `mutate`, `--validate-only` first, never `--force` or a cap
  change without a person's go-ahead, error kinds and hints, and one worked example of a paused
  Search campaign.

## Operator setup (once)

1. A Google Cloud project that is not on the Free Trial and has no suspended billing. Enable the
   Google Ads API.
2. Apply for API access on the project's Google Ads API page in the Cloud Console. Explorer is
   enough for campaign work; Keyword Planner needs Basic, which needs brand verification of the
   project. Google may revoke access that goes unused for 90 days.
3. Create a service account (it needs no IAM roles) and one JSON key per machine. A key can be
   downloaded only once.
4. In Google Ads, under Admin, Access and security, add the service account's email with Standard
   access.
5. On each machine, run `googleads init`, or `config set` for each key, including a budget cap.
6. In an account that already has campaigns, look for campaigns missing the EU political
   declaration before the first change.

## Out of scope for v1

- Browser (OAuth) login for people, and keyless service-account use (impersonation, Application
  Default Credentials).
- An MCP server mode.
- An account-wide spend total, per-customer caps, profiles for many client accounts.
- Tested support for conversion and customer-data uploads. The methods exist as `admin`-class
  commands; Google points new upload integrations to its Data Manager API.
- YouTube video upload (`media.upload`).
- Human-readable table output, OS keychain storage, a Homebrew tap, artifact signing.

## Open questions

These need credentials and are settled by the live checks:

- The exact error code Explorer returns for planning services, for the hint.
- How brand verification works for a project that only uses a service account.
- Whether `campaign.missing_eu_political_advertising_declaration` can be filtered in GAQL as the
  hint assumes.
- The real shape of rows and failures over REST (int64 strings, zero metrics, field paths), and
  the HTTP status Google uses for a concurrent modification.
