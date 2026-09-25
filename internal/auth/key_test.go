package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseKeyAcceptsServiceAccount(t *testing.T) {
	k, err := ParseKey(keyJSON(t, "kid-1"))
	if err != nil {
		t.Fatal(err)
	}
	if k.ClientEmail != "gads@proj.iam.gserviceaccount.com" || k.PrivateKeyID != "kid-1" || k.ProjectID != "proj" {
		t.Fatalf("%+v", k)
	}
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, keyJSON(t, "kid-1")...)
	if _, err := ParseKey(withBOM); err != nil {
		t.Fatalf("a UTF-8 BOM must be accepted: %v", err)
	}
}

func TestParseKeyNamesOtherCredentials(t *testing.T) {
	var full map[string]any
	json.Unmarshal(keyJSON(t, "kid-1"), &full)
	noEmail := map[string]any{}
	for k, v := range full {
		noEmail[k] = v
	}
	delete(noEmail, "client_email")
	badPEM := map[string]any{}
	for k, v := range full {
		badPEM[k] = v
	}
	badPEM["private_key"] = "not a key"
	cases := map[string]struct {
		v    any
		want string
	}{
		"installed app":   {map[string]any{"installed": map[string]any{"client_id": "x"}}, "OAuth client file"},
		"web app":         {map[string]any{"web": map[string]any{"client_id": "x"}}, "OAuth client file"},
		"user credential": {map[string]any{"type": "authorized_user", "refresh_token": "x"}, "user credential"},
		"no type":         {map[string]any{"client_email": "x"}, `no "type"`},
		"other type":      {map[string]any{"type": "external_account"}, "external_account"},
		"missing email":   {noEmail, "client_email"},
		"bad pem":         {badPEM, "not PEM"},
	}
	for name, c := range cases {
		raw, _ := json.Marshal(c.v)
		if _, err := ParseKey(raw); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	if _, err := ParseKey([]byte("not json")); err == nil {
		t.Error("garbage must fail")
	}
}
