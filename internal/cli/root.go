package cli

import (
	"time"

	"github.com/spf13/cobra"
)

func (a *app) newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "googleads",
		Short: "CLI for the Google Ads API, built for agents: JSON in, JSON out",
		Long: "CLI for the Google Ads API, built for agents: JSON in, JSON out.\n\n" +
			"stdout carries the API response and nothing else; errors are one line of JSON\n" +
			"on stderr. Exit codes: 0 success, 1 API or network error, 2 usage error.\n\n" +
			"Writes are dry runs by default: they go to Google with validate_only and apply nothing.\n" +
			"--apply applies a write; delete-, spend- and admin-class calls (enabling, budgets, bids,\n" +
			"billing, deletes) need --apply --confirm-spend. Read-only mode, on by default, refuses\n" +
			"every --apply. Every write attempt is appended to the audit log.\n\n" +
			"Run `googleads commands --json` for the machine-readable catalog of every method\n" +
			"and `googleads schema <Name>` for the fields of a resource.",
		// The root names no method. Printing help and exiting 0 would read as
		// success and break the stdout contract, so it is a usage error.
		RunE:              groupRunE,
		PersistentPreRunE: a.requireConfig,
		SilenceErrors:     true,
		SilenceUsage:      true,
	}
	pf := root.PersistentFlags()
	pf.Bool("verbose", false, "log requests to stderr (credentials are never logged)")
	pf.Duration("timeout", 60*time.Second, "per-attempt HTTP timeout")
	pf.Bool("apply", false, "apply a write; without it every write is a validate_only dry run")
	pf.Bool("confirm-spend", false, "with --apply, confirm a delete-, spend- or admin-class call (a person's decision)")
	pf.Bool("validate-only", false, "dry run: Google checks the request and applies nothing (the default for writes; required to dry-run gated calls)")
	pf.Bool("force", false, "deprecated alias for --confirm-spend; never a substitute for --apply")
	pf.String("output", "", "write the response body to a file instead of stdout")
	pf.String("customer-id", "", "customer to act on, e.g. 123-456-7890 (default: GOOGLE_ADS_CUSTOMER_ID or the config file)")
	pf.String("login-customer-id", "", "manager account the access goes through; sets the login-customer-id header")
	root.AddCommand(a.versionCommand(), a.commandsCommand(), a.schemaCommand(), a.apiCommand(),
		a.configCommand(), a.authCommand(), a.initCommand())
	a.addGeneratedCommands(root)
	return root
}
