package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/config"
)

// authStatus is `googleads auth status`: what is configured, never a secret.
type authStatus struct {
	Mode             string   `json:"mode"` // service_account or none
	Source           string   `json:"source,omitempty"`
	ClientEmail      string   `json:"client_email,omitempty"`
	PrivateKeyID     string   `json:"private_key_id,omitempty"`
	ProjectID        string   `json:"project_id,omitempty"`
	TokenCachedUntil string   `json:"token_cached_until,omitempty"`
	CustomerID       string   `json:"customer_id,omitempty"`
	LoginCustomerID  string   `json:"login_customer_id,omitempty"`
	BudgetCap        string   `json:"budget_cap,omitempty"`
	ReadOnly         bool     `json:"read_only"`
	Missing          []string `json:"missing,omitempty"`
	Hint             string   `json:"hint,omitempty"`
}

func (a *app) authCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Inspect the configured credential", RunE: groupRunE}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "status",
			Short: "Show the configured credential as one line of JSON (offline; never prints a key or token)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				// No HTML escaping, like every JSON output: the hints quote
				// commands such as `config set service-account-key < key.json`.
				enc := json.NewEncoder(a.stdout)
				enc.SetEscapeHTML(false)
				return enc.Encode(a.authStatus())
			},
		},
		&cobra.Command{
			Use:   "token",
			Short: "Print a valid access token to stdout (a credential: never log it)",
			Long: "Print a valid access token to stdout, for piping into another tool such as curl.\n" +
				"This writes a credential to stdout: do not log it or paste it anywhere.",
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if a.client.TokenSource == nil {
					return api.Usagef("no service-account key: set GOOGLE_ADS_JSON_KEY_FILE_PATH, store one with `googleads config set service-account-key < key.json`, or run `googleads init`")
				}
				tok, err := a.client.TokenSource(cmd.Context())
				if err != nil {
					return err
				}
				fmt.Fprintln(a.stdout, tok)
				return nil
			},
		},
	)
	return cmd
}

func (a *app) authStatus() authStatus {
	s := authStatus{Mode: "none", Source: a.res.KeySource(), CustomerID: a.res.CustomerID,
		LoginCustomerID: a.res.LoginCustomerID, ReadOnly: a.res.ReadOnly}
	if a.res.BudgetCapMicros > 0 {
		s.BudgetCap = config.FormatMicros(a.res.BudgetCapMicros)
	}
	var hints []string
	if s.Source == "" {
		s.Missing = append(s.Missing, "service_account_key")
		hints = append(hints, "run `googleads init`, or store a key with `googleads config set service-account-key < key.json`")
	} else {
		s.Mode = "service_account"
		if key, err := a.loadKey(); err != nil {
			s.Missing = append(s.Missing, "valid_service_account_key")
			hints = append(hints, err.Error())
		} else {
			s.ClientEmail, s.PrivateKeyID, s.ProjectID = key.ClientEmail, key.PrivateKeyID, key.ProjectID
			if until := a.newSource(key).CachedUntil(); until.After(time.Now()) {
				s.TokenCachedUntil = until.UTC().Format(time.RFC3339)
			}
		}
	}
	if s.CustomerID == "" {
		s.Missing = append(s.Missing, "customer_id")
		hints = append(hints, "set a customer with `googleads config set customer-id <id>` or pass --customer-id")
	}
	if s.BudgetCap == "" {
		hints = append(hints, "no budget cap: budget amounts cannot be set until one is configured with `googleads config set budget-cap <amount>`")
	}
	s.Hint = strings.Join(hints, "; ")
	return s
}
