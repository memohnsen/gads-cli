package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/auth"
	"github.com/memohnsen/gads-cli/internal/config"
)

func (a *app) configCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Manage the googleads config file", RunE: groupRunE}
	set := &cobra.Command{Use: "set", Short: "Set a config value", Args: cobra.ArbitraryArgs, RunE: keyGroupRunE("set")}
	set.AddCommand(
		&cobra.Command{
			Use:   "service-account-key",
			Short: "Read a service-account key (JSON) from stdin, check it and store it (0600)",
			Long: "Read a service-account key file from stdin, check it and store it in the config file (0600).\n\n" +
				"The key never comes from the command line, which every process on the machine can see:\n" +
				"  googleads config set service-account-key < key.json\n\n" +
				"Delete the downloaded key file afterwards. GOOGLE_ADS_JSON_KEY_FILE_PATH, when set, wins.",
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				raw, err := io.ReadAll(io.LimitReader(a.stdin, 1<<20))
				if err != nil {
					return api.Usagef("reading stdin: %v", err)
				}
				raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
				if len(bytes.TrimSpace(raw)) == 0 {
					return api.Usagef("no key on stdin; usage: googleads config set service-account-key < key.json")
				}
				key, err := auth.ParseKey(raw)
				if err != nil {
					return api.Usagef("%v", err)
				}
				var compact bytes.Buffer
				if err := json.Compact(&compact, raw); err != nil {
					return api.Usagef("%v", err)
				}
				if err := a.updateConfig(func(c *config.Config) { c.ServiceAccountKey = compact.Bytes() }); err != nil {
					return err
				}
				a.warnEnv("GOOGLE_ADS_JSON_KEY_FILE_PATH")
				fmt.Fprintf(a.stdout, "service-account key saved (%s, key id %s); delete the downloaded key file now\n",
					key.ClientEmail, key.PrivateKeyID)
				return nil
			},
		},
		a.customerIDSetter("customer-id", "customer to act on by default", "GOOGLE_ADS_CUSTOMER_ID",
			func(c *config.Config, v string) { c.CustomerID = v }),
		a.customerIDSetter("login-customer-id", "manager account the access goes through", "GOOGLE_ADS_LOGIN_CUSTOMER_ID",
			func(c *config.Config, v string) { c.LoginCustomerID = v }),
		&cobra.Command{
			Use:   "read-only <true|false>",
			Short: "Persist read-only mode",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				v, err := strconv.ParseBool(args[0])
				if err != nil {
					return api.Usagef("read-only wants true or false, got %q", args[0])
				}
				if err := a.updateConfig(func(c *config.Config) { c.ReadOnly = v }); err != nil {
					return err
				}
				fmt.Fprintf(a.stdout, "read_only = %v\n", v)
				return nil
			},
		},
		&cobra.Command{
			Use:   "budget-cap <amount>",
			Short: "Set the highest daily budget googleads may ever set, in the account's currency",
			Long: "Set the budget cap: the highest daily budget amount googleads accepts in any request, in the\n" +
				"account's currency (for example 30 or 29.61, a dot as the decimal separator). --force does not\n" +
				"override it, and no environment variable or flag can change it. Without a cap no budget amount\n" +
				"can be set at all.\n\n" +
				"Google may spend up to twice the daily budget on a single day and bills at most 30.4 times the\n" +
				"daily budget in a month.",
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				micros, err := config.ParseMoneyToMicros(args[0])
				if err != nil {
					return api.Usagef("budget-cap: %v", err)
				}
				if err := a.updateConfig(func(c *config.Config) { c.BudgetCapMicros = micros }); err != nil {
					return err
				}
				fmt.Fprintf(a.stdout, "budget_cap = %s (%d micros, in the account's currency)\n", config.FormatMicros(micros), micros)
				return nil
			},
		},
	)

	unset := &cobra.Command{Use: "unset", Short: "Remove a config value", Args: cobra.ArbitraryArgs, RunE: keyGroupRunE("unset")}
	for _, k := range []struct {
		use, short, done string
		stored           func(config.Config) bool
		clear            func(*config.Config)
	}{
		{"service-account-key", "Remove the stored service-account key from this machine",
			"service-account key removed from this machine; it stays valid until you delete it in the Google Cloud Console (IAM & Admin, Service accounts, Keys)",
			func(c config.Config) bool { return len(c.ServiceAccountKey) > 0 }, func(c *config.Config) { c.ServiceAccountKey = nil }},
		{"customer-id", "Remove the default customer", "customer-id removed",
			func(c config.Config) bool { return c.CustomerID != "" }, func(c *config.Config) { c.CustomerID = "" }},
		{"login-customer-id", "Remove the login customer", "login-customer-id removed",
			func(c config.Config) bool { return c.LoginCustomerID != "" }, func(c *config.Config) { c.LoginCustomerID = "" }},
		{"read-only", "Stop persisting read-only mode", "read-only removed",
			func(c config.Config) bool { return c.ReadOnly }, func(c *config.Config) { c.ReadOnly = false }},
		{"budget-cap", "Remove the budget cap", "budget-cap removed; no budget amount can be set until a new cap is configured",
			func(c config.Config) bool { return c.BudgetCapMicros != 0 }, func(c *config.Config) { c.BudgetCapMicros = 0 }},
	} {
		unset.AddCommand(&cobra.Command{
			Use: k.use, Short: k.short, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := config.Load()
				if err != nil {
					return api.Usagef("%v", err)
				}
				if !k.stored(c) {
					fmt.Fprintf(a.stdout, "%s was not set\n", k.use)
					return nil
				}
				k.clear(&c)
				if err := config.Save(c); err != nil {
					return api.Usagef("%v", err)
				}
				fmt.Fprintln(a.stdout, k.done)
				return nil
			},
		})
	}

	path := &cobra.Command{
		Use: "path", Short: "Print the config file location", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := config.Path()
			if err != nil {
				return api.Usagef("%v", err)
			}
			fmt.Fprintln(a.stdout, p)
			return nil
		},
	}
	cmd.AddCommand(set, unset, path)
	return cmd
}

