package guard

import (
	"errors"
	"strings"
	"testing"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/manifest"
)

const cap30 = 30000000

func testManifest() *manifest.Manifest {
	mut := func(field, res string) *manifest.Mutate {
		return &manifest.Mutate{OperationsField: field, Resource: res}
	}
	w := func(id, cmd, res string) manifest.Method {
		return manifest.Method{ID: id, Command: strings.Split(cmd, " "), Risk: "write", ValidateOnly: true, Mutate: mut("operations", res)}
	}
	return &manifest.Manifest{
		Methods: []manifest.Method{
			{ID: "customers.googleAds.search", Command: []string{"search"}, Risk: "read"},
			{ID: "customers.googleAds.mutate", Command: []string{"mutate"}, Risk: "write", ValidateOnly: true, Mutate: mut("mutateOperations", "")},
			w("customers.campaigns.mutate", "campaigns mutate", "Campaign"),
			w("customers.campaignBudgets.mutate", "campaign-budgets mutate", "CampaignBudget"),
			w("customers.adGroups.mutate", "ad-groups mutate", "AdGroup"),
			w("customers.adGroupAds.mutate", "ad-group-ads mutate", "AdGroupAd"),
			w("customers.adGroupCriteria.mutate", "ad-group-criteria mutate", "AdGroupCriterion"),
			w("customers.campaignCriteria.mutate", "campaign-criteria mutate", "CampaignCriterion"),
			w("customers.campaignBidModifiers.mutate", "campaign-bid-modifiers mutate", "CampaignBidModifier"),
			w("customers.biddingStrategies.mutate", "bidding-strategies mutate", "BiddingStrategy"),
			w("customers.biddingSeasonalityAdjustments.mutate", "bidding-seasonality-adjustments mutate", "BiddingSeasonalityAdjustment"),
			w("customers.conversionActions.mutate", "conversion-actions mutate", "ConversionAction"),
			w("customers.customerConversionGoals.mutate", "customer-conversion-goals mutate", "CustomerConversionGoal"),
			w("customers.assetGroups.mutate", "asset-groups mutate", "AssetGroup"),
			w("customers.assets.mutate", "assets mutate", "Asset"),
			w("customers.campaignAssets.mutate", "campaign-assets mutate", "CampaignAsset"),
			{ID: "customers.mutate", Command: []string{"customer", "mutate"}, Risk: "write", ValidateOnly: true, Mutate: mut("operation", "Customer")},
			{ID: "customers.customerUserAccesses.mutate", Command: []string{"customer-user-accesses", "mutate"}, Risk: "admin", ValidateOnly: true, Mutate: mut("operation", "CustomerUserAccess")},
			{ID: "customers.batchJobs.addOperations", Command: []string{"batch-jobs", "add-operations"}, Risk: "admin", Mutate: mut("mutateOperations", "")},
			{ID: "customers.experiments.promoteExperiment", Command: []string{"experiments", "promote-experiment"}, Risk: "spend"},
			{ID: "customers.recommendations.dismiss", Command: []string{"recommendations", "dismiss"}, Risk: "write"},
			{ID: "customers.recommendations.apply", Command: []string{"recommendations", "apply"}, Risk: "spend", Blocked: "applies amounts Google chooses"},
			{ID: "customers.reservations.bookCampaigns", Command: []string{"reservations", "book-campaigns"}, Risk: "spend", Blocked: "commits spend"},
		},
		GenericOperations: map[string]string{
			"campaignOperation":         "customers.campaigns.mutate",
			"campaignBudgetOperation":   "customers.campaignBudgets.mutate",
			"adGroupOperation":          "customers.adGroups.mutate",
			"adGroupAdOperation":        "customers.adGroupAds.mutate",
			"adGroupCriterionOperation": "customers.adGroupCriteria.mutate",
			"conversionActionOperation": "customers.conversionActions.mutate",
			"assetOperation":            "customers.assets.mutate",
			"campaignAssetOperation":    "customers.campaignAssets.mutate",
			"bookCampaignsOperation":    "customers.reservations.bookCampaigns",
		},
	}
}

