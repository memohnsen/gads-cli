package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/config"
	"github.com/memohnsen/gads-cli/internal/guard"
	"github.com/memohnsen/gads-cli/internal/manifest"
)

// gateFlags are the flags that decide whether and how a write is sent.
type gateFlags struct {
	apply, confirmSpend, forceAlias, validateOnly bool
}

func readGateFlags(cmd *cobra.Command) gateFlags {
	g := gateFlags{
		apply:        flagBool(cmd, "apply"),
		confirmSpend: flagBool(cmd, "confirm-spend"),
		forceAlias:   flagBool(cmd, "force"),
		validateOnly: flagBool(cmd, "validate-only"),
	}
	// --force is a deprecated alias for --confirm-spend, never for --apply.
	g.confirmSpend = g.confirmSpend || g.forceAlias
	return g
}

// execSpec is one call ready for the guard: the method (or the api command's
// stand-in for an unmatched path) and the request built from the flags.
type execSpec struct {
	meth        *manifest.Method
	req         *api.Request
	canValidate bool // the method takes validateOnly
	unmatched   bool // api: no manifest method matches the path
	forceRead   bool // api GET: always read-class
	partial     bool // --partial-failure
	all         bool // --all
	format      string
}

// execute is the one road every API call takes: guard, credential choice,
// audit log, account-wide budget cap, request, response. Generated
// commands, the api escape hatch and the hand-written wrappers all go
// through it, so none of them is a way around a gate.
func (a *app) execute(cmd *cobra.Command, x execSpec) error {
	g := readGateFlags(cmd)
	start := time.Now()
	entry := &auditEntry{
		Time: nowStamp(), Version: buildVersion(), APIVersion: a.manifest.APIVersion,
		Command: cmd.CommandPath(), MethodID: x.meth.ID, HTTPMethod: x.req.Method, Path: x.req.Path,
		CustomerID: customerFromPath(x.req.Path), LoginCustomerID: a.client.LoginCustomerID,
		RiskClass: x.meth.Risk, Apply: g.apply, ConfirmSpend: g.confirmSpend, ForceAlias: g.forceAlias,
		BodySHA256: bodyHash(x.req.Body),
	}
	audited := x.meth.Risk != manifest.RiskRead && !x.forceRead
	refuse := func(err error) error {
		if audited {
			entry.fail("refused", err)
			entry.DurationMS = time.Since(start).Milliseconds()
			a.auditBestEffort(entry)
		}
		return err
	}

	if m := strings.ToUpper(x.req.Method); x.req.Body != nil && (m == "GET" || m == "DELETE") {
		return refuse(api.Usagef("refusing to send a request body with %s %s: %s takes no body, so the guard never inspects one", m, x.req.Path, m))
	}
	dec, err := guard.Check(a.manifest, x.meth, x.req.Body, guard.Options{
		Apply: g.apply, ConfirmSpend: g.confirmSpend, ValidateOnly: g.validateOnly, CanValidate: x.canValidate,
		ReadOnly: a.res.ReadOnly, CapMicros: a.res.BudgetCapMicros, Unmatched: x.unmatched,
	})
	if err != nil {
		// A gate refusal still knows what the call would have been.
		if dec.Class != "" {
			entry.RiskClass, entry.Reasons, entry.Operations = dec.Class, dec.Reasons(), dec.Ops
		}
		return refuse(err)
	}
	if x.forceRead {
		dec.Class, dec.ValidateOnly = manifest.RiskRead, false
	}
	write := dec.Class != manifest.RiskRead
	audited = audited || write
	entry.RiskClass, entry.Reasons, entry.ValidateOnly, entry.Operations = dec.Class, dec.Reasons(), dec.ValidateOnly, dec.Ops
	if len(dec.Budgets) > 0 {
		entry.BudgetMicros = fmt.Sprint(dec.BudgetTotal())
	}

	role := config.RoleReader
	switch {
	case !write:
	case dec.ValidateOnly:
		role = a.res.DryRunCredential
	default:
		role = config.RoleWriter
	}
	entry.Credential = role
	if write && !a.res.Credential(role).Configured() {
		return refuse(api.Guardrailf("this call uses the %s credential, which is not configured; nothing was sent. %s",
			role, credentialHint(role)))
	}

	var log *auditLog
	if write {
		if log, err = openAudit(a.res.AuditLog); err != nil {
			return api.Guardrailf("cannot open the audit log %s (%v); nothing was sent. Fix the path or its permissions; "+
				"a person can move it with `googleads config set audit-log <absolute path>`", a.res.AuditLog, err)
		}
	}
	closeLog := func() {
		if log != nil {
			log.f.Close()
		}
	}

	x.req.Risk, x.req.ValidateOnly = dec.Class, dec.ValidateOnly
	if write && x.canValidate {
		if x.req.Body, err = setValidateOnly(x.req.Body, dec.ValidateOnly); err != nil {
			closeLog()
			return refuse(err)
		}
	}
	if x.partial {
		if x.req.Body, err = setPartialFailure(x.req.Body); err != nil {
			closeLog()
			return refuse(err)
		}
	}
	a.applyGlobalFlags(cmd)

	if write && !dec.ValidateOnly && len(dec.Budgets) > 0 {
		if err := a.checkAccountBudgets(cmd.Context(), entry.CustomerID, dec); err != nil {
			closeLog()
			return refuse(err)
		}
	}

	a.client.TokenSource = a.tokenSourceFor(role)
	out, err := openOutput(flagString(cmd, "output"))
	if err != nil {
		closeLog()
		return refuse(err)
	}
	defer out.discard()
	if x.all {
		closeLog()
		return a.runAll(cmd, x.meth, *x.req, out, x.format)
	}

	resp, callErr := a.client.Do(cmd.Context(), *x.req)
	if log != nil {
		entry.DurationMS = time.Since(start).Milliseconds()
		entry.CredentialType, entry.CredentialEmail = a.credentialIdentity(role)
		switch {
		case callErr != nil:
			var apiErr *api.Error
			if errors.As(callErr, &apiErr) && apiErr.IsLocal() {
				entry.fail("refused", callErr)
			} else {
				entry.fail("error", callErr)
			}
		default:
			entry.Result, entry.HTTPStatus = "applied", resp.Status
			if dec.ValidateOnly {
				entry.Result = "dry_run"
			}
			entry.RequestID = resp.Header.Get("request-id")
			if !dec.ValidateOnly {
				entry.ResourceNames = resourceNames(resp.Body)
			}
		}
		if lerr := log.write(entry); lerr != nil && callErr == nil {
			// The call went through; its response still goes out, then
			// the missing record is reported. Retrying would repeat it.
			if werr := a.writeResult(resp, out, dec, x); werr != nil {
				return werr
			}
			return &api.Error{Kind: api.KindAuditFailed, Status: resp.Status, Message: fmt.Sprintf(
				"the call succeeded, but its audit line could not be written to %s: %v; do not retry the change", a.res.AuditLog, lerr)}
		}
	}
	if callErr != nil {
		return callErr
	}
	if err := a.writeResult(resp, out, dec, x); err != nil {
		return err
	}
	if x.partial {
		if pf := api.PartialFailure(*x.req, resp); pf != nil {
			return pf
		}
	}
	return nil
}

