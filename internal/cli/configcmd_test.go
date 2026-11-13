package cli

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/memohnsen/gads-cli/internal/config"
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
	kp, _ := config.CredentialPath("reader")
	fi, err := os.Stat(kp)
	if err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) || !strings.Contains(out.String(), "gads@proj.iam.gserviceaccount.com") {
		t.Fatalf("stored %v %v, stdout %q", fi, err, out.String())
	}
	if strings.Contains(out.String(), "PRIVATE KEY") {
		t.Fatal("the key must never be echoed")
	}
	if raw, _ := os.ReadFile(func() string { p, _ := config.Path(); return p }()); strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatal("the config file must never hold a key")
	}
	a, _, errb = testApp(t, nil, config.Resolved{})
	a.stdin = bytes.NewReader(testKeyJSON(t))
	if code := a.run([]string{"config", "set", "service-account-key", "--role", "writer"}); code != 0 {
		t.Fatalf("writer: exit %d %s", code, errb.String())
	}
	if wp, _ := config.CredentialPath("writer"); func() bool { _, err := os.Stat(wp); return err != nil }() {
		t.Fatal("writer key not stored")
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
	if !strings.HasSuffix(strings.TrimSpace(out.String()), filepath.Join("gads", "config.json")) {
		t.Fatalf("%q", out.String())
	}
}

func TestConfigReadOnlyAndOpRef(t *testing.T) {
	isolate(t)
	a, _, _ := testApp(t, nil, config.Resolved{})
	a.run([]string{"config", "set", "read-only", "false"})
	a, _, _ = testApp(t, nil, config.Resolved{})
	a.run([]string{"config", "set", "op-ref", "--role", "writer", "op://Vault/w/credential"})
	c, _ := config.Load()
	if c.ReadOnly == nil || *c.ReadOnly || c.OpWriterRef != "op://Vault/w/credential" || c.OpReaderRef != "" {
		t.Fatalf("%+v", c)
	}
	a, _, _ = testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "op-ref", "not-a-ref"}); code != 2 {
		t.Fatalf("exit %d", code)
	}
	a, _, _ = testApp(t, nil, config.Resolved{})
	a.run([]string{"config", "unset", "read-only"})
	if c, _ := config.Load(); c.ReadOnly != nil {
		t.Fatalf("unset must return to the default: %+v", c)
	}
	r, _ := config.Resolve(func(string) string { return "" })
	if !r.ReadOnly || r.Writer.Source != config.SourceOp {
		t.Fatalf("%+v", r)
	}
}

func TestConfigEnvNameSuggestion(t *testing.T) {
	isolate(t)
	a, _, errb := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "GOOGLE_ADS_CUSTOMER_ID", "1"}); code != 2 || !strings.Contains(errb.String(), "config set customer-id") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

// A corrupt config file must not lock a person out of the commands that
// find and repair it; everything else names the file and stops.
func TestCorruptConfigLeavesVersionAndConfigWorking(t *testing.T) {
	isolate(t)
	p, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"budget_cap_micros":`), 0o600); err != nil {
		t.Fatal(err)
	}
	newApp := func() (*app, *bytes.Buffer, *bytes.Buffer) {
		var out, errb bytes.Buffer
		a := &app{manifest: testManifest(), stdout: &out, stderr: &errb, stdin: strings.NewReader("")}
		a.configure(func(string) string { return "" }, nil)
		return a, &out, &errb
	}
	a, out, errb := newApp()
	if code := a.run([]string{"version"}); code != 0 {
		t.Fatalf("version: exit %d %s", code, errb.String())
	}
	a, out, errb = newApp()
	if code := a.run([]string{"config", "path"}); code != 0 || strings.TrimSpace(out.String()) != p {
		t.Fatalf("config path: exit %d %q %s", code, out.String(), errb.String())
	}
	// config set and unset run and say what is wrong with the file.
	a, _, errb = newApp()
	if code := a.run([]string{"config", "set", "customer-id", "1234567890"}); code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), p) {
		t.Fatalf("config set: exit %d %s", code, errb.String())
	}
	for _, args := range [][]string{
		{"search", "--customer-id", "1234567890", "--query", "SELECT campaign.id FROM campaign"},
		{"auth", "status"},
	} {
		a, _, errb = newApp()
		if code := a.run(args); code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), p) {
			t.Fatalf("%v: exit %d %s", args, code, errb.String())
		}
	}
}
