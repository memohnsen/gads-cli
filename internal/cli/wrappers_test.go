package cli

import (
	"strings"
	"testing"

	"github.com/memohnsen/gads-cli/internal/config"
	"github.com/memohnsen/gads-cli/internal/manifest"
)

// realApp is testApp on the embedded manifest, so the wrappers resolve real
// method IDs and the guard sees real resources.
func realApp(t *testing.T, srv *fakeServer) (*app, *fakeServer) {
	t.Helper()
	m, err := manifest.Load()
	if err != nil {
		t.Fatal(err)
	}
	if srv == nil {
		srv = newFakeServer(t, reply(`{"results":[]}`))
	}
	a, _, _ := testApp(t, srv, config.Resolved{CustomerID: "1234567890", BudgetCapMicros: 50_000_000})
	a.manifest = m
	return a, srv
}

type wcase struct {
	name     string
	args     []string
	wantCode int
	path     string   // request path suffix when sent
	contains []string // substrings of the body sent
	errHas   string
}

func runWrapperCases(t *testing.T, cases []wcase) {
	t.Helper()
	for _, c := range cases {
		a, srv := realApp(t, nil)
		code := a.run(c.args)
		errOut := a.stderr.(interface{ String() string }).String()
		if code != c.wantCode {
			t.Errorf("%s: exit %d, want %d: %s", c.name, code, c.wantCode, errOut)
			continue
		}
		if c.errHas != "" && !strings.Contains(errOut, c.errHas) {
			t.Errorf("%s: stderr %q lacks %q", c.name, errOut, c.errHas)
		}
		if c.path == "" {
			if srv.hits.Load() != 0 && c.wantCode != 0 {
				t.Errorf("%s: a refused call reached the network", c.name)
			}
			continue
		}
		last := len(srv.uris) - 1
		if last < 0 || !strings.HasSuffix(srv.uris[last], c.path) {
			t.Errorf("%s: uris %v, want suffix %s", c.name, srv.uris, c.path)
			continue
		}
		for _, s := range c.contains {
			if !strings.Contains(srv.bodies[last], s) {
				t.Errorf("%s: body %s lacks %s", c.name, srv.bodies[last], s)
			}
		}
	}
}

func TestStatusWrappers(t *testing.T) {
	runWrapperCases(t, []wcase{
		{"pause campaign is a dry run", []string{"pause", "campaign", "42"}, 0, "/customers/1234567890/campaigns:mutate",
			[]string{`"resourceName":"customers/1234567890/campaigns/42"`, `"status":"PAUSED"`, `"updateMask":"status"`, `"validateOnly":true`}, ""},
		{"pause ad applies", []string{"pause", "ad", "7~8", "--apply"}, 0, "/adGroupAds:mutate", []string{`adGroupAds/7~8`, `"status":"PAUSED"`}, ""},
		{"enable keyword needs flags", []string{"enable", "keyword", "7~9"}, 2, "", nil, "--confirm-spend"},
		{"enable keyword, apply alone", []string{"enable", "keyword", "7~9", "--apply"}, 2, "", nil, "--confirm-spend"},
		{"enable keyword, confirmed", []string{"enable", "keyword", "7~9", "--apply", "--confirm-spend"}, 0, "/adGroupCriteria:mutate",
			[]string{`adGroupCriteria/7~9`, `"status":"ENABLED"`}, ""},
		{"enable ad group, validate-only", []string{"enable", "ad-group", "7", "--validate-only"}, 0, "/adGroups:mutate", []string{`"validateOnly":true`}, ""},
		{"bad id", []string{"pause", "keyword", "79"}, 2, "", nil, "<adGroupId>~<id>"},
	})
}