func (a *app) customerIDSetter(use, what, envName string, assign func(*config.Config, string)) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <id>",
		Short: "Save the " + what + " (e.g. 123-456-7890)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := config.NormalizeCustomerID(args[0])
			if err != nil {
				return api.Usagef("%s: %v", use, err)
			}
			if err := a.updateConfig(func(c *config.Config) { assign(c, id) }); err != nil {
				return err
			}
			a.warnEnv(envName)
			fmt.Fprintf(a.stdout, "%s = %s\n", strings.ReplaceAll(use, "-", "_"), id)
			return nil
		},
	}
}

// updateConfig loads the config file, applies edit and saves it.
func (a *app) updateConfig(edit func(*config.Config)) error {
	c, err := config.Load()
	if err != nil {
		return api.Usagef("%v", err)
	}
	edit(&c)
	if err := config.Save(c); err != nil {
		return api.Usagef("%v", err)
	}
	return nil
}

// warnEnv says on stderr when an environment variable overrides what was
// just saved.
func (a *app) warnEnv(name string) {
	if a.res.FromEnv[name] {
		fmt.Fprintf(a.stderr, "note: %s is set in this environment and wins over the config file\n", name)
	}
}

// keyGroupRunE answers `config set` or `config unset` given something that is
// not one of their keys. An environment-variable name is the expected mistake,
// so it is answered with the key it plainly means.
func keyGroupRunE(verb string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			if k := suggestConfigKey(args[0]); k != "" {
				return api.Usagef("config keys are service-account-key, customer-id, login-customer-id, read-only and budget-cap, "+
					"not environment variable names; did you mean `googleads config %s %s`?", verb, k)
			}
		}
		return groupRunE(cmd, args)
	}
}

func suggestConfigKey(given string) string {
	switch strings.TrimPrefix(strings.ToLower(given), "google_ads_") {
	case "json_key_file_path", "service_account_key", "key":
		return "service-account-key"
	case "customer_id", "customerid":
		return "customer-id"
	case "login_customer_id", "logincustomerid":
		return "login-customer-id"
	case "read_only", "readonly":
		return "read-only"
	case "budget_cap", "budgetcap", "budget_cap_micros":
		return "budget-cap"
	}
	return ""
}