// writeResult writes the response: a dry run inside an envelope that says
// nothing was applied, a report in the requested format, anything else as
// Google sent it.
func (a *app) writeResult(resp *api.Response, out *outputFile, dec guard.Decision, x execSpec) error {
	if dec.ValidateOnly && dec.Class != manifest.RiskRead {
		return a.writeResponse(&api.Response{Status: resp.Status, Header: resp.Header, Body: dryRunEnvelope(x, dec, resp.Body)}, out)
	}
	if x.format != "" && x.format != "json" {
		body, err := formatReport(resp.Body, x.format)
		if err != nil {
			return err
		}
		return a.writeResponse(&api.Response{Status: resp.Status, Header: resp.Header, Body: body}, out)
	}
	return a.writeResponse(resp, out)
}

// dryRunEnvelope wraps a validate-only response so no reader can mistake it
// for an applied change.
func dryRunEnvelope(x execSpec, dec guard.Decision, body []byte) []byte {
	resp := json.RawMessage(bytes.TrimSpace(body))
	if len(resp) == 0 || !json.Valid(resp) {
		s, _ := json.Marshal(string(body))
		resp = s
	}
	next := "re-run with --apply to apply it"
	if dec.Gated() {
		next = "re-run with --apply --confirm-spend to apply it; that decision is a person's"
	}
	env := struct {
		DryRun     bool              `json:"dry_run"`
		Applied    bool              `json:"applied"`
		Method     string            `json:"method"`
		RiskClass  string            `json:"risk_class"`
		Reasons    string            `json:"reasons,omitempty"`
		Operations []guard.OpSummary `json:"operations,omitempty"`
		Next       string            `json:"next"`
		Response   json.RawMessage   `json:"response"`
	}{true, false, x.meth.ID, dec.Class, dec.Reasons(), dec.Ops,
		"Google validated the request and applied nothing; " + next, resp}
	raw, _ := jsonCompact(env)
	return append(raw, '\n')
}