func TestCreateWrappers(t *testing.T) {
	runWrapperCases(t, []wcase{
		{"campaign on an existing budget", []string{"create", "campaign", "--name", "Brand", "--budget-id", "5", "--location", "2840", "--bidding", "maximize-clicks", "--max-cpc", "2"},
			0, "/googleAds:mutate", []string{`"status":"PAUSED"`, `"advertisingChannelType":"SEARCH"`, `campaignBudgets/5"`, `"targetSpend":{"cpcBidCeilingMicros":"2000000"}`,
				`"geoTargetConstant":"geoTargetConstants/2840"`, `"containsEuPoliticalAdvertising":"DOES_NOT_CONTAIN_EU_POLITICAL_ADVERTISING"`, `"validateOnly":true`}, ""},
		{"campaign with a new budget is spend-class", []string{"create", "campaign", "--name", "Brand", "--budget-amount", "10"}, 2, "", nil, "budget created with amount 10"},
		{"campaign with a new budget, validate-only", []string{"create", "campaign", "--name", "Brand", "--budget-amount", "10", "--validate-only"},
			0, "/googleAds:mutate", []string{`"campaignBudgetOperation"`, `"amountMicros":"10000000"`, `"explicitlyShared":false`}, ""},
		{"campaign with a budget above the cap", []string{"create", "campaign", "--name", "Brand", "--budget-amount", "60", "--validate-only"}, 2, "", nil, "exceeds the configured cap"},
		{"campaign needs one budget source", []string{"create", "campaign", "--name", "Brand"}, 2, "", nil, "exactly one"},
		{"ad group", []string{"create", "ad-group", "--campaign-id", "42", "--name", "G", "--cpc-bid", "1.5", "--apply"}, 0, "/adGroups:mutate",
			[]string{`"status":"PAUSED"`, `"cpcBidMicros":"1500000"`, `"type":"SEARCH_STANDARD"`}, ""},
		{"keywords", []string{"create", "keywords", "--ad-group-id", "7", "--keyword", "running shoes", "--keyword", "trail shoes", "--match-type", "exact"}, 0,
			"/adGroupCriteria:mutate", []string{`"text":"running shoes"`, `"text":"trail shoes"`, `"matchType":"EXACT"`, `"status":"PAUSED"`}, ""},
		{"campaign negatives", []string{"create", "keywords", "--campaign-id", "42", "--negative", "--keyword", "free", "--match-type", "BROAD"}, 0,
			"/campaignCriteria:mutate", []string{`"negative":true`, `"text":"free"`}, ""},
		{"positive keywords need an ad group", []string{"create", "keywords", "--campaign-id", "42", "--keyword", "x"}, 2, "", nil, "negative"},
		{"rsa", []string{"create", "rsa", "--ad-group-id", "7", "--headline", "A", "--headline", "B", "--headline", "C", "--description", "D1", "--description", "D2",
			"--final-url", "https://example.com", "--path1", "shoes"}, 0, "/adGroupAds:mutate",
			[]string{`"status":"PAUSED"`, `"responsiveSearchAd":{"descriptions":[{"text":"D1"},{"text":"D2"}],"headlines":[{"text":"A"},{"text":"B"},{"text":"C"}],"path1":"shoes"}`, `"finalUrls":["https://example.com"]`}, ""},
		{"rsa needs three headlines", []string{"create", "rsa", "--ad-group-id", "7", "--headline", "A", "--description", "D1", "--description", "D2", "--final-url", "https://e.x"}, 2, "", nil, "3 to 15"},
		{"sitelink linked to a campaign", []string{"create", "sitelink", "--link-text", "Pricing", "--final-url", "https://example.com/pricing", "--campaign-id", "42"}, 0,
			"/googleAds:mutate", []string{`"assetOperation"`, `"linkText":"Pricing"`, `"campaignAssetOperation"`, `"fieldType":"SITELINK"`}, ""},
		{"text asset", []string{"create", "text-asset", "--text", "Free shipping"}, 0, "/assets:mutate", []string{`"textAsset":{"text":"Free shipping"}`}, ""},
		{"budget is spend-class", []string{"create", "budget", "--name", "B", "--amount", "20", "--apply"}, 2, "", nil, "--confirm-spend"},
		{"budget, confirmed", []string{"create", "budget", "--name", "B", "--amount", "20", "--apply", "--confirm-spend"}, 0, "/campaignBudgets:mutate",
			[]string{`"amountMicros":"20000000"`}, ""},
		{"secondary conversion action", []string{"create", "conversion-action", "--name", "Lead", "--category", "submit_lead_form", "--apply"}, 0,
			"/conversionActions:mutate", []string{`"primaryForGoal":false`, `"category":"SUBMIT_LEAD_FORM"`}, ""},
		{"primary conversion action is gated", []string{"create", "conversion-action", "--name", "Lead", "--primary", "--apply"}, 2, "", nil, "primary"},
	})
}

func TestUpdateAndLinkWrappers(t *testing.T) {
	runWrapperCases(t, []wcase{
		{"rename campaign", []string{"update", "campaign", "42", "--name", "New", "--apply"}, 0, "/campaigns:mutate", []string{`"name":"New"`, `"updateMask":"name"`}, ""},
		{"campaign bidding is gated", []string{"update", "campaign", "42", "--bidding", "maximize-conversions", "--target-cpa", "12", "--apply"}, 2, "", nil, "bidding changed"},
		{"campaign budget move is gated", []string{"update", "campaign", "42", "--budget-id", "5", "--apply"}, 2, "", nil, "another budget"},
		{"ad group bid is gated", []string{"update", "ad-group", "7", "--cpc-bid", "3", "--apply"}, 2, "", nil, "cpcBidMicros"},
		{"keyword final URL", []string{"update", "keyword", "7~9", "--final-url", "https://e.x/k", "--apply"}, 0, "/adGroupCriteria:mutate", []string{`"updateMask":"final_urls"`}, ""},
		{"rsa headlines", []string{"update", "rsa", "99", "--headline", "A", "--headline", "B", "--headline", "C", "--path2", "sale", "--apply"}, 0, "/ads:mutate",
			[]string{`"resourceName":"customers/1234567890/ads/99"`, `"updateMask":"responsive_search_ad.headlines,responsive_search_ad.path2"`}, ""},
		{"rsa needs a field", []string{"update", "rsa", "99"}, 2, "", nil, "nothing to update"},
		{"budget amount is gated", []string{"update", "budget", "9", "--amount", "5", "--apply"}, 2, "", nil, "budget amount changed to 5"},
		{"budget amount, validate-only", []string{"update", "budget", "9", "--amount", "5", "--validate-only"}, 0, "/campaignBudgets:mutate", []string{`"amountMicros":"5000000"`}, ""},
		{"conversion made secondary", []string{"update", "conversion-action", "3", "--primary=false", "--apply"}, 0, "/conversionActions:mutate", []string{`"primaryForGoal":false`}, ""},
		{"conversion made primary is gated", []string{"update", "conversion-action", "3", "--primary", "--apply"}, 2, "", nil, "made primary"},
		{"link sitelink", []string{"link", "sitelink", "8", "--ad-group-id", "7", "--apply"}, 0, "/adGroupAssets:mutate", []string{`"asset":"customers/1234567890/assets/8"`}, ""},
		{"unlink sitelink is a delete", []string{"unlink", "sitelink", "8", "--campaign-id", "42", "--apply"}, 2, "", nil, "delete-class"},
		{"unlink sitelink, confirmed", []string{"unlink", "sitelink", "8", "--campaign-id", "42", "--apply", "--confirm-spend"}, 0, "/campaignAssets:mutate",
			[]string{`"remove":"customers/1234567890/campaignAssets/42~8~SITELINK"`}, ""},
	})
}