func check(id, body string, opt Options) (Decision, error) {
	m := testManifest()
	meth := m.ByID(id)
	if meth.ValidateOnly {
		opt.CanValidate = true
	}
	return Check(m, meth, []byte(body), opt)
}

// The flag combinations the cases use. CanValidate follows the method.
var (
	dry      = Options{CapMicros: cap30}                                  // no flags: the dry-run default
	validate = Options{ValidateOnly: true, CapMicros: cap30}              // explicit --validate-only
	apply    = Options{Apply: true, CapMicros: cap30}                     // --apply
	confirm  = Options{Apply: true, ConfirmSpend: true, CapMicros: cap30} // --apply --confirm-spend
	onlyConf = Options{ConfirmSpend: true, CapMicros: cap30}              // --confirm-spend without --apply
	roApply  = Options{Apply: true, ConfirmSpend: true, ReadOnly: true, CapMicros: cap30}
	roDry    = Options{ReadOnly: true, CapMicros: cap30}
	noCapAll = Options{Apply: true, ConfirmSpend: true}
)

const (
	pausedCampaign  = `{"operations":[{"create":{"name":"x","status":"PAUSED"}}]}`
	enabledCampaign = `{"operations":[{"create":{"name":"x","status":"ENABLED"}}]}`
	noStatusCamp    = `{"operations":[{"create":{"name":"x"}}]}`
	enableCampaign  = `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","status":"ENABLED"},"updateMask":"status"}]}`
	pauseCampaign   = `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","status":"PAUSED"},"updateMask":"status"}]}`
	budgetCreate    = `{"operations":[{"create":{"name":"b","amountMicros":"10000000"}}]}`
	budgetUpdate    = `{"operations":[{"update":{"resourceName":"customers/1/campaignBudgets/9","amountMicros":"5000000"},"updateMask":"amount_micros"}]}`
)

