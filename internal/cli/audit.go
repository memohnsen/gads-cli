package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/guard"
)

// auditEntry is one line of the mutation audit log: every attempt to call a
// method that is not read-class, whether it was refused, dry-run or applied.
// It carries a redacted summary of the operations and a hash of the body,
// never the body itself, and never a credential.
type auditEntry struct {
	Time            string            `json:"ts"`
	Version         string            `json:"gads_version"`
	APIVersion      string            `json:"api_version"`
	Command         string            `json:"command"`
	MethodID        string            `json:"method_id"`
	HTTPMethod      string            `json:"http_method"`
	Path            string            `json:"path"`
	CustomerID      string            `json:"customer_id,omitempty"`
	LoginCustomerID string            `json:"login_customer_id,omitempty"`
	RiskClass       string            `json:"risk_class"`
	Reasons         string            `json:"reasons,omitempty"`
	Apply           bool              `json:"apply"`
	ConfirmSpend    bool              `json:"confirm_spend"`
	ForceAlias      bool              `json:"force_alias,omitempty"`
	ValidateOnly    bool              `json:"validate_only"`
	Credential      string            `json:"credential,omitempty"` // reader or writer
	CredentialEmail string            `json:"credential_email,omitempty"`
	Operations      []guard.OpSummary `json:"operations,omitempty"`
	BudgetMicros    string            `json:"budget_micros_set,omitempty"`
	BodySHA256      string            `json:"body_sha256,omitempty"`
	// Result is refused (stopped locally, nothing sent), dry_run (sent with
	// validateOnly and accepted), applied, or error (sent and failed).
	Result        string   `json:"result"`
	ErrorKind     string   `json:"error_kind,omitempty"`
	Error         string   `json:"error,omitempty"`
	HTTPStatus    int      `json:"http_status,omitempty"`
	RequestID     string   `json:"request_id,omitempty"`
	ResourceNames []string `json:"resource_names,omitempty"`
	DurationMS    int64    `json:"duration_ms"`
}

var pathCustomer = regexp.MustCompile(`(?:^|/)customers/([0-9]{10})(?:[/:]|$)`)

// customerFromPath reads the customer a request acts on from its path.
func customerFromPath(path string) string {
	if m := pathCustomer.FindStringSubmatch(path); m != nil {
		return m[1]
	}
	return ""
}

func bodyHash(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// fail records an error on the entry. Only the message and kind are kept,
// cut to a length that keeps the log readable.
func (e *auditEntry) fail(result string, err error) {
	e.Result = result
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		e.ErrorKind, e.HTTPStatus = apiErr.Kind, apiErr.Status
		if apiErr.RequestID != "" {
			e.RequestID = apiErr.RequestID
		}
	}
	msg := err.Error()
	if len(msg) > 1000 {
		msg = msg[:1000] + "..."
	}
	e.Error = msg
}

// resourceNames collects the resourceName values of a mutate response, the
// IDs of what was created or changed, at most 200.
func resourceNames(body []byte) []string {
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		if len(out) >= 200 {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			if rn, ok := t["resourceName"].(string); ok {
				out = append(out, rn)
			}
			for k, x := range t {
				if k != "resourceName" {
					walk(x)
				}
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(v)
	return out
}

// auditLog is the open audit file. It is opened before a write leaves the
// machine, so a log that cannot be written stops the call instead of
// letting it through unrecorded.
type auditLog struct {
	path string
	f    *os.File
}

func openAudit(path string) (*auditLog, error) {
	if path == "" {
		return nil, errors.New("no audit log path is configured")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	// A log created looser by hand, or by an old umask, is tightened.
	if fi, err := f.Stat(); err == nil && runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		if err := f.Chmod(0o600); err != nil {
			f.Close()
			return nil, fmt.Errorf("%s is readable by others and cannot be made 0600: %w", path, err)
		}
	}
	return &auditLog{path: path, f: f}, nil
}

// write appends one JSON line and closes the file.
func (l *auditLog) write(e *auditEntry) error {
	defer l.f.Close()
	raw, err := jsonCompact(e)
	if err != nil {
		return err
	}
	_, err = l.f.Write(append(raw, '\n'))
	return err
}

// auditBestEffort records a refusal. A refused call sent nothing, so a log
// that cannot be opened does not change the outcome.
func (a *app) auditBestEffort(e *auditEntry) {
	if l, err := openAudit(a.res.AuditLog); err == nil {
		l.write(e)
	}
}

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }
