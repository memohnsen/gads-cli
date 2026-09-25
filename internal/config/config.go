// Package config resolves the CLI's settings from the config file and the
// environment. Environment variables win, except for the budget cap, which
// only the config file can set: an agent's environment must not be able to
// raise the limit a person chose.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DefaultAPIBase is the Google Ads API host.
const DefaultAPIBase = "https://googleads.googleapis.com"

// DefaultTokenURL is Google's OAuth 2.0 token endpoint.
const DefaultTokenURL = "https://oauth2.googleapis.com/token"

// userConfigDir is swapped in tests.
var userConfigDir = os.UserConfigDir

// Config is the on-disk configuration.
type Config struct {
	ServiceAccountKey json.RawMessage `json:"service_account_key,omitempty"`
	CustomerID        string          `json:"customer_id,omitempty"`
	LoginCustomerID   string          `json:"login_customer_id,omitempty"`
	ReadOnly          bool            `json:"read_only,omitempty"`
	BudgetCapMicros   int64           `json:"budget_cap_micros,omitempty"`
}

// Path returns <os.UserConfigDir()>/googleads/config.json.
func Path() (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "googleads", "config.json"), nil
}

// Load reads the config file. A missing file is the zero Config; a corrupt
// file is an error.
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
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
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
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	// Marshal, not MarshalIndent: MarshalIndent re-indents the stored key,
	// so Load would not return the bytes Save was given.
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	// Same directory as the target: rename is atomic only within a
	// filesystem, and CreateTemp already makes the file 0600.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
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
	// Best effort: make the rename itself durable. Windows cannot open a
	// directory for this, which is fine.
	if d, err := os.Open(filepath.Dir(p)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// Resolved is the effective configuration after the environment overlay.
type Resolved struct {
	// KeyPath names the key file (GOOGLE_ADS_JSON_KEY_FILE_PATH); KeyJSON is
	// the key stored in the config file. KeyPath wins.
	KeyPath         string
	KeyJSON         []byte
	CustomerID      string // ten digits, or ""
	LoginCustomerID string // ten digits, or ""
	ReadOnly        bool
	BudgetCapMicros int64 // 0 means no cap is configured
	APIBase         string
	TokenURL        string
	AllowCustomBase bool
	// FromEnv names the GOOGLE_ADS_* variables that were set non-empty.
	// Names only, never values.
	FromEnv map[string]bool
}

// KeySource reports where the key comes from: "env", "config" or "".
func (r Resolved) KeySource() string {
	switch {
	case r.KeyPath != "":
		return "env"
	case len(r.KeyJSON) > 0:
		return "config"
	}
	return ""
}

// Resolve loads the config file and overlays the environment.
func Resolve(getenv func(string) string) (Resolved, error) {
	c, err := Load()
	if err != nil {
		return Resolved{}, err
	}
	r := Resolved{
		KeyJSON: c.ServiceAccountKey, ReadOnly: c.ReadOnly, BudgetCapMicros: c.BudgetCapMicros,
		APIBase: DefaultAPIBase, TokenURL: DefaultTokenURL,
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
	r.KeyPath = env("GOOGLE_ADS_JSON_KEY_FILE_PATH")
	customer, login := c.CustomerID, c.LoginCustomerID
	if v := env("GOOGLE_ADS_CUSTOMER_ID"); v != "" {
		customer = v
	}
	if v := env("GOOGLE_ADS_LOGIN_CUSTOMER_ID"); v != "" {
		login = v
	}
	if customer != "" {
		if r.CustomerID, err = NormalizeCustomerID(customer); err != nil {
			return Resolved{}, fmt.Errorf("customer ID: %w", err)
		}
	}
	if login != "" {
		if r.LoginCustomerID, err = NormalizeCustomerID(login); err != nil {
			return Resolved{}, fmt.Errorf("login customer ID: %w", err)
		}
	}
	if v := env("GOOGLE_ADS_READ_ONLY"); v == "1" || strings.EqualFold(v, "true") {
		r.ReadOnly = true
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
