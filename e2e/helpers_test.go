// Package e2e drives the real binary as a subprocess: argv parsing, exit
// codes, stdout and stderr separation and the embedded manifest, exactly as
// a user gets them. Everything else in the repo tests in-process.
package e2e

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "googleads")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, "github.com/memohnsen/gads-cli/cmd/googleads").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// run executes the binary with exactly env and returns stdout, stderr and the exit code.
func run(t *testing.T, bin string, env []string, stdin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

// scrubbedEnv drops every GOOGLE_ADS_* variable and points every per-user
// directory into a temp dir, so a developer's real key or config can never
// turn these tests green or red.
func scrubbedEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	home := t.TempDir()
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOOGLE_ADS_") {
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+home, "USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"), "XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		"XDG_STATE_HOME="+filepath.Join(home, "state"),
		"AppData="+filepath.Join(home, "appdata"), "LocalAppData="+filepath.Join(home, "localappdata"))
	return append(env, extra...)
}

var (
	keyOnce sync.Once
	keyRaw  []byte
)

// writeKeyFile writes a throwaway service-account key and returns its path.
func writeKeyFile(t *testing.T) string {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		der, _ := x509.MarshalPKCS8PrivateKey(k)
		keyRaw, _ = json.Marshal(map[string]string{
			"type": "service_account", "project_id": "proj", "private_key_id": "kid-1",
			"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
			"client_email": "gads@proj.iam.gserviceaccount.com",
		})
	})
	p := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(p, keyRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Fake user-OAuth secrets; SENTINEL marks them for the leak checks.
const (
	e2eClientID     = "123456789012-e2e.apps.googleusercontent.com"
	e2eClientSecret = "cs-SENTINEL-e2e-secret"
	e2eRefreshToken = "rt-SENTINEL-e2e-refresh"
	e2eCode         = "ac-SENTINEL-e2e-code"
)

var versionSeg = regexp.MustCompile(`^/v[0-9]+/`)

// fakeGoogle is one server for the token endpoint and the API.
type fakeGoogle struct {
	*httptest.Server
	mutates  atomic.Int32
	mu       sync.Mutex
	lastBody string
	devToken atomic.Bool // a developer-token header ever arrived
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	g := &fakeGoogle{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		b, _ := io.ReadAll(r.Body)
		if r.Header.Get("developer-token") != "" {
			g.devToken.Store(true)
		}
		switch {
		case r.URL.Path == "/token":
			form, _ := url.ParseQuery(string(b))
			switch form.Get("grant_type") {
			case "authorization_code": // auth login
				if form.Get("code") != e2eCode || form.Get("client_secret") != e2eClientSecret || form.Get("code_verifier") == "" {
					w.WriteHeader(400)
					w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				fmt.Fprintf(w, `{"access_token":"smoke-token","expires_in":3600,"token_type":"Bearer","refresh_token":%q,"scope":"https://www.googleapis.com/auth/adwords"}`, e2eRefreshToken)
			case "refresh_token": // a user OAuth credential
				if form.Get("refresh_token") != e2eRefreshToken || form.Get("client_secret") != e2eClientSecret {
					w.WriteHeader(400)
					w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				w.Write([]byte(`{"access_token":"smoke-token","expires_in":3600,"token_type":"Bearer"}`))
			default: // a service-account assertion
				w.Write([]byte(`{"access_token":"smoke-token","expires_in":3600,"token_type":"Bearer"}`))
			}
			return
		case r.Header.Get("Authorization") != "Bearer smoke-token":
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"code":401,"message":"unauthenticated","status":"UNAUTHENTICATED"}}`))
			return
		}
		// Routes ignore the API version segment, so the smoke test follows
		// whatever version the embedded manifest targets.
		switch versionSeg.ReplaceAllString(r.URL.Path, "/") {
		case "/customers:listAccessibleCustomers":
			w.Write([]byte(`{"resourceNames":["customers/1234567890"]}`))
		case "/customers/1234567890/googleAds:search":
			w.Write([]byte(`{"results":[{"customer":{"resourceName":"customers/1234567890","id":"1234567890"}}]}`))
		case "/customers/1234567890/googleAds:mutate":
			g.mutates.Add(1)
			g.mu.Lock()
			g.lastBody = string(b)
			g.mu.Unlock()
			if strings.Contains(string(b), "FAIL_ME") {
				w.Header().Set("request-id", "rq-smoke")
				w.WriteHeader(400)
				w.Write([]byte(`{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.ads.googleads.v25.errors.GoogleAdsFailure","errors":[{"errorCode":{"fieldError":"REQUIRED"},"message":"The required field was not present.","location":{"fieldPathElements":[{"fieldName":"mutate_operations","index":0},{"fieldName":"campaign_operation"},{"fieldName":"create"},{"fieldName":"contains_eu_political_advertising"}]}}],"requestId":"rq-smoke"}]}}`))
				return
			}
			w.Write([]byte(`{"mutateOperationResponses":[]}`))
		default:
			w.WriteHeader(404)
			fmt.Fprintf(w, `{"error":{"code":404,"message":"no route %s","status":"NOT_FOUND"}}`, r.URL.Path)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *fakeGoogle) body() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastBody
}

// errLine parses the single JSON line a failure leaves on stderr.
func errLine(t *testing.T, stderr string) map[string]any {
	t.Helper()
	if strings.Count(strings.TrimSuffix(stderr, "\n"), "\n") != 0 {
		t.Fatalf("stderr is not one line: %q", stderr)
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(stderr), &e); err != nil {
		t.Fatalf("stderr is not JSON: %q", stderr)
	}
	return e
}
