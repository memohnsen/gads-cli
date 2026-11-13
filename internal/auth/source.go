package auth

import (
	"context"
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

// Source mints and caches access tokens for one key.
type Source struct {
	Key             *Key
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
	cfg := &jwt.Config{
		Email:        s.Key.ClientEmail,
		PrivateKey:   []byte(s.Key.PrivateKey),
		PrivateKeyID: s.Key.PrivateKeyID,
		Scopes:       []string{Scope},
		TokenURL:     s.TokenURL, // never the key file's token_uri
		// Expires stays unset: jws backdates iat by 10s and then sets
		// exp = iat + 1h, Google's maximum. An explicit hour would exceed it.
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	if s.HTTP != nil {
		cp := *s.HTTP
		hc = &cp
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	tok, err := cfg.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, hc)).Token()
	if err == nil {
		return tok, nil
	}
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
			return nil, &api.Error{Kind: api.KindTransport, Status: status,
				Message: fmt.Sprintf("token exchange: the token endpoint is unavailable (HTTP %d)%s; try again later", status, reason)}
		}
		return nil, &api.Error{Kind: api.KindAuth, Status: status,
			Message: "token exchange refused" + reason + " (was the key deleted or disabled in the Cloud Console?)"}
	}
	return nil, &api.Error{Kind: api.KindTransport, Message: "token exchange: " + err.Error()}
}

func checkTokenURL(raw string, allowCustom bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return api.Usagef("invalid token URL %q", raw)
	}
	host := u.Hostname()
	if host != "oauth2.googleapis.com" && !allowCustom {
		return api.Usagef("refusing to send the signed key assertion to %s; set GOOGLE_ADS_ALLOW_CUSTOM_BASE=1 if intentional", host)
	}
	if u.Scheme != "https" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return api.Usagef("refusing non-HTTPS token URL %s", raw)
	}
	return nil
}

func (s *Source) cacheKey() string {
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
