package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/memohnsen/gads-cli/internal/manifest"
)

// cleanDoc flattens a description into one plain line. pflag reads a
// back-quoted word in a usage string as the flag's value type.
func cleanDoc(doc string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(doc, "`", "")), " ")
}

// firstLine is the first sentence of a description, for a command's Short.
func firstLine(doc string) string {
	d := cleanDoc(doc)
	if i := strings.Index(d, ". "); i >= 0 {
		d = d[:i+1]
	}
	return d
}

// trimEnum drops UNSPECIFIED and UNKNOWN, which no request should send.
func trimEnum(values []string) []string {
	var out []string
	for _, v := range values {
		if v != "UNSPECIFIED" && v != "UNKNOWN" {
			out = append(out, v)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// fieldLine renders one field for help and `schema`, for example
//
//	status string (one of: ENABLED, PAUSED, REMOVED): The status of the campaign.
func fieldLine(f manifest.Field) string {
	t := f.Type
	switch {
	case f.Ref != "":
		t = manifest.ShortName(f.Ref)
	case f.Format == "int64" || f.Format == "uint64":
		t = "int64 as string"
	}
	if f.Repeated {
		t = "list of " + t
	}
	var notes []string
	if f.Required {
		notes = append(notes, "required")
	}
	if f.Immutable {
		notes = append(notes, "immutable")
	}
	if f.ReadOnly {
		notes = append(notes, "read-only")
	}
	if e := trimEnum(f.Enum); len(e) > 0 {
		notes = append(notes, "one of: "+strings.Join(e, ", "))
	}
	s := "  " + f.Name + " " + t
	if len(notes) > 0 {
		s += " (" + strings.Join(notes, "; ") + ")"
	}
	if f.Doc != "" {
		s += ": " + cleanDoc(f.Doc)
	}
	return s
}

func writeFieldTable(w io.Writer, fields []manifest.Field) {
	for _, f := range fields {
		fmt.Fprintln(w, fieldLine(f))
	}
}

// resourceSchemaID finds the schema of a resource short name, preferring the
// Resources package when the name is ambiguous.
func (a *app) resourceSchemaID(name string) string {
	id, candidates := a.manifest.LookupSchema(name)
	if id != "" {
		return id
	}
	for _, c := range candidates {
		if strings.Contains(c, "Resources__") {
			return c
		}
	}
	return ""
}

// longHelp renders what a caller needs without the API docs: the method,
// its risk, arguments, and for a mutate the fields of the resource.
func (a *app) longHelp(meth *manifest.Method) string {
	var b strings.Builder
	if meth.Blocked != "" {
		b.WriteString("BLOCKED in this version of googleads: " + meth.Blocked + "\n\n")
	}
	if meth.Description != "" {
		b.WriteString(cleanDoc(meth.Description) + "\n")
	}
	fmt.Fprintf(&b, "\n%s %s (risk: %s)\n", meth.HTTPMethod, meth.Path, meth.Risk)
	if meth.NeedsCustomer {
		b.WriteString("Customer: --customer-id, GOOGLE_ADS_CUSTOMER_ID or `googleads config set customer-id`.\n")
	}
	var positional []manifest.Param
	for _, p := range meth.PathParams {
		if p.Name != "customerId" {
			positional = append(positional, p)
		}
	}
	if len(positional) > 0 {
		b.WriteString("\nArguments:\n")
		for _, p := range positional {
			b.WriteString("  " + p.Name)
			if p.Pattern != "" {
				b.WriteString(" (form " + p.Pattern + ")")
			}
			if p.Doc != "" {
				b.WriteString(": " + cleanDoc(p.Doc))
			}
			b.WriteString("\n")
		}
	}
	if isReport(meth) {
		b.WriteString("\nExample:\n  googleads " + strings.Join(meth.Command, " ") +
			` --query "SELECT campaign.id, campaign.name, metrics.cost_micros FROM campaign WHERE segments.date DURING LAST_7_DAYS"` + "\n")
	}
	switch {
	case meth.Mutate != nil && meth.Mutate.OperationsField == "mutateOperations":
		b.WriteString("\nOperation types, one per entry of mutateOperations:\n")
		for _, typ := range sortedKeys(a.manifest.GenericOperations) {
			res := ""
			if t := a.manifest.ByID(a.manifest.GenericOperations[typ]); t != nil && t.Mutate != nil {
				res = t.Mutate.Resource
			}
			fmt.Fprintf(&b, "  %s -> %s\n", typ, res)
		}
		b.WriteString("\nRun `googleads schema <Resource>` for a resource's fields. Temporary resource names\n" +
			"(customers/<id>/campaigns/-1) link operations inside one request.\n")
	case meth.Mutate != nil && meth.Mutate.Resource != "":
		if id := a.resourceSchemaID(meth.Mutate.Resource); id != "" {
			fields := a.manifest.Schemas[id].Fields
			fmt.Fprintf(&b, "\n%s fields, for create and update (`googleads schema <Name>` shows nested types):\n", meth.Mutate.Resource)
			writeFieldTable(&b, fields)
			b.WriteString("\nExample body:\n  " + exampleBody(meth, fields) + "\n")
		}
	case meth.Request != "":
		if s, ok := a.manifest.Schemas[meth.Request]; ok && len(s.Fields) > 0 {
			b.WriteString("\nBody fields:\n")
			writeFieldTable(&b, s.Fields)
		}
	}
	if meth.ValidateOnly {
		b.WriteString("\n--validate-only: Google checks the request completely and applies nothing.\n")
	}
	if meth.Paging != manifest.PagingNone {
		fmt.Fprintf(&b, "--all follows page tokens and prints one merged JSON array of the %s rows.\n", meth.ResultsField)
	}
	return b.String()
}

// exampleBody renders a skeleton for a per-resource mutate: one create with
// the fields Google marks as required.
func exampleBody(meth *manifest.Method, fields []manifest.Field) string {
	create := map[string]any{}
	for _, f := range fields {
		if f.Required && !f.ReadOnly {
			create[f.Name] = placeholderValue(f)
		}
	}
	op := map[string]any{"create": create}
	body := map[string]any{"operations": []any{op}}
	if meth.Mutate.OperationsField == "operation" {
		body = map[string]any{"operation": op}
	}
	raw, _ := json.Marshal(body)
	return string(raw)
}

func placeholderValue(f manifest.Field) any {
	switch {
	case f.Repeated:
		return []any{}
	case f.Type == "object" || f.Type == "map":
		return map[string]any{}
	case f.Format == "int64" || f.Format == "uint64":
		return "0"
	case f.Type == "integer" || f.Type == "number":
		return 0
	case f.Type == "boolean":
		return false
	}
	if e := trimEnum(f.Enum); len(e) > 0 {
		return e[0]
	}
	return ""
}