// setValidateOnly makes the body's validateOnly match the decision: true for
// a dry run, absent for an apply. A validateOnly the caller put in --data is
// replaced either way, so what is logged is what is sent.
func setValidateOnly(body []byte, validateOnly bool) ([]byte, error) {
	obj, err := decodeObject(body, "--data")
	if err != nil {
		return nil, err
	}
	_, had := obj["validateOnly"]
	_, hadSnake := obj["validate_only"]
	if !validateOnly && !had && !hadSnake {
		return body, nil // nothing to change: the body goes out byte for byte
	}
	delete(obj, "validateOnly")
	delete(obj, "validate_only")
	if validateOnly {
		obj["validateOnly"] = true
	}
	return jsonCompact(obj)
}

func setPartialFailure(body []byte) ([]byte, error) {
	obj, err := decodeObject(body, "--data")
	if err != nil {
		return nil, err
	}
	delete(obj, "partial_failure")
	obj["partialFailure"] = true
	return jsonCompact(obj)
}

// decodeObject reads a JSON object body, keeping numbers as written. Re-encoding
// it is safe: a mutate body was parsed strictly by the guard (no duplicate
// keys, no mixed spellings, no trailing data), and the only changes made are
// validateOnly and partialFailure, which carry no amount, status or operation.
func decodeObject(body []byte, what string) (map[string]any, error) {
	obj := map[string]any{}
	if len(body) == 0 {
		return obj, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		return nil, api.Usagef("%s must be a JSON object: %v", what, err)
	}
	return obj, nil
}

// checkAccountBudgets is the account-wide half of the budget cap. Before an
// apply that sets budget amounts it reads every enabled daily budget in the
// account and refuses when the total after the change would exceed the cap:
// one invocation may raise budgets by at most what the cap has left. A read
// that fails refuses the apply, because the check could not be made.
func (a *app) checkAccountBudgets(ctx context.Context, customer string, dec guard.Decision) error {
	if customer == "" {
		return api.Guardrailf("cannot tell which account this budget change applies to, so the account-wide budget cap cannot be checked; nothing was sent")
	}
	search := a.manifest.ByID("customers.googleAds.search")
	if search == nil {
		return api.Guardrailf("this build lacks the search method the budget check needs; nothing was sent")
	}
	current, err := a.enabledBudgets(ctx, search, customer)
	if err != nil {
		return api.Guardrailf("the account-wide budget cap could not be checked, so nothing was sent: %v", err)
	}
	before := int64(0)
	for _, v := range current {
		before += v
	}
	after := before
	for _, b := range dec.Budgets {
		if old, ok := current[b.ResourceName]; ok && b.Verb == "update" {
			after += b.Micros - old
			current[b.ResourceName] = b.Micros
			continue
		}
		after += b.Micros
	}
	if after > a.res.BudgetCapMicros {
		return api.Guardrailf("applying this would bring the account's enabled daily budgets to %s in total (now %s), above the configured cap of %s; "+
			"the cap bounds the account's total daily budget, so at most %s more may be added; nothing was sent",
			config.FormatMicros(after), config.FormatMicros(before), config.FormatMicros(a.res.BudgetCapMicros),
			config.FormatMicros(max(0, a.res.BudgetCapMicros-before)))
	}
	return nil
}

// enabledBudgets maps each enabled daily budget of the account to its amount.
func (a *app) enabledBudgets(ctx context.Context, search *manifest.Method, customer string) (map[string]int64, error) {
	const query = "SELECT campaign_budget.resource_name, campaign_budget.amount_micros, campaign_budget.period " +
		"FROM campaign_budget WHERE campaign_budget.status = 'ENABLED'"
	client := *a.client
	client.TokenSource = a.tokenSourceFor(config.RoleReader)
	out := map[string]int64{}
	token := ""
	for page := 0; page < 50; page++ {
		body := map[string]any{"query": query}
		if token != "" {
			body["pageToken"] = token
		}
		raw, err := jsonCompact(body)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(ctx, api.Request{Method: search.HTTPMethod, Path: strings.Replace(search.Path, "{+customerId}", customer, 1),
			Body: raw, Risk: manifest.RiskRead, MethodID: search.ID})
		if err != nil {
			return nil, err
		}
		var r struct {
			Results []struct {
				CampaignBudget struct {
					ResourceName string `json:"resourceName"`
					AmountMicros string `json:"amountMicros"`
				} `json:"campaignBudget"`
			} `json:"results"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(resp.Body, &r); err != nil {
			return nil, fmt.Errorf("unreadable budget report: %v", err)
		}
		for _, row := range r.Results {
			var micros int64
			if row.CampaignBudget.AmountMicros != "" {
				if _, err := fmt.Sscan(row.CampaignBudget.AmountMicros, &micros); err != nil {
					return nil, fmt.Errorf("budget %s has an unreadable amount %q", row.CampaignBudget.ResourceName, row.CampaignBudget.AmountMicros)
				}
			}
			out[row.CampaignBudget.ResourceName] = micros
		}
		if token = r.NextPageToken; token == "" {
			return out, nil
		}
	}
	return nil, errors.New("too many budget pages")
}
