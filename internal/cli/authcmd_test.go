package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

// fakeGoogle serves a token endpoint and the two calls init makes.
func fakeGoogle(t *testing.T, searchStatus int) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/token":
			fmt.Fprintf(w, `{"access_token":"at-%d","expires_in":3600,"token_type":"Bearer"}`, n.Add(1))
		case r.URL.Path == "/v25/customers:listAccessibleCustomers":
			w.Write([]byte(`{"resourceNames":["customers/1234567890"]}`))
		case r.URL.Path == "/v25/customers/1234567890/googleAds:search" && searchStatus == 200:
			w.Write([]byte(`{"results":[{"customer":{"resourceName":"customers/1234567890","descriptiveName":"Muster AG","currencyCode":"CHF","timeZone":"Europe/Zurich"}}]}`))
		default:
			w.WriteHeader(403)
			w.Write([]byte(`{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.ads.googleads.v25.errors.GoogleAdsFailure","errors":[{"errorCode":{"authorizationError":"USER_PERMISSION_DENIED"},"message":"User doesn't have permission to access customer."}]}]}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func statusOf(t *testing.T, res config.Resolved) map[string]any {
	t.Helper()
	a, out, errb := testApp(t, nil, res)
	if code := a.run([]string{"auth", "status"}); code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	if strings.Contains(out.String(), "PRIVATE KEY") {
		t.Fatal("auth status must never print key material")
	}
	var s map[string]any
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAuthStatusNone(t *testing.T) {
	isolate(t)
	s := statusOf(t, config.Resolved{})
	missing := fmt.Sprint(s["missing"])
	if s["mode"] != "none" || !strings.Contains(missing, "service_account_key") || !strings.Contains(missing, "customer_id") {
		t.Fatalf("%v", s)
	}
}

func TestAuthStatusWithKey(t *testing.T) {
	isolate(t)
	s := statusOf(t, config.Resolved{KeyJSON: testKeyJSON(t), CustomerID: "1234567890", BudgetCapMicros: 29610000,
		TokenURL: config.DefaultTokenURL})
	if s["mode"] != "service_account" || s["source"] != "config" || s["client_email"] != "gads@proj.iam.gserviceaccount.com" ||
		s["budget_cap"] != "29.61" || s["missing"] != nil {
		t.Fatalf("%v", s)
	}
}

func TestAuthTokenPrintsAToken(t *testing.T) {
	isolate(t)
	srv := fakeGoogle(t, 200)
	res := config.Resolved{KeyJSON: testKeyJSON(t), TokenURL: srv.URL + "/token", AllowCustomBase: true}
	a, out, errb := testApp(t, nil, res)
	a.client.TokenSource = a.tokenSource()
	if code := a.run([]string{"auth", "token"}); code != 0 || strings.TrimSpace(out.String()) != "at-1" {
		t.Fatalf("exit %d out %q %s", code, out.String(), errb.String())
	}
}