func TestCheck(t *testing.T) {
	cases := []struct {
		name, id, body string
		opt            Options
		wantClass      string // expected class when the call is allowed
		wantValidate   bool   // expected Decision.ValidateOnly when allowed
		wantKind       string // expected error kind (guardrail or usage) when refused
		wantErr        string // expected substring of the refusal
	}{
		// Reads are untouched by the gates.
		{"read method", "customers.googleAds.search", `{"query":"SELECT customer.id FROM customer"}`, dry, "read", false, "", ""},
		{"read with apply", "customers.googleAds.search", `{"query":"x"}`, apply, "read", false, "", ""},
		{"read-only allows reads", "customers.googleAds.search", `{"query":"x"}`, roDry, "read", false, "", ""},

		// Rule 1: writes are dry runs unless --apply.
		{"paused campaign, no flags, dry run", "customers.campaigns.mutate", pausedCampaign, dry, "write", true, "", ""},
		{"paused campaign, --validate-only", "customers.campaigns.mutate", pausedCampaign, validate, "write", true, "", ""},
		{"paused campaign, --apply", "customers.campaigns.mutate", pausedCampaign, apply, "write", false, "", ""},
		{"validateOnly false in the body does not apply", "customers.campaigns.mutate", `{"validateOnly":false,"operations":[{"create":{"status":"PAUSED"}}]}`, dry, "write", true, "", ""},
		{"apply and validate-only contradict", "customers.campaigns.mutate", pausedCampaign, Options{Apply: true, ValidateOnly: true}, "", false, "usage", "contradict"},
		{"no dry run for a method without validateOnly", "customers.recommendations.dismiss", `{}`, dry, "", false, "guardrail", "cannot be dry-run"},
		{"method without validateOnly applies with --apply", "customers.recommendations.dismiss", `{}`, apply, "write", false, "", ""},
		{"gated method without validateOnly", "customers.experiments.promoteExperiment", `{}`, validate, "", false, "guardrail", "--apply --confirm-spend"},
		{"gated method without validateOnly, confirmed", "customers.experiments.promoteExperiment", `{}`, confirm, "spend", false, "", ""},

		// Rule 2: creates are PAUSED; a missing status is refused.
		{"campaign without status", "customers.campaigns.mutate", noStatusCamp, confirm, "", false, "guardrail", "must set \"status\":\"PAUSED\""},
		{"campaign without status, validate-only", "customers.campaigns.mutate", noStatusCamp, validate, "", false, "guardrail", "Google's default is ENABLED"},
		{"generic campaign without status", "customers.googleAds.mutate", `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x"}}}]}`, confirm, "", false, "guardrail", "campaign create must set"},
		{"enabled campaign create is spend", "customers.campaigns.mutate", enabledCampaign, apply, "", false, "guardrail", "created with status ENABLED"},
		{"enabled campaign create, confirmed", "customers.campaigns.mutate", enabledCampaign, confirm, "spend", false, "", ""},
		{"numeric status is not PAUSED", "customers.campaigns.mutate", `{"operations":[{"create":{"status":3}}]}`, apply, "", false, "guardrail", "created with status 3"},
		{"ad group without status", "customers.adGroups.mutate", `{"operations":[{"create":{"name":"g","campaign":"customers/1/campaigns/2"}}]}`, confirm, "", false, "guardrail", "ad group create must set"},
		{"paused ad group with a bid", "customers.adGroups.mutate", `{"operations":[{"create":{"name":"g","status":"PAUSED","cpcBidMicros":"1000000"}}]}`, apply, "write", false, "", ""},
		{"ad without status", "customers.adGroupAds.mutate", `{"operations":[{"create":{"adGroup":"customers/1/adGroups/3","ad":{}}}]}`, confirm, "", false, "guardrail", "ad create must set"},
		{"paused RSA", "customers.adGroupAds.mutate", `{"operations":[{"create":{"adGroup":"customers/1/adGroups/3","status":"PAUSED","ad":{"responsiveSearchAd":{}}}}]}`, apply, "write", false, "", ""},
		{"keyword without status", "customers.adGroupCriteria.mutate", `{"operations":[{"create":{"adGroup":"customers/1/adGroups/3","keyword":{"text":"x","matchType":"EXACT"}}}]}`, confirm, "", false, "guardrail", "keyword create must set"},
		{"paused keyword", "customers.adGroupCriteria.mutate", `{"operations":[{"create":{"adGroup":"customers/1/adGroups/3","status":"PAUSED","keyword":{"text":"x","matchType":"EXACT"}}}]}`, apply, "write", false, "", ""},
		{"negative keyword needs no status", "customers.adGroupCriteria.mutate", `{"operations":[{"create":{"adGroup":"customers/1/adGroups/3","negative":true,"keyword":{"text":"free","matchType":"BROAD"}}}]}`, apply, "write", false, "", ""},
		{"enabled keyword create", "customers.adGroupCriteria.mutate", `{"operations":[{"create":{"adGroup":"customers/1/adGroups/3","status":"ENABLED","keyword":{"text":"x","matchType":"EXACT"}}}]}`, apply, "", false, "guardrail", "keyword created with status ENABLED"},
		{"audience criterion without status", "customers.adGroupCriteria.mutate", `{"operations":[{"create":{"adGroup":"customers/1/adGroups/3","userList":{"userList":"customers/1/userLists/5"}}}]}`, apply, "write", false, "", ""},
		{"asset group without status is spend", "customers.assetGroups.mutate", `{"operations":[{"create":{"name":"a"}}]}`, apply, "", false, "guardrail", "asset group created without status PAUSED"},

		// Rule 3/4: enabling, bids, conversion bidding, budgets and deletes are gated.
		{"enable campaign, no flags", "customers.campaigns.mutate", enableCampaign, dry, "", false, "guardrail", "Dry-run it with --validate-only"},
		{"enable campaign, --apply alone", "customers.campaigns.mutate", enableCampaign, apply, "", false, "guardrail", "add --confirm-spend"},
		{"enable campaign, --confirm-spend alone", "customers.campaigns.mutate", enableCampaign, onlyConf, "", false, "guardrail", "nothing was sent"},
		{"enable campaign, --validate-only", "customers.campaigns.mutate", enableCampaign, validate, "spend", true, "", ""},
		{"enable campaign, confirmed", "customers.campaigns.mutate", enableCampaign, confirm, "spend", false, "", ""},
		{"pause campaign", "customers.campaigns.mutate", pauseCampaign, apply, "write", false, "", ""},
		{"status only in the mask", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2"},"updateMask":"status"}]}`, apply, "", false, "guardrail", "status set to a default"},
		{"campaign status REMOVED is a delete", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","status":"REMOVED"},"updateMask":"status"}]}`, apply, "", false, "guardrail", "delete-class"},
		{"move to another budget", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","campaignBudget":"customers/1/campaignBudgets/9"},"updateMask":"campaign_budget"}]}`, apply, "", false, "guardrail", "another budget"},
		{"campaign bid strategy change", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","targetSpend":{}},"updateMask":"target_spend.cpc_bid_ceiling_micros"}]}`, apply, "", false, "guardrail", "campaign bidding changed (targetSpend)"},
		{"campaign portfolio strategy swap", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","biddingStrategy":"customers/1/biddingStrategies/4"},"updateMask":"bidding_strategy"}]}`, confirm, "spend", false, "", ""},
		{"campaign rename is a write", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","name":"n"},"updateMask":"name"}]}`, apply, "write", false, "", ""},
		{"enable ad group", "customers.adGroups.mutate", `{"operations":[{"update":{"resourceName":"customers/1/adGroups/3","status":"ENABLED"},"updateMask":"status"}]}`, apply, "", false, "guardrail", "ad group status set to ENABLED"},
		{"ad group bid change", "customers.adGroups.mutate", `{"operations":[{"update":{"resourceName":"customers/1/adGroups/3","cpcBidMicros":"2000000"},"updateMask":"cpc_bid_micros"}]}`, apply, "", false, "guardrail", "ad group bidding changed (cpcBidMicros)"},
		{"enable ad", "customers.adGroupAds.mutate", `{"operations":[{"update":{"resourceName":"customers/1/adGroupAds/3~4","status":"ENABLED"},"updateMask":"status"}]}`, apply, "", false, "guardrail", "ad status set to ENABLED"},
		{"enable keyword", "customers.adGroupCriteria.mutate", `{"operations":[{"update":{"resourceName":"customers/1/adGroupCriteria/3~5","status":"ENABLED"},"updateMask":"status"}]}`, apply, "", false, "guardrail", "ad group criterion status set to ENABLED"},
		{"keyword bid change", "customers.adGroupCriteria.mutate", `{"operations":[{"update":{"resourceName":"customers/1/adGroupCriteria/3~5","cpcBidMicros":"900000"},"updateMask":"cpc_bid_micros"}]}`, apply, "", false, "guardrail", "bidding changed (cpcBidMicros)"},
		{"campaign criterion bid modifier", "customers.campaignCriteria.mutate", `{"operations":[{"create":{"campaign":"customers/1/campaigns/2","bidModifier":1.5,"device":{"type":"MOBILE"}}}]}`, apply, "", false, "guardrail", "bid modifier"},
		{"bid modifier", "customers.campaignBidModifiers.mutate", `{"operations":[{"create":{"bidModifier":2}}]}`, apply, "", false, "guardrail", "bid modifier set"},
		{"portfolio strategy update", "customers.biddingStrategies.mutate", `{"operations":[{"update":{"resourceName":"customers/1/biddingStrategies/4","targetCpa":{"targetCpaMicros":"1"}},"updateMask":"target_cpa.target_cpa_micros"}]}`, apply, "", false, "guardrail", "portfolio bid strategy changed"},
		{"portfolio strategy rename", "customers.biddingStrategies.mutate", `{"operations":[{"update":{"resourceName":"customers/1/biddingStrategies/4","name":"n"},"updateMask":"name"}]}`, apply, "write", false, "", ""},
		{"seasonality adjustment", "customers.biddingSeasonalityAdjustments.mutate", `{"operations":[{"create":{"conversionRateModifier":1.5}}]}`, apply, "", false, "guardrail", "smart bidding adjustment"},
		{"secondary conversion action create", "customers.conversionActions.mutate", `{"operations":[{"create":{"name":"lead","type":"WEBPAGE","category":"SUBMIT_LEAD_FORM","primaryForGoal":false}}]}`, apply, "write", false, "", ""},
		{"primary conversion action create", "customers.conversionActions.mutate", `{"operations":[{"create":{"name":"lead","type":"WEBPAGE","category":"SUBMIT_LEAD_FORM"}}]}`, apply, "", false, "guardrail", "created as primary"},
		{"conversion action made primary", "customers.conversionActions.mutate", `{"operations":[{"update":{"resourceName":"customers/1/conversionActions/7","primaryForGoal":true},"updateMask":"primary_for_goal"}]}`, apply, "", false, "guardrail", "made primary"},
		{"conversion action made secondary", "customers.conversionActions.mutate", `{"operations":[{"update":{"resourceName":"customers/1/conversionActions/7","primaryForGoal":false},"updateMask":"primary_for_goal"}]}`, apply, "write", false, "", ""},
		{"conversion action enabled", "customers.conversionActions.mutate", `{"operations":[{"update":{"resourceName":"customers/1/conversionActions/7","status":"ENABLED"},"updateMask":"status"}]}`, apply, "", false, "guardrail", "conversion action enabled"},
		{"conversion goal made biddable", "customers.customerConversionGoals.mutate", `{"operations":[{"update":{"resourceName":"customers/1/customerConversionGoals/PURCHASE~WEBSITE","biddable":true},"updateMask":"biddable"}]}`, apply, "", false, "guardrail", "made biddable"},
		{"remove", "customers.adGroups.mutate", `{"operations":[{"remove":"customers/1/adGroups/3"}]}`, apply, "", false, "guardrail", "removes AdGroup"},
		{"remove, confirmed", "customers.adGroups.mutate", `{"operations":[{"remove":"customers/1/adGroups/3"}]}`, confirm, "delete", false, "", ""},
		{"unlink sitelink is a delete", "customers.campaignAssets.mutate", `{"operations":[{"remove":"customers/1/campaignAssets/2~8~SITELINK"}]}`, apply, "", false, "guardrail", "removes CampaignAsset"},
		{"sitelink asset and link", "customers.googleAds.mutate", `{"mutateOperations":[{"assetOperation":{"create":{"resourceName":"customers/1/assets/-1","sitelinkAsset":{"linkText":"x"},"finalUrls":["https://e.x"]}}},{"campaignAssetOperation":{"create":{"campaign":"customers/1/campaigns/2","asset":"customers/1/assets/-1","fieldType":"SITELINK"}}}]}`, apply, "write", false, "", ""},
		{"admin method", "customers.customerUserAccesses.mutate", `{"operation":{"update":{"accessRole":"ADMIN"}}}`, apply, "", false, "guardrail", "admin-class"},
		{"single operation shape", "customers.mutate", `{"operation":{"update":{"autoTaggingEnabled":true},"updateMask":"auto_tagging_enabled"}}`, apply, "write", false, "", ""},
		{"unknown generic type", "customers.googleAds.mutate", `{"mutateOperations":[{"fooOperation":{"create":{}}}]}`, apply, "", false, "guardrail", "unknown operation type fooOperation"},
		{"snake_case spelling", "customers.googleAds.mutate", `{"mutate_operations":[{"campaign_operation":{"create":{"status":"ENABLED"}}}]}`, apply, "", false, "guardrail", "created with status ENABLED"},
		{"validateOnly in the body does not count", "customers.campaigns.mutate", `{"validateOnly":true,"operations":[{"create":{"status":"ENABLED"}}]}`, apply, "", false, "guardrail", "add --confirm-spend"},
		{"--force is not --apply", "customers.campaigns.mutate", enableCampaign, Options{ConfirmSpend: true}, "", false, "guardrail", "nothing was sent"},

		// Rule 6: the budget cap.
		{"budget create is gated", "customers.campaignBudgets.mutate", budgetCreate, apply, "", false, "guardrail", "budget created with amount 10"},
		{"budget create, validate-only", "customers.campaignBudgets.mutate", budgetCreate, validate, "spend", true, "", ""},
		{"budget create, confirmed", "customers.campaignBudgets.mutate", budgetCreate, confirm, "spend", false, "", ""},
		{"budget at the cap", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"30000000"}}]}`, confirm, "spend", false, "", ""},
		{"budget one micro above", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"30000001"}}]}`, confirm, "", false, "guardrail", "exceeds the configured cap"},
		{"above the cap, validate-only", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"30000001"}}]}`, validate, "", false, "guardrail", "exceeds"},
		{"budgets summing above the cap", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"20000000"}},{"create":{"amountMicros":"10000001"}}]}`, confirm, "", false, "guardrail", "totalling 30.000001"},
		{"budgets summing to the cap", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"20000000"}},{"create":{"amountMicros":"10000000"}}]}`, confirm, "spend", false, "", ""},
		{"generic budgets summing above the cap", "customers.googleAds.mutate", `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"amountMicros":"16000000"}}},{"campaignBudgetOperation":{"update":{"resourceName":"customers/1/campaignBudgets/9","amountMicros":"16000000"},"updateMask":"amount_micros"}}]}`, validate, "", false, "guardrail", "totalling 32"},
		{"no cap configured", "customers.campaignBudgets.mutate", budgetCreate, noCapAll, "", false, "guardrail", "no budget cap"},
		{"budget update, apply alone", "customers.campaignBudgets.mutate", budgetUpdate, apply, "", false, "guardrail", "budget amount changed to 5"},
		{"budget update, confirmed", "customers.campaignBudgets.mutate", budgetUpdate, confirm, "spend", false, "", ""},
		{"budget rename is a write", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaignBudgets/9","name":"n"},"updateMask":"name"}]}`, apply, "write", false, "", ""},
		{"amount only in the mask", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaignBudgets/9"},"updateMask":"amount_micros"}]}`, apply, "", false, "guardrail", "budget amount changed to"},
		{"amount only in the mask, no cap", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaignBudgets/9"},"updateMask":"amount_micros"}]}`, noCapAll, "", false, "guardrail", "no budget cap"},
		{"fixed total", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"totalAmountMicros":"1000000"}}]}`, confirm, "", false, "guardrail", "fixed-total"},
		{"custom period", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"period":"CUSTOM_PERIOD","amountMicros":"1"}}]}`, confirm, "", false, "guardrail", "fixed-total"},
		{"numeric period", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"period":3,"amountMicros":"1"}}]}`, confirm, "", false, "guardrail", "fixed-total"},
		{"budget network settings", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaignBudgets/9","budgetNetworkSettings":{}},"updateMask":"budget_network_settings"}]}`, apply, "", false, "guardrail", "network settings"},
		{"batch job above the cap", "customers.batchJobs.addOperations", `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"amountMicros":"99000000"}}}]}`, confirm, "", false, "guardrail", "exceeds"},
		{"batch job, apply alone", "customers.batchJobs.addOperations", `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"amountMicros":"1000000"}}}]}`, apply, "", false, "guardrail", "admin-class"},
		{"batch job, confirmed", "customers.batchJobs.addOperations", `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"amountMicros":"1000000"}}}]}`, confirm, "admin", false, "", ""},

		// Rule 7: read-only mode.
		{"read-only blocks apply", "customers.adGroups.mutate", `{"operations":[{"create":{"name":"x","status":"PAUSED"}}]}`, roApply, "", false, "guardrail", "read-only mode"},
		{"read-only allows the dry run", "customers.adGroups.mutate", `{"operations":[{"create":{"name":"x","status":"PAUSED"}}]}`, roDry, "write", true, "", ""},

		// Blocked methods and malformed bodies.
		{"blocked method", "customers.recommendations.apply", `{"operations":[]}`, confirm, "", false, "guardrail", "blocked"},
		{"blocked generic type", "customers.googleAds.mutate", `{"mutateOperations":[{"bookCampaignsOperation":{}}]}`, confirm, "", false, "guardrail", "blocked"},
		{"duplicate key", "customers.campaigns.mutate", `{"operations":[{"create":{"status":"PAUSED","status":"ENABLED"}}]}`, confirm, "", false, "usage", "duplicate key"},
		{"both spellings", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"amountMicros":"1","amount_micros":"999999999999"}}]}`, confirm, "", false, "usage", "both spellings"},
		{"not JSON", "customers.campaigns.mutate", `{"operations":`, confirm, "", false, "usage", "cannot read"},
		{"operations not an array", "customers.campaigns.mutate", `{"operations":{"create":{}}}`, confirm, "", false, "usage", "must be a JSON array"},
		{"remove and create", "customers.campaignBudgets.mutate", `{"operations":[{"remove":"customers/1/campaignBudgets/9","create":{"amountMicros":"999000000000"}}]}`, confirm, "", false, "usage", "more than one of create, update, remove"},
		{"create and update in snake_case", "customers.googleAds.mutate", `{"mutate_operations":[{"campaign_operation":{"create":{"status":"PAUSED"},"update":{"status":"ENABLED"}}}]}`, confirm, "", false, "usage", "more than one of create, update, remove"},
		{"updateMask not a string", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2"},"updateMask":{"paths":["status"]}}]}`, confirm, "", false, "usage", "updateMask must be"},
	}
	for _, c := range cases {
		d, err := check(c.id, c.body, c.opt)
		if c.wantErr != "" {
			var e *api.Error
			if !errors.As(err, &e) || e.Kind != c.wantKind || !strings.Contains(e.Message, c.wantErr) {
				t.Errorf("%s: err = %v, want a %s error containing %q", c.name, err, c.wantKind, c.wantErr)
			}
			continue
		}
		if err != nil || d.Class != c.wantClass || d.ValidateOnly != c.wantValidate {
			t.Errorf("%s: class %q validateOnly %v err %v, want class %q validateOnly %v", c.name, d.Class, d.ValidateOnly, err, c.wantClass, c.wantValidate)
		}
	}
}

