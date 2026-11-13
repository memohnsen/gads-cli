// Package config resolves the CLI's settings from the config file and the
// environment. Environment variables win where they can only add a
// credential or tighten a limit. The settings that loosen a guardrail
// (turning read-only mode off, the budget cap, the audit log location) are
// config-file only: an agent's environment must not be able to undo a
// limit a person chose.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// DefaultAPIBase is the Google Ads API host.
const DefaultAPIBase = "https://googleads.googleapis.com"

// DefaultTokenURL is Google's OAuth 2.0 token endpoint.
const DefaultTokenURL = "https://oauth2.googleapis.com/token"

// AppDir is the directory name under the user config, cache and state dirs.
const AppDir = "gads"

// Credential roles. The reader is used for every read and, by default, every
// dry run; the writer only for calls sent with --apply. With service
// accounts the reader is the Google Ads "Read only" user and the writer the
// "Standard" user; a user OAuth credential carries the access of the person
// who signed in, whichever role it is stored for.
const (
	RoleReader = "reader"
	RoleWriter = "writer"
)

// Credential sources, in the order they are tried.
const (
	SourceOp   = "op"   // a 1Password secret reference, read with `op read`
	SourceEnv  = "env"  // a key file named by an environment variable
	SourceFile = "file" // <config dir>/credentials/<role>.json
)

// userConfigDir and userStateDir are swapped in tests.
var (
	userConfigDir = os.UserConfigDir
	userStateDir  = defaultStateDir
)

// defaultStateDir is $XDG_STATE_HOME, else ~/.local/state, on every Unix
// including macOS, so the audit log has one documented home.
func defaultStateDir() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
		return d, nil
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LocalAppData"); d != "" {
			return d, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state"), nil
}

// Config is the on-disk configuration. It never holds key material: keys
// live in 1Password, in a file named by the environment, or in 0600 files
// under CredentialsDir.
type Config struct {
	CustomerID      string `json:"customer_id,omitempty"`
	LoginCustomerID string `json:"login_customer_id,omitempty"`
	// ReadOnly is nil until a person sets it; nil means read-only.
	ReadOnly        *bool `json:"read_only,omitempty"`
	BudgetCapMicros int64 `json:"budget_cap_micros,omitempty"`
	// OpReaderRef and OpWriterRef are 1Password secret references
	// (op://<vault>/<item>/<field>) to the credential JSON: a
	// service-account key or an authorized_user credential.
	OpReaderRef string `json:"op_reader_ref,omitempty"`
	OpWriterRef string `json:"op_writer_ref,omitempty"`
	// DryRunCredential is the role dry runs (validate_only) use: reader
	// (the default) or writer.
	DryRunCredential string `json:"dry_run_credential,omitempty"`
	// AuditLog overrides the audit log path.
	AuditLog string `json:"audit_log,omitempty"`
}

// Dir returns <os.UserConfigDir()>/gads.
func Dir() (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AppDir), nil
}

// Path returns <os.UserConfigDir()>/gads/config.json.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// CredentialsDir returns <os.UserConfigDir()>/gads/credentials.
func CredentialsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials"), nil
}

// CredentialPath returns the key file of role under CredentialsDir.
func CredentialPath(role string) (string, error) {
	dir, err := CredentialsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, role+".json"), nil
}

// DefaultAuditLog returns <state dir>/gads/mutations.jsonl.
func DefaultAuditLog() (string, error) {
	dir, err := userStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AppDir, "mutations.jsonl"), nil
}

// Load reads the config file. A missing file is the zero Config; a corrupt
// file is an error, and so is one that still carries a key from upstream
// googleads, which this fork keeps out of the config file.
func Load() (Config, error) {
	p, err := Path()
	if err != nil {
		return Config{}, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) == nil {
		if _, ok := probe["service_account_key"]; ok {
			return Config{}, fmt.Errorf("%s holds a service_account_key; move the key to a 0600 file with `googleads config set service-account-key < key.json` and delete the field", p)
		}
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%s is corrupt, repair or delete it: %w", p, err)
	}
	return c, nil
}

// Save writes the config file with 0600 perms inside a 0700 directory,
// through a temp file and a rename, so a reader never sees a partial file.
func Save(c Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(p, append(raw, '\n'))
}

