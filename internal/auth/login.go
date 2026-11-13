package auth

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/memohnsen/gads-cli/internal/api"
)

// AuthURL is Google's OAuth 2.0 authorization endpoint. It is pinned, like
// the token endpoint: the auth_uri inside a client file is ignored.
const AuthURL = "https://accounts.google.com/o/oauth2/v2/auth"

// Login runs the OAuth 2.0 installed-app flow with a loopback redirect
// (RFC 8252) and PKCE (RFC 7636): it listens on 127.0.0.1, sends the person
// to Google's consent page, takes the authorization code from the redirect
// and exchanges it for a refresh token. It never writes the client secret,
// the code or a token to Stdout or Stderr.
type Login struct {
	Client          *OAuthClient
	AuthURL         string // AuthURL unless a test overrides it
	TokenURL        string // pinned to Google unless AllowCustomBase
	AllowCustomBase bool
	HTTP            *http.Client // nil: a client with a 30s timeout
	// Port is the loopback port; 0 picks a free one. A fixed port lets a
	// person forward it over SSH to a machine with a browser.
	Port int
	// NoBrowser prints the authorization URL alone on Stdout instead of
	// opening a browser, and also accepts the redirect address pasted on
	// Paste, for a machine whose loopback the browser cannot reach.
	NoBrowser   bool
	OpenBrowser func(url string) error // nil: OpenBrowser
	Paste       io.Reader              // nil: no pasting
	Stdout      io.Writer
	Stderr      io.Writer
	Wait        time.Duration // how long to wait for the redirect; 0: 5 minutes
}

type codeResult struct {
	code string
	err  error
}