func TestAmountForms(t *testing.T) {
	for _, amount := range []string{`" 29610000"`, `"+29610000"`, `2.961e7`, `"29610000.0"`, `29610000.0`, `-1`, `"abc"`, `true`} {
		body := `{"operations":[{"create":{"amountMicros":` + amount + `}}]}`
		_, err := check("customers.campaignBudgets.mutate", body, confirm)
		if err == nil || !strings.Contains(err.Error(), "micros") {
			t.Errorf("amount %s: err = %v, want a refusal about micros", amount, err)
		}
	}
	for _, amount := range []string{`29610000`, `"29610000"`} {
		body := `{"operations":[{"create":{"amountMicros":` + amount + `}}]}`
		if _, err := check("customers.campaignBudgets.mutate", body, validate); err != nil {
			t.Errorf("amount %s: %v", amount, err)
		}
	}
}

func TestFindingsNameEveryOperation(t *testing.T) {
	body := `{"mutateOperations":[{"adGroupOperation":{"remove":"customers/1/adGroups/3"}},{"campaignOperation":{"create":{"name":"x","status":"ENABLED"}}}]}`
	_, err := check("customers.googleAds.mutate", body, apply)
	if err == nil || !strings.Contains(err.Error(), "operation 0: removes AdGroup") || !strings.Contains(err.Error(), "operation 1: campaign created") {
		t.Fatalf("err = %v", err)
	}
	d, err := check("customers.googleAds.mutate", body, confirm)
	if err != nil || len(d.Findings) != 2 || d.Class != "delete" {
		t.Fatalf("findings %+v class %s err %v", d.Findings, d.Class, err)
	}
}

