package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/config"
)

// Fake secrets with a marker that is easy to search for. They match none of
// the patterns scripts/check-secrets.sh looks for.
const (
	testClientID     = "123456789012-abcdefghijklmnop.apps.googleusercontent.com"
	testClientSecret = "cs-SENTINEL-client-secret"
	testRefreshToken = "rt-SENTINEL-refresh-token"
)

func userJSON(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	m := map[string]any{"type": "authorized_user", "client_id": testClientID,
		"client_secret": testClientSecret, "refresh_token": testRefreshToken}
	if mutate != nil {
		mutate(m)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertNoSecret(t *testing.T, where, s string) {
	t.Helper()
	for _, sec := range []string{testClientSecret, testRefreshToken, "SENTINEL"} {
		if strings.Contains(s, sec) {
			t.Fatalf("%s leaks a secret (%q): %s", where, sec, s)
		}
	}
}

func TestParseCredentialBothKinds(t *testing.T) {
	c, err := ParseCredential(userJSON(t, func(m map[string]any) { m["account"] = "ads@example.com" }))
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind() != KindAuthorizedUser || c.User.RefreshToken != testRefreshToken || c.Identity() != "ads@example.com" || c.Key != nil {
		t.Fatalf("%+v", c)
	}
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, userJSON(t, nil)...)
	if _, err := ParseCredential(withBOM); err != nil {
		t.Fatalf("BOM: %v", err)
	}
	c, err = ParseCredential(keyJSON(t, "kid-1"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind() != KindServiceAccount || c.Identity() != "gads@proj.iam.gserviceaccount.com" || c.User != nil {
		t.Fatalf("%+v", c)
	}
}

func TestParseCredentialRefusesIncompleteUserCredentials(t *testing.T) {
	cases := map[string]struct {
		raw  []byte
		want string
	}{
		"no refresh token": {userJSON(t, func(m map[string]any) { delete(m, "refresh_token") }), "refresh_token"},
		"no client secret": {userJSON(t, func(m map[string]any) { m["client_secret"] = " " }), "client_secret"},
		"no client id":     {userJSON(t, func(m map[string]any) { delete(m, "client_id") }), "client_id"},
		"wrong field type": {userJSON(t, func(m map[string]any) { m["client_id"] = 5 }), "authorized_user"},
		"client file":      {[]byte(`{"installed":{"client_id":"x","client_secret":"` + testClientSecret + `"}}`), "auth login"},
		"garbage":          {[]byte("not json " + testRefreshToken), "JSON"},
		"external account": {[]byte(`{"type":"external_account"}`), "external_account"},
	}
	for name, c := range cases {
		_, err := ParseCredential(c.raw)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
			continue
		}
		assertNoSecret(t, name+" error", err.Error())
	}
}

func TestParseKeyStillRefusesUserCredentials(t *testing.T) {
	_, err := ParseKey(userJSON(t, nil))
	if err == nil || !strings.Contains(err.Error(), "authorized_user") {
		t.Fatalf("err = %v", err)
	}
	assertNoSecret(t, "error", err.Error())
}

func TestParseClientFile(t *testing.T) {
	good := `{"installed":{"client_id":"` + testClientID + `","client_secret":"` + testClientSecret +
		`","auth_uri":"https://evil.example/auth","token_uri":"https://evil.example/token","redirect_uris":["http://localhost"]}}`
	c, err := ParseClientFile([]byte(good))
	if err != nil || c.ClientID != testClientID || c.ClientSecret != testClientSecret {
		t.Fatalf("%+v %v", c, err)
	}
	cases := map[string]string{
		`{"web":{"client_id":"x","client_secret":"y"}}`:             "Desktop app",
		`{"installed":{"client_id":"x"}}`:                           "client_secret",
		`{"type":"service_account"}`:                                "service-account key",
		string(userJSON(t, nil)):                                    "authorized_user",
		`{"other":1}`:                                               "installed",
		`{"installed":{"client_secret":"` + testClientSecret + `"}`: "JSON",
	}
	for raw, want := range cases {
		_, err := ParseClientFile([]byte(raw))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", raw, err, want)
			continue
		}
		assertNoSecret(t, "client file error", err.Error())
	}
}

func TestClientIDSuffix(t *testing.T) {
	if got := ClientIDSuffix(testClientID); got != "123456789012…" {
		t.Fatalf("%q", got)
	}
	if got := ClientIDSuffix("short"); got != "short" {
		t.Fatalf("%q", got)
	}
}

func TestMarshalUserCredentialRoundTrip(t *testing.T) {
	raw, err := MarshalUserCredential(&UserCredential{ClientID: testClientID, ClientSecret: testClientSecret, RefreshToken: testRefreshToken})
	if err != nil {
		t.Fatal(err)
	}
	u, err := ParseUserCredential(raw)
	if err != nil || u.Type != KindAuthorizedUser || u.RefreshToken != testRefreshToken {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestParseOpRef(t *testing.T) {
	r, err := ParseOpRef("op://Vault/Google Ads reader/credential")
	if err != nil || r.Vault != "Vault" || r.Item != "Google Ads reader" || r.Field != "credential" || r.String() != "op://Vault/Google Ads reader/credential" {
		t.Fatalf("%+v %v", r, err)
	}
	for _, bad := range []string{"Vault/item/field", "op://Vault/item", "op://Vault/item/section/field", "op://Vault//field", "op://V/i/f?attribute=otp"} {
		if _, err := ParseOpRef(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestRedact(t *testing.T) {
	cred := string(userJSON(t, nil))
	got := Redact("op said "+cred+" and "+testRefreshToken+` and "`+strings.ReplaceAll(cred, `"`, `\"`)+`"`, cred, testRefreshToken)
	assertNoSecret(t, "redacted", got)
}

// userTokenServer answers refresh-token and authorization-code grants.
type userTokenServer struct {
	*httptest.Server
	calls atomic.Int32
	mu    sync.Mutex
	forms []map[string]string
}

func newUserTokenServer(t *testing.T, handle func(form map[string]string, w http.ResponseWriter) bool) *userTokenServer {
	t.Helper()
	ts := &userTokenServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := ts.calls.Add(1)
		r.ParseForm()
		form := map[string]string{}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		ts.mu.Lock()
		ts.forms = append(ts.forms, form)
		ts.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if handle != nil && handle(form, w) {
			return
		}
		if form["grant_type"] != "refresh_token" || form["refresh_token"] != testRefreshToken ||
			form["client_id"] != testClientID || form["client_secret"] != testClientSecret {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
			return
		}
		fmt.Fprintf(w, `{"access_token":"user-at-%d","expires_in":3599,"token_type":"Bearer","scope":"%s"}`, n, Scope)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func userSource(t *testing.T, ts *userTokenServer, raw []byte, cacheDir string) *Source {
	t.Helper()
	u, err := ParseUserCredential(raw)
	if err != nil {
		t.Fatal(err)
	}
	return &Source{User: u, TokenURL: ts.URL + "/token", AllowCustomBase: true, HTTP: ts.Client(),
		CacheDir: func() (string, error) { return cacheDir, nil }}
}

func TestUserCredentialRefreshAndCache(t *testing.T) {
	ts := newUserTokenServer(t, nil)
	dir := t.TempDir()
	s := userSource(t, ts, userJSON(t, nil), dir)
	for i := 0; i < 3; i++ {
		if tok, err := s.Token(context.Background()); err != nil || tok != "user-at-1" {
			t.Fatalf("call %d: %q %v", i, tok, err)
		}
	}
	if ts.calls.Load() != 1 {
		t.Fatalf("%d refreshes, want 1", ts.calls.Load())
	}
	raw, err := os.ReadFile(filepath.Join(dir, config.AppDir, "token.json"))
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecret(t, "token cache", string(raw))
	// A new login (another refresh token) must not reuse the old token.
	other := userSource(t, ts, userJSON(t, func(m map[string]any) { m["refresh_token"] = "rt-other" }), dir)
	if _, err := other.Token(context.Background()); err == nil {
		t.Fatal("the fake refuses rt-other, so a refresh must have been attempted and failed")
	}
}

func TestUserCredentialRefusedIsAuthErrorWithLoginHint(t *testing.T) {
	ts := newUserTokenServer(t, nil)
	s := userSource(t, ts, userJSON(t, func(m map[string]any) { m["client_secret"] = "cs-SENTINEL-wrong" }), t.TempDir())
	_, err := s.Token(context.Background())
	var e *api.Error
	if !errors.As(err, &e) || e.Kind != api.KindAuth || !strings.Contains(e.Message, "invalid_grant") ||
		!strings.Contains(e.Message, "auth login") {
		t.Fatalf("err = %v", err)
	}
	assertNoSecret(t, "refresh error", e.Error())
}

func TestUserCredentialTokenURLPinned(t *testing.T) {
	u, _ := ParseUserCredential(userJSON(t, nil))
	s := &Source{User: u, TokenURL: "https://evil.example/token", CacheDir: func() (string, error) { return t.TempDir(), nil }}
	_, err := s.Token(context.Background())
	var e *api.Error
	if !errors.As(err, &e) || e.Kind != api.KindUsage {
		t.Fatalf("err = %v", err)
	}
}

func writePrivate(t *testing.T, raw []byte, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cred.json")
	if err := os.WriteFile(p, raw, mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, mode)
	return p
}

func TestLoadCredentialFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no permission bits")
	}
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660} {
		p := writePrivate(t, userJSON(t, nil), mode)
		_, err := LoadCredential(context.Background(), config.Credential{Role: "reader", Source: config.SourceFile, Path: p})
		if err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("mode %04o: err = %v", mode, err)
			continue
		}
		assertNoSecret(t, "permission error", err.Error())
	}
	for _, mode := range []os.FileMode{0o600, 0o400} {
		p := writePrivate(t, userJSON(t, nil), mode)
		c, err := LoadCredential(context.Background(), config.Credential{Role: "writer", Source: config.SourceEnv, Path: p})
		if err != nil || c.Kind() != KindAuthorizedUser {
			t.Errorf("mode %04o: %v", mode, err)
		}
	}
	// LoadKey, the service-account-only loader, still refuses it.
	p := writePrivate(t, userJSON(t, nil), 0o600)
	if _, err := LoadKey(context.Background(), config.Credential{Role: "reader", Source: config.SourceFile, Path: p}); err == nil {
		t.Fatal("LoadKey must refuse a user credential")
	}
}

func TestLoadClientPermissionsAndSources(t *testing.T) {
	client := []byte(`{"installed":{"client_id":"` + testClientID + `","client_secret":"` + testClientSecret + `"}}`)
	if _, err := LoadClient(context.Background(), "", ""); err == nil || !strings.Contains(err.Error(), "--client-file") {
		t.Fatalf("err = %v", err)
	}
	if _, err := LoadClient(context.Background(), "", "vault/item/field"); err == nil {
		t.Fatal("a reference without op:// must be refused")
	}
	if runtime.GOOS != "windows" {
		_, err := LoadClient(context.Background(), writePrivate(t, client, 0o644), "")
		if err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Fatalf("err = %v", err)
		}
		assertNoSecret(t, "client permission error", err.Error())
	}
	c, err := LoadClient(context.Background(), writePrivate(t, client, 0o600), "")
	if err != nil || c.ClientSecret != testClientSecret {
		t.Fatalf("%v", err)
	}
}
