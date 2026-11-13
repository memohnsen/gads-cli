// Package guard decides, before anything is sent, whether a call may run and
// how. It starts from the method's reviewed risk class, raises it by what
// each operation in a mutate body does, holds budget amounts to the
// configured cap, and then applies the gates:
//
//   - A write without --apply is a dry run: it goes out with validateOnly,
//     so Google checks it and applies nothing. A method that has no
//     validateOnly cannot be dry-run and is refused without --apply.
//   - delete-, spend- and admin-class calls need --apply --confirm-spend to
//     apply. Without --apply they run only as an explicit --validate-only.
//   - Read-only mode refuses every --apply.
//
// A refusal is an *api.Error of kind guardrail (a safety rule) or usage (a
// malformed request); either way nothing has been sent.
package guard

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/config"
	"github.com/memohnsen/gads-cli/internal/manifest"
)

// Options are the flags and settings that bear on the decision.
type Options struct {
	// Apply is --apply: send the call for real. Without it a write is a
	// dry run.
	Apply bool
	// ConfirmSpend is --confirm-spend (or its deprecated alias --force). It
	// confirms a delete-, spend- or admin-class apply and means nothing
	// without Apply.
	ConfirmSpend bool
	// ValidateOnly is the explicit --validate-only flag. A validateOnly
	// field inside the body does not count: only the flags decide.
	ValidateOnly bool
	// CanValidate says the method takes validateOnly, so it can be dry-run.
	CanValidate bool
	ReadOnly    bool
	CapMicros   int64 // 0 means no cap is configured
	// Unmatched says meth is the api command's stand-in for a path no
	// manifest method matches. Its body has no known shape, so every budget
	// amount in it, wherever it sits, is held to the cap.
	Unmatched bool
}

// Finding is one reason a call needs --confirm-spend.
type Finding struct {
	Operation int // index in the operation list; -1 for the method itself
	Class     string
	Reason    string
}

// BudgetAmount is one daily budget amount a call sets.
type BudgetAmount struct {
	Operation int
	// Verb is create or update for a campaign budget operation, and ""
	// for an amount found in a body of unknown shape.
	Verb string
	// ResourceName is the budget the amount applies to, as the body names
	// it: a real resource name for an update, a temporary one (or "") for
	// a create.
	ResourceName string
	Micros       int64
}

// OpSummary is the redacted description of one operation that the audit
// log records: what it touches and the fields that decide its class, never
// the whole body.
type OpSummary struct {
	Index        int    `json:"index"`
	Type         string `json:"type,omitempty"` // generic mutate operation type
	Verb         string `json:"verb,omitempty"` // create, update or remove
	Resource     string `json:"resource,omitempty"`
	ResourceName string `json:"resource_name,omitempty"`
	Name         string `json:"name,omitempty"`
	Status       string `json:"status,omitempty"`
	UpdateMask   string `json:"update_mask,omitempty"`
	AmountMicros string `json:"amount_micros,omitempty"`
	Class        string `json:"class"`
}

// Decision is the outcome for a call that may be sent.
type Decision struct {
	// Class is the highest class found; what the client is told. Among the
	// gated classes (delete, spend, admin), which are peers, the first one
	// found is kept.
	Class    string
	Findings []Finding // every reason above write
	// ValidateOnly says the call goes out as a dry run, with validateOnly.
	ValidateOnly bool
	Budgets      []BudgetAmount
	Ops          []OpSummary
}

// Gated reports whether the decision's class needs --confirm-spend.
func (d Decision) Gated() bool { return manifest.NeedsConfirm(d.Class) }

// Reasons renders the findings for a message.
func (d Decision) Reasons() string { return describe(d.Findings) }

// BudgetTotal is the sum of every budget amount the call sets.
func (d Decision) BudgetTotal() int64 {
	var t int64
	for _, b := range d.Budgets {
		t += b.Micros
	}
	return t
}