func TestDecisionSummarizesOperationsAndBudgets(t *testing.T) {
	body := `{"mutateOperations":[
{"campaignBudgetOperation":{"create":{"resourceName":"customers/1/campaignBudgets/-1","name":"b","amountMicros":"10000000"}}},
{"campaignOperation":{"create":{"resourceName":"customers/1/campaigns/-2","name":"c","status":"PAUSED","campaignBudget":"customers/1/campaignBudgets/-1"}}},
{"adGroupOperation":{"update":{"resourceName":"customers/1/adGroups/3","status":"PAUSED"},"updateMask":"status"}}]}`
	d, err := check("customers.googleAds.mutate", body, validate)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Ops) != 3 || d.Ops[0].Class != "spend" || d.Ops[0].AmountMicros != "10000000" || d.Ops[1].Status != "PAUSED" ||
		d.Ops[1].Class != "write" || d.Ops[2].Verb != "update" || d.Ops[2].UpdateMask != "status" || d.Ops[2].ResourceName != "customers/1/adGroups/3" {
		t.Fatalf("ops %+v", d.Ops)
	}
	if len(d.Budgets) != 1 || d.Budgets[0].Micros != 10000000 || d.Budgets[0].Verb != "create" || d.BudgetTotal() != 10000000 {
		t.Fatalf("budgets %+v", d.Budgets)
	}
}

