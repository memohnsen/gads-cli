package cli

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Version is the released version, set with -ldflags at build time.
var Version = "dev"

// resolveVersion prefers the linker-set version. A build without one, such
// as `go install github.com/wir-drei-digital/google-ads-cli/cmd/googleads@v0.1.0`,
// falls back to the module version Go recorded. A checkout build records a
// VCS pseudo-version (Go 1.24 and later), or "(devel)", which stays "dev".
func resolveVersion(linker string, info *debug.BuildInfo, ok bool) string {
	if linker != "dev" || !ok || info == nil {
		return linker
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return linker
}

func (a *app) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version and the Google Ads API version it speaks",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			info, ok := debug.ReadBuildInfo()
			fmt.Fprintf(a.stdout, "googleads %s (Google Ads API %s, Discovery revision %s)\n",
				resolveVersion(Version, info, ok), a.manifest.APIVersion, a.manifest.DiscoveryRevision)
		},
	}
}
