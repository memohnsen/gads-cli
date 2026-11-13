package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/manifest"
)

// catalogEntry is one command as `googleads commands --json` publishes it:
// a stable projection of the manifest. schema_version is the contract a
// consumer pins to; new optional keys may appear under the same version.
type catalogEntry struct {
	Command        string           `json:"command"`
	ID             string           `json:"id"`
	HTTPMethod     string           `json:"http_method"`
	Path           string           `json:"path"`
	Risk           string           `json:"risk"`
	Blocked        bool             `json:"blocked,omitempty"`
	BlockedReason  string           `json:"blocked_reason,omitempty"`
	Summary        string           `json:"summary,omitempty"`
	NeedsCustomer  bool             `json:"needs_customer"`
	PathParams     []manifest.Param `json:"path_params,omitempty"` // positional; customerId is the --customer-id flag
	QueryParams    []manifest.Param `json:"query_params,omitempty"`
	Request        string           `json:"request,omitempty"`  // short schema name of the body
	Resource       string           `json:"resource,omitempty"` // for per-resource mutates
	ValidateOnly   bool             `json:"validate_only"`
	PartialFailure bool             `json:"partial_failure"`
	Paging         string           `json:"paging"`
	ResultsField   string           `json:"results_field,omitempty"`
}

func (a *app) commandsCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "commands",
		Short: "List every API command (--json for the machine-readable catalog)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !asJSON {
				for _, m := range a.manifest.Methods {
					tag := ""
					if m.Blocked != "" {
						tag = " blocked"
					}
					fmt.Fprintf(a.stdout, "%-60s %-6s [%s]%s\n", strings.Join(m.Command, " "), m.HTTPMethod, m.Risk, tag)
				}
				return nil
			}
			out := struct {
				SchemaVersion     int            `json:"schema_version"`
				APIVersion        string         `json:"api_version"`
				DiscoveryRevision string         `json:"discovery_revision"`
				Commands          []catalogEntry `json:"commands"`
			}{SchemaVersion: manifest.SchemaVersion, APIVersion: a.manifest.APIVersion,
				DiscoveryRevision: a.manifest.DiscoveryRevision, Commands: []catalogEntry{}}
			for _, m := range a.manifest.Methods {
				e := catalogEntry{
					Command: strings.Join(m.Command, " "), ID: m.ID, HTTPMethod: m.HTTPMethod, Path: m.Path, Risk: m.Risk,
					Blocked: m.Blocked != "", BlockedReason: m.Blocked, Summary: firstLine(m.Description),
					NeedsCustomer: m.NeedsCustomer, QueryParams: m.QueryParams, ValidateOnly: m.ValidateOnly,
					PartialFailure: m.PartialFailure, Paging: m.Paging, ResultsField: m.ResultsField,
				}
				for _, p := range m.PathParams {
					if p.Name != "customerId" {
						e.PathParams = append(e.PathParams, p)
					}
				}
				if m.Request != "" {
					e.Request = manifest.ShortName(m.Request)
				}
				if m.Mutate != nil {
					e.Resource = m.Mutate.Resource
				}
				out.Commands = append(out.Commands, e)
			}
			enc := json.NewEncoder(a.stdout)
			enc.SetEscapeHTML(false)
			return enc.Encode(out)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the machine-readable catalog")
	return cmd
}
