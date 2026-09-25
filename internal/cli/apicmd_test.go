package cli

import (
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

func TestAPIMatchedPathInheritsTheGuard(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{})
	code := a.run([]string{"api", "POST", "v25/customers/1234567890/campaigns:mutate", "--data", `{"operations":[{"create":{"name":"x"}}]}`})
	if code != 2 || !strings.Contains(errb.String(), "PAUSED") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestAPIOtherVersionIsInspected(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{})
	code := a.run([]string{"api", "POST", "/v24/customers/1234567890/campaigns:mutate", "--data", `{"operations":[{"create":{"status":"ENABLED"}}]}`})
	if code != 2 || srv.hits.Load() != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestAPIUnmatchedMutationIsAdmin(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"api", "POST", "v25/customers/1/somethingNew:do", "--data", `{}`}); code != 2 || !strings.Contains(errb.String(), "admin") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	a, _, errb = testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"api", "POST", "v25/customers/1/somethingNew:do", "--data", `{}`, "--force"}); code != 0 || srv.hits.Load() != 1 {
		t.Fatalf("forced: exit %d %s", code, errb.String())
	}
}

func TestAPIBlockedPath(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{})
	code := a.run([]string{"api", "POST", "v25/customers/1234567890/recommendations:apply", "--data", `{}`, "--force"})
	if code != 2 || !strings.Contains(errb.String(), "blocked") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestAPIRefusals(t *testing.T) {
	for _, args := range [][]string{
		{"api", "GET", "v25/customers:listAccessibleCustomers", "--query", "access_token=x"},
		{"api", "GET", "v25/customers:listAccessibleCustomers?key=x"},
		{"api", "TRACE", "v25/x"},
		{"api", "GET", ""},
	} {
		a, _, errb := testApp(t, nil, config.Resolved{})
		if code := a.run(args); code != 2 {
			t.Errorf("%v: exit %d %s", args, code, errb.String())
		}
	}
}

