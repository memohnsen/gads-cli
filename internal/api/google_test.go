package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func quotaBody(delay string) string {
	return `{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.ads.googleads.v25.errors.GoogleAdsFailure","errors":[{"errorCode":{"quotaError":"RESOURCE_TEMPORARILY_EXHAUSTED"},"message":"Too many requests.","details":{"quotaErrorDetails":{"rateScope":"ACCOUNT","retryDelay":"` + delay + `"}}}],"requestId":"rq-429"}]}}`
}

func failureBody(code, value, message, path string) string {
	loc := ""
	if path != "" {
		loc = `,"location":{"fieldPathElements":[{"fieldName":"mutate_operations","index":0},{"fieldName":"campaign_operation"},{"fieldName":"create"},{"fieldName":"` + path + `"}]}`
	}
	return `{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.ads.googleads.v25.errors.GoogleAdsFailure","errors":[{"errorCode":{"` + code + `":"` + value + `"},"message":"` + message + `"` + loc + `}],"requestId":"rq-1"}]}}`
}

func respond(status int, body string, header map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}
}

func TestGoogleAdsFailureMessage(t *testing.T) {
	c, _ := testClient(t, respond(400, failureBody("fieldError", "REQUIRED", "The required field was not present.", "contains_eu_political_advertising"), nil))
	_, err := c.Do(context.Background(), Request{Method: "POST", Path: "v25/customers/1/googleAds:mutate", Body: []byte(`{}`), Risk: "write"})
	e := asErr(t, err)
	want := "POST v25/customers/1/googleAds:mutate: HTTP 400: fieldError.REQUIRED: The required field was not present. (mutate_operations[0].campaign_operation.create.contains_eu_political_advertising)"
	if e.Kind != KindValidation || e.Message != want {
		t.Fatalf("got  %q\nwant %q", e.Message, want)
	}
	if e.RequestID != "rq-1" || e.Status != 400 {
		t.Fatalf("%+v", e)
	}
	if _, ok := e.Details.(map[string]any); !ok {
		t.Fatalf("details %T", e.Details)
	}
}

func TestRequestIDHeaderWins(t *testing.T) {
	c, _ := testClient(t, respond(404, `{"error":{"code":404,"message":"not found","status":"NOT_FOUND"}}`, map[string]string{"request-id": "hdr-7"}))
	_, err := c.Do(context.Background(), search)
	if e := asErr(t, err); e.Kind != KindNotFound || e.RequestID != "hdr-7" || !strings.HasSuffix(e.Message, "HTTP 404: not found") {
		t.Fatalf("%+v", e)
	}
}

func TestHints(t *testing.T) {
	cases := map[string]struct {
		status   int
		body     string
		methodID string
		want     string
	}{
		"test access":  {403, failureBody("authorizationError", "CLOUD_PROJECT_NOT_APPROVED_FOR_PRODUCTION", "x", ""), "customers.googleAds.search", "Test access"},
		"no access":    {403, failureBody("authorizationError", "USER_PERMISSION_DENIED", "x", ""), "customers.googleAds.search", "Access and security"},
		"eu political": {400, failureBody("campaignError", "EU_POLITICAL_ADVERTISING_DECLARATION_REQUIRED", "x", ""), "customers.googleAds.mutate", "missing_eu_political_advertising_declaration"},
		"planning":     {403, `{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`, "customers.generateKeywordIdeas", "Basic access"},
	}
	for name, tc := range cases {
		c, _ := testClient(t, respond(tc.status, tc.body, nil))
		r := search
		r.MethodID = tc.methodID
		_, err := c.Do(context.Background(), r)
		if e := asErr(t, err); !strings.Contains(e.Message, "hint: ") || !strings.Contains(e.Message, tc.want) {
			t.Errorf("%s: %q", name, e.Message)
		}
	}
}

func TestConflictKinds(t *testing.T) {
	c, _ := testClient(t, respond(409, `{"error":{"code":409,"message":"aborted","status":"ABORTED"}}`, nil))
	if _, err := c.Do(context.Background(), search); asErr(t, err).Kind != KindConflict {
		t.Fatal("409 must be conflict")
	}
	c, _ = testClient(t, respond(400, failureBody("databaseError", "CONCURRENT_MODIFICATION", "Multiple requests were attempting to modify the same resource at once.", ""), nil))
	if _, err := c.Do(context.Background(), search); asErr(t, err).Kind != KindConflict {
		t.Fatal("CONCURRENT_MODIFICATION must be conflict")
	}
}

func TestPartialFailure(t *testing.T) {
	r := Request{Method: "POST", Path: "v25/customers/1/campaigns:mutate"}
	failed := &Response{Status: 200, Header: http.Header{}, Body: []byte(`{"results":[{},{"resourceName":"customers/1/campaigns/2"}],"partialFailureError":{"code":3,"message":"Multiple errors in details.","details":[{"@type":"type.googleapis.com/google.ads.googleads.v25.errors.GoogleAdsFailure","errors":[{"errorCode":{"fieldError":"REQUIRED"},"message":"The required field was not present.","location":{"fieldPathElements":[{"fieldName":"operations","index":0}]}}]}]}}`)}
	e := PartialFailure(r, failed)
	if e == nil || e.Kind != KindPartialFailure || !strings.Contains(e.Message, "fieldError.REQUIRED") {
		t.Fatalf("%+v", e)
	}
	ok := &Response{Status: 200, Header: http.Header{}, Body: []byte(`{"results":[{"resourceName":"customers/1/campaigns/2"}]}`)}
	if PartialFailure(r, ok) != nil {
		t.Fatal("no partialFailureError means no error")
	}
}
