package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/memohnsen/gads-cli/internal/auth"
	"github.com/memohnsen/gads-cli/internal/config"
)

// Fake secrets; the SENTINEL marker makes a leak easy to find. None of them
// matches scripts/check-secrets.sh.
const (
	loginClientID     = "123456789012-abcdefghijklmnop.apps.googleusercontent.com"
	loginClientSecret = "cs-SENTINEL-client-secret"
	loginRefreshToken = "rt-SENTINEL-refresh-token"
	loginCode         = "ac-SENTINEL-auth-code"
)

func noSecretIn(t *testing.T, where, s string) {
	t.Helper()
	if strings.Contains(s, "SENTINEL") {
		t.Fatalf("%s leaks a secret: %s", where, s)
	}
}

// loginTokenServer answers the authorization-code grant with a refresh
// token and the refresh grant with an access token.
func loginTokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		f := r.PostForm
		switch {
		case f.Get("grant_type") == "authorization_code" && f.Get("code") == loginCode &&
			f.Get("client_secret") == loginClientSecret && f.Get("code_verifier") != "":
			fmt.Fprintf(w, `{"access_token":"at-1","expires_in":3599,"token_type":"Bearer","refresh_token":%q,"scope":%q}`, loginRefreshToken, auth.Scope)
		case f.Get("grant_type") == "refresh_token" && f.Get("refresh_token") == loginRefreshToken:
			fmt.Fprintf(w, `{"access_token":"at-2","expires_in":3599,"token_type":"Bearer"}`)
		default:
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func clientFile(t *testing.T, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "client.json")
	raw := `{"installed":{"client_id":"` + loginClientID + `","client_secret":"` + loginClientSecret + `","redirect_uris":["http://localhost"]}}`
	if err := os.WriteFile(p, []byte(raw), mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, mode)
	return p
}

// browserAnswering plays the browser: it follows the consent URL straight
// to the loopback redirect with the code, as Google would after consent.
func browserAnswering(t *testing.T) func(string) error {
	return func(consent string) error {
		if strings.Contains(consent, "SENTINEL") {
			t.Errorf("consent URL carries a secret: %s", consent)
		}
		u, _ := url.Parse(consent)
		q := u.Query()
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?code=" + loginCode + "&state=" + url.QueryEscape(q.Get("state")))
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

// lockedBuffer is a buffer the login goroutines and the test can share.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *lockedBuffer) Len() int { return len(l.String()) }

func loginApp(t *testing.T, tokenURL string) (*app, *lockedBuffer, *lockedBuffer) {
	t.Helper()
	a, _, _ := testApp(t, nil, config.Resolved{Reader: noCredential, Writer: noCredential, TokenURL: tokenURL, AllowCustomBase: true})
	a.openBrowser = browserAnswering(t)
	out, errb := &lockedBuffer{}, &lockedBuffer{}
	a.stdout, a.stderr = out, errb
	return a, out, errb
}

// treeHasNoSecret walks dir and fails if any file other than allowed holds
// a secret.
func treeHasNoSecret(t *testing.T, dir string, allowed ...string) {
	t.Helper()
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		for _, a := range allowed {
			if p == a {
				return nil
			}
		}
		raw, _ := os.ReadFile(p)
		noSecretIn(t, p, string(raw))
		return nil
	})
}

func TestAuthLoginStoresA0600FileAndPrintsNoSecret(t *testing.T) {
	home := isolate(t)
	srv := loginTokenServer(t)
	a, out, errb := loginApp(t, srv.URL+"/token")
	if code := a.run([]string{"auth", "login", "--role", "writer", "--client-file", clientFile(t, 0o600)}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	noSecretIn(t, "stdout", out.String())
	noSecretIn(t, "stderr", errb.String())
	if !strings.Contains(out.String(), "writer credential (user OAuth, client 123456789012…) saved to") {
		t.Fatalf("stdout %q", out.String())
	}
	p, _ := config.CredentialPath(config.RoleWriter)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("credential file mode %04o", fi.Mode().Perm())
	}
	raw, _ := os.ReadFile(p)
	u, err := auth.ParseUserCredential(raw)
	if err != nil || u.RefreshToken != loginRefreshToken || u.ClientSecret != loginClientSecret || u.ClientID != loginClientID {
		t.Fatalf("stored %+v %v", u, err)
	}
	// Nothing else under the home directory (config, cache, state) holds a secret.
	treeHasNoSecret(t, home, p)
}

func TestAuthLoginHeadlessStdoutIsTheURL(t *testing.T) {
	isolate(t)
	srv := loginTokenServer(t)
	a, out, errb := loginApp(t, srv.URL+"/token")
	a.openBrowser = func(string) error { t.Error("--no-browser must not open a browser"); return nil }
	// The fake browser answers the loopback once the URL appears on stdout.
	pr, pw := io.Pipe()
	a.stdin = pr
	done := make(chan int)
	go func() {
		done <- a.run([]string{"auth", "login", "--no-browser", "--client-file", clientFile(t, 0o600)})
	}()
	var consent string
	for i := 0; consent == "" && i < 1000; i++ {
		if s := out.String(); strings.Contains(s, "\n") {
			consent = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
		}
		time.Sleep(5 * time.Millisecond)
	}
	u, _ := url.Parse(consent)
	q := u.Query()
	fmt.Fprintln(pw, q.Get("redirect_uri")+"?code="+loginCode+"&state="+url.QueryEscape(q.Get("state")))
	if code := <-done; code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	pw.Close()
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "https://accounts.google.com/") || !strings.Contains(lines[1], "reader credential") {
		t.Fatalf("stdout %q", out.String())
	}
	noSecretIn(t, "stdout", out.String())
	noSecretIn(t, "stderr", errb.String())
}

