package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is the released version, set with -ldflags at build time.
var Version = "dev"

func (a *app) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version and the Google Ads API version it speaks",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(a.stdout, "googleads %s (Google Ads API %s, Discovery revision %s)\n",
				Version, a.manifest.APIVersion, a.manifest.DiscoveryRevision)
		},
	}
}
