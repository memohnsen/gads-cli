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
			"Run `googleads commands --json` for the machine-readable catalog of every method\n" +
			"and `googleads schema <Name>` for the fields of a resource.",
		// The root names no method. Printing help and exiting 0 would read as
		// success and break the stdout contract, so it is a usage error.
		RunE:          groupRunE,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	pf := root.PersistentFlags()
	pf.Bool("verbose", false, "log requests to stderr (credentials are never logged)")
	pf.Duration("timeout", 60*time.Second, "per-attempt HTTP timeout")
	pf.Bool("force", false, "confirm a delete-, spend- or admin-class call")
	pf.String("output", "", "write the response body to a file instead of stdout")
	pf.String("customer-id", "", "customer to act on, e.g. 123-456-7890 (default: GOOGLE_ADS_CUSTOMER_ID or the config file)")
	pf.String("login-customer-id", "", "manager account the access goes through; sets the login-customer-id header")
	root.AddCommand(a.versionCommand())
	a.addGeneratedCommands(root)
	return root
}
