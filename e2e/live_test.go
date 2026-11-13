package e2e

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/memohnsen/gads-cli/internal/config"
)

// liveCampaign is a complete minimal Search campaign built with temporary
// resource names. It carries no language criterion: Google announced that
// Search campaigns stop accepting them through the API from late September
// 2026.
const liveCampaign = `{"mutateOperations":[
{"campaignBudgetOperation":{"create":{"resourceName":"customers/CID/campaignBudgets/-1","name":"googleads live check NAME","amountMicros":"AMOUNT","deliveryMethod":"STANDARD","explicitlyShared":false}}},
{"campaignOperation":{"create":{"resourceName":"customers/CID/campaigns/-2","name":"googleads live check NAME",STATUS"advertisingChannelType":"SEARCH","campaignBudget":"customers/CID/campaignBudgets/-1","manualCpc":{"enhancedCpcEnabled":false},"networkSettings":{"targetGoogleSearch":true,"targetSearchNetwork":false,"targetContentNetwork":false,"targetPartnerSearchNetwork":false},"containsEuPoliticalAdvertising":"DOES_NOT_CONTAIN_EU_POLITICAL_ADVERTISING"}}},
{"campaignCriterionOperation":{"create":{"campaign":"customers/CID/campaigns/-2","location":{"geoTargetConstant":"geoTargetConstants/2756"}}}},
{"adGroupOperation":{"create":{"resourceName":"customers/CID/adGroups/-3","name":"googleads live check","campaign":"customers/CID/campaigns/-2","type":"SEARCH_STANDARD","status":"PAUSED","cpcBidMicros":"1000000"}}},
{"adGroupCriterionOperation":{"create":{"adGroup":"customers/CID/adGroups/-3","status":"PAUSED","keyword":{"text":"googleads live check","matchType":"EXACT"}}}},
{"adGroupCriterionOperation":{"create":{"adGroup":"customers/CID/adGroups/-3","status":"PAUSED","keyword":{"text":"googleads live check","matchType":"PHRASE"}}}},
{"adGroupAdOperation":{"create":{"adGroup":"customers/CID/adGroups/-3","status":"PAUSED","ad":{"finalUrls":["FINALURL"],"responsiveSearchAd":{"headlines":[{"text":"Live check headline one"},{"text":"Live check headline two"},{"text":"Live check headline three"}],"descriptions":[{"text":"First description for the googleads live check."},{"text":"Second description for the googleads live check."}]}}}}}
]}`

// TestLive runs against a real Google Ads account with the operator's real
// configuration and changes nothing: the whole PAUSED campaign is sent with
// --validate-only (budget creates are spend-class, so the dry run is
// explicit), the same body without --apply is refused locally, and the
// variant without a campaign status is refused before it leaves.
//
//	GOOGLE_ADS_LIVE=1 GOOGLE_ADS_LIVE_FINAL_URL=https://example.com go test ./e2e -run TestLive -v
func TestLive(t *testing.T) {
	if os.Getenv("GOOGLE_ADS_LIVE") != "1" {
		t.Skip("set GOOGLE_ADS_LIVE=1 with a configured key, customer and budget cap to run the live checks")
	}
	bin := buildBinary(t)
	env := os.Environ()

	out, errOut, code := run(t, bin, env, "", "auth", "status")
	var st struct {
		CustomerID      string `json:"customer_id"`
		LoginCustomerID string `json:"login_customer_id"`
		BudgetCap       string `json:"budget_cap"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || st.CustomerID == "" || st.BudgetCap == "" {
		t.Fatalf("configure a key, a customer and a budget cap first: %s %s", out, errOut)
	}

	out, errOut, code = run(t, bin, env, "", "list-accessible-customers")
	if code != 0 {
		t.Fatalf("list-accessible-customers: exit %d %s", code, errOut)
	}
	if !strings.Contains(out, "customers/"+st.CustomerID) {
		if st.LoginCustomerID == "" {
			t.Fatalf("the service account cannot reach customer %s directly: %s", st.CustomerID, out)
		}
		t.Logf("customer %s is reached through manager %s", st.CustomerID, st.LoginCustomerID)
	}

	out, errOut, code = run(t, bin, env, "", "search", "--query",
		"SELECT customer.descriptive_name, customer.currency_code, customer.time_zone FROM customer LIMIT 1")
	if code != 0 {
		t.Fatalf("search: exit %d %s", code, errOut)
	}
	t.Logf("account: %s", strings.TrimSpace(out))

	capMicros, err := config.ParseMoneyToMicros(st.BudgetCap)
	if err != nil {
		t.Fatal(err)
	}
	amount := min(int64(1_000_000), capMicros)
	payload := func(paused bool) string {
		status := ""
		if paused {
			status = `"status":"PAUSED",`
		}
		return strings.NewReplacer("CID", st.CustomerID, "NAME", time.Now().UTC().Format("20060102T150405"),
			"AMOUNT", strconv.FormatInt(amount, 10), "STATUS", status,
			"FINALURL", os.Getenv("GOOGLE_ADS_LIVE_FINAL_URL")).Replace(liveCampaign)
	}

	if os.Getenv("GOOGLE_ADS_LIVE_FINAL_URL") == "" {
		t.Log("GOOGLE_ADS_LIVE_FINAL_URL is not set: skipping the validate-only campaign check")
	} else {
		if _, errOut, code := run(t, bin, env, "", "mutate", "--validate-only", "--data", payload(true)); code != 0 {
			t.Fatalf("validate-only campaign: exit %d %s", code, errOut)
		}
	}

	_, errOut, code = run(t, bin, env, "", "mutate", "--data", payload(true))
	if code != 2 || !strings.Contains(errOut, "--validate-only") {
		t.Fatalf("a budget create without --validate-only or --apply --confirm-spend must be refused locally: exit %d %s", code, errOut)
	}
	_, errOut, code = run(t, bin, env, "", "mutate", "--validate-only", "--data", payload(false))
	if code != 2 || !strings.Contains(errOut, "PAUSED") {
		t.Fatalf("a campaign without a status must be refused locally: exit %d %s", code, errOut)
	}
}
