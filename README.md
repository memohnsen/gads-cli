# gads-cli (`googleads`)

A command-line client for the [Google Ads API](https://developers.google.com/google-ads/api),
built for agents and scripts: JSON in, JSON out, one static binary, no runtime dependencies, and
hard gates on anything that can spend money.

This is a fork of [wir-drei-digital/google-ads-cli](https://github.com/wir-drei-digital/google-ads-cli)
(MIT). Upstream generates the whole Google Ads REST surface from Google's Discovery document and
authenticates with a service account without a developer token. This fork adds:

- **Dry-run by default.** Every write is sent with `validate_only` unless you pass `--apply`.
- **Two-flag spend gate.** Enabling things, budgets, bids and bid strategies, conversion changes
  that feed bidding, billing, admin calls and deletes need `--apply --confirm-spend`.
- **PAUSED creates.** Campaign, ad group, ad and keyword creates must say `"status":"PAUSED"`.
- **Account-wide budget cap**, on top of upstream's per-budget cap.
- **Separate reader and writer credentials**, loaded from 1Password, an env-named `chmod 600` file,
  or a `0600` file under the config directory. Read-only mode is on by default.
- **Audit log**: one JSON line for every write attempt.
- **First-class wrappers** (`list`, `get`, `pause`, `enable`, `create`, `update`, `link`, `unlink`)
  and CSV/table output for reports.
- **Google Ads API v26.**

stdout carries the API response and nothing else; every failure leaves one line of JSON on stderr.
gads-cli is not affiliated with or endorsed by Google.

## Install

Prebuilt binaries for Linux and macOS (amd64, arm64) are on
[GitHub Releases](https://github.com/memohnsen/gads-cli/releases), with `checksums.txt`. Unpack,
put `googleads` on your `PATH`, and add the short alias:

```sh
ln -s googleads "$(dirname "$(command -v googleads)")/gads"
```

From source (Go 1.26 or newer):

```sh
git clone https://github.com/memohnsen/gads-cli && cd gads-cli
make install            # builds bin/googleads, installs it and a `gads` symlink into ~/.local/bin
# or: go install github.com/memohnsen/gads-cli/cmd/googleads@latest
```

`googleads` and `gads` are the same binary; the examples below use `gads`.

## Prerequisites (done once, by a person)

The CLI cannot do these for you, and it never creates Cloud or Ads resources itself.

- [ ] **Cloud project.** Create or choose a Google Cloud project you own that is not on a
      suspended billing account.
- [ ] **Enable the Google Ads API** on that project. This grants Test access immediately (test
      accounts only).
- [ ] **Apply for Explorer access** on the project's Google Ads API overview page in the Cloud
      Console. It is automated and usually takes minutes. Explorer covers everything in this CLI
      except planning services (Keyword Planner, forecasts, audience insights), which need Basic.
- [ ] **Two service accounts** in that project, neither with IAM roles:
      - a **reader**, added to Google Ads with **Read only** access, used for reports and dry runs;
      - a **writer**, added with **Standard** access, used only for calls sent with `--apply`.
      Download each JSON key once and put it straight into a password manager (1Password works
      directly, see below) or a `chmod 600` file. Never paste a key into a chat, an issue or a repo.
- [ ] **Grant access in Google Ads**: Admin > Access and security, add each service-account email
      to your account with the matching level (Read only, Standard). Service accounts cannot be
      "Email only". Do not grant Admin.
- [ ] **Configure the CLI** on each machine (next section), including a budget cap.
- [ ] In an account with existing campaigns, find campaigns that lack the EU political advertising
      declaration; one undeclared campaign blocks every campaign change:
      `gads search --query "SELECT campaign.id, campaign.name FROM campaign WHERE campaign.missing_eu_political_advertising_declaration = TRUE"`

No developer token is used. Google retired it on 2026-09-09; access now belongs to the Cloud
project. The CLI never sends a `developer-token` header and refuses to. If your organisation forbids
service accounts, user OAuth is the documented fallback in Google's docs, but this CLI does not
implement it.

## Configuration

```sh
gads config set customer-id 123-456-7890        # the account to act on by default
gads config set budget-cap 30                    # the most daily budget the account may total
gads config set login-customer-id 111-222-3333   # only if access goes through a manager account
```

### Credentials

Each role (`reader`, `writer`) takes its key from the first source configured, in this order:

1. **1Password**: `gads config set op-ref op://<vault>/<item>/<field>` (add `--role writer` for the
   writer), or `GOOGLE_ADS_OP_READER_REF` / `GOOGLE_ADS_OP_WRITER_REF`. The CLI runs
   `op read --no-newline <ref>` with stdin closed when it needs the key, so the key never lands on
   disk. `op` must be signed in (desktop-app integration or `OP_SERVICE_ACCOUNT_TOKEN`).
2. **A key file named by the environment**: `GOOGLE_ADS_JSON_KEY_FILE_PATH` (reader) and
   `GOOGLE_ADS_WRITE_JSON_KEY_FILE_PATH` (writer). The file must be `chmod 600`; a key readable by
   group or others is refused.
3. **A stored key file**: `gads config set service-account-key [--role writer] < key.json` reads the
   key from stdin, checks it, and writes `<config dir>/gads/credentials/<role>.json` with mode
   `0600` in a `0700` directory. Delete the download afterwards.

The config file itself never holds a key. Reads and dry runs use the reader; `--apply` uses the
writer, and fails if no writer is configured. If your account refuses `validate_only` requests
from a Read only user, run `gads config set dry-run-credential writer`; dry runs still apply
nothing. With only one Standard key at first, configure it for both roles and keep read-only mode on
until you mean to write.

`gads auth status` prints one line of JSON: each role's source, client email, project and the
last characters of its key id, plus the customer, budget cap, read-only state and audit log path.
It never prints a private key or a token, and there is no command that prints a token.

`gads init` is an interactive wizard for a person at a terminal that does the same as the commands
above.

### Read-only mode

Read-only mode is **on by default**. It allows reads and dry runs and refuses every `--apply`. A
person turns it off with `gads config set read-only false`. `GOOGLE_ADS_READ_ONLY=1` can switch it
back on for one environment (an agent's, say) but no environment variable can switch it off.

### All settings

| `config set` key | Value | Environment |
| --- | --- | --- |
| `customer-id` | default customer, `123-456-7890` or `1234567890` | `GOOGLE_ADS_CUSTOMER_ID` wins |
| `login-customer-id` | manager account the access goes through | `GOOGLE_ADS_LOGIN_CUSTOMER_ID` wins |
| `op-ref [--role]` | 1Password secret reference to a key | `GOOGLE_ADS_OP_READER_REF`, `GOOGLE_ADS_OP_WRITER_REF` win |
| `service-account-key [--role]` | key JSON on stdin, stored `0600` | `GOOGLE_ADS_JSON_KEY_FILE_PATH`, `GOOGLE_ADS_WRITE_JSON_KEY_FILE_PATH` win |
| `read-only` | `true` / `false` (default `true`) | `GOOGLE_ADS_READ_ONLY=1` can only switch it on |
| `budget-cap` | amount in the account currency (`30`, `29.61`) | none, on purpose |
| `audit-log` | absolute path of the audit log | none, on purpose |
| `dry-run-credential` | `reader` (default) or `writer` | none |

`gads config unset <key>` removes a value; `gads config path` prints the config file location
(`<user config dir>/gads/config.json`). For tests and proxies, `GOOGLE_ADS_API_BASE` and
`GOOGLE_ADS_TOKEN_URL` work only together with `GOOGLE_ADS_ALLOW_CUSTOM_BASE=1`.

## Usage

### Everyday operations

```sh
# Reports (GAQL). Each list/get follows every page and prints one JSON array; --format csv|table too.
gads list campaigns
gads list ad-groups --campaign-id 123
gads list keywords --ad-group-id 456 --format table
gads list ads | negatives | budgets | assets | sitelinks | sitelink-links | conversion-actions
gads get campaign 123
gads get keyword 456~789                      # <adGroupId>~<criterionId>
gads list keywords --show-query               # print the GAQL instead of running it

# Creates are always PAUSED. Without --apply each command is a dry run.
gads create budget --name "Brand" --amount 20 --validate-only               # budgets are spend-class
gads create campaign --name "Brand" --budget-id 111 --location 2840 \
    --bidding maximize-clicks --max-cpc 2
gads create ad-group --campaign-id 123 --name "Shoes" --cpc-bid 1.50
gads create keywords --ad-group-id 456 --keyword "running shoes" --keyword "trail shoes" --match-type PHRASE
gads create keywords --campaign-id 123 --negative --keyword free --match-type BROAD
gads create rsa --ad-group-id 456 --final-url https://example.com \
    --headline "Run faster" --headline "Free returns" --headline "Shop shoes" \
    --description "Lightweight running shoes." --description "Ships in 24 hours." --path1 shoes
gads create sitelink --link-text "Pricing" --final-url https://example.com/pricing --campaign-id 123
gads create text-asset --text "Free shipping"
gads create image-asset --file logo.png --name "Logo"
gads create conversion-action --name "Lead form" --category SUBMIT_LEAD_FORM    # secondary by default

# Updates
gads update campaign 123 --name "Brand 2026"
gads update rsa 999 --headline A --headline B --headline C                       # <adId>
gads update keyword 456~789 --final-url https://example.com/shoes
gads update conversion-action 321 --primary=false

# Status
gads pause campaign 123 --apply
gads pause keyword 456~789 --apply
gads enable campaign 123 --validate-only                                         # dry run of a spend change
gads enable campaign 123 --apply --confirm-spend                                 # a person's decision

# Sitelinks on existing assets
gads link sitelink 888 --campaign-id 123 --apply
gads unlink sitelink 888 --campaign-id 123 --apply --confirm-spend               # removing is delete-class
```

Money flags take amounts in the account currency (`1.50`), never micros. Every wrapper prints the
same output and goes through the same gates and audit log as the raw commands below.

### The full API

The generated command tree covers every method of the API version, with the request body as JSON:

```sh
gads search --query "SELECT campaign.id, campaign.name, campaign.status FROM campaign"
gads search --all --format csv --query @report.gaql      # every page, as CSV
gads mutate --data @campaign.json                        # generic mutate; dry run
gads mutate --data @campaign.json --apply                # apply
gads campaigns mutate --help                             # fields, example body, risk class
gads schema Campaign                                     # fields of any type
gads commands --json                                     # machine-readable catalog
gads api GET v26/customers:listAccessibleCustomers       # raw escape hatch; same gates
```

A whole Search campaign can go in one `mutate` with temporary resource names (negative ids)
that later operations reference:

```json
{"mutateOperations":[
  {"campaignBudgetOperation":{"create":{"resourceName":"customers/1234567890/campaignBudgets/-1","name":"Brand","amountMicros":"20000000","deliveryMethod":"STANDARD","explicitlyShared":false}}},
  {"campaignOperation":{"create":{"resourceName":"customers/1234567890/campaigns/-2","name":"Brand","status":"PAUSED","advertisingChannelType":"SEARCH","campaignBudget":"customers/1234567890/campaignBudgets/-1","manualCpc":{},"networkSettings":{"targetGoogleSearch":true,"targetSearchNetwork":false,"targetContentNetwork":false,"targetPartnerSearchNetwork":false},"containsEuPoliticalAdvertising":"DOES_NOT_CONTAIN_EU_POLITICAL_ADVERTISING"}}},
  {"adGroupOperation":{"create":{"resourceName":"customers/1234567890/adGroups/-3","name":"Brand","campaign":"customers/1234567890/campaigns/-2","type":"SEARCH_STANDARD","status":"PAUSED","cpcBidMicros":"1000000"}}}
]}
```

Because it creates a budget, that request is spend-class: dry-run it with `--validate-only`, apply
it with `--apply --confirm-spend`.

Bodies come from `--data '{...}'`, `--data @file.json` or `--data -`. Responses are Google's
camelCase JSON; int64 values are strings and money is in micros (1 unit = 1,000,000 micros).
`--all` merges every page into one array; `--format csv|table` flattens report rows into dotted
columns (`campaign.id`, `metrics.clicks`). Global flags: `--customer-id`, `--login-customer-id`,
`--apply`, `--confirm-spend`, `--validate-only`, `--output <file>`, `--timeout`, `--verbose`.

### Dry-run output

A dry run prints an envelope, so nobody mistakes it for a change:

```json
{"dry_run":true,"applied":false,"method":"customers.campaigns.mutate","risk_class":"write","operations":[{"index":0,"verb":"create","resource":"Campaign","name":"Brand","status":"PAUSED","class":"write"}],"next":"Google validated the request and applied nothing; re-run with --apply to apply it","response":{}}
```

An applied call prints Google's response unchanged.

## Safety model

### The gates

| Call | No flags | `--validate-only` | `--apply` | `--apply --confirm-spend` |
| --- | --- | --- | --- | --- |
| read | runs | runs | runs | runs |
| write (create PAUSED, rename, pause, assets, negatives, ...) | dry run | dry run | **applies** | applies |
| delete / spend / admin | refused, nothing sent | dry run | refused, nothing sent | **applies** |
| method with no `validate_only` | refused, nothing sent | refused | applies if write | applies |

- A refusal exits 2 with kind `guardrail` before any network I/O.
- `--force` survives as a deprecated alias for `--confirm-spend` only. It never stands in for
  `--apply`.
- A `validateOnly` field inside `--data` counts for nothing; only the flags decide. On `--apply`
  the CLI removes it, so what is logged is what is sent.
- `--apply` and `--validate-only` together are a usage error.
- In read-only mode every `--apply` is refused; dry runs still work.

### What is spend-, delete- or admin-class

Each method has a reviewed class in `tools/genmanifest/classifications.json`. Before a mutate is
sent (generic `mutate`, any per-resource `mutate`, `batch-jobs add-operations`, and `api` calls to
those paths) the guard reads every operation and raises the class:

| Raised to | When |
| --- | --- |
| refused outright | a campaign, ad group, ad (`AdGroupAd`) or keyword create without `status` (Google's default is `ENABLED`) |
| spend | a create with a status other than `PAUSED`; an update that sets status to anything but `PAUSED`/`REMOVED` (enabling), for campaigns, ad groups, ads, ad group criteria and asset groups |
| spend | bids and bidding: campaign bidding fields (`manualCpc`, `targetSpend`, `maximizeConversions`, `targetCpa`, `targetRoas`, `biddingStrategy`, ...) on update; ad group and criterion bid fields on update; bid modifiers; portfolio bid-strategy updates; seasonality adjustments and data exclusions |
| spend | budgets: any budget amount set on create or update; budget network settings; moving a campaign to another budget |
| spend | conversions that feed bidding: a conversion action created without `"primaryForGoal": false`, made primary, or re-enabled; a customer or campaign conversion goal made biddable; campaign goal config changes |
| delete | any `remove` operation; a status set to `REMOVED` |
| admin | billing setups, account budget proposals, users, links, uploads, batch jobs, any unknown operation type and any `api` call to an unknown path |

Pausing anything, renaming, creating PAUSED things, negatives and assets are plain writes.

The body is read strictly: duplicate keys, a field in both spellings (`amountMicros` and
`amount_micros`), trailing data and non-digit amounts are refused, so the guard always sees what
Google will see.

### Budget cap

`gads config set budget-cap <amount>` is the most **daily budget the account may total**. No flag
or environment variable changes it, and without one no budget amount can be set at all.

- Every budget amount in a request must be at or below the cap.
- The amounts one invocation sets may not add up to more than the cap.
- Before an `--apply` that sets budget amounts, the CLI reads every enabled daily budget in the
  account with the reader key and refuses if the total afterwards would exceed the cap. One
  invocation can only add what the cap has left. If that read fails, the apply is refused.
- Fixed-total and custom-period budgets are refused; the cap is a daily cap.

Google may spend up to twice a daily budget on one day and bills at most 30.4 times it in a month.

### Audit log

Every attempt to call a non-read method appends one JSON line to
`$XDG_STATE_HOME/gads/mutations.jsonl` (default `~/.local/state/gads/mutations.jsonl`, mode `0600`
in a `0700` directory; move it with `gads config set audit-log /abs/path`). That includes refusals,
dry runs, applies and failures. Each line holds the time, customer, method and path, risk class
and reasons, `apply` / `confirm_spend` / `validate_only`, the credential role and its client email,
a redacted per-operation summary (verb, resource, resource name, name, status, update mask, budget
amount), a SHA-256 of the body, the HTTP status, request id, created resource names, and the
result (`refused`, `dry_run`, `applied`, `error`). It never holds the body, a token or a key. A
write whose log cannot be opened is refused; if the line cannot be written after a successful
call, the call exits 1 with kind `audit_failed` (do not retry it).

### Blocked methods

Refused whatever the flags, as commands, inside `mutate`, and through `api`:
`recommendations apply` (can set budgets Google chooses), `recommendation-subscriptions
mutate-recommendation-subscription` (auto-apply), `reservations book-campaigns` (commits spend).
`media.upload` (YouTube video upload) is not implemented.

### Other protections

Credentials go only to `oauth2.googleapis.com` and `googleads.googleapis.com` over HTTPS; redirects
are never followed; `api` cannot set `Authorization`, `developer-token`, method-override headers or
credential query parameters, and refuses path spellings that could dodge the method match. See
[SECURITY.md](SECURITY.md).

## Errors and exit codes

Errors are one line of JSON on stderr: `{"kind","error","status","details","request_id"}`.

| Exit | Meaning |
| --- | --- |
| `0` | success, including a dry run |
| `1` | API or network error, or a post-success problem (`output_failed`, `audit_failed`) |
| `2` | `usage` (malformed command) or `guardrail` (a safety rule refused it); nothing was sent |

Kinds: `auth`, `forbidden`, `not_found`, `validation`, `conflict`, `rate_limited`, `server`,
`transport`, `outcome_unknown`, `incomplete`, `partial_failure`, `output_failed`, `audit_failed`,
`usage`, `guardrail`. `outcome_unknown` means a change may or may not have landed: check with a
report before retrying. Rate limits and safe transient failures are retried inside the CLI; a write
is never replayed. Setup errors get hints (Test access, missing account access, Explorer vs Basic,
EU political declaration).

## API version and upgrades

| | |
| --- | --- |
| Pinned version | **Google Ads API v26** (Discovery revision in `spec/PROVENANCE.json`; calls use the version's latest minor automatically) |
| v26 sunset | scheduled for **November 2027** |
| Previous version | v25, valid until August 2027 (upstream still ships it) |

Google releases a major version a few times a year and sunsets each one about a year later. Plan at
most two upgrades a year, and subscribe to the
[Google Ads Developer Blog](https://ads-developers.googleblog.com/search/label/google_ads_api) for
sunset notices. Upgrading:

1. `make update-spec VERSION=vNN` fetches and validates the new Discovery document, replaces the
   old one in `spec/`, and regenerates the embedded manifest.
2. New methods are never guessed: the generator lists unclassified ones and fails.
   `go run ./tools/genmanifest -write-proposals` proposes classes; review each one before it goes
   into `classifications.json`.
3. Review the command-catalog diff (`UPDATE_GOLDEN=1 go test ./tools/genmanifest` updates
   `tools/genmanifest/testdata/golden_commands.txt`) and run `go test ./...`. The test
   `TestReportFieldsExistInTheSchema` catches GAQL fields the wrappers select that the new version
   renamed.
4. Release under SemVer: **minor** when commands are only added, **major** when a command
   disappears or is renamed. Update the table above.

## Development

```sh
make build       # bin/googleads
make install     # ~/.local/bin/googleads and the gads symlink (PREFIX=/usr/local to change)
make test        # go test ./...
make check       # vet, manifest drift check, tests
./scripts/check-secrets.sh   # what CI runs to keep credentials out of the tree
```

CI (GitHub Actions) runs vet and tests on Linux and macOS, the secret scan, gofmt, the manifest
drift check and a cross-compile. Pushing a `v*` tag runs GoReleaser, which publishes Linux and macOS
binaries for amd64 and arm64 to GitHub Releases (`-rc` tags publish as pre-releases).

`e2e/smoke_test.go` drives the real binary against a fake Google. `e2e/live_test.go` runs against a
real account and changes nothing: it reads the account, dry-runs a complete PAUSED Search campaign
with `--validate-only`, and checks the local refusals. It is skipped unless you ask for it:

```sh
GOOGLE_ADS_LIVE=1 GOOGLE_ADS_LIVE_FINAL_URL=https://your-site.example go test ./e2e -run TestLive -v
```

The public interface (command paths, flags, exit codes, the stderr schema and the
`commands --json` schema) follows SemVer.

## License

MIT, see [LICENSE](LICENSE). Original work by wir drei digital; fork changes by the gads-cli
contributors.