// Run performs the flow and returns the new credential.
func (l *Login) Run(ctx context.Context) (*UserCredential, error) {
	if l.Client == nil {
		return nil, api.Usagef("auth login: no OAuth client")
	}
	if err := checkTokenURL(l.TokenURL, l.AllowCustomBase); err != nil {
		return nil, err
	}
	authURL := l.AuthURL
	if authURL == "" {
		authURL = AuthURL
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(l.Port)))
	if err != nil {
		return nil, api.Usagef("auth login: cannot listen on 127.0.0.1:%d: %v", l.Port, err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	redirect := fmt.Sprintf("http://127.0.0.1:%d/", port)

	state, err := randomString()
	if err != nil {
		return nil, api.Usagef("auth login: %v", err)
	}
	verifier := oauth2.GenerateVerifier()
	cfg := &oauth2.Config{
		ClientID: l.Client.ClientID, ClientSecret: l.Client.ClientSecret, RedirectURL: redirect,
		Scopes:   []string{Scope},
		Endpoint: oauth2.Endpoint{AuthURL: authURL, TokenURL: l.TokenURL, AuthStyle: oauth2.AuthStyleInParams},
	}
	// prompt=consent makes Google issue a refresh token even when the
	// person approved this client before.
	consentURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "consent"))

	results := make(chan codeResult, 1)
	var once sync.Once
	deliver := func(r codeResult) { once.Do(func() { results <- r }) }
	srv := &http.Server{
		Handler:           callbackHandler(state, deliver),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go srv.Serve(ln)
	defer srv.Close()

	if l.NoBrowser {
		fmt.Fprintln(l.Stdout, consentURL)
		fmt.Fprintf(l.Stderr, "Open the URL above in a browser signed in as the Google Ads user and approve access.\n"+
			"Google then sends the browser to %s, which only works if that browser runs on this machine\n"+
			"or the port is forwarded (ssh -L %d:127.0.0.1:%d <this host>, with --port %d next time).\n", redirect, port, port, port)
		if l.Paste != nil {
			fmt.Fprintln(l.Stderr, "Otherwise the browser shows a page that cannot be reached: copy the whole address from its\n"+
				"address bar and paste it here, then press Enter.")
			go readPasted(l.Paste, state, deliver, l.Stderr)
		}
	} else {
		open := l.OpenBrowser
		if open == nil {
			open = OpenBrowser
		}
		if err := open(consentURL); err != nil {
			fmt.Fprintf(l.Stderr, "Could not open a browser (%v). Open this URL in a browser on this machine,\n"+
				"or run again with --no-browser:\n%s\n", err, consentURL)
		} else {
			fmt.Fprintf(l.Stderr, "A browser should open for Google sign-in; approve access as the Google Ads user.\n"+
				"If none opens, visit:\n%s\n", consentURL)
		}
	}

	wait := l.Wait
	if wait <= 0 {
		wait = 5 * time.Minute
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var res codeResult
	select {
	case res = <-results:
	case <-timer.C:
		return nil, api.Usagef("auth login: no answer from the browser within %s; run it again", wait)
	case <-ctx.Done():
		return nil, api.Usagef("auth login: cancelled")
	}
	if res.err != nil {
		return nil, res.err
	}

	hc := &http.Client{Timeout: 30 * time.Second}
	if l.HTTP != nil {
		cp := *l.HTTP
		hc = &cp
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	tok, err := cfg.Exchange(context.WithValue(ctx, oauth2.HTTPClient, hc), res.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, tokenError(err, " (Google refused the authorization code; run auth login again)")
	}
	if tok.RefreshToken == "" {
		return nil, &api.Error{Kind: api.KindAuth, Message: "Google returned no refresh token; remove this app's access at " +
			"https://myaccount.google.com/permissions and run auth login again"}
	}
	if granted, _ := tok.Extra("scope").(string); granted != "" && !strings.Contains(" "+granted+" ", " "+Scope+" ") {
		return nil, &api.Error{Kind: api.KindAuth, Message: "the Google Ads scope was not granted; run auth login again " +
			"and leave the Google Ads permission checked on the consent screen"}
	}
	return &UserCredential{Type: KindAuthorizedUser, ClientID: l.Client.ClientID,
		ClientSecret: l.Client.ClientSecret, RefreshToken: tok.RefreshToken}, nil
}

// callbackHandler answers the loopback redirect. A request whose state
// does not match is turned away without ending the wait, so another local
// process cannot cut the flow short or inject a code.
func callbackHandler(state string, deliver func(codeResult)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		code, err := codeFromQuery(r.URL.Query(), state)
		if errors.Is(err, errStateMismatch) || errors.Is(err, errNoCode) {
			http.Error(w, "googleads: this is not the redirect it is waiting for", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		msg := "googleads is authorized. You can close this tab and return to the terminal."
		if err != nil {
			msg = "Authorization did not complete: " + err.Error()
		}
		fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>googleads</title><p>%s</p>\n", html.EscapeString(msg))
		deliver(codeResult{code: code, err: err})
	})
}

var (
	errStateMismatch = errors.New("state mismatch")
	errNoCode        = errors.New("no authorization code")
)

// codeFromQuery reads the authorization response (RFC 6749 section 4.1.2).
func codeFromQuery(q url.Values, state string) (string, error) {
	if q.Get("state") != state {
		return "", errStateMismatch
	}
	if e := q.Get("error"); e != "" {
		msg := "Google did not grant access: " + e
		if d := q.Get("error_description"); d != "" {
			msg += ": " + d
		}
		return "", &api.Error{Kind: api.KindAuth, Message: msg}
	}
	code := q.Get("code")
	if code == "" {
		return "", errNoCode
	}
	return code, nil
}

// readPasted reads lines until one is the redirect address with the right
// state. A line is never echoed back: it holds the authorization code.
func readPasted(r io.Reader, state string, deliver func(codeResult), stderr io.Writer) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.RawQuery == "" {
			fmt.Fprintln(stderr, "That is not the redirect address. Paste the whole address from the browser's address bar.")
			continue
		}
		code, err := codeFromQuery(u.Query(), state)
		switch {
		case errors.Is(err, errStateMismatch):
			fmt.Fprintln(stderr, "That address belongs to another sign-in attempt (state mismatch). Use the URL printed above.")
			continue
		case errors.Is(err, errNoCode):
			fmt.Fprintln(stderr, "That address carries no authorization code. Paste the whole address from the address bar.")
			continue
		}
		deliver(codeResult{code: code, err: err})
		return
	}
}

func randomString() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// OpenBrowser opens url in the desktop's browser. The URL carries the
// client ID, a PKCE challenge and the state, none of them secret.
func OpenBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no graphical display")
		}
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
