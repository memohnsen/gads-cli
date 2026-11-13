# gads-cli: agent reference

Paste the block below into your agent's instructions. It is the whole contract.

```markdown
# gads / googleads (Google Ads API CLI): agent reference

Setup is a person's job. Never run `gads init`, never change config (`gads config set ...`),
never handle key files, and never pass `--confirm-spend` (or `--force`) unless a person told you to,
in this conversation, for this exact command.

Status
- `gads auth status`: one line of JSON, never a secret: customer, budget cap, read-only state,
  audit log path, and per role (reader, writer) whether a key is configured.

Reading (always allowed)
- `gads list campaigns|ad-groups|keywords|negatives|ads|budgets|assets|sitelinks|sitelink-links|conversion-actions`
  with `--campaign-id`, `--ad-group-id`, `--include-removed`, `--format json|csv|table`.
- `gads get campaign|ad-group|keyword|negative|ad|budget|asset|sitelink|conversion-action <id>`;
  keyword and ad IDs are `<adGroupId>~<id>`.
- `gads search --query "<GAQL>"` (or `--query @file.gaql`), `--all` merges every page,
  `--format csv|table`. GAQL is snake_case: SELECT campaign.id, metrics.cost_micros FROM campaign
  WHERE segments.date DURING LAST_7_DAYS
- int64 values are strings; money is micros (1 unit = 1,000,000) in responses. Wrapper flags take
  plain currency amounts (1.50).

Writing
- Every write is a DRY RUN unless you pass --apply. A dry run prints
  {"dry_run":true,"applied":false,...}: nothing changed. Check that before you claim success.
- Creates are always PAUSED (the wrappers do it; raw bodies must set "status":"PAUSED").
- Plain writes (create PAUSED, rename, pause, negatives, assets, sitelinks, RSA text) apply with
  --apply.
- Gated (spend/delete/admin): enabling anything, budgets, bids and bid strategies, making a
  conversion primary/biddable, removing or unlinking anything, billing, users. Without flags they
  are refused; dry-run them with --validate-only; only a person may approve --apply --confirm-spend.
- Read-only mode (default) refuses every --apply. If you get kind "guardrail" mentioning read-only,
  stop and tell the person; do not try to turn it off.
- A budget above the cap, or one that would push the account's total daily budget above it, is
  refused even with every flag. Tell the person; do not look for another way.
- Blocked: recommendations apply, auto-apply subscriptions, reservation booking.
- Raw API: `gads mutate --data @body.json`, `gads <resource> mutate`, `gads api <METHOD> <path>`
  follow the same rules. Discover fields with `gads <command> --help`, `gads schema <Name>`,
  `gads commands --json`; never guess a field name.

Errors
- stdout is the result only. A failure is one line of JSON on stderr:
  {"kind","error","status","details","request_id"}. Exit 0 ok (including dry runs), 1 API/network,
  2 usage or guardrail (nothing was sent).
- guardrail: a safety rule refused it; read the message, it names the flag or the person's step.
- outcome_unknown: the change may have landed; check with a report before retrying.
- output_failed / audit_failed: the call succeeded; do not repeat it.
- conflict: retry once after a short wait. Rate limits are retried for you.
```

## Notes for the person wiring this up

- Give agents the reader key and `GOOGLE_ADS_READ_ONLY=1` where writing is not their job. Dry runs
  still work there.
- `--confirm-spend` has no environment variable or config setting on purpose: every spend decision
  stays a visible flag in the transcript, and the audit log records it.
- Set a budget cap before any agent works on campaigns: `gads config set budget-cap <amount>`.
- Review `~/.local/state/gads/mutations.jsonl` for what was attempted and applied.
- Regenerate the agent's command knowledge from `gads commands --json` after an upgrade.
