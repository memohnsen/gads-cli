package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old, oldState := userConfigDir, userStateDir
	userConfigDir = func() (string, error) { return dir, nil }
	userStateDir = func() (string, error) { return filepath.Join(dir, "state"), nil }
	t.Cleanup(func() { userConfigDir, userStateDir = old, oldState })
	return dir
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPathAndMissingFile(t *testing.T) {
	dir := setTestDir(t)
	p, err := Path()
	if err != nil || p != filepath.Join(dir, "gads", "config.json") {
		t.Fatalf("path %q %v", p, err)
	}
	c, err := Load()
	if err != nil || c.CustomerID != "" || c.ReadOnly != nil {
		t.Fatalf("missing file must load as zero: %+v %v", c, err)
	}
	if k, _ := CredentialPath(RoleWriter); k != filepath.Join(dir, "gads", "credentials", "writer.json") {
		t.Fatalf("credential path %q", k)
	}
}

func TestSaveLoadRoundTripAndPerms(t *testing.T) {
	setTestDir(t)
	off := false
	in := Config{CustomerID: "1234567890", LoginCustomerID: "1112223333", ReadOnly: &off, BudgetCapMicros: 29610000,
		OpReaderRef: "op://Vault/r/credential", OpWriterRef: "op://Vault/w/credential", DryRunCredential: RoleWriter,
		AuditLog: "/var/log/gads.jsonl"}
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil || out.CustomerID != in.CustomerID || out.LoginCustomerID != in.LoginCustomerID || out.ReadOnly == nil ||
		*out.ReadOnly || out.BudgetCapMicros != in.BudgetCapMicros || out.OpReaderRef != in.OpReaderRef ||
		out.OpWriterRef != in.OpWriterRef || out.DryRunCredential != RoleWriter || out.AuditLog != in.AuditLog {
		t.Fatalf("round trip %+v %v", out, err)
	}
	p, _ := Path()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("perms %v, want 0600", fi.Mode().Perm())
	}
}

func TestLoadRefusesAKeyInTheConfigFile(t *testing.T) {
	setTestDir(t)
	p, _ := Path()
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(`{"service_account_key":{"type":"service_account"}}`), 0o600)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "service_account_key") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no permission bits")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "k.json")
	os.WriteFile(p, []byte("{}"), 0o644)
	if err := CheckPrivate(p); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("0644 must be refused: %v", err)
	}
	os.Chmod(p, 0o600)
	if err := CheckPrivate(p); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivate(dir); err == nil {
		t.Fatal("a directory is not a key file")
	}
}

func TestLoadCorrupt(t *testing.T) {
	setTestDir(t)
	p, _ := Path()
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte("{nope"), 0o600)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("err = %v", err)
	}
}