// WriteFileAtomic writes data to p with 0600 perms inside a 0700 directory,
// through a temp file and a rename.
func WriteFileAtomic(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	// Same directory as the target: rename is atomic only within a
	// filesystem, and CreateTemp already makes the file 0600.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// fsync before the rename: rename is atomic, not durable.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return err
	}
	// Best effort: make the rename itself durable.
	if d, err := os.Open(filepath.Dir(p)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// CheckPrivate refuses a credential file (a service-account key, a user
// OAuth credential or an OAuth client file) that group or others can read:
// it must be chmod 600 (or stricter). Windows has no such bits to check.
func CheckPrivate(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s has mode %04o; a credential file must not be readable by group or others: chmod 600 %s",
			path, fi.Mode().Perm(), path)
	}
	return nil
}

// Credential says where one role's key comes from. Nothing here is secret.
type Credential struct {
	Role   string
	Source string // SourceOp, SourceEnv, SourceFile or "" when none is configured
	OpRef  string // for SourceOp
	Path   string // for SourceEnv and SourceFile
	// EnvName names the variable that supplied OpRef or Path, if any.
	EnvName string
}

// Configured reports whether the role has a key source.
func (c Credential) Configured() bool { return c.Source != "" }

// Resolved is the effective configuration after the environment overlay.
type Resolved struct {
	Reader, Writer  Credential
	CustomerID      string // ten digits, or ""
	CustomerOrigin  string // origin of CustomerID: env or config
	LoginCustomerID string // ten digits, or ""
	ReadOnly        bool
	// ReadOnlyOrigin says why ReadOnly is what it is: default, config or env.
	ReadOnlyOrigin  string
	BudgetCapMicros int64 // 0 means no cap is configured
	// DryRunCredential is RoleReader or RoleWriter.
	DryRunCredential string
	AuditLog         string // path of the audit log
	APIBase          string
	TokenURL         string
	AllowCustomBase  bool
	// FromEnv names the GOOGLE_ADS_* variables that were set non-empty.
	// Names only, never values.
	FromEnv map[string]bool
}

// Credential returns the credential of role.
func (r Resolved) Credential(role string) Credential {
	if role == RoleWriter {
		return r.Writer
	}
	return r.Reader
}

// KeySource reports where the reader key comes from: "op", "env", "file" or "".
func (r Resolved) KeySource() string { return r.Reader.Source }

// Resolve loads the config file and overlays the environment.
func Resolve(getenv func(string) string) (Resolved, error) {
	c, err := Load()
	if err != nil {
		return Resolved{}, err
	}
	r := Resolved{
		BudgetCapMicros: c.BudgetCapMicros, APIBase: DefaultAPIBase, TokenURL: DefaultTokenURL,
		ReadOnly: true, ReadOnlyOrigin: "default", DryRunCredential: RoleReader,
	}
	if c.ReadOnly != nil {
		r.ReadOnly, r.ReadOnlyOrigin = *c.ReadOnly, "config"
	}
	env := func(name string) string {
		v := strings.TrimSpace(getenv(name))
		if v != "" {
			if r.FromEnv == nil {
				r.FromEnv = map[string]bool{}
			}
			r.FromEnv[name] = true
		}
		return v
	}
	if r.Reader, err = resolveCredential(RoleReader, c.OpReaderRef, env, "GOOGLE_ADS_OP_READER_REF", "GOOGLE_ADS_JSON_KEY_FILE_PATH"); err != nil {
		return Resolved{}, err
	}
	if r.Writer, err = resolveCredential(RoleWriter, c.OpWriterRef, env, "GOOGLE_ADS_OP_WRITER_REF", "GOOGLE_ADS_WRITE_JSON_KEY_FILE_PATH"); err != nil {
		return Resolved{}, err
	}
	switch c.DryRunCredential {
	case "", RoleReader:
	case RoleWriter:
		r.DryRunCredential = RoleWriter
	default:
		return Resolved{}, fmt.Errorf("dry_run_credential is %q; want reader or writer", c.DryRunCredential)
	}
	customer, origin := "", ""
	if c.CustomerID != "" {
		customer, origin = c.CustomerID, "config"
	}
	if v := env("GOOGLE_ADS_CUSTOMER_ID"); v != "" {
		customer, origin = v, "env"
	}
	login := c.LoginCustomerID
	if v := env("GOOGLE_ADS_LOGIN_CUSTOMER_ID"); v != "" {
		login = v
	}
	if customer != "" {
		if r.CustomerID, err = NormalizeCustomerID(customer); err != nil {
			return Resolved{}, fmt.Errorf("customer ID: %w", err)
		}
		r.CustomerOrigin = origin
	}
	if login != "" {
		if r.LoginCustomerID, err = NormalizeCustomerID(login); err != nil {
			return Resolved{}, fmt.Errorf("login customer ID: %w", err)
		}
	}
	// The environment can switch read-only mode on, never off.
	if v := env("GOOGLE_ADS_READ_ONLY"); v == "1" || strings.EqualFold(v, "true") {
		r.ReadOnly, r.ReadOnlyOrigin = true, "env"
	}
	if c.AuditLog != "" {
		if !filepath.IsAbs(c.AuditLog) {
			return Resolved{}, fmt.Errorf("audit_log %q must be an absolute path", c.AuditLog)
		}
		r.AuditLog = c.AuditLog
	} else if r.AuditLog, err = DefaultAuditLog(); err != nil {
		return Resolved{}, fmt.Errorf("audit log location: %w", err)
	}
	if v := env("GOOGLE_ADS_API_BASE"); v != "" {
		r.APIBase = strings.TrimRight(v, "/")
	}
	if v := env("GOOGLE_ADS_TOKEN_URL"); v != "" {
		r.TokenURL = v
	}
	if env("GOOGLE_ADS_ALLOW_CUSTOM_BASE") == "1" {
		r.AllowCustomBase = true
	}
	return r, nil
}