// Check decides whether meth may be called with body, and how.
func Check(m *manifest.Manifest, meth *manifest.Method, body []byte, opt Options) (Decision, error) {
	name := "googleads " + strings.Join(meth.Command, " ")
	if meth.Blocked != "" {
		return Decision{}, api.Guardrailf("%s is blocked in this version of googleads: %s", name, meth.Blocked)
	}
	d := Decision{Class: meth.Risk}
	if manifest.NeedsConfirm(meth.Risk) {
		d.Findings = append(d.Findings, Finding{Operation: -1, Class: meth.Risk, Reason: name + " is " + meth.Risk + "-class"})
	}
	if meth.Mutate != nil && len(body) > 0 {
		obj, err := parseStrict(body)
		if err != nil {
			return Decision{}, api.Usagef("refusing to send a mutate body the guard cannot read: %v", err)
		}
		if err := inspect(m, meth, obj, opt, &d); err != nil {
			return Decision{}, err
		}
	}
	if opt.Unmatched && len(body) > 0 {
		obj, err := parseStrict(body)
		if err != nil {
			return Decision{}, api.Usagef("refusing to send a body the guard cannot read: %v", err)
		}
		if err := scanAmounts(obj, "", opt, &d); err != nil {
			return Decision{}, err
		}
	}
	if len(d.Budgets) > 1 && d.BudgetTotal() > opt.CapMicros {
		return Decision{}, api.Guardrailf("this call sets %d budget amounts totalling %s (%d micros), above the configured cap of %s; "+
			"one invocation may not set more daily budget in total than the cap, and no flag overrides it",
			len(d.Budgets), config.FormatMicros(d.BudgetTotal()), d.BudgetTotal(), config.FormatMicros(opt.CapMicros))
	}
	return d, gate(name, &d, opt)
}

// gate applies the dry-run default, the --apply/--confirm-spend gates and
// read-only mode to a decision whose class is settled.
func gate(name string, d *Decision, opt Options) error {
	if d.Class == manifest.RiskRead {
		return nil
	}
	gated := d.Gated()
	if opt.Apply && opt.ValidateOnly {
		return api.Usagef("--apply and --validate-only contradict each other: --validate-only is a dry run, --apply applies")
	}
	if !opt.Apply {
		if !opt.CanValidate {
			confirm := ""
			if gated {
				confirm = " --confirm-spend"
			}
			return api.Guardrailf("%s cannot be dry-run: the method has no validate_only mode, so nothing was sent; "+
				"re-run with --apply%s to apply it (%s-class)", name, confirm, d.Class)
		}
		if gated && !opt.ValidateOnly {
			return api.Guardrailf("%s is %s-class (%s); nothing was sent. Dry-run it with --validate-only, "+
				"or apply it with --apply --confirm-spend", name, d.Class, describe(d.Findings))
		}
		d.ValidateOnly = true
		return nil
	}
	if opt.ReadOnly {
		return api.Guardrailf("read-only mode blocks --apply of %s (%s-class); dry runs still work. "+
			"Read-only mode is on by default and only a person turns it off: googleads config set read-only false", name, d.Class)
	}
	if gated && !opt.ConfirmSpend {
		return api.Guardrailf("%s is %s-class (%s); --apply alone does not apply it: add --confirm-spend, "+
			"which only a person may decide, or dry-run it with --validate-only instead of --apply", name, d.Class, describe(d.Findings))
	}
	return nil
}

func describe(fs []Finding) string {
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		if f.Operation < 0 {
			parts = append(parts, f.Reason)
		} else {
			parts = append(parts, fmt.Sprintf("operation %d: %s", f.Operation, f.Reason))
		}
	}
	return strings.Join(parts, "; ")
}

// raise lifts the decision's class and records why when the class is gated.
func raise(d *Decision, op int, class, reason string) {
	if manifest.RiskRank(class) > manifest.RiskRank(d.Class) {
		d.Class = class
	}
	if manifest.NeedsConfirm(class) {
		d.Findings = append(d.Findings, Finding{Operation: op, Class: class, Reason: reason})
	}
}

