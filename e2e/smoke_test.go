package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSmoke drives the real binary against a fake Google through the
// acceptance criteria: reads with the reader key and no developer token,
// dry runs by default, PAUSED creates, the spend gates, read-only mode, the
// budget cap and the audit log.
func TestSmoke(t *testing.T) {
	bin := buildBinary(t)
	g := newFakeGoogle(t)
	key := writeKeyFile(t)
	env := scrubbedEnv(t,
		"GOOGLE_ADS_JSON_KEY_FILE_PATH="+key, "GOOGLE_ADS_WRITE_JSON_KEY_FILE_PATH="+key,
		"GOOGLE_ADS_TOKEN_URL="+g.URL+"/token", "GOOGLE_ADS_API_BASE="+g.URL, "GOOGLE_ADS_ALLOW_CUSTOM_BASE=1",
		"GOOGLE_ADS_CUSTOMER_ID=123-456-7890")
	var home string
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			home = strings.TrimPrefix(kv, "HOME=")
		}
	}
	auditPath := filepath.Join(home, "state", "gads", "mutations.jsonl")
	audit := func() []map[string]any {
		raw, _ := os.ReadFile(auditPath)
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) == nil {
				out = append(out, m)
			}
		}
		return out
	}
	paused := `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x","status":"PAUSED"}}}]}`
	noStatus := `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x"}}}]}`
	enable := `{"mutateOperations":[{"campaignOperation":{"update":{"resourceName":"customers/1234567890/campaigns/2","status":"ENABLED"},"updateMask":"status"}}]}`

	t.Run("version", func(t *testing.T) {
		out, _, code := run(t, bin, env, "", "version")
		if code != 0 || !strings.Contains(out, "Google Ads API v26") {
			t.Fatalf("exit %d out %q", code, out)
		}
	})
	t.Run("gads symlink", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("no symlinks")
		}
		link := filepath.Join(t.TempDir(), "gads")
		if err := os.Symlink(bin, link); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(link, "version").Output()
		if err != nil || !strings.Contains(string(out), "v26") {
			t.Fatalf("gads version: %v %q", err, out)
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
	t.Run("search with the reader key and no developer token", func(t *testing.T) {
		out, errOut, code := run(t, bin, env, "", "search", "--query", "SELECT campaign.id, campaign.name, campaign.status FROM campaign")
		if code != 0 || errOut != "" || !strings.Contains(out, `"results"`) || g.devToken.Load() {
			t.Fatalf("exit %d out %q err %q", code, out, errOut)
		}
	})
	t.Run("read-only is the default", func(t *testing.T) {
		out, _, _ := run(t, bin, env, "", "auth", "status")
		if !strings.Contains(out, `"read_only":true`) || !strings.Contains(out, `"read_only_origin":"default"`) {
			t.Fatalf("%s", out)
		}
	})
	t.Run("a create without --apply is a dry run", func(t *testing.T) {
		before := g.mutates.Load()
		out, errOut, code := run(t, bin, env, "", "mutate", "--data", paused)
		if code != 0 || !strings.Contains(g.body(), `"validateOnly":true`) || !strings.Contains(out, `"dry_run":true`) || g.mutates.Load() != before+1 {
			t.Fatalf("exit %d out %q err %q body %q", code, out, errOut, g.body())
		}
	})
	t.Run("a missing status is refused", func(t *testing.T) {
		before := g.mutates.Load()
		_, errOut, code := run(t, bin, env, "", "mutate", "--validate-only", "--data", noStatus)
		if code != 2 || errLine(t, errOut)["kind"] != "guardrail" || !strings.Contains(errOut, "PAUSED") || g.mutates.Load() != before {
			t.Fatalf("exit %d err %q", code, errOut)
		}
	})
	t.Run("enable needs --apply --confirm-spend and never reaches the network without them", func(t *testing.T) {
		before := g.mutates.Load()
		for _, flags := range [][]string{nil, {"--apply"}, {"--force"}, {"--confirm-spend"}} {
			_, errOut, code := run(t, bin, env, "", append([]string{"mutate", "--data", enable}, flags...)...)
			if code != 2 || errLine(t, errOut)["kind"] != "guardrail" {
				t.Fatalf("%v: exit %d err %q", flags, code, errOut)
			}
		}
		if g.mutates.Load() != before {
			t.Fatalf("a gated call reached the network")
		}
		if _, errOut, code := run(t, bin, env, "", "mutate", "--validate-only", "--data", enable); code != 0 || !strings.Contains(g.body(), `"validateOnly":true`) {
			t.Fatalf("explicit dry run: exit %d %q", code, errOut)
		}
	})
	t.Run("read-only mode refuses applies", func(t *testing.T) {
		before := g.mutates.Load()
		_, errOut, code := run(t, bin, env, "", "mutate", "--apply", "--confirm-spend", "--data", enable)
		if code != 2 || !strings.Contains(errOut, "read-only") || g.mutates.Load() != before {
			t.Fatalf("exit %d err %q", code, errOut)
		}
	})
	t.Run("a person turns read-only off; the apply is audited", func(t *testing.T) {
		if _, errOut, code := run(t, bin, env, "", "config", "set", "read-only", "false"); code != 0 {
			t.Fatalf("exit %d %q", code, errOut)
		}
		// The environment can switch read-only back on, never off.
		roEnv := append(append([]string{}, env...), "GOOGLE_ADS_READ_ONLY=1")
		if _, _, code := run(t, bin, roEnv, "", "mutate", "--apply", "--data", paused); code != 2 {
			t.Fatalf("GOOGLE_ADS_READ_ONLY=1: exit %d", code)
		}
		out, errOut, code := run(t, bin, env, "", "mutate", "--apply", "--confirm-spend", "--data", enable)
		if code != 0 || strings.Contains(g.body(), "validateOnly") || strings.Contains(out, "dry_run") {
			t.Fatalf("exit %d out %q err %q body %q", code, out, errOut, g.body())
		}
		lines := audit()
		last := lines[len(lines)-1]
		if last["result"] != "applied" || last["apply"] != true || last["confirm_spend"] != true || last["risk_class"] != "spend" ||
			last["credential_email"] != "gads@proj.iam.gserviceaccount.com" {
			t.Fatalf("audit %v", last)
		}
		raw, _ := os.ReadFile(auditPath)
		if strings.Contains(string(raw), "smoke-token") || strings.Contains(string(raw), "PRIVATE KEY") {
			t.Fatal("the audit log carries a credential")
		}
		if fi, err := os.Stat(auditPath); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
			t.Fatalf("audit log %v %v", fi, err)
		}
	})
	t.Run("google error", func(t *testing.T) {
		body := `{"mutateOperations":[{"campaignOperation":{"create":{"name":"FAIL_ME","status":"PAUSED"}}}]}`
		_, errOut, code := run(t, bin, env, "", "mutate", "--apply", "--data", body)
		e := errLine(t, errOut)
		if code != 1 || e["kind"] != "validation" || e["request_id"] != "rq-smoke" ||
			!strings.Contains(e["error"].(string), "(mutate_operations[0].campaign_operation.create.contains_eu_political_advertising)") {
			t.Fatalf("exit %d err %v", code, e)
		}
		if last := audit()[len(audit())-1]; last["result"] != "error" || last["request_id"] != "rq-smoke" {
			t.Fatalf("audit %v", last)
		}
	})
	t.Run("auth status", func(t *testing.T) {
		out, _, code := run(t, bin, env, "", "auth", "status")
		if code != 0 || strings.Contains(out, "PRIVATE KEY") || strings.Contains(out, "smoke-token") || !strings.Contains(out, `"mode":"service_account"`) {
			t.Fatalf("exit %d out %q", code, out)
		}
	})
	t.Run("budget cap", func(t *testing.T) {
		budget := func(amount string) string {
			return `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"name":"b","amountMicros":"` + amount + `","explicitlyShared":false}}}]}`
		}
		if _, errOut, code := run(t, bin, env, "", "mutate", "--validate-only", "--data", budget("1000000")); code != 2 || !strings.Contains(errOut, "no budget cap") {
			t.Fatalf("no cap: exit %d %q", code, errOut)
		}
		if _, errOut, code := run(t, bin, env, "", "config", "set", "budget-cap", "30"); code != 0 {
			t.Fatalf("set cap: exit %d %q", code, errOut)
		}
		before := g.mutates.Load()
		if _, errOut, code := run(t, bin, env, "", "mutate", "--apply", "--confirm-spend", "--data", budget("30000001")); code != 2 || !strings.Contains(errOut, "exceeds") {
			t.Fatalf("above cap: exit %d %q", code, errOut)
		}
		if g.mutates.Load() != before {
			t.Fatal("a budget above the cap reached the network")
		}
		if _, errOut, code := run(t, bin, env, "", "mutate", "--apply", "--data", budget("30000000")); code != 2 || !strings.Contains(errOut, "--confirm-spend") {
			t.Fatalf("budget create without --confirm-spend: exit %d %q", code, errOut)
		}
		if _, errOut, code := run(t, bin, env, "", "mutate", "--apply", "--confirm-spend", "--data", budget("30000000")); code != 0 {
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
