package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/memohnsen/gads-cli/internal/config"
)

const enableCampaign = `{"operations":[{"update":{"resourceName":"customers/1234567890/campaigns/2","status":"ENABLED"},"updateMask":"status"}]}`

func TestWriteIsADryRunByDefault(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, out, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"campaigns", "mutate", "--data", pausedCampaign}); code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	if !strings.Contains(srv.bodies[0], `"validateOnly":true`) {
		t.Fatalf("a write without --apply must be sent validate_only: %q", srv.bodies[0])
	}
	if got := srv.headers[0].Get("Authorization"); got != "Bearer tok-reader" {
		t.Fatalf("dry runs use the reader credential: %q", got)
	}
	var env map[string]any
	if err := json.Unmarshal(out.Bytes(), &env); err != nil || env["dry_run"] != true || env["applied"] != false {
		t.Fatalf("stdout must say nothing was applied: %q", out.String())
	}
	l := auditLines(t, a)
	if len(l) != 1 || l[0]["result"] != "dry_run" || l[0]["validate_only"] != true || l[0]["apply"] != false ||
		l[0]["customer_id"] != "1234567890" || l[0]["credential"] != "reader" {
		t.Fatalf("audit %v", l)
	}
}

func TestApplySendsTheWriteWithTheWriterCredential(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		w.Header().Set("request-id", "rq-1")
		w.Write([]byte(`{"results":[{"resourceName":"customers/1234567890/campaigns/77"}]}`))
	})
	a, out, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	body := `{"validateOnly":true,"operations":[{"create":{"name":"x","status":"PAUSED"}}]}`
	if code := a.run([]string{"campaigns", "mutate", "--apply", "--data", body}); code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	if strings.Contains(srv.bodies[0], "validateOnly") || srv.headers[0].Get("Authorization") != "Bearer tok-writer" {
		t.Fatalf("body %q auth %q", srv.bodies[0], srv.headers[0].Get("Authorization"))
	}
	if !strings.HasPrefix(out.String(), `{"results"`) {
		t.Fatalf("an apply prints Google's response as sent: %q", out.String())
	}
	l := auditLines(t, a)
	if len(l) != 1 || l[0]["result"] != "applied" || l[0]["request_id"] != "rq-1" || l[0]["credential"] != "writer" ||
		!strings.Contains(asJSON(l[0]["resource_names"]), "campaigns/77") || l[0]["body_sha256"] == "" {
		t.Fatalf("audit %v", l)
	}
}

func TestSpendIsGatedBeforeTheNetwork(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	for _, flags := range [][]string{nil, {"--apply"}, {"--confirm-spend"}, {"--force"}} {
		a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
		code := a.run(append([]string{"campaigns", "mutate", "--data", enableCampaign}, flags...))
		if code != 2 || errJSON(t, errb)["kind"] != "guardrail" {
			t.Fatalf("%v: exit %d %s", flags, code, errb.String())
		}
		if l := auditLines(t, a); len(l) != 1 || l[0]["result"] != "refused" || l[0]["risk_class"] != "spend" ||
			!strings.Contains(l[0]["reasons"].(string), "status set to ENABLED") {
			t.Fatalf("%v: refusals are audited: %v", flags, l)
		}
	}
	if srv.hits.Load() != 0 {
		t.Fatalf("%d requests reached the server", srv.hits.Load())
	}
	// An explicit dry run is allowed and goes out validate_only.
	a, out, _ := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"campaigns", "mutate", "--validate-only", "--data", enableCampaign}); code != 0 ||
		!strings.Contains(srv.bodies[0], `"validateOnly":true`) || !strings.Contains(out.String(), "--confirm-spend") {
		t.Fatalf("exit %d body %q out %q", code, srv.bodies, out.String())
	}
	// --apply --confirm-spend, and the deprecated --apply --force, apply it.
	for _, flags := range [][]string{{"--apply", "--confirm-spend"}, {"--apply", "--force"}} {
		a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
		if code := a.run(append([]string{"campaigns", "mutate", "--data", enableCampaign}, flags...)); code != 0 {
			t.Fatalf("%v: exit %d %s", flags, code, errb.String())
		}
		l := auditLines(t, a)
		if len(l) != 1 || l[0]["result"] != "applied" || l[0]["risk_class"] != "spend" || l[0]["confirm_spend"] != true ||
			!strings.Contains(l[0]["reasons"].(string), "status set to ENABLED") {
			t.Fatalf("%v: audit %v", flags, l)
		}
	}
}