// inspect walks the operations of a mutate body.
func inspect(m *manifest.Manifest, meth *manifest.Method, body map[string]any, opt Options, d *Decision) error {
	field := meth.Mutate.OperationsField
	raw, ok := body[field]
	if !ok {
		return nil // no operations: nothing is applied, Google rejects the request
	}
	var ops []any
	switch arr, isArr := raw.([]any); {
	case field == "operation":
		ops = []any{raw}
	case isArr:
		ops = arr
	default:
		return api.Usagef("%s must be a JSON array", field)
	}
	for i, op := range ops {
		obj, ok := op.(map[string]any)
		if !ok {
			return api.Usagef("operation %d is not a JSON object", i)
		}
		resource, typ := meth.Mutate.Resource, ""
		opClass := meth.Risk
		if field == "mutateOperations" {
			if len(obj) != 1 {
				return api.Usagef("operation %d must name exactly one operation type", i)
			}
			var inner any
			for k, v := range obj {
				typ, inner = k, v
			}
			target := m.ByID(m.GenericOperations[typ])
			if target == nil {
				raise(d, i, manifest.RiskAdmin, "unknown operation type "+typ)
				d.Ops = append(d.Ops, OpSummary{Index: i, Type: typ, Class: manifest.RiskAdmin})
				continue
			}
			if target.Blocked != "" {
				return api.Guardrailf("operation %d (%s) is blocked in this version of googleads: %s", i, typ, target.Blocked)
			}
			raise(d, i, target.Risk, typ+" is "+target.Risk+"-class")
			opClass = target.Risk
			if obj, ok = inner.(map[string]any); !ok {
				return api.Usagef("operation %d (%s) is not a JSON object", i, typ)
			}
			resource = ""
			if target.Mutate != nil {
				resource = target.Mutate.Resource
			}
		}
		sum := OpSummary{Index: i, Type: typ, Resource: resource, Class: opClass}
		seen := len(d.Findings)
		if err := inspectOperation(i, resource, obj, opt, d, &sum); err != nil {
			return err
		}
		for _, f := range d.Findings[seen:] {
			if manifest.RiskRank(f.Class) > manifest.RiskRank(sum.Class) {
				sum.Class = f.Class
			}
		}
		d.Ops = append(d.Ops, sum)
	}
	return nil
}

// Fields whose change on an update alters what Google bids.
var (
	campaignBidFields = []string{"biddingStrategy", "commission", "fixedCpm", "manualCpa", "manualCpc", "manualCpm",
		"manualCpv", "maximizeConversionValue", "maximizeConversions", "percentCpc", "targetCpa", "targetCpc", "targetCpm",
		"targetCpv", "targetImpressionShare", "targetRoas", "targetSpend", "realTimeBiddingSetting"}
	adGroupBidFields = []string{"cpcBidMicros", "cpmBidMicros", "cpvBidMicros", "fixedCpmMicros", "percentCpcBidMicros",
		"targetCpaMicros", "targetCpcMicros", "targetCpmMicros", "targetCpvMicros", "targetRoas"}
	criterionBidFields = []string{"cpcBidMicros", "cpmBidMicros", "cpvBidMicros", "percentCpcBidMicros", "bidModifier", "entityBid"}
)

// opCheck carries one create or update through the payload rules.
type opCheck struct {
	i    int
	verb string
	obj  map[string]any
	mask map[string]bool
	d    *Decision
	sum  *OpSummary
}

// set reports whether the operation writes field: present in the body, or,
// for an update, named by the updateMask (which can clear it).
func (o *opCheck) set(field string) bool {
	_, present := o.obj[field]
	return present || (o.verb == "update" && o.mask[field])
}

func (o *opCheck) spend(reason string) { raise(o.d, o.i, manifest.RiskSpend, reason) }

// createStatus applies the rule for a create of something that serves:
// status PAUSED is a write, anything else starts spend. With required, a
// missing status is refused outright, because Google's default is ENABLED.
func (o *opCheck) createStatus(what string, required bool) error {
	st, present := o.obj["status"]
	if !present || st == nil {
		if required {
			return api.Guardrailf(`operation %d: a %s create must set "status":"PAUSED"; a missing status is refused because Google's default is ENABLED, which starts spend`, o.i, what)
		}
		o.spend(what + " created without status PAUSED (Google's default is ENABLED)")
		return nil
	}
	if s, ok := st.(string); !ok || s != "PAUSED" {
		o.spend(what + " created with status " + show(st))
	}
	return nil
}

// updateStatus gates a status change on an update: PAUSED is a write,
// REMOVED is a delete, anything else (ENABLED) starts spend.
func (o *opCheck) updateStatus(what string) {
	if o.verb != "update" || !o.set("status") {
		return
	}
	switch st := o.obj["status"]; st {
	case "PAUSED":
	case "REMOVED":
		raise(o.d, o.i, manifest.RiskDelete, what+" status set to REMOVED")
	default:
		o.spend(what + " status set to " + show(st))
	}
}

// bidChange gates an update that writes any of fields.
func (o *opCheck) bidChange(what string, fields []string) {
	if o.verb != "update" {
		return
	}
	for _, f := range fields {
		if o.set(f) {
			o.spend(what + " bidding changed (" + f + ")")
			return
		}
	}
}

