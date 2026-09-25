package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

func (a *app) schemaCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "schema <Name>",
		Short: "Print the fields of a request or resource type, e.g. Campaign or Campaign_NetworkSettings",
		Long: "Print the fields of a request or resource type. Names are Google's schema names without the\n" +
			"GoogleAdsGoogleadsV25<Package> prefix (Campaign, Campaign_NetworkSettings, ResponsiveSearchAdInfo);\n" +
			"a full name works too. int64 fields travel as strings, amounts in micros.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, candidates := a.manifest.LookupSchema(args[0])
			if id == "" {
				if len(candidates) > 0 {
					return api.Usagef("schema name %q is ambiguous; use one of: %s", args[0], strings.Join(candidates, ", "))
				}
				return api.Usagef("no schema named %q; names look like Campaign, CampaignBudget, Campaign_NetworkSettings", args[0])
			}
			fields := a.manifest.Schemas[id].Fields
			if asJSON {
				enc := json.NewEncoder(a.stdout)
				enc.SetEscapeHTML(false)
				return enc.Encode(struct {
					Name   string           `json:"name"`
					ID     string           `json:"id"`
					Fields []manifest.Field `json:"fields"`
				}{manifest.ShortName(id), id, fields})
			}
			fmt.Fprintf(a.stdout, "%s (%s)\n\n", manifest.ShortName(id), id)
			writeFieldTable(a.stdout, fields)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the field table as JSON")
	return cmd
}