// resolveCredential picks one role's key source: a 1Password reference
// (config, else environment), else a key file named by the environment,
// else the role's file under CredentialsDir when it exists.
func resolveCredential(role, cfgOpRef string, env func(string) string, opEnv, pathEnv string) (Credential, error) {
	c := Credential{Role: role}
	opRef, opFrom := cfgOpRef, ""
	if v := env(opEnv); v != "" {
		opRef, opFrom = v, opEnv
	}
	if opRef != "" {
		if !strings.HasPrefix(opRef, "op://") {
			return c, fmt.Errorf("%s 1Password reference must start with op://, got %q", role, opRef)
		}
		c.Source, c.OpRef, c.EnvName = SourceOp, opRef, opFrom
		return c, nil
	}
	if v := env(pathEnv); v != "" {
		c.Source, c.Path, c.EnvName = SourceEnv, v, pathEnv
		return c, nil
	}
	p, err := CredentialPath(role)
	if err != nil {
		return c, nil // no config dir: no file credential
	}
	if _, err := os.Stat(p); err == nil {
		c.Source, c.Path = SourceFile, p
	}
	return c, nil
}

var tenDigits = regexp.MustCompile(`^[0-9]{10}$`)

// NormalizeCustomerID accepts a customer ID as Google Ads displays it
// ("123-456-7890") or as bare digits, with stray spaces, and returns the ten
// digits the API wants.
func NormalizeCustomerID(s string) (string, error) {
	d := strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s))
	if !tenDigits.MatchString(d) {
		return "", fmt.Errorf("want 10 digits such as 123-456-7890, got %q", s)
	}
	return d, nil
}

var moneyPattern = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]{1,6}))?$`)

// maxUnits bounds amounts far below int64 micros overflow.
const maxUnits = 1_000_000

// ParseMoneyToMicros reads an amount in currency units ("29.61") and returns
// micros (29610000). Only a dot separates decimals, at most six places; zero,
// negatives, exponents and thousands separators are refused.
func ParseMoneyToMicros(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, ",") {
		return 0, fmt.Errorf("use a dot as the decimal separator and no thousands separator (29.61, not 29,61), got %q", s)
	}
	m := moneyPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("want an amount such as 30 or 29.61 (at most six decimal places), got %q", s)
	}
	units, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || units > maxUnits || (units == maxUnits && strings.Trim(m[2], "0") != "") {
		return 0, fmt.Errorf("amount %q is too large (at most %d)", s, maxUnits)
	}
	frac, _ := strconv.ParseInt(m[2]+strings.Repeat("0", 6-len(m[2])), 10, 64)
	micros := units*1_000_000 + frac
	if micros == 0 {
		return 0, errors.New("amount must be greater than 0")
	}
	return micros, nil
}

// FormatMicros renders micros as currency units without trailing zeros:
// 29610000 becomes "29.61", 30000000 becomes "30".
func FormatMicros(micros int64) string {
	units, frac := micros/1_000_000, micros%1_000_000
	if frac == 0 {
		return strconv.FormatInt(units, 10)
	}
	return strings.TrimRight(fmt.Sprintf("%d.%06d", units, frac), "0")
}