// inspectOperation applies the payload rules to one create, update or remove.
func inspectOperation(i int, resource string, op map[string]any, opt Options, d *Decision, sum *OpSummary) error {
	verbs := 0
	for _, v := range []string{"create", "update", "remove"} {
		if _, ok := op[v]; ok {
			verbs++
		}
	}
	if verbs > 1 {
		return api.Usagef("operation %d names more than one of create, update, remove", i)
	}
	if m, ok := op["updateMask"]; ok {
		s, isString := m.(string)
		if !isString {
			return api.Usagef("operation %d: updateMask must be a comma-separated string of field paths", i)
		}
		sum.UpdateMask = s
	}
	if rn, ok := op["remove"]; ok {
		what := resource
		if what == "" {
			what = "a resource"
		}
		sum.Verb = "remove"
		if s, ok := rn.(string); ok {
			sum.ResourceName = s
		}
		raise(d, i, manifest.RiskDelete, "removes "+what)
		return nil
	}
	verb, obj := "create", asObject(op["create"])
	if obj == nil {
		verb, obj = "update", asObject(op["update"])
	}
	if obj == nil {
		return nil
	}
	sum.Verb = verb
	sum.ResourceName, _ = obj["resourceName"].(string)
	sum.Name, _ = obj["name"].(string)
	if st, ok := obj["status"].(string); ok {
		sum.Status = st
	}
	o := &opCheck{i: i, verb: verb, obj: obj, mask: maskFields(op["updateMask"]), d: d, sum: sum}
	switch resource {
	case "Campaign":
		if verb == "create" {
			if err := o.createStatus("campaign", true); err != nil {
				return err
			}
		}
		o.updateStatus("campaign")
		if verb == "update" && o.set("campaignBudget") {
			o.spend("campaign moved to another budget")
		}
		o.bidChange("campaign", campaignBidFields)
	case "AdGroup":
		if verb == "create" {
			if err := o.createStatus("ad group", true); err != nil {
				return err
			}
		}
		o.updateStatus("ad group")
		o.bidChange("ad group", adGroupBidFields)
	case "AdGroupAd":
		if verb == "create" {
			if err := o.createStatus("ad", true); err != nil {
				return err
			}
		}
		o.updateStatus("ad")
	case "AdGroupCriterion":
		negative, _ := obj["negative"].(bool)
		_, keyword := obj["keyword"]
		if verb == "create" && !negative {
			if _, has := obj["status"]; keyword || has {
				what := "ad group criterion"
				if keyword {
					what = "keyword"
				}
				if err := o.createStatus(what, keyword); err != nil {
					return err
				}
			}
		}
		o.updateStatus("ad group criterion")
		o.bidChange("ad group criterion", criterionBidFields)
	case "CampaignCriterion":
		if o.set("bidModifier") {
			o.spend("campaign criterion bid modifier set")
		}
	case "CampaignBidModifier", "AdGroupBidModifier":
		if o.set("bidModifier") {
			o.spend("bid modifier set")
		}
	case "BiddingStrategy":
		if verb == "update" {
			for f := range obj {
				if f != "resourceName" && f != "name" {
					o.spend("portfolio bid strategy changed (" + f + ")")
					break
				}
			}
			for f := range o.mask {
				if f != "name" {
					o.spend("portfolio bid strategy changed (" + f + ")")
					break
				}
			}
		}
	case "BiddingSeasonalityAdjustment", "BiddingDataExclusion":
		o.spend("smart bidding adjustment " + verb + "d")
	case "AssetGroup":
		if verb == "create" {
			if err := o.createStatus("asset group", false); err != nil {
				return err
			}
		}
		o.updateStatus("asset group")
	case "ConversionAction":
		pf, present := obj["primaryForGoal"]
		if verb == "create" && pf != false {
			o.spend(`conversion action created as primary, which feeds bidding (primaryForGoal defaults to true); set "primaryForGoal": false to create it as secondary`)
		}
		if verb == "update" && (present || o.mask["primaryForGoal"]) && pf != false {
			o.spend("conversion action made primary for bidding")
		}
		if verb == "update" && o.set("status") && obj["status"] == "ENABLED" {
			o.spend("conversion action enabled")
		}
	case "CustomerConversionGoal", "CampaignConversionGoal":
		if v, present := obj["biddable"]; verb == "update" && (present || o.mask["biddable"]) && v != false {
			o.spend("conversion goal made biddable")
		}
	case "ConversionGoalCampaignConfig":
		if verb == "update" {
			o.spend("campaign conversion goal configuration changed")
		}
	case "CustomConversionGoal":
		if verb == "update" && o.set("status") && obj["status"] == "ENABLED" {
			o.spend("custom conversion goal enabled")
		}
	case "CampaignBudget":
		period, hasPeriod := obj["period"]
		if o.set("totalAmountMicros") || (hasPeriod && period != "DAILY") {
			return api.Guardrailf("operation %d: fixed-total budgets (totalAmountMicros, or a period other than DAILY) are refused in this version; the budget cap is a daily cap", i)
		}
		if verb == "update" && o.set("budgetNetworkSettings") {
			o.spend("budget network settings changed")
		}
		if o.set("amountMicros") {
			amount, err := capAmount(fmt.Sprintf("operation %d", i), "amountMicros", obj["amountMicros"], opt)
			if err != nil {
				return err
			}
			sum.AmountMicros = strconv.FormatInt(amount, 10)
			d.Budgets = append(d.Budgets, BudgetAmount{Operation: i, Verb: verb, ResourceName: sum.ResourceName, Micros: amount})
			if verb == "update" {
				o.spend("budget amount changed to " + config.FormatMicros(amount))
			} else {
				o.spend("budget created with amount " + config.FormatMicros(amount))
			}
		}
	}
	return nil
}

