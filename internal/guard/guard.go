// Package guard decides, before anything is sent, whether a call may run. It
// starts from the method's reviewed risk class, raises it by what each
// operation in a mutate body does, holds budget amounts to the configured
// cap, and applies --force, --validate-only, read-only mode and the blocked
// list. A refusal is an *api.Error of kind usage.
package guard

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/config"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// Options are the flags and settings that bear on the decision.
type Options struct {
	Force bool
	// ValidateOnly is the --validate-only flag. A validateOnly field inside
	// the body does not count: only the flag relaxes a gate.
	ValidateOnly bool
	ReadOnly     bool
	CapMicros    int64 // 0 means no cap is configured
}

// Finding is one reason a call needs --force.
type Finding struct {
	Operation int // index in the operation list; -1 for the method itself
	Class     string
	Reason    string
}

// Decision is the outcome for a call that may be sent.
type Decision struct {
	Class    string    // the highest class found; what the client is told
	Findings []Finding // every reason above write
}

// Check decides whether meth may be called with body.
func Check(m *manifest.Manifest, meth *manifest.Method, body []byte, opt Options) (Decision, error) {
	name := "googleads " + strings.Join(meth.Command, " ")
	if meth.Blocked != "" {
		return Decision{}, api.Usagef("%s is blocked in this version of googleads: %s", name, meth.Blocked)
	}
	d := Decision{Class: meth.Risk}
	if manifest.NeedsForce(meth.Risk) {
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
	if opt.ReadOnly && d.Class != manifest.RiskRead && !opt.ValidateOnly {
		return Decision{}, api.Usagef("read-only mode blocks %s (%s-class); --validate-only calls are still allowed", name, d.Class)
	}
	if manifest.NeedsForce(d.Class) && !opt.Force && !opt.ValidateOnly {
		return Decision{}, api.Usagef("%s needs --force: %s; re-run with --force to confirm, or with --validate-only to let Google check it without applying anything",
			name, describe(d.Findings))
	}
	return d, nil
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

// raise lifts the decision's class and records why when the class needs --force.
func raise(d *Decision, op int, class, reason string) {
	if manifest.RiskRank(class) > manifest.RiskRank(d.Class) {
		d.Class = class
	}
	if manifest.NeedsForce(class) {
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
		resource := meth.Mutate.Resource
		if field == "mutateOperations" {
			if len(obj) != 1 {
				return api.Usagef("operation %d must name exactly one operation type", i)
			}
			var typ string
			var inner any
			for k, v := range obj {
				typ, inner = k, v
			}
			target := m.ByID(m.GenericOperations[typ])
			if target == nil {
				raise(d, i, manifest.RiskAdmin, "unknown operation type "+typ)
				continue
			}
			if target.Blocked != "" {
				return api.Usagef("operation %d (%s) is blocked in this version of googleads: %s", i, typ, target.Blocked)
			}
			raise(d, i, target.Risk, typ+" is "+target.Risk+"-class")
			if obj, ok = inner.(map[string]any); !ok {
				return api.Usagef("operation %d (%s) is not a JSON object", i, typ)
			}
			resource = ""
			if target.Mutate != nil {
				resource = target.Mutate.Resource
			}
		}
		if err := inspectOperation(i, resource, obj, opt, d); err != nil {
			return err
		}
	}
	return nil
}

// inspectOperation applies the payload rules to one create, update or remove.
func inspectOperation(i int, resource string, op map[string]any, opt Options, d *Decision) error {
	if _, ok := op["remove"]; ok {
		what := resource
		if what == "" {
			what = "a resource"
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
	mask := maskFields(op["updateMask"])
	set := func(field string) bool {
		_, present := obj[field]
		return present || (verb == "update" && mask[field])
	}
	switch resource {
	case "Campaign":
		status, isString := obj["status"].(string)
		paused := isString && status == "PAUSED"
		switch {
		case verb == "create" && !paused:
			raise(d, i, manifest.RiskSpend, "campaign created without status PAUSED (Google's default is ENABLED)")
		case verb == "update" && set("status") && !paused:
			raise(d, i, manifest.RiskSpend, "campaign status set to "+show(obj["status"]))
		}
		if verb == "update" && set("campaignBudget") {
			raise(d, i, manifest.RiskSpend, "campaign moved to another budget")
		}
	case "CampaignBudget":
		if set("totalAmountMicros") || obj["period"] == "CUSTOM_PERIOD" {
			return api.Usagef("operation %d: fixed-total budgets (totalAmountMicros, period CUSTOM_PERIOD) are refused in this version; the budget cap is a daily cap", i)
		}
		if set("amountMicros") {
			amount, err := micros(obj["amountMicros"])
			if err != nil {
				return api.Usagef("operation %d: amountMicros %v", i, err)
			}
			if opt.CapMicros == 0 {
				return api.Usagef("operation %d sets a budget amount, but no budget cap is configured; a person sets one with: googleads config set budget-cap <amount>", i)
			}
			if amount > opt.CapMicros {
				return api.Usagef("operation %d: budget amount %s (%d micros) exceeds the configured cap of %s (%d micros); --force does not override the cap",
					i, config.FormatMicros(amount), amount, config.FormatMicros(opt.CapMicros), opt.CapMicros)
			}
			if verb == "update" {
				raise(d, i, manifest.RiskSpend, "budget amount changed to "+config.FormatMicros(amount))
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
