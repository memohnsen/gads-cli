package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/config"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// testManifest is a small hand-built manifest with every shape the CLI handles.
func testManifest() *manifest.Manifest {
	mut := func(field, res string) *manifest.Mutate {
		return &manifest.Mutate{OperationsField: field, Resource: res}
	}
	cid := manifest.Param{Name: "customerId", Type: "string", Required: true, Pattern: "^[^/]+$"}
	return &manifest.Manifest{
		SchemaVersion: 1, APIVersion: "v25", DiscoveryRevision: "20260923",
		Methods: []manifest.Method{
			{Command: []string{"search"}, ID: "customers.googleAds.search", HTTPMethod: "POST",
				Path: "v25/customers/{+customerId}/googleAds:search", Risk: "read", NeedsCustomer: true,
				PathParams: []manifest.Param{cid}, Request: "SearchRequest", ValidateOnly: true,
				Paging: manifest.PagingBodyToken, ResultsField: "results", Description: "Returns all rows that match the search query."},
			{Command: []string{"mutate"}, ID: "customers.googleAds.mutate", HTTPMethod: "POST",
				Path: "v25/customers/{+customerId}/googleAds:mutate", Risk: "write", NeedsCustomer: true,
				PathParams: []manifest.Param{cid}, Request: "MutateGoogleAdsRequest", ValidateOnly: true, PartialFailure: true,
				Paging: manifest.PagingNone, Mutate: mut("mutateOperations", "")},
			{Command: []string{"campaigns", "mutate"}, ID: "customers.campaigns.mutate", HTTPMethod: "POST",
				Path: "v25/customers/{+customerId}/campaigns:mutate", Risk: "write", NeedsCustomer: true,
				PathParams: []manifest.Param{cid}, Request: "MutateCampaignsRequest", ValidateOnly: true, PartialFailure: true,
				Paging: manifest.PagingNone, Mutate: mut("operations", "Campaign")},
			{Command: []string{"campaign-budgets", "mutate"}, ID: "customers.campaignBudgets.mutate", HTTPMethod: "POST",
				Path: "v25/customers/{+customerId}/campaignBudgets:mutate", Risk: "write", NeedsCustomer: true,
				PathParams: []manifest.Param{cid}, Request: "MutateCampaignBudgetsRequest", ValidateOnly: true, PartialFailure: true,
				Paging: manifest.PagingNone, Mutate: mut("operations", "CampaignBudget")},
			{Command: []string{"operations", "list"}, ID: "customers.operations.list", HTTPMethod: "GET", Path: "v25/{+name}",
				Risk: "read", PathParams: []manifest.Param{{Name: "name", Type: "string", Required: true, Pattern: "^customers/[^/]+/operations$"}},
				QueryParams: []manifest.Param{{Name: "pageSize", Type: "integer"}, {Name: "pageToken", Type: "string"}},
				Paging:      manifest.PagingQueryToken, ResultsField: "operations"},
			{Command: []string{"recommendations", "apply"}, ID: "customers.recommendations.apply", HTTPMethod: "POST",
				Path: "v25/customers/{+customerId}/recommendations:apply", Risk: "spend", Blocked: "applies amounts Google chooses",
				NeedsCustomer: true, PathParams: []manifest.Param{cid}, Request: "ApplyRecommendationRequest", Paging: manifest.PagingNone},
			{Command: []string{"list-accessible-customers"}, ID: "customers.listAccessibleCustomers", HTTPMethod: "GET",
				Path: "v25/customers:listAccessibleCustomers", Risk: "read", Paging: manifest.PagingNone},
		},
		Schemas: map[string]manifest.Schema{
			"GoogleAdsGoogleadsV25Resources__Campaign": {Fields: []manifest.Field{
				{Name: "id", Type: "string", Format: "int64", ReadOnly: true, Doc: "The ID of the campaign."},
				{Name: "name", Type: "string", Doc: "The name of the campaign."},
				{Name: "networkSettings", Type: "object", Ref: "GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings"},
				{Name: "status", Type: "string", Enum: []string{"UNSPECIFIED", "UNKNOWN", "ENABLED", "PAUSED", "REMOVED"}, Doc: "The status of the campaign."},
			}},
			"GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings": {Fields: []manifest.Field{{Name: "targetGoogleSearch", Type: "boolean"}}},
			"SearchRequest": {Fields: []manifest.Field{{Name: "pageToken", Type: "string"}, {Name: "query", Type: "string", Required: true}}},
		},
		GenericOperations: map[string]string{"campaignOperation": "customers.campaigns.mutate"},
	}
}

