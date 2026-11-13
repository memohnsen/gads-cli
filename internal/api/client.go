// Package api is the HTTP client for the Google Ads REST API. It owns the
// host guard, the headers, the retry policy and the error shape.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/memohnsen/gads-cli/internal/manifest"
)

// Client performs API requests. BaseURL and TokenSource are required;
// Timeout, RetryBudget and MaxAttempts fall back to defaults.
type Client struct {
	BaseURL                   string
	LoginCustomerID           string // sent as login-customer-id when set
	ReadOnly, AllowCustomBase bool
	// TokenSource supplies the bearer token for each attempt; nil means no
	// credential is configured. Return a bare *Error to choose the kind.
	TokenSource func(context.Context) (string, error)
	Timeout     time.Duration // per attempt; default 60s
	RetryBudget time.Duration // total wait on 429; default 60s
	MaxAttempts int           // attempts for read-safe transient failures; default 3
	Verbose     io.Writer     // nil = silent; never receives a credential
	HTTP        *http.Client
	Sleep       func(time.Duration)
}

// StaticToken adapts a fixed token to the TokenSource seam.
func StaticToken(tok string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return tok, nil }
}

// Request is one API call.
type Request struct {
	Method string
	Path   string // relative to BaseURL: "v25/customers/1234567890/googleAds:search"
	Query  url.Values
	Body   []byte
	// Risk is the class the guard settled on; ValidateOnly says the
	// --validate-only flag was given, so Google applies nothing.
	Risk         string
	ValidateOnly bool
	MethodID     string // manifest method id, for hints; "" for raw calls
	// Headers are applied after the defaults. Authorization is the
	// client's alone and cannot be set here.
	Headers http.Header
}

// Response is a successful (2xx) response.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// readSafe reports whether the call cannot change anything: a read, or a
// validate-only call. Read-only mode allows exactly these, and only these
// are retried after a transient failure.
func (r Request) readSafe() bool { return r.Risk == manifest.RiskRead || r.ValidateOnly }

var credentialParams = []string{"access_token", "oauth_token", "key"}

func isLoopback(host string) bool { return host == "localhost" || host == "127.0.0.1" || host == "::1" }

func (c *Client) logf(format string, a ...any) {
	if c.Verbose != nil {
		fmt.Fprintf(c.Verbose, format+"\n", a...)
	}
}

// methodOverrideHeaders make some servers treat a request as another method,
// so a GET could be applied as a POST the guard never classified.
var methodOverrideHeaders = []string{"x-http-method-override", "x-http-method", "x-method-override"}

// headerIs compares header names the way proxies may: case-insensitively,
// with "_" and "-" as the same character.
func headerIs(k, name string) bool { return strings.EqualFold(strings.ReplaceAll(k, "_", "-"), name) }

// shape rejects a request that could change something the guard never saw:
// a body on a method that has none, or a non-GET call without a risk class.
func (r Request) shape() *Error {
	method := strings.ToUpper(r.Method)
	if method == "" {
		method = http.MethodGet // what net/http sends for an empty method
	}
	if r.Body != nil && (method == http.MethodGet || method == http.MethodDelete) {
		return Usagef("refusing to send a request body with %s %s: %s takes no body, so the guard never inspects one", method, r.Path, method)
	}
	if method != http.MethodGet && r.Risk == "" {
		return Usagef("refusing to send %s %s: no risk class was assigned, so the guard has not checked it", method, r.Path)
	}
	return nil
}

// guard rejects a request locally, before anything is sent.
func (c *Client) guard(r Request) *Error {
	if err := r.shape(); err != nil {
		return err
	}
	if c.TokenSource == nil {
		return Usagef("no service-account key: set GOOGLE_ADS_JSON_KEY_FILE_PATH, store one with `googleads config set service-account-key < key.json`, or run `googleads init`")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" {
		return Usagef("invalid API base URL %q", c.BaseURL)
	}
	host := u.Hostname()
	if host != "googleads.googleapis.com" && !c.AllowCustomBase {
		return Usagef("refusing to send credentials to %s; set GOOGLE_ADS_ALLOW_CUSTOM_BASE=1 if intentional", host)
	}
	if u.Scheme != "https" && !isLoopback(host) {
		return Usagef("refusing non-HTTPS API base %s", c.BaseURL)
	}
	if c.ReadOnly && !r.readSafe() {
		return Usagef("read-only mode (GOOGLE_ADS_READ_ONLY) blocks %s %s; --validate-only calls are still allowed", r.Method, r.Path)
	}
	for _, k := range credentialParams {
		if r.Query.Has(k) {
			return Usagef("query parameter %q carries a credential; the client owns authentication", k)
		}
	}
	for k := range r.Headers {
		if headerIs(k, "developer-token") {
			return Usagef("refusing to send a developer-token header: googleads authenticates with a service account only")
		}
		for _, name := range methodOverrideHeaders {
			if headerIs(k, name) {
				return Usagef("refusing to send the %s header: a method-override header could apply %s %s as another method the guard never checked", k, r.Method, r.Path)
			}
		}
	}
	return nil
}