func TestNormalizeCustomerID(t *testing.T) {
	ok := map[string]string{
		"1234567890":       "1234567890",
		"123-456-7890":     "1234567890",
		" 123 456 7890 ":   "1234567890",
		"123 - 456 - 7890": "1234567890",
	}
	for in, want := range ok {
		if got, err := NormalizeCustomerID(in); err != nil || got != want {
			t.Errorf("NormalizeCustomerID(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "123-456-789", "12345678901", "abc-def-ghij", "123_456_7890"} {
		if _, err := NormalizeCustomerID(bad); err == nil {
			t.Errorf("NormalizeCustomerID(%q) must fail", bad)
		}
	}
}

func TestParseMoneyToMicros(t *testing.T) {
	ok := map[string]int64{"30": 30000000, "29.61": 29610000, "0.000001": 1, " 5.5 ": 5500000, "1000000": 1000000000000}
	for in, want := range ok {
		if got, err := ParseMoneyToMicros(in); err != nil || got != want {
			t.Errorf("ParseMoneyToMicros(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	bad := map[string]string{
		"29,61":      "dot", // the Swiss and German decimal comma must never be misread
		"1,000":      "dot",
		"0":          "greater than 0",
		"0.0":        "greater than 0",
		"-5":         "want an amount",
		"1e3":        "want an amount",
		"29.1234567": "want an amount",
		"":           "want an amount",
		"1000001":    "too large",
	}
	for in, want := range bad {
		if _, err := ParseMoneyToMicros(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseMoneyToMicros(%q) err = %v, want it to contain %q", in, err, want)
		}
	}
}

func TestFormatMicros(t *testing.T) {
	cases := map[int64]string{30000000: "30", 29610000: "29.61", 1: "0.000001", 1500000: "1.5", 0: "0"}
	for in, want := range cases {
		if got := FormatMicros(in); got != want {
			t.Errorf("FormatMicros(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveDefaults(t *testing.T) {
	dir := setTestDir(t)
	r, err := Resolve(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.APIBase != DefaultAPIBase || r.TokenURL != DefaultTokenURL || r.KeySource() != "" || r.Writer.Configured() || r.FromEnv != nil {
		t.Fatalf("%+v", r)
	}
	if !r.ReadOnly || r.ReadOnlyOrigin != "default" {
		t.Fatalf("read-only must be on by default: %+v", r)
	}
	if r.CustomerID != "" || r.CustomerOrigin != "" {
		t.Fatalf("no customer is built in: %q (%s)", r.CustomerID, r.CustomerOrigin)
	}
	if r.DryRunCredential != RoleReader || r.AuditLog != filepath.Join(dir, "state", "gads", "mutations.jsonl") {
		t.Fatalf("%+v", r)
	}
}

func TestResolveFileValues(t *testing.T) {
	setTestDir(t)
	off := false
	Save(Config{CustomerID: "1234567890", BudgetCapMicros: 30000000, ReadOnly: &off})
	kp, _ := CredentialPath(RoleReader)
	os.MkdirAll(filepath.Dir(kp), 0o700)
	os.WriteFile(kp, []byte("{}"), 0o600)
	r, err := Resolve(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.KeySource() != SourceFile || r.Reader.Path != kp || r.Writer.Configured() || r.CustomerID != "1234567890" ||
		r.CustomerOrigin != "config" || r.BudgetCapMicros != 30000000 || r.ReadOnly || r.ReadOnlyOrigin != "config" {
		t.Fatalf("%+v", r)
	}
}

func TestResolveCredentialOrder(t *testing.T) {
	setTestDir(t)
	kp, _ := CredentialPath(RoleWriter)
	os.MkdirAll(filepath.Dir(kp), 0o700)
	os.WriteFile(kp, []byte("{}"), 0o600)
	// file only
	r, _ := Resolve(env(nil))
	if r.Writer.Source != SourceFile {
		t.Fatalf("file: %+v", r.Writer)
	}
	// env path beats the file
	r, _ = Resolve(env(map[string]string{"GOOGLE_ADS_WRITE_JSON_KEY_FILE_PATH": "/k/w.json"}))
	if r.Writer.Source != SourceEnv || r.Writer.Path != "/k/w.json" || r.Writer.EnvName != "GOOGLE_ADS_WRITE_JSON_KEY_FILE_PATH" {
		t.Fatalf("env: %+v", r.Writer)
	}
	// 1Password beats both
	Save(Config{OpWriterRef: "op://Vault/w/credential"})
	r, _ = Resolve(env(map[string]string{"GOOGLE_ADS_WRITE_JSON_KEY_FILE_PATH": "/k/w.json"}))
	if r.Writer.Source != SourceOp || r.Writer.OpRef != "op://Vault/w/credential" {
		t.Fatalf("op: %+v", r.Writer)
	}
	if r.Reader.Configured() {
		t.Fatalf("the writer's sources must not configure the reader: %+v", r.Reader)
	}
	if _, err := Resolve(env(map[string]string{"GOOGLE_ADS_OP_READER_REF": "Vault/r"})); err == nil {
		t.Fatal("an op reference without op:// must be refused")
	}
}

func TestResolveNormalizesEnvCustomerID(t *testing.T) {
	setTestDir(t)
	Save(Config{CustomerID: "1111111111"})
	r, err := Resolve(env(map[string]string{
		"GOOGLE_ADS_CUSTOMER_ID": "123-456-7890", "GOOGLE_ADS_LOGIN_CUSTOMER_ID": " 999 888 7777 ",
		"GOOGLE_ADS_JSON_KEY_FILE_PATH": "/keys/k.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if r.CustomerID != "1234567890" || r.CustomerOrigin != "env" || r.LoginCustomerID != "9998887777" {
		t.Fatalf("customer %q login %q", r.CustomerID, r.LoginCustomerID)
	}
	if r.Reader.Path != "/keys/k.json" || r.KeySource() != SourceEnv {
		t.Fatalf("key %+v", r)
	}
	if !r.FromEnv["GOOGLE_ADS_CUSTOMER_ID"] || !r.FromEnv["GOOGLE_ADS_JSON_KEY_FILE_PATH"] {
		t.Fatalf("FromEnv %v", r.FromEnv)
	}
}

func TestResolveRejectsBadCustomerID(t *testing.T) {
	setTestDir(t)
	if _, err := Resolve(env(map[string]string{"GOOGLE_ADS_CUSTOMER_ID": "12345"})); err == nil ||
		!strings.Contains(err.Error(), "customer ID") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveReadOnlyValues(t *testing.T) {
	setTestDir(t)
	off := false
	Save(Config{ReadOnly: &off})
	// The environment can switch read-only mode on, never off.
	for v, want := range map[string]bool{"1": true, "true": true, "TRUE": true, "0": false, "no": false, "false": false} {
		r, _ := Resolve(env(map[string]string{"GOOGLE_ADS_READ_ONLY": v}))
		if r.ReadOnly != want {
			t.Errorf("GOOGLE_ADS_READ_ONLY=%s over config false: %v", v, r.ReadOnly)
		}
	}
	Save(Config{})
	for _, v := range []string{"0", "false", "no"} {
		if r, _ := Resolve(env(map[string]string{"GOOGLE_ADS_READ_ONLY": v})); !r.ReadOnly {
			t.Errorf("GOOGLE_ADS_READ_ONLY=%s must not switch the default read-only mode off", v)
		}
	}
}

func TestAuditLogMustBeAbsolute(t *testing.T) {
	setTestDir(t)
	Save(Config{AuditLog: "relative/log.jsonl"})
	if _, err := Resolve(env(nil)); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err = %v", err)
	}
}

func TestBudgetCapHasNoEnvironmentOverride(t *testing.T) {
	setTestDir(t)
	Save(Config{BudgetCapMicros: 30000000})
	r, _ := Resolve(env(map[string]string{"GOOGLE_ADS_BUDGET_CAP": "999", "GOOGLE_ADS_BUDGET_CAP_MICROS": "999"}))
	if r.BudgetCapMicros != 30000000 {
		t.Fatalf("cap %d: the environment must not move the cap", r.BudgetCapMicros)
	}
}

func TestResolveBaseOverrides(t *testing.T) {
	setTestDir(t)
	r, _ := Resolve(env(map[string]string{
		"GOOGLE_ADS_API_BASE": "http://127.0.0.1:9/", "GOOGLE_ADS_TOKEN_URL": "http://127.0.0.1:9/token",
		"GOOGLE_ADS_ALLOW_CUSTOM_BASE": "1",
	}))
	if r.APIBase != "http://127.0.0.1:9" || r.TokenURL != "http://127.0.0.1:9/token" || !r.AllowCustomBase {
		t.Fatalf("%+v", r)
	}
}
