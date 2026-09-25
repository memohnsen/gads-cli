package cli

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

// isolate points every per-user directory at a temp dir, so the tests never
// read or write the developer's real config file or token cache.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for k, v := range map[string]string{
		"HOME": home, "USERPROFILE": home,
		"XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_CACHE_HOME": filepath.Join(home, "cache"),
		"AppData": filepath.Join(home, "appdata"), "LocalAppData": filepath.Join(home, "localappdata"),
	} {
		t.Setenv(k, v)
	}
	return home
}

var (
	cliKeyOnce sync.Once
	cliKey     []byte
)

func testKeyJSON(t *testing.T) []byte {
	t.Helper()
	cliKeyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		der, _ := x509.MarshalPKCS8PrivateKey(k)
		cliKey, _ = json.Marshal(map[string]string{
			"type": "service_account", "project_id": "proj", "private_key_id": "kid-1",
			"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
			"client_email": "gads@proj.iam.gserviceaccount.com",
		})
	})
	return cliKey
}

func TestConfigSetServiceAccountKeyFromStdin(t *testing.T) {
	isolate(t)
	a, out, errb := testApp(t, nil, config.Resolved{})
	a.stdin = bytes.NewReader(testKeyJSON(t))
	if code := a.run([]string{"config", "set", "service-account-key"}); code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	c, _ := config.Load()
	if len(c.ServiceAccountKey) == 0 || !strings.Contains(out.String(), "gads@proj.iam.gserviceaccount.com") {
		t.Fatalf("stored %d bytes, stdout %q", len(c.ServiceAccountKey), out.String())
	}
	a, _, _ = testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "service-account-key", "secret-from-argv"}); code != 2 {
		t.Fatalf("a key from argv must be refused: exit %d", code)
	}
}

func TestConfigSetServiceAccountKeyRejectsOAuthClient(t *testing.T) {
	isolate(t)
	a, _, errb := testApp(t, nil, config.Resolved{})
	a.stdin = strings.NewReader(`{"installed":{"client_id":"x"}}`)
	if code := a.run([]string{"config", "set", "service-account-key"}); code != 2 || !strings.Contains(errb.String(), "OAuth client file") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestConfigSetCustomerIDs(t *testing.T) {
	isolate(t)
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "customer-id", "123-456-7890"}); code != 0 || !strings.Contains(out.String(), "customer_id = 1234567890") {
		t.Fatalf("exit %d %q", code, out.String())
	}
	a, _, _ = testApp(t, nil, config.Resolved{})
	a.run([]string{"config", "set", "login-customer-id", "111 222 3333"})
	if c, _ := config.Load(); c.CustomerID != "1234567890" || c.LoginCustomerID != "1112223333" {
		t.Fatalf("%+v", c)
	}
	a, _, _ = testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "customer-id", "12345"}); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestConfigSetBudgetCapComma(t *testing.T) {
	isolate(t)
	a, _, errb := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "budget-cap", "29,61"}); code != 2 || !strings.Contains(errb.String(), "dot") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "budget-cap", "29.61"}); code != 0 || !strings.Contains(out.String(), "29.61 (29610000 micros") {
		t.Fatalf("exit %d %q", code, out.String())
	}
	if c, _ := config.Load(); c.BudgetCapMicros != 29610000 {
		t.Fatalf("cap %d", c.BudgetCapMicros)
	}
}

func TestConfigUnsetAndPath(t *testing.T) {
	isolate(t)
	config.Save(config.Config{BudgetCapMicros: 1})
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "unset", "budget-cap"}); code != 0 || !strings.Contains(out.String(), "removed") {
		t.Fatalf("exit %d %q", code, out.String())
	}
	a, out, _ = testApp(t, nil, config.Resolved{})
	if a.run([]string{"config", "unset", "budget-cap"}); !strings.Contains(out.String(), "was not set") {
		t.Fatalf("%q", out.String())
	}
	a, out, _ = testApp(t, nil, config.Resolved{})
	a.run([]string{"config", "path"})
	if !strings.HasSuffix(strings.TrimSpace(out.String()), filepath.Join("googleads", "config.json")) {
		t.Fatalf("%q", out.String())
	}
}

func TestConfigEnvNameSuggestion(t *testing.T) {
	isolate(t)
	a, _, errb := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "GOOGLE_ADS_CUSTOMER_ID", "1"}); code != 2 || !strings.Contains(errb.String(), "config set customer-id") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}
