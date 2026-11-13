package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/memohnsen/gads-cli/internal/auth"
	"github.com/memohnsen/gads-cli/internal/config"
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
		case r.URL.Path == "/v26/customers:listAccessibleCustomers":
			w.Write([]byte(`{"resourceNames":["customers/1234567890"]}`))
		case r.URL.Path == "/v26/customers/1234567890/googleAds:search" && searchStatus == 200:
			w.Write([]byte(`{"results":[{"customer":{"resourceName":"customers/1234567890","descriptiveName":"Muster AG","currencyCode":"CHF","timeZone":"Europe/Zurich"}}]}`))
		default:
			w.WriteHeader(403)
			w.Write([]byte(`{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.ads.googleads.v26.errors.GoogleAdsFailure","errors":[{"errorCode":{"authorizationError":"USER_PERMISSION_DENIED"},"message":"User doesn't have permission to access customer."}]}]}}`))
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
	s := statusOf(t, config.Resolved{Reader: noCredential, Writer: noCredential, ReadOnly: true, ReadOnlyOrigin: "default"})
	missing := fmt.Sprint(s["missing"])
	if s["mode"] != "none" || !strings.Contains(missing, "reader_key") || !strings.Contains(missing, "writer_key") ||
		!strings.Contains(missing, "customer_id") || s["read_only"] != true {
		t.Fatalf("%v", s)
	}
}

// keyFile writes the test key with the given mode and returns its path.
func keyFile(t *testing.T, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(p, testKeyJSON(t), mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, mode)
	return p
}

func TestAuthStatusWithKeys(t *testing.T) {
	isolate(t)
	p := keyFile(t, 0o600)
	s := statusOf(t, config.Resolved{
		Reader:     config.Credential{Role: "reader", Source: config.SourceEnv, Path: p, EnvName: "GOOGLE_ADS_JSON_KEY_FILE_PATH"},
		Writer:     config.Credential{Role: "writer", Source: config.SourceFile, Path: p},
		CustomerID: "1234567890", BudgetCapMicros: 29610000, TokenURL: config.DefaultTokenURL,
	})
	creds := s["credentials"].(map[string]any)
	r, w := creds["reader"].(map[string]any), creds["writer"].(map[string]any)
	if s["mode"] != "service_account" || r["source"] != "env" || r["client_email"] != "gads@proj.iam.gserviceaccount.com" ||
		r["key_id_suffix"] != "kid-1" || w["source"] != "file" || s["budget_cap"] != "29.61" || s["missing"] != nil {
		t.Fatalf("%v", s)
	}
}

func TestKeyFileReadableByOthersIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no permission bits")
	}
	isolate(t)
	p := keyFile(t, 0o644)
	s := statusOf(t, config.Resolved{Reader: config.Credential{Role: "reader", Source: config.SourceEnv, Path: p}, Writer: noCredential})
	r := s["credentials"].(map[string]any)["reader"].(map[string]any)
	if !strings.Contains(fmt.Sprint(r["error"]), "chmod 600") || !strings.Contains(fmt.Sprint(s["missing"]), "valid_reader_key") {
		t.Fatalf("%v", s)
	}
}

func TestAuthTokenIsGone(t *testing.T) {
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"auth", "token"}); code != 2 || out.Len() != 0 {
		t.Fatalf("auth token must not exist: exit %d out %q", code, out.String())
	}
}

// A fake op binary hands out the key; googleads must read it from op's
// stdout and never print it.
func TestOpReferenceLoadsTheKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	isolate(t)
	dir := t.TempDir()
	keyPath := keyFile(t, 0o600)
	script := "#!/bin/sh\n[ \"$1\" = read ] && [ \"$2\" = --no-newline ] && [ \"$3\" = op://Vault/reader/credential ] && exec cat " + keyPath + "\necho '[ERROR] item not found' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "op"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := auth.OpBinary
	auth.OpBinary = filepath.Join(dir, "op")
	t.Cleanup(func() { auth.OpBinary = old })
	s := statusOf(t, config.Resolved{
		Reader: config.Credential{Role: "reader", Source: config.SourceOp, OpRef: "op://Vault/reader/credential"},
		Writer: config.Credential{Role: "writer", Source: config.SourceOp, OpRef: "op://Vault/missing/credential"},
	})
	creds := s["credentials"].(map[string]any)
	r, w := creds["reader"].(map[string]any), creds["writer"].(map[string]any)
	if r["client_email"] != "gads@proj.iam.gserviceaccount.com" || !strings.Contains(fmt.Sprint(w["error"]), "item not found") {
		t.Fatalf("%v", s)
	}
}