type fakeServer struct {
	*httptest.Server
	hits    atomic.Int32
	mu      sync.Mutex
	bodies  []string
	uris    []string
	headers []http.Header
}

func newFakeServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body string)) *fakeServer {
	t.Helper()
	fs := &fakeServer{}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		fs.mu.Lock()
		fs.bodies = append(fs.bodies, string(b))
		fs.uris = append(fs.uris, r.URL.RequestURI())
		fs.headers = append(fs.headers, r.Header.Clone())
		fs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		handler(w, r, string(b))
	}))
	t.Cleanup(fs.Close)
	return fs
}

func reply(body string) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, _ string) { w.Write([]byte(body)) }
}

func testApp(t *testing.T, srv *fakeServer, res config.Resolved) (*app, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errb bytes.Buffer
	base := "https://googleads.googleapis.com"
	if srv != nil {
		base = srv.URL
	}
	a := &app{manifest: testManifest(), res: res, stdout: &out, stderr: &errb, stdin: strings.NewReader("")}
	a.client = &api.Client{BaseURL: base, AllowCustomBase: true, TokenSource: api.StaticToken("tok"),
		ReadOnly: res.ReadOnly, LoginCustomerID: res.LoginCustomerID, Sleep: func(time.Duration) {}}
	return a, &out, &errb
}

func errJSON(t *testing.T, errb *bytes.Buffer) map[string]any {
	t.Helper()
	if n := strings.Count(strings.TrimSuffix(errb.String(), "\n"), "\n"); n != 0 {
		t.Fatalf("stderr spans %d extra lines: %q", n, errb.String())
	}
	var e map[string]any
	if err := json.Unmarshal(errb.Bytes(), &e); err != nil {
		t.Fatalf("stderr is not JSON: %q", errb.String())
	}
	return e
}

func writeFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const pausedCampaign = `{"operations":[{"create":{"name":"x","status":"PAUSED"}}]}`

func TestVersion(t *testing.T) {
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"version"}); code != 0 || !strings.Contains(out.String(), "Google Ads API v25") {
		t.Fatalf("exit %d out %q", code, out.String())
	}
}

func TestUnknownCommandAndBareGroupAreUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"nope"}, {"campaigns"}, {}} {
		a, _, errb := testApp(t, nil, config.Resolved{})
		if code := a.run(args); code != 2 || errJSON(t, errb)["kind"] != "usage" {
			t.Errorf("%v: exit %d stderr %q", args, code, errb.String())
		}
	}
}

func TestCustomerIDFlagWithDashes(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[]}`))
	a, out, errb := testApp(t, srv, config.Resolved{})
	code := a.run([]string{"search", "--customer-id", "123-456-7890", "--query", "SELECT customer.id FROM customer WHERE metrics.clicks > 0"})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if srv.uris[0] != "/v25/customers/1234567890/googleAds:search" {
		t.Fatalf("uri %q", srv.uris[0])
	}
	if srv.bodies[0] != `{"query":"SELECT customer.id FROM customer WHERE metrics.clicks > 0"}` {
		t.Fatalf("body %q", srv.bodies[0])
	}
	if out.String() != "{\"results\":[]}\n" {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestCustomerFromConfigAndMissingCustomer(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, _ := testApp(t, srv, config.Resolved{CustomerID: "1112223333"})
	if code := a.run([]string{"search", "--query", "SELECT customer.id FROM customer"}); code != 0 || srv.uris[0] != "/v25/customers/1112223333/googleAds:search" {
		t.Fatalf("exit %d uris %v", code, srv.uris)
	}
	a, _, errb := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"search", "--query", "x"}); code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), "customer") {
		t.Fatalf("exit %d stderr %q", code, errb.String())
	}
	if srv.hits.Load() != 1 {
		t.Fatal("a call without a customer must not reach the network")
	}
}

func TestQueryAndDataConflict(t *testing.T) {
	a, _, errb := testApp(t, nil, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--query", "x", "--data", `{"query":"y"}`}); code != 2 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

func TestQueryFromFile(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	p := writeFile(t, "q.gaql", append([]byte{0xEF, 0xBB, 0xBF}, []byte("SELECT campaign.id FROM campaign\n")...))
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--query", "@" + p}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if srv.bodies[0] != `{"query":"SELECT campaign.id FROM campaign"}` {
		t.Fatalf("body %q", srv.bodies[0])
	}
}

func TestDataFileWithUTF8BOMAccepted(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[]}`))
	p := writeFile(t, "body.json", append([]byte{0xEF, 0xBB, 0xBF}, []byte(pausedCampaign)...))
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"campaigns", "mutate", "--data", "@" + p}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if srv.bodies[0] != pausedCampaign {
		t.Fatalf("body %q", srv.bodies[0])
	}
}

