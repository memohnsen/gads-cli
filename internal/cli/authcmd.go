package cli

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/auth"
	"github.com/memohnsen/gads-cli/internal/config"
)

// credentialStatus describes one role's credential without revealing it.
type credentialStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"` // op, env or file
	OpRef      string `json:"op_ref,omitempty"`
	Path       string `json:"path,omitempty"`
	// Type is service_account or authorized_user (user OAuth).
	Type        string `json:"type,omitempty"`
	ClientEmail string `json:"client_email,omitempty"`
	// OAuthClient and Account describe a user credential: the start of its
	// OAuth client ID and the account label, if the file has one.
	OAuthClient      string `json:"oauth_client,omitempty"`
	Account          string `json:"account,omitempty"`
	ProjectID        string `json:"project_id,omitempty"`
	KeyIDSuffix      string `json:"key_id_suffix,omitempty"`
	TokenCachedUntil string `json:"token_cached_until,omitempty"`
	Error            string `json:"error,omitempty"`
}

// authStatus is `googleads auth status`: what is configured, never a secret.
type authStatus struct {
	Mode             string                      `json:"mode"` // the reader's credential type: service_account, authorized_user or none
	APIVersion       string                      `json:"api_version"`
	CustomerID       string                      `json:"customer_id,omitempty"`
	CustomerOrigin   string                      `json:"customer_origin,omitempty"`
	LoginCustomerID  string                      `json:"login_customer_id,omitempty"`
	BudgetCap        string                      `json:"budget_cap,omitempty"`
	ReadOnly         bool                        `json:"read_only"`
	ReadOnlyOrigin   string                      `json:"read_only_origin"`
	DryRunCredential string                      `json:"dry_run_credential"`
	AuditLog         string                      `json:"audit_log"`
	Credentials      map[string]credentialStatus `json:"credentials"`
	Missing          []string                    `json:"missing,omitempty"`
	Hint             string                      `json:"hint,omitempty"`
}

func (a *app) authCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Inspect the configured credentials, or create a user OAuth credential", RunE: groupRunE}
	cmd.AddCommand(a.loginCommand())
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show the configured credentials and limits as one line of JSON (never prints a key or token)",
		Long: "Show the configured credentials and limits as one line of JSON. It loads each configured\n" +
			"credential (a 1Password reference is read with `op read`) to report its type and, for a service\n" +
			"account, its client email, project and the last characters of its key id, or, for a user OAuth\n" +
			"credential, the start of its OAuth client ID. It never prints a private key, a client secret, a\n" +
			"refresh token or an access token, and it makes no Google Ads call.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// No HTML escaping, like every JSON output: the hints quote
			// commands such as `config set service-account-key < key.json`.
			enc := json.NewEncoder(a.stdout)
			enc.SetEscapeHTML(false)
			return enc.Encode(a.authStatus(cmd))
		},
	})
	return cmd
}

func keyIDSuffix(id string) string {
	if len(id) <= 6 {
		return id
	}
	return "…" + id[len(id)-6:]
}

func (a *app) authStatus(cmd *cobra.Command) authStatus {
	s := authStatus{
		Mode: "none", APIVersion: a.manifest.APIVersion, CustomerID: a.res.CustomerID, CustomerOrigin: a.res.CustomerOrigin,
		LoginCustomerID: a.res.LoginCustomerID, ReadOnly: a.res.ReadOnly, ReadOnlyOrigin: a.res.ReadOnlyOrigin,
		DryRunCredential: a.res.DryRunCredential, AuditLog: a.res.AuditLog, Credentials: map[string]credentialStatus{},
	}
	if a.res.BudgetCapMicros > 0 {
		s.BudgetCap = config.FormatMicros(a.res.BudgetCapMicros)
	}
	var hints []string
	for _, role := range []string{config.RoleReader, config.RoleWriter} {
		c := a.res.Credential(role)
		cs := credentialStatus{Configured: c.Configured(), Source: c.Source, OpRef: c.OpRef, Path: c.Path}
		if !c.Configured() {
			s.Missing = append(s.Missing, role+"_key")
			if role == config.RoleReader {
				hints = append(hints, "no reader key: "+credentialHint(role))
			} else {
				hints = append(hints, "no writer key, so --apply is unavailable: "+credentialHint(role))
			}
		} else if cred, err := a.loadCredential(cmd.Context(), role); err != nil {
			cs.Error = err.Error()
			s.Missing = append(s.Missing, "valid_"+role+"_key")
		} else {
			cs.Type = cred.Kind()
			if key := cred.Key; key != nil {
				cs.ClientEmail, cs.ProjectID, cs.KeyIDSuffix = key.ClientEmail, key.ProjectID, keyIDSuffix(key.PrivateKeyID)
			} else {
				cs.OAuthClient, cs.Account = auth.ClientIDSuffix(cred.User.ClientID), cred.User.Account
			}
			if until := a.newSource(cred).CachedUntil(); until.After(time.Now()) {
				cs.TokenCachedUntil = until.UTC().Format(time.RFC3339)
			}
			if role == config.RoleReader {
				s.Mode = cred.Kind()
			}
		}
		s.Credentials[role] = cs
	}
	if s.CustomerID == "" {
		s.Missing = append(s.Missing, "customer_id")
		hints = append(hints, "set a customer with `googleads config set customer-id <id>` or pass --customer-id")
	}
	if s.BudgetCap == "" {
		hints = append(hints, "no budget cap: budget amounts cannot be set until a person configures one with `googleads config set budget-cap <amount>`")
	}
	if s.ReadOnly {
		hints = append(hints, "read-only mode ("+s.ReadOnlyOrigin+"): every --apply is refused; dry runs work")
	}
	s.Hint = strings.Join(hints, "; ")
	return s
}
