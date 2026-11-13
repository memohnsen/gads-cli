package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/auth"
	"github.com/memohnsen/gads-cli/internal/config"
)

func roleFlag(cmd *cobra.Command) {
	cmd.Flags().String("role", config.RoleReader, "credential role: reader (Google Ads Read only user) or writer (Standard user, used only with --apply)")
}

func roleOf(cmd *cobra.Command) (string, error) {
	switch r := flagString(cmd, "role"); r {
	case config.RoleReader, config.RoleWriter:
		return r, nil
	default:
		return "", api.Usagef("--role wants reader or writer, got %q", r)
	}
}

func (a *app) configCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Manage the googleads config file and stored keys", RunE: groupRunE}
	set := &cobra.Command{Use: "set", Short: "Set a config value", Args: cobra.ArbitraryArgs, RunE: keyGroupRunE("set")}

	setKey := &cobra.Command{
		Use:   "service-account-key",
		Short: "Read a service-account key (JSON) from stdin, check it and store it in a 0600 file",
		Long: "Read a service-account key file from stdin, check it and store it as\n" +
			"<config dir>/gads/credentials/<role>.json with mode 0600 (directory 0700).\n\n" +
			"The key never comes from the command line, which every process on the machine can see:\n" +
			"  googleads config set service-account-key < reader-key.json\n" +
			"  googleads config set service-account-key --role writer < writer-key.json\n\n" +
			"Delete the downloaded key file afterwards. A 1Password reference (config set op-ref) or\n" +
			"GOOGLE_ADS_JSON_KEY_FILE_PATH / GOOGLE_ADS_WRITE_JSON_KEY_FILE_PATH, when set, win.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			role, err := roleOf(cmd)
			if err != nil {
				return err
			}
			raw, err := io.ReadAll(io.LimitReader(a.stdin, 1<<20))
			if err != nil {
				return api.Usagef("reading stdin: %v", err)
			}
			raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
			if len(bytes.TrimSpace(raw)) == 0 {
				return api.Usagef("no key on stdin; usage: googleads config set service-account-key [--role writer] < key.json")
			}
			key, err := auth.ParseKey(raw)
			if err != nil {
				return api.Usagef("%v", err)
			}
			p, err := config.CredentialPath(role)
			if err != nil {
				return api.Usagef("%v", err)
			}
			if err := config.WriteFileAtomic(p, raw); err != nil {
				return api.Usagef("%v", err)
			}
			if c := a.res.Credential(role); c.Source == config.SourceOp || c.Source == config.SourceEnv {
				fmt.Fprintf(a.stderr, "note: the %s key from %s wins over the stored file\n", role, c.Source)
			}
			fmt.Fprintf(a.stdout, "%s key saved to %s (%s, key id %s); delete the downloaded key file now\n",
				role, p, key.ClientEmail, keyIDSuffix(key.PrivateKeyID))
			return nil
		},
	}
	roleFlag(setKey)

	setOp := &cobra.Command{
		Use:   "op-ref <op://vault/item/field>",
		Short: "Read a role's key from 1Password with `op read` (tried before any key file)",
		Long: "Store a 1Password secret reference to a service-account key. googleads runs\n" +
			"`op read --no-newline <ref>` with stdin closed when it needs the key, so the key never sits\n" +
			"on disk. The 1Password CLI must be signed in (desktop-app integration or\n" +
			"OP_SERVICE_ACCOUNT_TOKEN). Example:\n" +
			"  googleads config set op-ref op://<vault>/<reader item>/<field>\n" +
			"  googleads config set op-ref --role writer op://<vault>/<writer item>/<field>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, err := roleOf(cmd)
			if err != nil {
				return err
			}
			ref := strings.TrimSpace(args[0])
			if !strings.HasPrefix(ref, "op://") {
				return api.Usagef("op-ref wants a secret reference such as op://<vault>/<item>/<field>, got %q", ref)
			}
			if err := a.updateConfig(func(c *config.Config) {
				if role == config.RoleWriter {
					c.OpWriterRef = ref
				} else {
					c.OpReaderRef = ref
				}
			}); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "op_%s_ref = %s\n", role, ref)
			return nil
		},
	}
	roleFlag(setOp)

	set.AddCommand(setKey, setOp,
		a.customerIDSetter("customer-id", "customer to act on by default", "GOOGLE_ADS_CUSTOMER_ID",
			func(c *config.Config, v string) { c.CustomerID = v }),
		a.customerIDSetter("login-customer-id", "manager account the access goes through", "GOOGLE_ADS_LOGIN_CUSTOMER_ID",
			func(c *config.Config, v string) { c.LoginCustomerID = v }),
		&cobra.Command{
			Use:   "read-only <true|false>",
			Short: "Turn read-only mode on or off (on by default; only a person turns it off)",
			Long: "Read-only mode refuses every --apply; reads and dry runs still work. It is on until the config\n" +
				"file says otherwise, and GOOGLE_ADS_READ_ONLY=1 can switch it on but never off.",
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				v, err := strconv.ParseBool(args[0])
				if err != nil {
					return api.Usagef("read-only wants true or false, got %q", args[0])
				}
				if err := a.updateConfig(func(c *config.Config) { c.ReadOnly = &v }); err != nil {
					return err
				}
				fmt.Fprintf(a.stdout, "read_only = %v\n", v)
				return nil
			},
		},
		&cobra.Command{
			Use:   "budget-cap <amount>",
			Short: "Set the highest total daily budget googleads may leave the account with, in its currency",
			Long: "Set the budget cap, in the account's currency (for example 30 or 29.61, a dot as the decimal\n" +
				"separator). googleads refuses any request that sets a daily budget above it, any one call\n" +
				"whose budget amounts sum above it, and any apply after which the account's enabled daily\n" +
				"budgets would total more than it. No flag overrides it and no environment variable changes\n" +
				"it. Without a cap no budget amount can be set at all.\n\n" +
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
		&cobra.Command{
			Use:   "audit-log <absolute path>",
			Short: "Move the mutation audit log (default: ~/.local/state/gads/mutations.jsonl)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				p := expandHome(args[0])
				if !filepath.IsAbs(p) {
					return api.Usagef("audit-log wants an absolute path, got %q", args[0])
				}
				if err := a.updateConfig(func(c *config.Config) { c.AuditLog = p }); err != nil {
					return err
				}
				fmt.Fprintf(a.stdout, "audit_log = %s\n", p)
				return nil
			},
		},
		&cobra.Command{
			Use:   "dry-run-credential <reader|writer>",
			Short: "Choose the credential dry runs (validate_only) use; reader by default",
			Long: "Dry runs use the reader (Read only) credential by default. If Google refuses validate_only\n" +
				"requests from a Read only user in your account, switch dry runs to the writer credential;\n" +
				"they still apply nothing.",
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				v := args[0]
				if v != config.RoleReader && v != config.RoleWriter {
					return api.Usagef("dry-run-credential wants reader or writer, got %q", v)
				}
				if err := a.updateConfig(func(c *config.Config) { c.DryRunCredential = v }); err != nil {
					return err
				}
				fmt.Fprintf(a.stdout, "dry_run_credential = %s\n", v)
				return nil
			},
		},
	)

	unset := &cobra.Command{Use: "unset", Short: "Remove a config value", Args: cobra.ArbitraryArgs, RunE: keyGroupRunE("unset")}
	unsetKey := &cobra.Command{
		Use: "service-account-key", Short: "Delete a role's stored key file from this machine", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			role, err := roleOf(cmd)
			if err != nil {
				return err
			}
			p, err := config.CredentialPath(role)
			if err != nil {
				return api.Usagef("%v", err)
			}
			if err := os.Remove(p); errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(a.stdout, "no %s key file was stored\n", role)
				return nil
			} else if err != nil {
				return api.Usagef("%v", err)
			}
			fmt.Fprintf(a.stdout, "%s key file removed from this machine; it stays valid until you delete it in the Google Cloud Console (IAM & Admin, Service accounts, Keys)\n", role)
			return nil
		},
	}
	roleFlag(unsetKey)
	unsetOp := &cobra.Command{
		Use: "op-ref", Short: "Forget a role's 1Password reference", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			role, err := roleOf(cmd)
			if err != nil {
				return err
			}
			if err := a.updateConfig(func(c *config.Config) {
				if role == config.RoleWriter {
					c.OpWriterRef = ""
				} else {
					c.OpReaderRef = ""
				}
			}); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "op_%s_ref removed\n", role)
			return nil
		},
	}
	roleFlag(unsetOp)
	unset.AddCommand(unsetKey, unsetOp)
	for _, k := range []struct {
		use, short, done string
		stored           func(config.Config) bool
		clear            func(*config.Config)
	}{
		{"customer-id", "Remove the default customer", "customer-id removed",
			func(c config.Config) bool { return c.CustomerID != "" }, func(c *config.Config) { c.CustomerID = "" }},
		{"login-customer-id", "Remove the login customer", "login-customer-id removed",
			func(c config.Config) bool { return c.LoginCustomerID != "" }, func(c *config.Config) { c.LoginCustomerID = "" }},
		{"read-only", "Return read-only mode to its default (on)", "read-only removed; read-only mode is on (the default)",
			func(c config.Config) bool { return c.ReadOnly != nil }, func(c *config.Config) { c.ReadOnly = nil }},
		{"budget-cap", "Remove the budget cap", "budget-cap removed; no budget amount can be set until a new cap is configured",
			func(c config.Config) bool { return c.BudgetCapMicros != 0 }, func(c *config.Config) { c.BudgetCapMicros = 0 }},
		{"audit-log", "Return the audit log to its default location", "audit-log removed; the default location applies",
			func(c config.Config) bool { return c.AuditLog != "" }, func(c *config.Config) { c.AuditLog = "" }},
		{"dry-run-credential", "Return dry runs to the reader credential", "dry-run-credential removed; dry runs use the reader credential",
			func(c config.Config) bool { return c.DryRunCredential != "" }, func(c *config.Config) { c.DryRunCredential = "" }},
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

// configKeys lists the keys `config set` and `config unset` take.
const configKeys = "service-account-key, op-ref, customer-id, login-customer-id, read-only, budget-cap, audit-log and dry-run-credential"

// keyGroupRunE answers `config set` or `config unset` given something that is
// not one of their keys. An environment-variable name is the expected mistake,
// so it is answered with the key it plainly means.
func keyGroupRunE(verb string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			if k := suggestConfigKey(args[0]); k != "" {
				return api.Usagef("config keys are %s, not environment variable names; did you mean `googleads config %s %s`?",
					configKeys, verb, k)
			}
		}
		return groupRunE(cmd, args)
	}
}

func suggestConfigKey(given string) string {
	switch strings.TrimPrefix(strings.ToLower(given), "google_ads_") {
	case "json_key_file_path", "write_json_key_file_path", "service_account_key", "key":
		return "service-account-key"
	case "op_reader_ref", "op_writer_ref", "op_ref":
		return "op-ref"
	case "customer_id", "customerid":
		return "customer-id"
	case "login_customer_id", "logincustomerid":
		return "login-customer-id"
	case "read_only", "readonly":
		return "read-only"
	case "budget_cap", "budgetcap", "budget_cap_micros":
		return "budget-cap"
	case "audit_log":
		return "audit-log"
	}
	return ""
}