func TestDataFileUTF16Refused(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	p := writeFile(t, "body.json", []byte{0xFF, 0xFE, '{', 0, '}', 0})
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	code := a.run([]string{"campaigns", "mutate", "--data", "@" + p})
	msg, _ := errJSON(t, errb)["error"].(string)
	if code != 2 || !strings.Contains(msg, "UTF-16") || !strings.Contains(msg, "UTF-8") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d msg %q", code, msg)
	}
}

func TestSpendNeedsForceAndNeverReachesTheNetwork(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[{}]}`))
	enable := `{"operations":[{"create":{"name":"x"}}]}`
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	code := a.run([]string{"campaigns", "mutate", "--data", enable})
	if code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), "PAUSED") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d stderr %q hits %d", code, errb.String(), srv.hits.Load())
	}
	a, _, errb = testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"campaigns", "mutate", "--data", enable, "--force"}); code != 0 || srv.hits.Load() != 1 {
		t.Fatalf("forced: exit %d stderr %q", code, errb.String())
	}
}

func TestValidateOnlySetsTheFieldAndSkipsForce(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	code := a.run([]string{"campaigns", "mutate", "--validate-only", "--data", `{"validate_only":false,"operations":[{"create":{"name":"x"}}]}`})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	b := srv.bodies[0]
	if !strings.Contains(b, `"validateOnly":true`) || strings.Contains(b, "validate_only") || !strings.Contains(b, `"operations"`) {
		t.Fatalf("body %q", b)
	}
}

func TestPartialFailureExitsOneWithTheResponseOnStdout(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[{}],"partialFailureError":{"code":3,"message":"Multiple errors in details.","details":[{"@type":"type.googleapis.com/google.ads.googleads.v25.errors.GoogleAdsFailure","errors":[{"errorCode":{"fieldError":"REQUIRED"},"message":"The required field was not present."}]}]}}`))
	a, out, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	code := a.run([]string{"campaigns", "mutate", "--partial-failure", "--data", pausedCampaign})
	if code != 1 || errJSON(t, errb)["kind"] != "partial_failure" || !strings.Contains(out.String(), "partialFailureError") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out.String(), errb.String())
	}
	if !strings.Contains(srv.bodies[0], `"partialFailure":true`) {
		t.Fatalf("body %q", srv.bodies[0])
	}
}

func TestBlockedMethodRefused(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{})
	code := a.run([]string{"recommendations", "apply", "--force", "--data", `{"operations":[]}`})
	if code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), "blocked") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d stderr %q", code, errb.String())
	}
}

func TestReadOnlyAllowsValidateOnly(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	res := config.Resolved{CustomerID: "1234567890", ReadOnly: true}
	a, _, _ := testApp(t, srv, res)
	if code := a.run([]string{"campaigns", "mutate", "--data", pausedCampaign}); code != 2 {
		t.Fatalf("read-only write: exit %d", code)
	}
	a, _, errb := testApp(t, srv, res)
	if code := a.run([]string{"campaigns", "mutate", "--validate-only", "--data", pausedCampaign}); code != 0 {
		t.Fatalf("read-only validate-only: exit %d %s", code, errb.String())
	}
}

func TestAllBodyTokenMerges(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, body string) {
		if strings.Contains(body, `"pageToken":"p2"`) {
			w.Write([]byte(`{"results":[3]}`))
			return
		}
		w.Write([]byte(`{"results":[1,2],"nextPageToken":"p2"}`))
	})
	a, out, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--all", "--query", "SELECT campaign.id FROM campaign"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if out.String() != "[1,2,3]\n" {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestAllQueryTokenMerges(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.URL.Query().Get("pageToken") == "t" {
			w.Write([]byte(`{"operations":[{"name":"b"}]}`))
			return
		}
		w.Write([]byte(`{"operations":[{"name":"a"}],"nextPageToken":"t"}`))
	})
	a, out, errb := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"operations", "list", "customers/1234567890/operations", "--all"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if out.String() != `[{"name":"a"},{"name":"b"}]`+"\n" || !strings.HasPrefix(srv.uris[0], "/v25/customers/1234567890/operations") {
		t.Fatalf("stdout %q uris %v", out.String(), srv.uris)
	}
}

