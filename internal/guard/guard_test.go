package guard

import (
	"errors"
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

const cap30 = 30000000

func testManifest() *manifest.Manifest {
	mut := func(field, res string) *manifest.Mutate {
		return &manifest.Mutate{OperationsField: field, Resource: res}
	}
	return &manifest.Manifest{
		Methods: []manifest.Method{
			{ID: "customers.googleAds.search", Command: []string{"search"}, Risk: "read"},
			{ID: "customers.googleAds.mutate", Command: []string{"mutate"}, Risk: "write", Mutate: mut("mutateOperations", "")},
			{ID: "customers.campaigns.mutate", Command: []string{"campaigns", "mutate"}, Risk: "write", Mutate: mut("operations", "Campaign")},
			{ID: "customers.campaignBudgets.mutate", Command: []string{"campaign-budgets", "mutate"}, Risk: "write", Mutate: mut("operations", "CampaignBudget")},
			{ID: "customers.adGroups.mutate", Command: []string{"ad-groups", "mutate"}, Risk: "write", Mutate: mut("operations", "AdGroup")},
			{ID: "customers.mutate", Command: []string{"customer", "mutate"}, Risk: "write", Mutate: mut("operation", "Customer")},
			{ID: "customers.customerUserAccesses.mutate", Command: []string{"customer-user-accesses", "mutate"}, Risk: "admin", Mutate: mut("operation", "CustomerUserAccess")},
			{ID: "customers.batchJobs.addOperations", Command: []string{"batch-jobs", "add-operations"}, Risk: "admin", Mutate: mut("mutateOperations", "")},
			{ID: "customers.recommendations.apply", Command: []string{"recommendations", "apply"}, Risk: "spend", Blocked: "applies amounts Google chooses"},
			{ID: "customers.reservations.bookCampaigns", Command: []string{"reservations", "book-campaigns"}, Risk: "spend", Blocked: "commits spend"},
		},
		GenericOperations: map[string]string{
			"campaignOperation":       "customers.campaigns.mutate",
			"campaignBudgetOperation": "customers.campaignBudgets.mutate",
			"adGroupOperation":        "customers.adGroups.mutate",
			"bookCampaignsOperation":  "customers.reservations.bookCampaigns",
		},
	}
}

func check(id, body string, opt Options) (Decision, error) {
	m := testManifest()
	return Check(m, m.ByID(id), []byte(body), opt)
}

func TestCheck(t *testing.T) {
	withCap := Options{CapMicros: cap30}
	cases := []struct {
		name, id, body string
		opt            Options
		wantClass      string // expected class when the call is allowed
		wantErr        string // expected substring of a usage refusal
	}{
		{"read method", "customers.googleAds.search", `{"query":"SELECT customer.id FROM customer"}`, Options{}, "read", ""},
		{"paused campaign and budget", "customers.googleAds.mutate", `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"amountMicros":"29610000"}}},{"campaignOperation":{"create":{"status":"PAUSED","name":"x"}}}]}`, withCap, "write", ""},
		{"campaign without status", "customers.googleAds.mutate", `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x"}}}]}`, withCap, "", "Google's default is ENABLED"},
		{"campaign without status, forced", "customers.googleAds.mutate", `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x"}}}]}`, Options{Force: true}, "spend", ""},
		{"campaign without status, validate-only", "customers.googleAds.mutate", `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x"}}}]}`, Options{ValidateOnly: true}, "spend", ""},
		{"numeric status is not PAUSED", "customers.campaigns.mutate", `{"operations":[{"create":{"status":3}}]}`, Options{}, "", "needs --force"},
		{"enable campaign", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","status":"ENABLED"},"updateMask":"status"}]}`, Options{}, "", "status set to ENABLED"},
		{"pause campaign", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","status":"PAUSED"},"updateMask":"status"}]}`, Options{}, "write", ""},
		{"status only in the mask", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2"},"updateMask":"status"}]}`, Options{}, "", "needs --force"},
		{"move to another budget", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","campaignBudget":"customers/1/campaignBudgets/9"},"updateMask":"campaign_budget"}]}`, Options{}, "", "another budget"},
		{"snake_case spelling", "customers.googleAds.mutate", `{"mutate_operations":[{"campaign_operation":{"create":{"status":"ENABLED"}}}]}`, Options{}, "", "needs --force"},
		{"duplicate key", "customers.campaigns.mutate", `{"operations":[{"create":{"status":"PAUSED","status":"ENABLED"}}]}`, Options{Force: true}, "", "duplicate key"},
		{"both spellings", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"amountMicros":"1","amount_micros":"999999999999"}}]}`, Options{Force: true, CapMicros: cap30}, "", "both spellings"},
		{"budget at the cap", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"30000000"}}]}`, withCap, "write", ""},
		{"budget one micro above", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"30000001"}}]}`, withCap, "", "exceeds the configured cap"},
		{"above the cap, forced", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"30000001"}}]}`, Options{Force: true, CapMicros: cap30}, "", "exceeds"},
		{"above the cap, validate-only", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"30000001"}}]}`, Options{ValidateOnly: true, CapMicros: cap30}, "", "exceeds"},
		{"no cap configured", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"1000000"}}]}`, Options{Force: true}, "", "no budget cap"},
		{"budget update within the cap", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaignBudgets/9","amountMicros":"5000000"},"updateMask":"amount_micros"}]}`, withCap, "", "budget amount changed to 5"},
		{"budget update within the cap, forced", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaignBudgets/9","amountMicros":"5000000"},"updateMask":"amount_micros"}]}`, Options{Force: true, CapMicros: cap30}, "spend", ""},
		{"fixed total", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"totalAmountMicros":"1000000"}}]}`, Options{Force: true, CapMicros: cap30}, "", "fixed-total"},
		{"custom period", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"period":"CUSTOM_PERIOD","amountMicros":"1"}}]}`, Options{Force: true, CapMicros: cap30}, "", "fixed-total"},
		{"remove", "customers.adGroups.mutate", `{"operations":[{"remove":"customers/1/adGroups/3"}]}`, Options{}, "", "removes AdGroup"},
		{"unknown generic type", "customers.googleAds.mutate", `{"mutateOperations":[{"fooOperation":{"create":{}}}]}`, Options{}, "", "unknown operation type fooOperation"},
		{"blocked method", "customers.recommendations.apply", `{"operations":[]}`, Options{Force: true, ValidateOnly: true}, "", "blocked"},
		{"blocked generic type", "customers.googleAds.mutate", `{"mutateOperations":[{"bookCampaignsOperation":{}}]}`, Options{Force: true}, "", "blocked"},
		{"batch job above the cap", "customers.batchJobs.addOperations", `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"amountMicros":"99000000"}}}]}`, Options{Force: true, CapMicros: cap30}, "", "exceeds"},
		{"batch job needs force", "customers.batchJobs.addOperations", `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"amountMicros":"1000000"}}}]}`, withCap, "", "admin-class"},
		{"batch job forced", "customers.batchJobs.addOperations", `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"amountMicros":"1000000"}}}]}`, Options{Force: true, CapMicros: cap30}, "admin", ""},
		{"admin method", "customers.customerUserAccesses.mutate", `{"operation":{"update":{"accessRole":"ADMIN"}}}`, Options{}, "", "admin-class"},
		{"single operation shape", "customers.mutate", `{"operation":{"update":{"autoTaggingEnabled":true},"updateMask":"auto_tagging_enabled"}}`, Options{}, "write", ""},
		{"read-only blocks writes", "customers.adGroups.mutate", `{"operations":[{"create":{"name":"x"}}]}`, Options{ReadOnly: true}, "", "read-only mode"},
		{"read-only allows validate-only", "customers.adGroups.mutate", `{"operations":[{"create":{"name":"x"}}]}`, Options{ReadOnly: true, ValidateOnly: true}, "write", ""},
		{"read-only allows reads", "customers.googleAds.search", `{"query":"x"}`, Options{ReadOnly: true}, "read", ""},
		{"not JSON", "customers.campaigns.mutate", `{"operations":`, Options{Force: true}, "", "cannot read"},
		{"operations not an array", "customers.campaigns.mutate", `{"operations":{"create":{}}}`, Options{Force: true}, "", "must be a JSON array"},
		{"validateOnly in the body does not count", "customers.campaigns.mutate", `{"validateOnly":true,"operations":[{"create":{"status":"ENABLED"}}]}`, Options{}, "", "needs --force"},
		{"remove and create, forced above the cap", "customers.campaignBudgets.mutate", `{"operations":[{"remove":"customers/1/campaignBudgets/9","create":{"amountMicros":"999000000000"}}]}`, Options{Force: true, CapMicros: cap30}, "", "more than one of create, update, remove"},
		{"create and update", "customers.campaigns.mutate", `{"operations":[{"create":{"status":"PAUSED"},"update":{"resourceName":"customers/1/campaigns/2","status":"ENABLED"},"updateMask":"status"}]}`, Options{}, "", "more than one of create, update, remove"},
		{"create and update in snake_case", "customers.googleAds.mutate", `{"mutate_operations":[{"campaign_operation":{"create":{"status":"PAUSED"},"update":{"status":"ENABLED"}}}]}`, Options{Force: true}, "", "more than one of create, update, remove"},
		{"numeric period", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"period":3,"amountMicros":"1"}}]}`, Options{Force: true, CapMicros: cap30}, "", "fixed-total"},
		{"daily period", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"period":"DAILY","amountMicros":"1"}}]}`, withCap, "write", ""},
		{"updateMask not a string", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2"},"updateMask":{"paths":["status"]}}]}`, Options{Force: true}, "", "updateMask must be"},
		{"amount only in the mask", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaignBudgets/9"},"updateMask":"amount_micros"}]}`, withCap, "", "budget amount changed to"},
		{"amount only in the mask, forced", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaignBudgets/9"},"updateMask":"amount_micros"}]}`, Options{Force: true, CapMicros: cap30}, "spend", ""},
		{"amount only in the mask, no cap", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaignBudgets/9"},"updateMask":"amount_micros"}]}`, Options{Force: true}, "", "no budget cap"},
	}
	for _, c := range cases {
		d, err := check(c.id, c.body, c.opt)
		if c.wantErr != "" {
			var e *api.Error
			if !errors.As(err, &e) || e.Kind != api.KindUsage || !strings.Contains(e.Message, c.wantErr) {
				t.Errorf("%s: err = %v, want a usage error containing %q", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil || d.Class != c.wantClass {
			t.Errorf("%s: class %q err %v, want class %q", c.name, d.Class, err, c.wantClass)
		}
	}
}

func TestAmountForms(t *testing.T) {
	for _, amount := range []string{`" 29610000"`, `"+29610000"`, `2.961e7`, `"29610000.0"`, `29610000.0`, `-1`, `"abc"`, `true`} {
		body := `{"operations":[{"create":{"amountMicros":` + amount + `}}]}`
		_, err := check("customers.campaignBudgets.mutate", body, Options{Force: true, CapMicros: cap30})
		if err == nil || !strings.Contains(err.Error(), "micros") {
			t.Errorf("amount %s: err = %v, want a refusal about micros", amount, err)
		}
	}
	for _, amount := range []string{`29610000`, `"29610000"`} {
		body := `{"operations":[{"create":{"amountMicros":` + amount + `}}]}`
		if _, err := check("customers.campaignBudgets.mutate", body, Options{CapMicros: cap30}); err != nil {
			t.Errorf("amount %s: %v", amount, err)
		}
	}
}

func TestFindingsNameEveryOperation(t *testing.T) {
	body := `{"mutateOperations":[{"adGroupOperation":{"remove":"customers/1/adGroups/3"}},{"campaignOperation":{"create":{"name":"x"}}}]}`
	_, err := check("customers.googleAds.mutate", body, Options{})
	if err == nil || !strings.Contains(err.Error(), "operation 0: removes AdGroup") || !strings.Contains(err.Error(), "operation 1: campaign created") {
		t.Fatalf("err = %v", err)
	}
	d, err := check("customers.googleAds.mutate", body, Options{Force: true})
	if err != nil || len(d.Findings) != 2 {
		t.Fatalf("findings %+v err %v", d.Findings, err)
	}
}

// An api call whose path matches no method still has every budget amount in
// its body held to the cap, wherever it sits.
func TestUnmatchedBodyAmountScan(t *testing.T) {
	raw := &manifest.Method{ID: "raw POST v25/x", Command: []string{"api", "POST", "v25/x"}, Risk: "admin"}
	cases := []struct {
		name, body string
		opt        Options
		wantErr    string
	}{
		{"no cap", `{"operations":[{"create":{"amountMicros":"999999999999"}}]}`, Options{Force: true}, "no budget cap"},
		{"snake case, no cap", `{"operations":[{"create":{"amount_micros":"1"}}]}`, Options{Force: true}, "no budget cap"},
		{"total amount, no cap", `{"x":{"totalAmountMicros":"1"}}`, Options{Force: true}, "no budget cap"},
		{"total amount snake case over cap", `{"x":[{"y":{"total_amount_micros":"30000001"}}]}`, Options{Force: true, CapMicros: cap30}, "exceeds the configured cap"},
		{"over cap", `{"operations":[{"update":{"amountMicros":"30000001"}}]}`, Options{Force: true, CapMicros: cap30}, "exceeds the configured cap"},
		{"not digits", `{"a":{"amountMicros":"2.9e7"}}`, Options{Force: true, CapMicros: cap30}, "micros"},
		{"object value", `{"a":{"amountMicros":{"v":1}}}`, Options{Force: true, CapMicros: cap30}, "micros"},
		{"mixed spellings", `{"amountMicros":"1","amount_micros":"999999999999"}`, Options{Force: true, CapMicros: cap30}, "both spellings"},
		{"under cap", `{"operations":[{"create":{"amountMicros":"29610000"}}]}`, Options{Force: true, CapMicros: cap30}, ""},
		{"no amount", `{"operations":[{"create":{"name":"x"}}]}`, Options{Force: true}, ""},
		{"under cap still needs force", `{"amountMicros":"1"}`, Options{CapMicros: cap30}, "needs --force"},
	}
	for _, c := range cases {
		d, err := Check(testManifest(), raw, []byte(c.body), Options{Force: c.opt.Force, CapMicros: c.opt.CapMicros, Unmatched: true})
		switch {
		case c.wantErr == "" && (err != nil || d.Class != "admin"):
			t.Errorf("%s: class %q err %v", c.name, d.Class, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: err %v, want %q", c.name, err, c.wantErr)
		}
	}
}