func TestAuthLoginStoresIn1PasswordAndSetsTheRef(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	isolate(t)
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"$@\" >> " + dir + "/argv\ncase \"$1 $2\" in\n" +
		"\"item get\") echo \"[ERROR] \\\"$3\\\" isn't an item in the \\\"$5\\\" vault.\" >&2; exit 1;;\n" +
		"\"item create\") cat > " + dir + "/stdin; cat " + dir + "/stdin; exit 0;;\nesac\nexit 2\n"
	os.WriteFile(filepath.Join(dir, "op"), []byte(script), 0o755)
	old := auth.OpBinary
	auth.OpBinary = filepath.Join(dir, "op")
	t.Cleanup(func() { auth.OpBinary = old })

	srv := loginTokenServer(t)
	a, out, errb := loginApp(t, srv.URL+"/token")
	ref := "op://Vault/Google Ads reader/credential"
	if code := a.run([]string{"auth", "login", "--client-file", clientFile(t, 0o600), "--op-ref", ref}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	noSecretIn(t, "stdout", out.String())
	noSecretIn(t, "stderr", errb.String())
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	noSecretIn(t, "op argv", string(argv))
	stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	if !strings.Contains(string(stdin), "SENTINEL") {
		t.Fatal("the credential must reach op on stdin")
	}
	c, err := config.Load()
	if err != nil || c.OpReaderRef != ref || c.OpWriterRef != "" {
		t.Fatalf("config %+v %v", c, err)
	}
	cfgPath, _ := config.Path()
	raw, _ := os.ReadFile(cfgPath)
	noSecretIn(t, "config file", string(raw))
	if p, _ := config.CredentialPath(config.RoleReader); fileExists(p) {
		t.Fatal("an op store must not also write a file")
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestAuthLoginRefusesLooseClientFileAndBadFlags(t *testing.T) {
	isolate(t)
	cases := [][]string{
		{"auth", "login"}, // no client
		{"auth", "login", "--client-file", clientFile(t, 0o600), "--op-ref", "op://Vault/item"},
		{"auth", "login", "--client-file", clientFile(t, 0o600), "--role", "admin"},
		{"auth", "login", "--client-file", clientFile(t, 0o600), "--port", "70000"},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, []string{"auth", "login", "--client-file", clientFile(t, 0o644)})
	}
	for _, args := range cases {
		a, out, errb := loginApp(t, config.DefaultTokenURL)
		a.openBrowser = func(string) error { t.Errorf("%v: must stop before the browser", args); return nil }
		if code := a.run(args); code != 2 || out.Len() != 0 {
			t.Errorf("%v: exit %d out %q err %s", args, code, out.String(), errb.String())
		}
		noSecretIn(t, "stderr", errb.String())
	}
}

func userCredentialFile(t *testing.T, mode os.FileMode) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"type": "authorized_user", "client_id": loginClientID,
		"client_secret": loginClientSecret, "refresh_token": loginRefreshToken})
	p := filepath.Join(t.TempDir(), "user.json")
	if err := os.WriteFile(p, raw, mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, mode)
	return p
}

func TestAuthStatusWithAUserCredential(t *testing.T) {
	isolate(t)
	p := userCredentialFile(t, 0o600)
	a, out, errb := testApp(t, nil, config.Resolved{
		Reader: config.Credential{Role: "reader", Source: config.SourceEnv, Path: p, EnvName: "GOOGLE_ADS_JSON_KEY_FILE_PATH"},
		Writer: config.Credential{Role: "writer", Source: config.SourceFile, Path: keyFile(t, 0o600)},
	})
	if code := a.run([]string{"auth", "status"}); code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	noSecretIn(t, "auth status", out.String())
	var s map[string]any
	json.Unmarshal([]byte(out.String()), &s)
	creds := s["credentials"].(map[string]any)
	r, w := creds["reader"].(map[string]any), creds["writer"].(map[string]any)
	if s["mode"] != "authorized_user" || r["type"] != "authorized_user" || r["oauth_client"] != "123456789012…" ||
		r["client_email"] != nil || w["type"] != "service_account" {
		t.Fatalf("%v", s)
	}
}

func TestUserCredentialReadableByOthersIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no permission bits")
	}
	isolate(t)
	p := userCredentialFile(t, 0o640)
	s := statusOf(t, config.Resolved{Reader: config.Credential{Role: "reader", Source: config.SourceFile, Path: p}, Writer: noCredential})
	r := s["credentials"].(map[string]any)["reader"].(map[string]any)
	if !strings.Contains(fmt.Sprint(r["error"]), "chmod 600") || s["mode"] != "none" {
		t.Fatalf("%v", s)
	}
	noSecretIn(t, "status error", fmt.Sprint(r["error"]))
}