// refuseRedirects never follows a redirect: Go keeps Authorization across a
// same-host https to http hop, and a 307/308 would replay a mutation.
func refuseRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// attempt performs one round trip; Do wraps it with the retry loop.
func (c *Client) attempt(ctx context.Context, r Request) (*Response, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tok, err := c.TokenSource(ctx)
	if err != nil {
		var apiErr *Error
		if errors.As(err, &apiErr) {
			return nil, apiErr
		}
		return nil, &Error{Kind: KindAuth, Message: "obtaining an access token: " + err.Error()}
	}
	u := strings.TrimRight(c.BaseURL, "/") + "/" + strings.TrimLeft(r.Path, "/")
	if len(r.Query) > 0 {
		u += "?" + r.Query.Encode()
	}
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u, body)
	if err != nil {
		return nil, &Error{Kind: KindUsage, Message: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.LoginCustomerID != "" {
		req.Header.Set("login-customer-id", c.LoginCustomerID)
	}
	for k, vs := range r.Headers {
		if http.CanonicalHeaderKey(k) == "Authorization" {
			continue
		}
		req.Header[http.CanonicalHeaderKey(k)] = vs
	}
	c.logf("> %s %s", r.Method, r.Path)
	httpClient := &http.Client{}
	if c.HTTP != nil {
		cp := *c.HTTP
		httpClient = &cp
	}
	httpClient.CheckRedirect = refuseRedirects
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	c.logf("< %d (%d bytes)", resp.StatusCode, len(raw))
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: raw}, nil
}

func errorDetails(resp *Response) any {
	if len(resp.Body) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(resp.Body, &v) == nil {
		return v
	}
	return string(resp.Body)
}

// errorFor builds the error for a non-2xx response.
func (c *Client) errorFor(r Request, resp *Response, f googleFailure, extra string) *Error {
	kind := kindForStatus(resp.Status)
	if f.concurrent() {
		kind = KindConflict
	}
	rid := resp.Header.Get("request-id")
	if rid == "" {
		rid = f.RequestID
	}
	return &Error{
		Kind:      kind,
		Message:   fmt.Sprintf("%s %s: HTTP %d%s%s%s", r.Method, r.Path, resp.Status, f.summary(), extra, hint(r, f, resp.Status)),
		Status:    resp.Status,
		Details:   errorDetails(resp),
		RequestID: rid,
	}
}

// Do sends a request with the retry policy: 429 is retried for every call
// within the retry budget, waiting as long as Google asks; network failures
// and 5xx are retried for read-safe calls only. A mutation is never replayed.
// The error is always *Error.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	if err := c.guard(r); err != nil {
		return nil, err
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	budget := c.RetryBudget
	if budget == 0 {
		budget = 60 * time.Second
	}
	maxAttempts := c.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 3
	}
	var waited time.Duration
	transientTries := 0
	backoff := time.Second
	// pause waits before the next attempt. A context cancelled meanwhile
	// ends the call: another attempt would only fail, and for a mutation it
	// would turn a clean refusal into an unknown outcome.
	pause := func(d time.Duration) *Error {
		sleep(d)
		if err := ctx.Err(); err != nil {
			return &Error{Kind: KindTransport, Message: fmt.Sprintf("%s %s: cancelled while waiting to retry: %v", r.Method, r.Path, err)}
		}
		return nil
	}
	for {
		resp, err := c.attempt(ctx, r)
		if err != nil {
			var apiErr *Error
			if errors.As(err, &apiErr) {
				return nil, apiErr
			}
			e := classifyTransport(err, r.readSafe())
			if e.Kind == KindTransport && r.readSafe() {
				transientTries++
				if transientTries < maxAttempts {
					if e := pause(backoff); e != nil {
						return nil, e
					}
					backoff *= 2
					continue
				}
			}
			return nil, e
		}
		switch {
		case resp.Status >= 200 && resp.Status < 300:
			return resp, nil
		case resp.Status >= 300 && resp.Status < 400:
			return nil, &Error{
				Kind: KindValidation, Status: resp.Status, Details: errorDetails(resp), RequestID: resp.Header.Get("request-id"),
				Message: fmt.Sprintf("%s %s: HTTP %d redirect to %q refused: googleads never follows redirects "+
					"(they can strip HTTPS off the token and replay a mutation); check GOOGLE_ADS_API_BASE",
					r.Method, r.Path, resp.Status, resp.Header.Get("Location")),
			}
		case resp.Status == http.StatusTooManyRequests:
			f := parseFailure(resp.Body)
			wait := backoff
			if d, ok := f.retryDelay(); ok {
				wait = max(d, backoff)
			} else if ra := retryAfter(resp.Header); ra > 0 {
				wait = max(ra, backoff)
			}
			wait += jitter()
			if waited+wait > budget {
				return nil, c.errorFor(r, resp, f, fmt.Sprintf("; Google asks to wait %s, beyond the %s retry budget",
					wait.Round(time.Second), budget))
			}
			waited += wait
			c.logf("* 429, waiting %s", wait)
			if e := pause(wait); e != nil {
				return nil, e
			}
			backoff *= 2
			continue
		case resp.Status >= 500 && r.readSafe():
			transientTries++
			if transientTries < maxAttempts {
				if e := pause(backoff); e != nil {
					return nil, e
				}
				backoff *= 2
				continue
			}
		}
		return nil, c.errorFor(r, resp, parseFailure(resp.Body), "")
	}
}
