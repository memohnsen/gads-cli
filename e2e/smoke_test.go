package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSmoke(t *testing.T) {
	bin := buildBinary(t)
	g := newFakeGoogle(t)
	env := scrubbedEnv(t,
		"GOOGLE_ADS_JSON_KEY_FILE_PATH="+writeKeyFile(t), "GOOGLE_ADS_TOKEN_URL="+g.URL+"/token",
		"GOOGLE_ADS_API_BASE="+g.URL, "GOOGLE_ADS_ALLOW_CUSTOM_BASE=1", "GOOGLE_ADS_CUSTOMER_ID=123-456-7890")
	enable := `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x"}}}]}`

	t.Run("version", func(t *testing.T) {
		out, _, code := run(t, bin, env, "", "version")
		if code != 0 || !strings.Contains(out, "Google Ads API v26") {
			t.Fatalf("exit %d out %q", code, out)
		}
	})
	t.Run("catalog", func(t *testing.T) {
		out, _, code := run(t, bin, env, "", "commands", "--json")
		var cat struct {
			Commands []struct {
				Command string `json:"command"`
			} `json:"commands"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &cat) != nil || len(cat.Commands) < 150 {
			t.Fatalf("exit %d, %d commands", code, len(cat.Commands))
		}
	})
	t.Run("search", func(t *testing.T) {
		out, errOut, code := run(t, bin, env, "", "search", "--query", "SELECT customer.id FROM customer")
		if code != 0 || errOut != "" || !strings.Contains(out, `"results"`) {
			t.Fatalf("exit %d out %q err %q", code, out, errOut)
		}
	})
	t.Run("spend needs force", func(t *testing.T) {
		_, errOut, code := run(t, bin, env, "", "mutate", "--data", enable)
		if code != 2 || errLine(t, errOut)["kind"] != "usage" || g.mutates.Load() != 0 {
			t.Fatalf("exit %d err %q mutates %d", code, errOut, g.mutates.Load())
		}
	})
	t.Run("validate-only", func(t *testing.T) {
		_, errOut, code := run(t, bin, env, "", "mutate", "--validate-only", "--data", enable)
		if code != 0 || !strings.Contains(g.body(), `"validateOnly":true`) {
			t.Fatalf("exit %d err %q body %q", code, errOut, g.body())
		}
	})
	t.Run("google error", func(t *testing.T) {
		body := `{"mutateOperations":[{"campaignOperation":{"create":{"name":"FAIL_ME","status":"PAUSED"}}}]}`
		_, errOut, code := run(t, bin, env, "", "mutate", "--data", body)
		e := errLine(t, errOut)
		if code != 1 || e["kind"] != "validation" || e["request_id"] != "rq-smoke" ||
			!strings.Contains(e["error"].(string), "(mutate_operations[0].campaign_operation.create.contains_eu_political_advertising)") {
			t.Fatalf("exit %d err %v", code, e)
		}
	})
	t.Run("auth status", func(t *testing.T) {
		out, _, code := run(t, bin, env, "", "auth", "status")
		if code != 0 || strings.Contains(out, "PRIVATE KEY") || !strings.Contains(out, `"mode":"service_account"`) {
			t.Fatalf("exit %d out %q", code, out)
		}
	})
	t.Run("budget cap", func(t *testing.T) {
		budget := func(amount string) string {
			return `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"name":"b","amountMicros":"` + amount + `","explicitlyShared":false}}}]}`
		}
		if _, errOut, code := run(t, bin, env, "", "mutate", "--data", budget("1000000")); code != 2 || !strings.Contains(errOut, "no budget cap") {
			t.Fatalf("no cap: exit %d %q", code, errOut)
		}
		if _, errOut, code := run(t, bin, env, "", "config", "set", "budget-cap", "30"); code != 0 {
			t.Fatalf("set cap: exit %d %q", code, errOut)
		}
		if _, errOut, code := run(t, bin, env, "", "mutate", "--force", "--data", budget("30000001")); code != 2 || !strings.Contains(errOut, "exceeds") {
			t.Fatalf("above cap: exit %d %q", code, errOut)
		}
		if _, errOut, code := run(t, bin, env, "", "mutate", "--data", budget("30000000")); code != 0 {
			t.Fatalf("at cap: exit %d %q", code, errOut)
		}
	})
	t.Run("no key", func(t *testing.T) {
		_, errOut, code := run(t, bin, scrubbedEnv(t, "GOOGLE_ADS_CUSTOMER_ID=1234567890"), "", "search", "--query", "x")
		if code != 2 || !strings.Contains(errLine(t, errOut)["error"].(string), "no service-account key") {
			t.Fatalf("exit %d err %q", code, errOut)
		}
	})
}
