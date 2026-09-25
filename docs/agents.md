# googleads: agent reference

Paste the block below into your agent's instructions. It is the whole contract.

```markdown
# googleads (Google Ads API CLI): agent reference

Setup is a person's job. Never run `googleads init`, never change the budget cap
(`googleads config set budget-cap`), and never pass `--force` unless a person told you to,
in this conversation, for this exact call.

Credential and account
- `googleads auth status`: one line of JSON, offline, never a secret. `mode` is service_account or
  none; `missing` lists what blocks calls (service_account_key, valid_service_account_key,
  customer_id); `budget_cap` is the highest daily budget the CLI accepts, in the account currency.
- The customer comes from `--customer-id` (dashes allowed), else GOOGLE_ADS_CUSTOMER_ID or the config.

Discovery
- `googleads commands --json`: every command with id, HTTP method, path, risk, blocked state,
  parameters and paging.
- `googleads <command> --help`: fields, an example body and the risk class.
- `googleads schema <Name>`: fields of any type (Campaign, CampaignBudget, AdGroup, AdGroupAd,
  AdGroupCriterion, CampaignCriterion, Campaign_NetworkSettings, ResponsiveSearchAdInfo).
- Never guess a command path or a field name: read it from the catalog, the help or `schema`.

Reading
- `googleads search --query "<GAQL>"` or `--query @file.gaql`; `--all` merges every page into one
  array. GAQL names are snake_case: SELECT campaign.id, campaign.name, metrics.cost_micros
  FROM campaign WHERE segments.date DURING LAST_7_DAYS
- GAQL field metadata: googleads google-ads-fields search --data '{"query":"SELECT name, selectable, filterable WHERE name LIKE \"campaign.%\""}'
- Keyword ideas: `googleads generate-keyword-ideas --data @ideas.json` (needs Basic access).
- Responses are camelCase JSON. int64 values (ids, costMicros, amountMicros, clicks) are strings.
  Money is in micros: 1 unit of the account currency is 1,000,000 micros.

Writing
- Bodies are JSON: `--data '{...}'`, `--data @file.json` or `--data -`.
- Build a whole campaign in one `googleads mutate` with temporary resource names
  (customers/<id>/campaignBudgets/-1, customers/<id>/campaigns/-2) that later operations reference.
- Create campaigns with "status":"PAUSED" and
  "containsEuPoliticalAdvertising":"DOES_NOT_CONTAIN_EU_POLITICAL_ADVERTISING"; create budgets with
  "explicitlyShared":false.
- Validate first: `--validate-only` makes Google check the whole request and apply nothing. It
  runs without --force and in read-only mode.
- Classes: read and write run; delete, spend and admin need --force, which you do not pass on your
  own. spend means switching a campaign on, changing a budget amount, moving a campaign to another
  budget, promoting or scheduling experiments. Pausing always works without --force.
- A budget amount above the cap is refused even with --force. Tell the person; do not look for
  another way.
- Blocked commands (recommendations apply, recommendation auto-apply subscriptions, reservation
  booking) always refuse. Read recommendations with search and carry out a good one through mutate.
- `googleads api <METHOD> <path>` is for what the command tree lacks, and the same guardrails
  apply. GET and DELETE take no --data; the method-override headers (X-HTTP-Method-Override,
  X-HTTP-Method, X-Method-Override) are refused; every path segment must be non-empty, not "." or
  "..", with no %, whitespace or backslash.
- `--output <file>` writes the response to a file. A path that cannot be written is a usage error
  and nothing is sent.

Errors
- stdout is the API response only. A failure is one line of JSON on stderr:
  {"kind","error","status","details","request_id"}. Exit 0 ok, 1 API or network error, 2 usage
  error, which includes every guardrail refusal.
- kinds: auth, forbidden, not_found, validation, rate_limited, server, transport, outcome_unknown,
  incomplete, usage, partial_failure, conflict, output_failed.
- outcome_unknown: the change may or may not have landed; check with search before retrying.
- partial_failure (only with --partial-failure): stdout holds the full response; some operations failed.
- conflict: the same resource changed concurrently; retry once after a short wait.
- incomplete: --all hit --max-pages; the output is partial.
- output_failed: the call succeeded, but the response could not be written; with --output it is on
  stdout instead. Do not repeat a change.
- The error text leads with Google's error code and field path, and may end with "hint: ..." naming
  the next step for the person (access level, account access, EU political declaration).
- Rate limits are retried automatically; do not add a retry loop of your own.
```

## Notes for the person wiring this up

- Give agents a read-only posture where writing is not their job: `GOOGLE_ADS_READ_ONLY=1` in their
  environment. `--validate-only` still works there. The variable only switches read-only mode on;
  it cannot switch off a read-only mode set in the config file.
- `--force` has no environment variable or config setting on purpose: every spend decision stays a
  visible flag in the transcript.
- Set a budget cap before any agent works on campaigns: `googleads config set budget-cap <amount>`.
- Regenerate the agent's command knowledge from `googleads commands --json` after an upgrade.