func TestMissingStatusIsRefusedLocally(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	code := a.run([]string{"campaigns", "mutate", "--apply", "--confirm-spend", "--data", `{"operations":[{"create":{"name":"x"}}]}`})
	if code != 2 || !strings.Contains(errb.String(), "PAUSED") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestApplyNeedsTheWriterCredential(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890", Writer: noCredential})
	if code := a.run([]string{"campaigns", "mutate", "--apply", "--data", pausedCampaign}); code != 2 ||
		!strings.Contains(errb.String(), "writer credential") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	a, _, errb = testApp(t, srv, config.Resolved{CustomerID: "1234567890", Writer: noCredential})
	if code := a.run([]string{"campaigns", "mutate", "--data", pausedCampaign}); code != 0 {
		t.Fatalf("a dry run needs only the reader: exit %d %s", code, errb.String())
	}
	a, _, errb = testApp(t, srv, config.Resolved{CustomerID: "1234567890", DryRunCredential: config.RoleWriter})
	if code := a.run([]string{"campaigns", "mutate", "--data", pausedCampaign}); code != 0 || srv.headers[1].Get("Authorization") != "Bearer tok-writer" {
		t.Fatalf("dry-run-credential writer: exit %d %s", code, errb.String())
	}
}

func TestUnwritableAuditLogStopsTheWrite(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	dir := t.TempDir() // a directory cannot be opened as the log file
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890", AuditLog: dir})
	if code := a.run([]string{"campaigns", "mutate", "--data", pausedCampaign}); code != 2 || !strings.Contains(errb.String(), "audit log") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	a, _, errb = testApp(t, srv, config.Resolved{CustomerID: "1234567890", AuditLog: dir})
	if code := a.run([]string{"search", "--query", "x"}); code != 0 {
		t.Fatalf("reads do not need the audit log: exit %d %s", code, errb.String())
	}
	if srv.hits.Load() != 1 {
		t.Fatalf("hits %d", srv.hits.Load())
	}
}

func TestAuditLogIsPrivateAndHoldsNoSecrets(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[]}`))
	a, _, _ := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	a.run([]string{"campaigns", "mutate", "--apply", "--data", pausedCampaign})
	raw, err := os.ReadFile(a.res.AuditLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "tok-") || strings.Contains(string(raw), "Bearer") || strings.Contains(string(raw), "PRIVATE") {
		t.Fatalf("the audit log carries a credential: %s", raw)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(a.res.AuditLog)
		di, _ := os.Stat(filepath.Dir(a.res.AuditLog))
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Fatalf("log %v dir %v", fi.Mode().Perm(), di.Mode().Perm())
		}
	}
}

// budgetServer answers the account-wide budget read with existing budgets
// and records every other call.
func budgetServer(t *testing.T, budgets string) *fakeServer {
	return newFakeServer(t, func(w http.ResponseWriter, r *http.Request, body string) {
		if strings.HasSuffix(r.URL.Path, "googleAds:search") && strings.Contains(body, "campaign_budget") {
			w.Write([]byte(`{"results":[` + budgets + `]}`))
			return
		}
		w.Write([]byte(`{"results":[{"resourceName":"customers/1234567890/campaignBudgets/1"}]}`))
	})
}

func budgetRow(id, micros string) string {
	return `{"campaignBudget":{"resourceName":"customers/1234567890/campaignBudgets/` + id + `","amountMicros":"` + micros + `"}}`
}

func TestAccountWideBudgetCap(t *testing.T) {
	res := config.Resolved{CustomerID: "1234567890", BudgetCapMicros: 30_000_000}
	existing := budgetRow("9", "20000000") + "," + budgetRow("8", "5000000") // 25 of 30 in use
	create := func(amount string) string {
		return `{"operations":[{"create":{"name":"b","amountMicros":"` + amount + `","explicitlyShared":false}}]}`
	}
	update := func(id, amount string) string {
		return `{"operations":[{"update":{"resourceName":"customers/1234567890/campaignBudgets/` + id + `","amountMicros":"` + amount + `"},"updateMask":"amount_micros"}]}`
	}

	// 25 + 10 > 30: refused after the read, before the mutate.
	srv := budgetServer(t, existing)
	a, _, errb := testApp(t, srv, res)
	if code := a.run([]string{"campaign-budgets", "mutate", "--apply", "--confirm-spend", "--data", create("10000000")}); code != 2 ||
		!strings.Contains(errb.String(), "at most 5 more") || srv.hits.Load() != 1 {
		t.Fatalf("over the remaining cap: exit %d hits %d %s", code, srv.hits.Load(), errb.String())
	}
	// 25 + 5 = 30: allowed.
	a, _, errb = testApp(t, srv, res)
	if code := a.run([]string{"campaign-budgets", "mutate", "--apply", "--confirm-spend", "--data", create("5000000")}); code != 0 || srv.hits.Load() != 3 {
		t.Fatalf("within the remaining cap: exit %d hits %d %s", code, srv.hits.Load(), errb.String())
	}
	// Raising budget 9 from 20 to 26 brings the total to 31: refused.
	a, _, errb = testApp(t, srv, res)
	if code := a.run([]string{"campaign-budgets", "mutate", "--apply", "--confirm-spend", "--data", update("9", "26000000")}); code != 2 || srv.hits.Load() != 4 {
		t.Fatalf("raise over the cap: exit %d hits %d %s", code, srv.hits.Load(), errb.String())
	}
	// Lowering it to 15 is fine.
	a, _, errb = testApp(t, srv, res)
	if code := a.run([]string{"campaign-budgets", "mutate", "--apply", "--confirm-spend", "--data", update("9", "15000000")}); code != 0 {
		t.Fatalf("lowering: exit %d %s", code, errb.String())
	}
	// A dry run makes no account read: validate_only cannot spend.
	n := srv.hits.Load()
	a, _, errb = testApp(t, srv, res)
	if code := a.run([]string{"campaign-budgets", "mutate", "--validate-only", "--data", create("10000000")}); code != 0 || srv.hits.Load() != n+1 {
		t.Fatalf("dry run: exit %d %s", code, errb.String())
	}
	// A read that fails refuses the apply.
	failing := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		w.WriteHeader(403)
		w.Write([]byte(`{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`))
	})
	a, _, errb = testApp(t, failing, res)
	if code := a.run([]string{"campaign-budgets", "mutate", "--apply", "--confirm-spend", "--data", create("1000000")}); code != 2 ||
		!strings.Contains(errb.String(), "could not be checked") || failing.hits.Load() != 1 {
		t.Fatalf("failed read: exit %d %s", code, errb.String())
	}
}

func TestBudgetAboveTheCapIsRefusedEvenWhenConfirmed(t *testing.T) {
	srv := budgetServer(t, "")
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890", BudgetCapMicros: 30_000_000})
	code := a.run([]string{"campaign-budgets", "mutate", "--apply", "--confirm-spend", "--data", `{"operations":[{"create":{"amountMicros":"31000000"}}]}`})
	if code != 2 || !strings.Contains(errb.String(), "exceeds the configured cap") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestSearchFormats(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[{"campaign":{"resourceName":"customers/1/campaigns/2","id":"2","name":"A, B","status":"PAUSED"},"metrics":{"clicks":"5"}},{"campaign":{"resourceName":"customers/1/campaigns/3","id":"3","name":"C","status":"ENABLED"},"metrics":{"clicks":"0"}}]}`))
	a, out, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--query", "x", "--format", "csv"}); code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	want := "campaign.resourceName,campaign.id,campaign.name,campaign.status,metrics.clicks\n" +
		"customers/1/campaigns/2,2,\"A, B\",PAUSED,5\ncustomers/1/campaigns/3,3,C,ENABLED,0\n"
	if out.String() != want {
		t.Fatalf("csv %q", out.String())
	}
	a, out, _ = testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--all", "--query", "x", "--format", "table"}); code != 0 ||
		!strings.HasPrefix(out.String(), "campaign.resourceName") || !strings.Contains(out.String(), "A, B") {
		t.Fatalf("table %q", out.String())
	}
	a, _, errb = testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--query", "x", "--format", "xml"}); code != 2 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
