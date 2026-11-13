// Package auth turns a credential into access tokens for the Google Ads API
// and caches them on disk until shortly before they expire. A credential is
// either a Google Cloud service-account key (a signed JWT exchanged at
// Google's token endpoint, RFC 7523) or an OAuth user credential in Google's
// authorized_user format (a refresh token, RFC 6749 section 6), which
// `googleads auth login` creates with the installed-app loopback flow.
package auth

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
)

// Scope is the OAuth scope of the Google Ads API.
const Scope = "https://www.googleapis.com/auth/adwords"

// Key is the part of a service-account key file the CLI uses.
type Key struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ParseKey validates a service-account key file. When the file is some
// other Google credential it says which, because "invalid key" sends people
// to the wrong page of the Cloud Console.
func ParseKey(raw []byte) (*Key, error) {
	raw = bytes.TrimPrefix(raw, utf8BOM)
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("not a JSON key file: %w", err)
	}
	for _, kind := range []string{"installed", "web"} {
		if _, ok := probe[kind]; ok {
			if kind == "installed" {
				return nil, errors.New("this is an OAuth client file (installed app), not a credential: turn it into a user credential with `googleads auth login --client-file <file>`")
			}
			return nil, fmt.Errorf("this is an OAuth client file (%s app), not a service-account key: create a key under IAM & Admin, Service accounts, Keys, or a Desktop app client for `googleads auth login`", kind)
		}
	}
	var k Key
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, fmt.Errorf("not a service-account key: %w", err)
	}
	switch k.Type {
	case "service_account":
	case "authorized_user":
		return nil, errors.New("this is a user credential (authorized_user), not a service-account key: use it through `googleads config set op-ref` or GOOGLE_ADS_JSON_KEY_FILE_PATH, or create one with `googleads auth login`")
	case "":
		return nil, errors.New(`not a service-account key: the file has no "type" field`)
	default:
		return nil, fmt.Errorf("credential type %q is not a service-account key", k.Type)
	}
	if k.ClientEmail == "" || k.PrivateKeyID == "" || k.PrivateKey == "" {
		return nil, errors.New("the service-account key lacks client_email, private_key_id or private_key")
	}
	if _, err := parseRSA(k.PrivateKey); err != nil {
		return nil, fmt.Errorf("service-account key: %w", err)
	}
	return &k, nil
}

func parseRSA(pemKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, errors.New("private_key is not PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private_key is not an RSA key")
		}
		return rk, nil
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}