func TestAllMaxPagesIsIncomplete(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[1],"nextPageToken":"more"}`))
	a, out, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	code := a.run([]string{"search", "--all", "--max-pages", "2", "--query", "x"})
	if code != 1 || errJSON(t, errb)["kind"] != "incomplete" || out.String() != "[1,1]\n" {
		t.Fatalf("exit %d stdout %q", code, out.String())
	}
}

func TestAllRefusesSummaryRow(t *testing.T) {
	a, _, _ := testApp(t, nil, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--all", "--data", `{"query":"x","searchSettings":{"returnSummaryRow":true}}`}); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestPositionalValidation(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	for _, name := range []string{"customers/1/../x", "customers/1234567890/other", "customers/1/operations?x=1"} {
		a, _, errb := testApp(t, srv, config.Resolved{})
		if code := a.run([]string{"operations", "list", name}); code != 2 {
			t.Errorf("%q: exit %d %s", name, code, errb.String())
		}
	}
	if srv.hits.Load() != 0 {
		t.Fatal("an invalid path parameter must not reach the network")
	}
}

func TestLoginCustomerIDFlagSetsHeader(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--query", "x", "--login-customer-id", "111-222-3333"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if got := srv.headers[0].Get("login-customer-id"); got != "1112223333" {
		t.Fatalf("login-customer-id %q", got)
	}
}

func TestTopLevelLeafCommand(t *testing.T) {
	srv := newFakeServer(t, reply(`{"resourceNames":["customers/1234567890"]}`))
	a, out, errb := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"list-accessible-customers"}); code != 0 || srv.uris[0] != "/v25/customers:listAccessibleCustomers" {
		t.Fatalf("exit %d uris %v %s", code, srv.uris, errb.String())
	}
	if !strings.Contains(out.String(), "customers/1234567890") {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestAPIErrorIsOneJSONLine(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		w.Header().Set("request-id", "rq-9")
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":400,"message":"bad","status":"INVALID_ARGUMENT"}}`))
	})
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--query", "x"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	e := errJSON(t, errb)
	if e["kind"] != "validation" || e["request_id"] != "rq-9" || e["status"] != float64(400) {
		t.Fatalf("%v", e)
	}
}

func TestValidateOnlyOfferedOnlyWhereTheManifestSaysSo(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	for _, args := range [][]string{
		{"list-accessible-customers", "--validate-only"},
		{"operations", "list", "customers/1234567890/operations", "--validate-only"},
	} {
		a, _, errb := testApp(t, srv, config.Resolved{})
		code := a.run(args)
		if code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), "validate-only") {
			t.Errorf("%v: exit %d stderr %q", args, code, errb.String())
		}
	}
	if srv.hits.Load() != 0 {
		t.Fatal("an unknown flag must not reach the network")
	}
}

func TestContextSleeperStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	contextSleeper(ctx)(time.Hour)
	if time.Since(start) > 5*time.Second {
		t.Fatal("the sleeper kept waiting after the context was cancelled")
	}
}

// TestEmbeddedManifestBuildsTheTree renders help for every generated leaf of
// the real manifest: a duplicate flag or a bad usage string would panic here
// instead of in the binary.
func TestEmbeddedManifestBuildsTheTree(t *testing.T) {
	m, err := manifest.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, meth := range m.Methods {
		a, out, errb := testApp(t, nil, config.Resolved{})
		a.manifest = m
		if code := a.run(append(append([]string{}, meth.Command...), "--help")); code != 0 || !strings.Contains(out.String(), meth.Path) {
			t.Fatalf("%v: exit %d stderr %q", meth.Command, code, errb.String())
		}
	}
}

func TestAllDropsAStalePageToken(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, body string) {
		switch {
		case strings.Contains(body, `"pageToken":"p2"`):
			w.Write([]byte(`{"results":[3]}`))
		case strings.Contains(body, "stale"):
			w.Write([]byte(`{"results":[9]}`))
		default:
			w.Write([]byte(`{"results":[1,2],"nextPageToken":"p2"}`))
		}
	})
	for _, key := range []string{"pageToken", "page_token"} {
		a, out, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
		n := len(srv.bodies)
		if code := a.run([]string{"search", "--all", "--data", `{"query":"x","` + key + `":"stale"}`}); code != 0 {
			t.Fatalf("%s: exit %d: %s", key, code, errb.String())
		}
		if first := srv.bodies[n]; strings.Contains(first, "pageToken") || strings.Contains(first, "page_token") {
			t.Fatalf("%s: first request carries a page token: %q", key, first)
		}
		if out.String() != "[1,2,3]\n" {
			t.Fatalf("%s: stdout %q", key, out.String())
		}
	}
}

func TestErrorJSONKeepsAngleBracketsLiteral(t *testing.T) {
	a, _, errb := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"schema", "<a&b>"}); code != 2 || !strings.Contains(errb.String(), "<a&b>") {
		t.Fatalf("exit %d stderr %q", code, errb.String())
	}
	errJSON(t, errb)
}