func TestAPIReadOnly(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[]}`))
	res := config.Resolved{ReadOnly: true}
	a, _, errb := testApp(t, srv, res)
	if code := a.run([]string{"api", "POST", "v25/customers/1234567890/googleAds:search", "--data", `{"query":"x"}`}); code != 0 {
		t.Fatalf("read-class POST in read-only mode: exit %d %s", code, errb.String())
	}
	a, _, _ = testApp(t, srv, res)
	if code := a.run([]string{"api", "GET", "v25/customers/1234567890/anythingElse"}); code != 0 {
		t.Fatalf("GET in read-only mode: exit %d", code)
	}
	a, _, _ = testApp(t, srv, res)
	if code := a.run([]string{"api", "POST", "v25/customers/1234567890/campaigns:mutate", "--data", pausedCampaign}); code != 2 {
		t.Fatalf("write in read-only mode: exit %d", code)
	}
}

func TestAPIAuthorizationIsTheClients(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, out, _ := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"api", "GET", "v25/customers:listAccessibleCustomers", "--header", "Authorization: Bearer stolen", "--header", "X-Test: 1"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := srv.headers[0].Get("Authorization"); got != "Bearer tok" || srv.headers[0].Get("X-Test") != "1" {
		t.Fatalf("headers %v", srv.headers[0])
	}
	if out.String() != "{}\n" {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestAPIDeveloperTokenHeaderRefused(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	for _, h := range []string{"developer-token: x", "Developer_Token: x"} {
		a, _, errb := testApp(t, srv, config.Resolved{})
		code := a.run([]string{"api", "GET", "v25/customers:listAccessibleCustomers", "--header", h})
		if code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), "developer-token") {
			t.Errorf("%q: exit %d %s", h, code, errb.String())
		}
	}
	if srv.hits.Load() != 0 {
		t.Fatal("a developer-token header must not reach the network")
	}
}

func TestAPILoginCustomerIDFlagSetsHeader(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"api", "GET", "v25/customers:listAccessibleCustomers", "--login-customer-id", "111-222-3333"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if got := srv.headers[0].Get("login-customer-id"); got != "1112223333" {
		t.Fatalf("login-customer-id %q", got)
	}
	a, _, errb = testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"api", "GET", "v25/customers:listAccessibleCustomers", "--login-customer-id", "12"}); code != 2 || srv.hits.Load() != 1 {
		t.Fatalf("bad id: exit %d %s", code, errb.String())
	}
}

const hugeBudget = `{"operations":[{"create":{"amountMicros":"999999999999"}}]}`

// The review's repro: a GET that matches no method carried a budget body and
// a method-override header past read-only mode, the cap and --force.
func TestAPIReadOnlyGETWithOverrideAndBudgetBody(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{ReadOnly: true})
	code := a.run([]string{"api", "GET", "v25/customers/1234567890/campaignBudgets:mutate",
		"--header", "X-HTTP-Method-Override: POST", "--data", hugeBudget})
	if code != 2 || srv.hits.Load() != 0 {
		t.Fatalf("exit %d, %d hits: %s", code, srv.hits.Load(), errb.String())
	}
}

func TestAPIMethodOverrideHeadersRefused(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	for _, h := range []string{
		"X-HTTP-Method-Override: POST", "x-http-method-override: POST", "X_HTTP_Method_Override: POST",
		"X-HTTP-Method: POST", "X_HTTP_METHOD: POST", "X-Method-Override: POST", "x_method_override: DELETE",
	} {
		a, _, errb := testApp(t, srv, config.Resolved{})
		code := a.run([]string{"api", "GET", "v25/customers:listAccessibleCustomers", "--header", h})
		if code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), "method-override") {
			t.Errorf("%q: exit %d %s", h, code, errb.String())
		}
	}
	if n := srv.hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
}

func TestAPIGETWithDataRefused(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	for _, method := range []string{"GET", "DELETE"} {
		a, _, errb := testApp(t, srv, config.Resolved{BudgetCapMicros: 1_000_000_000_000})
		code := a.run([]string{"api", method, "v25/customers/1234567890/anything", "--data", hugeBudget, "--force"})
		if code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), "body") {
			t.Errorf("%s: exit %d %s", method, code, errb.String())
		}
	}
	if n := srv.hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
}

// Path spellings that do not match the budget method literally must not go
// out as an uninspected admin call, even with --force and no cap.
func TestAPIPathVariantsRefused(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	for _, p := range []string{
		"v25/customers/1234567890/campaignBudgets%3Amutate",
		"v25/customers/x/../1234567890/campaignBudgets:mutate",
		"v25/customers/1234567890/./campaignBudgets:mutate",
		"v25//customers/1234567890/campaignBudgets:mutate",
		"v25/customers/1234567890/campaignBudgets:mutate/",
		"./v25/customers/1234567890/campaignBudgets:mutate",
		"v25/customers/1234567890/campaign Budgets:mutate",
		"v25/customers/1234567890/campaignBudgets:mutate\t",
		`v25/customers/1234567890\campaignBudgets:mutate`,
	} {
		a, _, errb := testApp(t, srv, config.Resolved{})
		code := a.run([]string{"api", "POST", p, "--data", hugeBudget, "--force"})
		if code != 2 || errJSON(t, errb)["kind"] != "usage" {
			t.Errorf("%q: exit %d %s", p, code, errb.String())
		}
	}
	if n := srv.hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
	// Control: the canonical path is matched and meets the cap.
	a, _, errb := testApp(t, srv, config.Resolved{})
	code := a.run([]string{"api", "POST", "v25/customers/1234567890/campaignBudgets:mutate", "--data", hugeBudget, "--force"})
	if code != 2 || !strings.Contains(errb.String(), "no budget cap") || srv.hits.Load() != 0 {
		t.Fatalf("canonical path: exit %d %s", code, errb.String())
	}
}
