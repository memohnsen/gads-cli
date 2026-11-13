package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/memohnsen/gads-cli/internal/api"
)

const testCode = "ac-SENTINEL-auth-code"

// syncBuffer is a bytes.Buffer safe for the flow's goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// loginFake is a token endpoint for the authorization-code grant that
// checks the PKCE verifier against the challenge the consent URL carried.
type loginFake struct {
	*userTokenServer
	mu        sync.Mutex
	challenge string
	redirect  string
	// reply overrides the success body.
	reply string
}

func newLoginFake(t *testing.T) *loginFake {
	f := &loginFake{}
	f.userTokenServer = newUserTokenServer(t, func(form map[string]string, w http.ResponseWriter) bool {
		if form["grant_type"] != "authorization_code" {
			return false
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		sum := sha256.Sum256([]byte(form["code_verifier"]))
		if form["code"] != testCode || form["client_id"] != testClientID || form["client_secret"] != testClientSecret ||
			form["redirect_uri"] != f.redirect || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant","error_description":"Bad Request"}`))
			return true
		}
		if f.reply != "" {
			w.Write([]byte(f.reply))
			return true
		}
		fmt.Fprintf(w, `{"access_token":"at-x","expires_in":3599,"token_type":"Bearer","refresh_token":%q,"scope":%q}`, testRefreshToken, Scope)
		return true
	})
	return f
}