// An api call whose path matches no method still has every budget amount in
// its body held to the cap, wherever it sits.
func TestUnmatchedBodyAmountScan(t *testing.T) {
	raw := &manifest.Method{ID: "raw POST v26/x", Command: []string{"api", "POST", "v26/x"}, Risk: "admin"}
	cases := []struct {
		name, body string
		opt        Options
		wantErr    string
	}{
		{"no cap", `{"operations":[{"create":{"amountMicros":"999999999999"}}]}`, noCapAll, "no budget cap"},
		{"snake case, no cap", `{"operations":[{"create":{"amount_micros":"1"}}]}`, noCapAll, "no budget cap"},
		{"total amount, no cap", `{"x":{"totalAmountMicros":"1"}}`, noCapAll, "no budget cap"},
		{"total amount snake case over cap", `{"x":[{"y":{"total_amount_micros":"30000001"}}]}`, confirm, "exceeds the configured cap"},
		{"over cap", `{"operations":[{"update":{"amountMicros":"30000001"}}]}`, confirm, "exceeds the configured cap"},
		{"sum over cap", `{"a":{"amountMicros":"20000000"},"b":{"amountMicros":"20000000"}}`, confirm, "totalling 40"},
		{"not digits", `{"a":{"amountMicros":"2.9e7"}}`, confirm, "micros"},
		{"object value", `{"a":{"amountMicros":{"v":1}}}`, confirm, "micros"},
		{"mixed spellings", `{"amountMicros":"1","amount_micros":"999999999999"}`, confirm, "both spellings"},
		{"under cap", `{"operations":[{"create":{"amountMicros":"29610000"}}]}`, confirm, ""},
		{"no amount", `{"operations":[{"create":{"name":"x"}}]}`, confirm, ""},
		{"under cap, apply alone", `{"amountMicros":"1"}`, apply, "--confirm-spend"},
		{"no dry run for an unmatched path", `{"amountMicros":"1"}`, validate, "cannot be dry-run"},
	}
	for _, c := range cases {
		opt := c.opt
		opt.Unmatched = true
		d, err := Check(testManifest(), raw, []byte(c.body), opt)
		switch {
		case c.wantErr == "" && (err != nil || d.Class != "admin"):
			t.Errorf("%s: class %q err %v", c.name, d.Class, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: err %v, want %q", c.name, err, c.wantErr)
		}
	}
}