// capAmount reads one budget amount and holds it to the cap. subject names
// where it sits, for the message.
func capAmount(subject, field string, v any, opt Options) (int64, error) {
	amount, err := micros(v)
	if err != nil {
		return 0, api.Usagef("%s: %s %v", subject, field, err)
	}
	if opt.CapMicros == 0 {
		return 0, api.Guardrailf("%s sets a budget amount, but no budget cap is configured; a person sets one with: googleads config set budget-cap <amount>", subject)
	}
	if amount > opt.CapMicros {
		return 0, api.Guardrailf("%s: budget amount %s (%d micros) exceeds the configured cap of %s (%d micros); no flag overrides the cap",
			subject, config.FormatMicros(amount), amount, config.FormatMicros(opt.CapMicros), opt.CapMicros)
	}
	return amount, nil
}

// budgetAmountFields are the fields that set a budget amount, as parseStrict
// returns them: lowerCamelCase, whichever spelling the caller used.
var budgetAmountFields = map[string]bool{"amountMicros": true, "totalAmountMicros": true}

// scanAmounts walks a body of unknown shape and holds every budget amount
// field to the cap. Keys are visited in sorted order so the first refusal is
// always the same one.
func scanAmounts(v any, path string, opt Options, d *Decision) error {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := join(path, k)
			if budgetAmountFields[k] {
				amount, err := capAmount("the body at "+p, k, t[k], opt)
				if err != nil {
					return err
				}
				d.Budgets = append(d.Budgets, BudgetAmount{Operation: -1, Micros: amount})
			}
			if err := scanAmounts(t[k], p, opt, d); err != nil {
				return err
			}
		}
	case []any:
		for i, x := range t {
			if err := scanAmounts(x, fmt.Sprintf("%s[%d]", path, i), opt, d); err != nil {
				return err
			}
		}
	}
	return nil
}

func asObject(v any) map[string]any {
	o, _ := v.(map[string]any)
	return o
}

// maskFields returns the top-level fields an updateMask names, in camelCase.
func maskFields(v any) map[string]bool {
	s, _ := v.(string)
	out := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			top, _, _ := strings.Cut(p, ".")
			out[toCamel(top)] = true
		}
	}
	return out
}

func show(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return "a default through updateMask"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

var digitsOnly = regexp.MustCompile(`^[0-9]+$`)

// micros reads an amount the way Google accepts it, a JSON number or a
// decimal string, but only in its plain form: digits only. Anything else is
// refused rather than interpreted, so the cap compares exactly what Google
// would use.
func micros(v any) (int64, error) {
	var s string
	switch t := v.(type) {
	case nil:
		return 0, nil // cleared through updateMask
	case json.Number:
		s = t.String()
	case string:
		s = t
	default:
		return 0, fmt.Errorf("must be a whole number of micros, got %v", t)
	}
	if !digitsOnly.MatchString(s) {
		return 0, fmt.Errorf("must be written as a plain whole number of micros (digits only), got %q", s)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("is out of range for micros: %q", s)
	}
	return n, nil
}