func TestListAndGetWrappers(t *testing.T) {
	a, _ := realApp(t, nil)
	out := a.stdout.(interface{ String() string })
	if code := a.run([]string{"list", "keywords", "--campaign-id", "42", "--show-query"}); code != 0 {
		t.Fatal(code)
	}
	want := "SELECT ad_group_criterion.criterion_id, ad_group_criterion.keyword.text, ad_group_criterion.keyword.match_type, ad_group_criterion.status, ad_group_criterion.negative, ad_group_criterion.cpc_bid_micros, ad_group_criterion.final_urls, ad_group.id, campaign.id FROM ad_group_criterion WHERE ad_group_criterion.type = 'KEYWORD' AND campaign.id = 42 AND ad_group_criterion.status != 'REMOVED'\n"
	if out.String() != want {
		t.Fatalf("query %q", out.String())
	}
	runWrapperCases(t, []wcase{
		{"list campaigns runs search", []string{"list", "campaigns"}, 0, "/googleAds:search", []string{`FROM campaign WHERE campaign.status != 'REMOVED'`}, ""},
		{"get keyword", []string{"get", "keyword", "7~9"}, 0, "/googleAds:search", []string{`ad_group.id = 7 AND ad_group_criterion.criterion_id = 9`}, ""},
		{"get campaign needs a number", []string{"get", "campaign", "x"}, 2, "", nil, "numeric"},
	})
	a, srv := realApp(t, newFakeServer(t, reply(`{"results":[{"campaign":{"id":"1","name":"A"}}]}`)))
	if code := a.run([]string{"list", "campaigns", "--format", "csv"}); code != 0 || out == nil || srv.hits.Load() != 1 {
		t.Fatalf("exit %d", code)
	}
	if got := a.stdout.(interface{ String() string }).String(); got != "campaign.id,campaign.name\n1,A\n" {
		t.Fatalf("csv %q", got)
	}
	if lines := auditLines(t, a); len(lines) != 0 {
		t.Fatal("reports are reads and are not audited")
	}
}

// TestReportFieldsExistInTheSchema resolves every GAQL field the list and
// get wrappers select against the resource schemas of the embedded API
// version, so an upgrade that renames a field fails here, not in a live call.
func TestReportFieldsExistInTheSchema(t *testing.T) {
	m, err := manifest.Load()
	if err != nil {
		t.Fatal(err)
	}
	camel := func(s string) string {
		parts := strings.Split(s, "_")
		for i := 1; i < len(parts); i++ {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
		return strings.Join(parts, "")
	}
	pascal := func(s string) string { c := camel(s); return strings.ToUpper(c[:1]) + c[1:] }
	fieldOf := func(schemaID, name string) (manifest.Field, bool) {
		for _, f := range m.Schemas[schemaID].Fields {
			if f.Name == name {
				return f, true
			}
		}
		return manifest.Field{}, false
	}
	for _, k := range reportKinds {
		for _, f := range strings.Split(k.fields, ",") {
			parts := strings.Split(strings.TrimSpace(f), ".")
			id, amb := m.LookupSchema(pascal(parts[0]))
			if id == "" {
				// Resource names are unique among Resources__ schemas.
				for _, c := range amb {
					if strings.Contains(c, "Resources__") {
						id = c
					}
				}
			}
			if id == "" {
				t.Errorf("%s: no schema for resource %s", f, parts[0])
				continue
			}
			for i, p := range parts[1:] {
				fld, ok := fieldOf(id, camel(p))
				if !ok {
					t.Errorf("%s: %s has no field %s", strings.TrimSpace(f), manifest.ShortName(id), camel(p))
					break
				}
				if i < len(parts)-2 {
					id = fld.Ref
				}
			}
		}
	}
}
