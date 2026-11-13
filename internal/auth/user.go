package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Credential kinds, as the "type" field of a credential file names them.
const (
	KindServiceAccount = "service_account"
	KindAuthorizedUser = "authorized_user"
)

// UserCredential is an OAuth user credential in Google's authorized_user
// format: the client that was authorized and the refresh token it got. It
// acts with the Google Ads access of the person who signed in. ClientSecret
// and RefreshToken are secrets: nothing prints them, and error messages name
// fields, never values.
type UserCredential struct {
	Type         string `json:"type"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	// Account is an optional label for the signed-in user (gcloud writes
	// one); it is shown by auth status and recorded in the audit log.
	Account string `json:"account,omitempty"`
}

// Credential is one loaded credential: exactly one of Key and User is set.
type Credential struct {
	Key  *Key
	User *UserCredential
}

// Kind is KindServiceAccount or KindAuthorizedUser.
func (c *Credential) Kind() string {
	if c.User != nil {
		return KindAuthorizedUser
	}
	return KindServiceAccount
}

// Identity is who the credential acts as, as far as the file says: the
// service-account email, or the user credential's account label ("" when
// it has none).
func (c *Credential) Identity() string {
	if c.User != nil {
		return c.User.Account
	}
	return c.Key.ClientEmail
}

// ClientIDSuffix shortens an OAuth client ID to its project-number prefix
// and a hint of the rest, which is enough to tell clients apart in
// auth status without copying the whole ID around. The client ID is not a
// secret; this is about readability.
func ClientIDSuffix(id string) string {
	id = strings.TrimSuffix(id, ".apps.googleusercontent.com")
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}

// ParseCredential validates a credential file of either kind: a
// service-account key or an authorized_user credential. An OAuth client
// file is refused with the command that turns it into a credential.
func ParseCredential(raw []byte) (*Credential, error) {
	raw = bytes.TrimPrefix(raw, utf8BOM)
	var probe struct {
		Type json.RawMessage `json:"type"`
	}
	if json.Unmarshal(raw, &probe) == nil && string(probe.Type) == `"`+KindAuthorizedUser+`"` {
		u, err := ParseUserCredential(raw)
		if err != nil {
			return nil, err
		}
		return &Credential{User: u}, nil
	}
	// Everything else, garbage included, gets the service-account checks and
	// their messages, which name the credential a file turned out to be.
	k, err := ParseKey(raw)
	if err != nil {
		return nil, err
	}
	return &Credential{Key: k}, nil
}

// ParseUserCredential validates an authorized_user credential.
func ParseUserCredential(raw []byte) (*UserCredential, error) {
	raw = bytes.TrimPrefix(raw, utf8BOM)
	var u UserCredential
	if err := json.Unmarshal(raw, &u); err != nil {
		// json errors quote types and offsets, never values.
		return nil, fmt.Errorf("not an authorized_user credential: %w", err)
	}
	if u.Type != KindAuthorizedUser {
		return nil, fmt.Errorf("credential type %q is not authorized_user", u.Type)
	}
	var missing []string
	for _, f := range []struct{ name, v string }{
		{"client_id", u.ClientID}, {"client_secret", u.ClientSecret}, {"refresh_token", u.RefreshToken},
	} {
		if strings.TrimSpace(f.v) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the authorized_user credential lacks %s", strings.Join(missing, ", "))
	}
	return &u, nil
}

// MarshalUserCredential renders u as compact authorized_user JSON, the form
// it is stored in.
func MarshalUserCredential(u *UserCredential) ([]byte, error) {
	cp := *u
	cp.Type = KindAuthorizedUser
	return json.Marshal(cp)
}

// OAuthClient is the part of an OAuth client file `auth login` uses. The
// auth_uri and token_uri in the file are ignored: the endpoints are pinned
// to Google's, as a service-account key's token_uri is.
type OAuthClient struct {
	ClientID     string
	ClientSecret string
}

// ParseClientFile validates an OAuth client file downloaded from the Cloud
// Console (APIs & Services, Credentials). Only a Desktop app ("installed")
// client can use the loopback redirect.
func ParseClientFile(raw []byte) (*OAuthClient, error) {
	raw = bytes.TrimPrefix(raw, utf8BOM)
	var f struct {
		Type      string `json:"type"`
		Installed *struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		} `json:"installed"`
		Web json.RawMessage `json:"web"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("not a JSON OAuth client file: %w", err)
	}
	switch {
	case f.Installed != nil:
	case f.Web != nil:
		return nil, errors.New(`this is a "Web application" OAuth client; auth login needs a "Desktop app" client (APIs & Services, Credentials, Create OAuth client ID, Desktop app)`)
	case f.Type == KindServiceAccount:
		return nil, errors.New("this is a service-account key, not an OAuth client file; store it with `googleads config set service-account-key`")
	case f.Type == KindAuthorizedUser:
		return nil, errors.New("this is already a user credential (authorized_user); point op-ref or GOOGLE_ADS_JSON_KEY_FILE_PATH at it instead of logging in again")
	default:
		return nil, errors.New(`not an OAuth client file: want the JSON downloaded for a "Desktop app" OAuth client, with an "installed" section`)
	}
	c := &OAuthClient{ClientID: strings.TrimSpace(f.Installed.ClientID), ClientSecret: strings.TrimSpace(f.Installed.ClientSecret)}
	if c.ClientID == "" || c.ClientSecret == "" {
		return nil, errors.New("the OAuth client file lacks installed.client_id or installed.client_secret")
	}
	return c, nil
}
