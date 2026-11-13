package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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
	"time"

	"github.com/memohnsen/gads-cli/internal/api"
)

var (
	keyOnce sync.Once
	testRSA *rsa.PrivateKey
)

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testRSA = k
	})
	return testRSA
}

// keyJSON is a service-account key file whose own token_uri points at an
// attacker: the client must ignore it.
func keyJSON(t *testing.T, keyID string) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(rsaKey(t))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "proj", "private_key_id": keyID,
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "gads@proj.iam.gserviceaccount.com", "client_id": "1",
		"token_uri": "https://attacker.example/token",
	})
	return raw
}

type tokenServer struct {
	*httptest.Server
	calls  atomic.Int32
	mu     sync.Mutex
	claims map[string]any
	kid    string
}

func newTokenServer(t *testing.T, refuse bool) *tokenServer {
	t.Helper()
	ts := &tokenServer{}
	pub := &rsaKey(t).PublicKey
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := ts.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		r.ParseForm()
		parts := strings.Split(r.Form.Get("assertion"), ".")
		if refuse || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || len(parts) != 3 {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`))
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) != nil {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant","error_description":"bad signature"}`))
			return
		}
		hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
		body, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var h map[string]any
		json.Unmarshal(hdr, &h)
		ts.mu.Lock()
		ts.kid, _ = h["kid"].(string)
		ts.claims = nil
		json.Unmarshal(body, &ts.claims)
		ts.mu.Unlock()
		fmt.Fprintf(w, `{"access_token":"at-%d","expires_in":3600,"token_type":"Bearer"}`, n)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func newSource(t *testing.T, ts *tokenServer, keyID, cacheDir string) *Source {
	t.Helper()
	k, err := ParseKey(keyJSON(t, keyID))
	if err != nil {
		t.Fatal(err)
	}
	return &Source{Key: k, TokenURL: ts.URL + "/token", AllowCustomBase: true, HTTP: ts.Client(),
		CacheDir: func() (string, error) { return cacheDir, nil }}
}

func TestTokenSendsAValidAssertion(t *testing.T) {
	ts := newTokenServer(t, false)
	s := newSource(t, ts, "kid-1", t.TempDir())
	tok, err := s.Token(context.Background())
	if err != nil || tok != "at-1" {
		t.Fatalf("tok %q err %v", tok, err)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	c := ts.claims
	if c["iss"] != "gads@proj.iam.gserviceaccount.com" || c["scope"] != Scope || c["aud"] != ts.URL+"/token" {
		t.Fatalf("claims %v", c)
	}
	if exp, iat := c["exp"].(float64), c["iat"].(float64); exp-iat != 3600 {
		t.Fatalf("lifetime %v", exp-iat)
	}
	if _, ok := c["sub"]; ok {
		t.Fatal("no subject: the service account acts as itself")
	}
	if ts.kid != "kid-1" {
		t.Fatalf("kid %q", ts.kid)
	}
}

func TestTokenIsCachedAndKeyFileTokenURIIgnored(t *testing.T) {
	ts := newTokenServer(t, false)
	s := newSource(t, ts, "kid-1", t.TempDir())
	for i := 0; i < 3; i++ {
		if tok, err := s.Token(context.Background()); err != nil || tok != "at-1" {
			t.Fatalf("call %d: %q %v", i, tok, err)
		}
	}
	if ts.calls.Load() != 1 {
		t.Fatalf("%d exchanges, want 1", ts.calls.Load())
	}
	if until := s.CachedUntil(); time.Until(until) < 55*time.Minute {
		t.Fatalf("cached until %v", until)
	}
}

func TestTokenReMintsNearExpiry(t *testing.T) {
	ts := newTokenServer(t, false)
	s := newSource(t, ts, "kid-1", t.TempDir())
	s.Token(context.Background())
	s.Now = func() time.Time { return time.Now().Add(59*time.Minute + 30*time.Second) }
	if tok, _ := s.Token(context.Background()); tok != "at-2" {
		t.Fatalf("tok %q: less than a minute left must re-mint", tok)
	}
}

func TestCacheKeyedByKeyID(t *testing.T) {
	ts := newTokenServer(t, false)
	dir := t.TempDir()
	newSource(t, ts, "kid-1", dir).Token(context.Background())
	if tok, _ := newSource(t, ts, "kid-2", dir).Token(context.Background()); tok != "at-2" {
		t.Fatalf("a rotated key must not reuse the old key's token: %q", tok)
	}
}

func TestCorruptCacheIgnoredAndPerms(t *testing.T) {
	ts := newTokenServer(t, false)
	dir := t.TempDir()
	p := filepath.Join(dir, "googleads", "token.json")
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte("{garbage"), 0o600)
	if _, err := newSource(t, ts, "kid-1", dir).Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("cache perms %v", fi.Mode().Perm())
	}
}

func TestTokenURLPinned(t *testing.T) {
	k, _ := ParseKey(keyJSON(t, "kid-1"))
	cases := map[string]Source{
		"foreign host": {Key: k, TokenURL: "https://evil.example/token"},
		"plain http":   {Key: k, TokenURL: "http://example.com/token", AllowCustomBase: true},
	}
	for name, s := range cases {
		s.CacheDir = func() (string, error) { return t.TempDir(), nil }
		_, err := s.Token(context.Background())
		var e *api.Error
		if !errors.As(err, &e) || e.Kind != api.KindUsage {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestRefusedExchangeIsAuthError(t *testing.T) {
	ts := newTokenServer(t, true)
	_, err := newSource(t, ts, "kid-1", t.TempDir()).Token(context.Background())
	var e *api.Error
	if !errors.As(err, &e) || e.Kind != api.KindAuth || !strings.Contains(e.Message, "invalid_grant") {
		t.Fatalf("err = %v", err)
	}
}

func TestTokenEndpointUnavailableIsTransport(t *testing.T) {
	cases := map[int]string{
		503: api.KindTransport,
		500: api.KindTransport,
		429: api.KindTransport,
		401: api.KindAuth,
		403: api.KindAuth,
	}
	k, _ := ParseKey(keyJSON(t, "kid-1"))
	for status, kind := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			w.Write([]byte(`{"error":"x","error_description":"y"}`))
		}))
		s := &Source{Key: k, TokenURL: srv.URL + "/token", AllowCustomBase: true, HTTP: srv.Client(),
			CacheDir: func() (string, error) { return t.TempDir(), nil }}
		_, err := s.Token(context.Background())
		srv.Close()
		var e *api.Error
		if !errors.As(err, &e) || e.Kind != kind || e.Status != status {
			t.Errorf("HTTP %d: err = %+v, want kind %s with status", status, e, kind)
		}
	}
}
