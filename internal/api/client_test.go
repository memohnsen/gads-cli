package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var slept []time.Duration
	return &Client{BaseURL: srv.URL, TokenSource: StaticToken("tok"), AllowCustomBase: true,
		Sleep: func(d time.Duration) { slept = append(slept, d) }}, &slept
}

func asErr(t *testing.T, err error) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not *Error", err)
	}
	return e
}

var search = Request{Method: "POST", Path: "v25/customers/1234567890/googleAds:search",
	Body: []byte(`{"query":"SELECT customer.id FROM customer"}`), Risk: manifest.RiskRead, MethodID: "customers.googleAds.search"}

func TestDoSendsHeadersAndJoinsPath(t *testing.T) {
	var got *http.Request
	var body string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Write([]byte(`{"results":[]}`))
	})
	c.LoginCustomerID = "1112223333"
	resp, err := c.Do(context.Background(), search)
	if err != nil || resp.Status != 200 || string(resp.Body) != `{"results":[]}` {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if got.URL.Path != "/v25/customers/1234567890/googleAds:search" {
		t.Fatalf("path %q", got.URL.Path)
	}
	if got.Header.Get("Authorization") != "Bearer tok" || got.Header.Get("Accept") != "application/json" ||
		got.Header.Get("Content-Type") != "application/json" || got.Header.Get("login-customer-id") != "1112223333" {
		t.Fatalf("headers %v", got.Header)
	}
	if got.Header.Get("developer-token") != "" {
		t.Fatal("the developer-token header must never be sent")
	}
	if body != string(search.Body) {
		t.Fatalf("body %q", body)
	}
}

func TestNoLoginCustomerIDHeaderWhenUnset(t *testing.T) {
	var got http.Header
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { got = r.Header.Clone(); w.Write([]byte(`{}`)) })
	if _, err := c.Do(context.Background(), search); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["Login-Customer-Id"]; ok {
		t.Fatal("login-customer-id sent while unset")
	}
}

func TestGuardRefusals(t *testing.T) {
	cases := map[string]struct {
		c    Client
		r    Request
		want string
	}{
		"no credential":   {Client{BaseURL: "https://googleads.googleapis.com"}, search, "no service-account key"},
		"foreign host":    {Client{BaseURL: "https://evil.example", TokenSource: StaticToken("t")}, search, "refusing to send credentials to evil.example"},
		"plain http":      {Client{BaseURL: "http://example.com", TokenSource: StaticToken("t"), AllowCustomBase: true}, search, "non-HTTPS"},
		"read-only write": {Client{BaseURL: "https://googleads.googleapis.com", TokenSource: StaticToken("t"), ReadOnly: true}, Request{Method: "POST", Path: "v25/x:mutate", Risk: manifest.RiskWrite}, "read-only"},
		"credential param": {Client{BaseURL: "https://googleads.googleapis.com", TokenSource: StaticToken("t")},
			Request{Method: "GET", Path: "v25/x", Risk: manifest.RiskRead, Query: url.Values{"access_token": {"x"}}}, "access_token"},
	}
	for name, tc := range cases {
		_, err := tc.c.Do(context.Background(), tc.r)
		if e := asErr(t, err); e.Kind != KindUsage || !strings.Contains(e.Message, tc.want) {
			t.Errorf("%s: %+v", name, e)
		}
	}
}

func TestReadOnlyAllowsValidateOnly(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	c.ReadOnly = true
	_, err := c.Do(context.Background(), Request{Method: "POST", Path: "v25/customers/1/campaigns:mutate",
		Body: []byte(`{}`), Risk: manifest.RiskSpend, ValidateOnly: true})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCallerCannotOverrideAuthorization(t *testing.T) {
	var auth string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	})
	r := search
	r.Headers = http.Header{"Authorization": {"Bearer stolen"}, "X-Extra": {"1"}}
	if _, err := c.Do(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" {
		t.Fatalf("Authorization %q", auth)
	}
}

func TestRedirectRefused(t *testing.T) {
	var target int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&target, 1) }))
	defer other.Close()
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	})
	_, err := c.Do(context.Background(), search)
	e := asErr(t, err)
	if e.Kind != KindValidation || !strings.Contains(e.Message, other.URL) || atomic.LoadInt32(&target) != 0 {
		t.Fatalf("%+v, target hit %d times", e, target)
	}
}

func Test429HonorsRetryDelay(t *testing.T) {
	var n int32
	c, slept := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			w.Write([]byte(quotaBody("5s")))
			return
		}
		w.Write([]byte(`{}`))
	})
	if _, err := c.Do(context.Background(), search); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 1 || (*slept)[0] < 5*time.Second || (*slept)[0] > 6*time.Second {
		t.Fatalf("slept %v", *slept)
	}
}

func Test429BeyondBudgetFailsAtOnce(t *testing.T) {
	c, slept := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		w.Write([]byte(quotaBody("3600s")))
	})
	_, err := c.Do(context.Background(), search)
	e := asErr(t, err)
	if e.Kind != KindRateLimited || !strings.Contains(e.Message, "retry budget") || len(*slept) != 0 {
		t.Fatalf("%+v slept %v", e, *slept)
	}
}

func TestReadRetriesServerErrorsEvenAsPOST(t *testing.T) {
	var n int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{}`))
	})
	if _, err := c.Do(context.Background(), search); err != nil || atomic.LoadInt32(&n) != 3 {
		t.Fatalf("err %v attempts %d", err, n)
	}
}

func TestMutationIsNeverReplayed(t *testing.T) {
	var n int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&n, 1); w.WriteHeader(503) })
	_, err := c.Do(context.Background(), Request{Method: "POST", Path: "v25/customers/1/campaigns:mutate", Body: []byte(`{}`), Risk: manifest.RiskWrite})
	if e := asErr(t, err); e.Kind != KindServer || atomic.LoadInt32(&n) != 1 {
		t.Fatalf("%+v after %d attempts", e, n)
	}
}

func TestDroppedConnection(t *testing.T) {
	var n int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	})
	_, err := c.Do(context.Background(), Request{Method: "POST", Path: "v25/customers/1/campaigns:mutate", Body: []byte(`{}`), Risk: manifest.RiskWrite})
	if e := asErr(t, err); e.Kind != KindOutcomeUnknown {
		t.Fatalf("write: %+v", e)
	}
	atomic.StoreInt32(&n, 0)
	_, err = c.Do(context.Background(), search)
	if e := asErr(t, err); e.Kind != KindTransport || atomic.LoadInt32(&n) != 3 {
		t.Fatalf("read: %+v after %d attempts", e, n)
	}
}

func TestTokenSourceErrorKeepsItsKind(t *testing.T) {
	c := &Client{BaseURL: "https://googleads.googleapis.com", TokenSource: func(context.Context) (string, error) {
		return "", &Error{Kind: KindAuth, Message: "token exchange refused: invalid_grant"}
	}}
	_, err := c.Do(context.Background(), search)
	if e := asErr(t, err); e.Kind != KindAuth || !strings.Contains(e.Message, "invalid_grant") {
		t.Fatalf("%+v", e)
	}
}
