package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/config"
)

// Source mints and caches access tokens for one credential: a
// service-account Key or a User credential, exactly one of them set.
type Source struct {
	Key             *Key
	User            *UserCredential
	TokenURL        string // pinned to Google unless AllowCustomBase
	AllowCustomBase bool
	HTTP            *http.Client           // nil: a client with a 30s timeout
	CacheDir        func() (string, error) // nil: os.UserCacheDir
	Now             func() time.Time       // nil: time.Now
}

// minValidity is how much lifetime a cached token needs left to be reused.
const minValidity = 60 * time.Second

type cacheEntry struct {
	AccessToken string    `json:"access_token"`
	Expiry      time.Time `json:"expiry"`
}

type cacheFile struct {
	Entries map[string]cacheEntry `json:"entries"`
}

// Token returns a valid access token. The error is always *api.Error:
// usage for a bad configuration, auth when Google refuses the key,
// transport when the token endpoint cannot be reached.
func (s *Source) Token(ctx context.Context) (string, error) {
	if err := checkTokenURL(s.TokenURL, s.AllowCustomBase); err != nil {
		return "", err
	}
	now := s.now()
	if e, ok := s.readCache()[s.cacheKey()]; ok && e.Expiry.Sub(now) >= minValidity {
		return e.AccessToken, nil
	}
	tok, err := s.mint(ctx)
	if err != nil {
		return "", err
	}
	s.writeCache(cacheEntry{AccessToken: tok.AccessToken, Expiry: tok.Expiry}, now)
	return tok.AccessToken, nil
}

// CachedUntil reports when the cached token for this key expires; zero when
// nothing is cached.
func (s *Source) CachedUntil() time.Time { return s.readCache()[s.cacheKey()].Expiry }

func (s *Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Source) mint(ctx context.Context) (*oauth2.Token, error) {
	hc := &http.Client{Timeout: 30 * time.Second}
	if s.HTTP != nil {
		cp := *s.HTTP
		hc = &cp
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx = context.WithValue(ctx, oauth2.HTTPClient, hc)
	var ts oauth2.TokenSource
	hint := " (was the key deleted or disabled in the Cloud Console?)"
	if s.User != nil {
		// A refresh-token grant (RFC 6749 section 6). The scope is the one
		// the person consented to at login; a refresh cannot widen it.
		cfg := &oauth2.Config{
			ClientID: s.User.ClientID, ClientSecret: s.User.ClientSecret,
			Endpoint: oauth2.Endpoint{TokenURL: s.TokenURL, AuthStyle: oauth2.AuthStyleInParams},
		}
		ts = cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: s.User.RefreshToken})
		hint = " (the refresh token was revoked or has expired; tokens from an OAuth consent screen in Testing expire after 7 days. Run `googleads auth login` again)"
	} else {
		cfg := &jwt.Config{
			Email:        s.Key.ClientEmail,
			PrivateKey:   []byte(s.Key.PrivateKey),
			PrivateKeyID: s.Key.PrivateKeyID,
			Scopes:       []string{Scope},
			TokenURL:     s.TokenURL, // never the key file's token_uri
			// Expires stays unset: jws backdates iat by 10s and then sets
			// exp = iat + 1h, Google's maximum. An explicit hour would exceed it.
		}
		ts = cfg.TokenSource(ctx)
	}
	tok, err := ts.Token()
	if err == nil {
		return tok, nil
	}
	return nil, tokenError(err, hint)
}

// tokenError turns a failed token exchange into an *api.Error. It quotes
// only the RFC 6749 error code and description from the response, never the
// request, so no credential can reach the message.
func tokenError(err error, refusedHint string) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		code, desc := re.ErrorCode, re.ErrorDescription
		if code == "" {
			// jwt.Config leaves the RFC 6749 fields unparsed; read them from the body.
			var body struct {
				Error            string `json:"error"`
				ErrorDescription string `json:"error_description"`
			}
			if json.Unmarshal(re.Body, &body) == nil {
				code, desc = body.Error, body.ErrorDescription
			}
		}
		reason := ""
		if code != "" {
			reason += ": " + code
		}
		if desc != "" {
			reason += ": " + desc
		}
		status := 0
		if re.Response != nil {
			status = re.Response.StatusCode
		}
		// 429 and 5xx say nothing about the key: the endpoint is busy or down.
		if status == http.StatusTooManyRequests || status >= 500 {
			return &api.Error{Kind: api.KindTransport, Status: status,
				Message: fmt.Sprintf("token exchange: the token endpoint is unavailable (HTTP %d)%s; try again later", status, reason)}
		}
		return &api.Error{Kind: api.KindAuth, Status: status, Message: "token exchange refused" + reason + refusedHint}
	}
	return &api.Error{Kind: api.KindTransport, Message: "token exchange: " + err.Error()}
}

func checkTokenURL(raw string, allowCustom bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return api.Usagef("invalid token URL %q", raw)
	}
	host := u.Hostname()
	if host != "oauth2.googleapis.com" && !allowCustom {
		return api.Usagef("refusing to send a credential to %s; set GOOGLE_ADS_ALLOW_CUSTOM_BASE=1 if intentional", host)
	}
	if u.Scheme != "https" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return api.Usagef("refusing non-HTTPS token URL %s", raw)
	}
	return nil
}

// cacheKey names the credential in the token cache. A user credential is
// named by its client ID and a hash of its refresh token, so a new login
// gets a new entry and the cache file never holds the refresh token.
func (s *Source) cacheKey() string {
	if s.User != nil {
		sum := sha256.Sum256([]byte(s.User.RefreshToken))
		return "user|" + s.User.ClientID + "|" + hex.EncodeToString(sum[:8]) + "|" + s.TokenURL
	}
	return s.Key.ClientEmail + "|" + s.Key.PrivateKeyID + "|" + s.TokenURL
}

func (s *Source) cachePath() (string, error) {
	dirFn := s.CacheDir
	if dirFn == nil {
		dirFn = os.UserCacheDir
	}
	dir, err := dirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, config.AppDir, "token.json"), nil
}

// readCache returns the cached entries; a missing or corrupt cache is empty.
func (s *Source) readCache() map[string]cacheEntry {
	p, err := s.cachePath()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var f cacheFile
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	return f.Entries
}

// writeCache stores e for this key and drops expired entries. Best effort:
// a cache that cannot be written only costs the next call an exchange.
func (s *Source) writeCache(e cacheEntry, now time.Time) {
	if e.Expiry.IsZero() {
		return
	}
	p, err := s.cachePath()
	if err != nil {
		return
	}
	entries := map[string]cacheEntry{}
	for k, v := range s.readCache() {
		if v.Expiry.After(now) {
			entries[k] = v
		}
	}
	entries[s.cacheKey()] = e
	raw, err := json.Marshal(cacheFile{Entries: entries})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".token-*.json")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() != nil {
		return
	}
	os.Rename(tmp.Name(), p)
}