// consent checks the consent URL and remembers its PKCE challenge and
// redirect, as Google would.
func (f *loginFake) consent(t *testing.T, raw string) (redirect, state string) {
	t.Helper()
	if strings.Contains(raw, testClientSecret) {
		t.Errorf("the consent URL carries the client secret: %s", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "accounts.google.com" || q.Get("client_id") != testClientID || q.Get("scope") != Scope ||
		q.Get("access_type") != "offline" || q.Get("prompt") != "consent" || q.Get("response_type") != "code" ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || len(q.Get("state")) < 20 ||
		!strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Errorf("consent URL %s", raw)
	}
	f.mu.Lock()
	f.challenge, f.redirect = q.Get("code_challenge"), q.Get("redirect_uri")
	f.mu.Unlock()
	return q.Get("redirect_uri"), q.Get("state")
}

func newLogin(f *loginFake, stdout, stderr io.Writer) *Login {
	return &Login{
		Client:   &OAuthClient{ClientID: testClientID, ClientSecret: testClientSecret},
		TokenURL: f.URL + "/token", AllowCustomBase: true, HTTP: f.Client(),
		Stdout: stdout, Stderr: stderr, Wait: 10 * time.Second,
	}
}

func get(t *testing.T, u string) int {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Error(err)
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestLoginBrowserFlow(t *testing.T) {
	f := newLoginFake(t)
	var stdout, stderr syncBuffer
	l := newLogin(f, &stdout, &stderr)
	l.OpenBrowser = func(consentURL string) error {
		redirect, state := f.consent(t, consentURL)
		go func() {
			// Requests that are not the answer are turned away and do not end the wait.
			if c := get(t, redirect+"?code=injected&state=wrong"); c != 400 {
				t.Errorf("wrong state: HTTP %d", c)
			}
			if c := get(t, redirect+"?state="+url.QueryEscape(state)); c != 400 {
				t.Errorf("no code: HTTP %d", c)
			}
			if c := get(t, strings.TrimSuffix(redirect, "/")+"/favicon.ico"); c != 404 {
				t.Errorf("favicon: HTTP %d", c)
			}
			if c := get(t, redirect+"?code="+testCode+"&state="+url.QueryEscape(state)+"&scope="+url.QueryEscape(Scope)); c != 200 {
				t.Errorf("answer: HTTP %d", c)
			}
		}()
		return nil
	}
	cred, err := l.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cred.Type != KindAuthorizedUser || cred.ClientID != testClientID || cred.ClientSecret != testClientSecret || cred.RefreshToken != testRefreshToken {
		t.Fatalf("%+v", cred)
	}
	if stdout.String() != "" {
		t.Fatalf("browser mode prints nothing on stdout: %q", stdout.String())
	}
	assertNoSecret(t, "stderr", stderr.String())
}

func TestLoginHeadlessPrintsOnlyTheURLAndTakesAPastedRedirect(t *testing.T) {
	f := newLoginFake(t)
	var stdout, stderr syncBuffer
	pr, pw := io.Pipe()
	defer pw.Close()
	l := newLogin(f, &stdout, &stderr)
	l.NoBrowser, l.Paste = true, pr
	l.OpenBrowser = func(string) error { t.Error("--no-browser must not open a browser"); return nil }
	go func() {
		var line string
		for i := 0; i < 500 && !strings.Contains(line, "\n"); i++ {
			time.Sleep(5 * time.Millisecond)
			line = stdout.String()
		}
		redirect, state := f.consent(t, strings.TrimSpace(line))
		// Noise first: none of it may end the wait or be echoed.
		fmt.Fprintln(pw, "")
		fmt.Fprintln(pw, "not a url "+testCode)
		fmt.Fprintln(pw, redirect+"?code="+testCode+"&state=other")
		fmt.Fprintln(pw, redirect+"?code="+testCode+"&state="+url.QueryEscape(state))
	}()
	cred, err := l.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cred.RefreshToken != testRefreshToken {
		t.Fatalf("%+v", cred)
	}
	out := stdout.String()
	if strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, "https://accounts.google.com/") {
		t.Fatalf("stdout must be the consent URL alone: %q", out)
	}
	assertNoSecret(t, "stdout", out)
	assertNoSecret(t, "stderr", stderr.String())
	if !strings.Contains(stderr.String(), "state mismatch") {
		t.Fatalf("stderr %q", stderr.String())
	}
}

func runLoginWith(t *testing.T, f *loginFake, query func(state string) string) error {
	t.Helper()
	var stdout, stderr syncBuffer
	l := newLogin(f, &stdout, &stderr)
	l.OpenBrowser = func(consentURL string) error {
		redirect, state := f.consent(t, consentURL)
		go get(t, redirect+"?"+query(url.QueryEscape(state)))
		return nil
	}
	_, err := l.Run(context.Background())
	assertNoSecret(t, "stdout", stdout.String())
	assertNoSecret(t, "stderr", stderr.String())
	if err != nil {
		assertNoSecret(t, "error", err.Error())
	}
	return err
}

func TestLoginFailures(t *testing.T) {
	asAuth := func(t *testing.T, err error, want string) {
		t.Helper()
		var e *api.Error
		if !errors.As(err, &e) || e.Kind != api.KindAuth || !strings.Contains(e.Message, want) {
			t.Fatalf("err = %v, want auth error with %q", err, want)
		}
	}
	t.Run("consent denied", func(t *testing.T) {
		err := runLoginWith(t, newLoginFake(t), func(s string) string { return "error=access_denied&state=" + s })
		asAuth(t, err, "access_denied")
	})
	t.Run("code refused", func(t *testing.T) {
		err := runLoginWith(t, newLoginFake(t), func(s string) string { return "code=ac-SENTINEL-other&state=" + s })
		asAuth(t, err, "invalid_grant")
	})
	t.Run("no refresh token", func(t *testing.T) {
		f := newLoginFake(t)
		f.reply = `{"access_token":"at-x","expires_in":3599,"token_type":"Bearer"}`
		asAuth(t, runLoginWith(t, f, func(s string) string { return "code=" + testCode + "&state=" + s }), "no refresh token")
	})
	t.Run("scope unchecked", func(t *testing.T) {
		f := newLoginFake(t)
		f.reply = `{"access_token":"at-x","expires_in":3599,"token_type":"Bearer","refresh_token":"` + testRefreshToken + `","scope":"openid"}`
		asAuth(t, runLoginWith(t, f, func(s string) string { return "code=" + testCode + "&state=" + s }), "scope was not granted")
	})
	t.Run("timeout", func(t *testing.T) {
		f := newLoginFake(t)
		var out, errb syncBuffer
		l := newLogin(f, &out, &errb)
		l.Wait = 50 * time.Millisecond
		l.OpenBrowser = func(string) error { return errors.New("no display") }
		_, err := l.Run(context.Background())
		var e *api.Error
		if !errors.As(err, &e) || e.Kind != api.KindUsage || !strings.Contains(e.Message, "no answer") {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(errb.String(), "Could not open a browser") || !strings.Contains(errb.String(), "accounts.google.com") {
			t.Fatalf("a failed browser open must print the URL on stderr: %q", errb.String())
		}
		assertNoSecret(t, "stderr", errb.String())
	})
	t.Run("token URL pinned", func(t *testing.T) {
		l := &Login{Client: &OAuthClient{ClientID: "x", ClientSecret: "y"}, TokenURL: "https://evil.example/token",
			Stdout: io.Discard, Stderr: io.Discard}
		_, err := l.Run(context.Background())
		var e *api.Error
		if !errors.As(err, &e) || e.Kind != api.KindUsage {
			t.Fatalf("err = %v", err)
		}
	})
}
