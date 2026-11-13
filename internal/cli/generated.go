package cli

import (
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/manifest"
)

// flagName maps a camelCase API parameter to a kebab-case flag:
// "returnPartialSuccess" becomes "return-partial-success". The API name goes
// on the wire.
func flagName(param string) string {
	var b strings.Builder
	for i, r := range param {
		switch {
		case unicode.IsUpper(r):
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
		case r == '_':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func flagBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

func flagString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

// groupRunE answers a namespace invoked without a subcommand: that is a
// usage error, not a success that prints help.
func groupRunE(cmd *cobra.Command, args []string) error {
	var names []string
	for _, sub := range cmd.Commands() {
		if !sub.Hidden && sub.Name() != "help" && sub.Name() != "completion" {
			names = append(names, sub.Name())
		}
	}
	if len(names) > 12 {
		names = append(names[:12], "...")
	}
	return api.Usagef("%s needs a subcommand: %s", cmd.CommandPath(), strings.Join(names, ", "))
}

// addGeneratedCommands hangs one leaf per manifest method off root, creating
// the group commands its path implies. A one-element path is a leaf directly
// under root (search, mutate, generate-keyword-ideas).
func (a *app) addGeneratedCommands(root *cobra.Command) {
	groups := map[string]*cobra.Command{}
	for i := range a.manifest.Methods {
		meth := &a.manifest.Methods[i]
		parent := root
		for depth := 1; depth < len(meth.Command); depth++ {
			key := strings.Join(meth.Command[:depth], " ")
			g, ok := groups[key]
			if !ok {
				g = &cobra.Command{Use: meth.Command[depth-1], Short: meth.Command[depth-1] + " methods", RunE: groupRunE}
				groups[key] = g
				parent.AddCommand(g)
			}
			parent = g
		}
		parent.AddCommand(a.leafCommand(meth))
	}
}

// isReport marks the two methods that take GAQL through --query.
func isReport(meth *manifest.Method) bool {
	return meth.ID == "customers.googleAds.search" || meth.ID == "customers.googleAds.searchStream"
}

func (a *app) leafCommand(meth *manifest.Method) *cobra.Command {
	use := meth.Command[len(meth.Command)-1]
	positional := 0
	for _, p := range meth.PathParams {
		if p.Name != "customerId" {
			use += " <" + p.Name + ">"
			positional++
		}
	}
	short := firstLine(meth.Description)
	if meth.Blocked != "" {
		short = "[blocked] " + short
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(positional),
		RunE:  func(cmd *cobra.Command, args []string) error { return a.runMethod(cmd, meth, args) },
	}
	// Help is built on demand: rendering field tables for every command on
	// every run would cost more than the call itself.
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		c.Long = a.longHelp(meth)
		c.Parent().HelpFunc()(c, args)
	})
	for _, q := range meth.QueryParams {
		usage := cleanDoc(q.Doc)
		if len(q.Enum) > 0 {
			usage += " (one of: " + strings.Join(trimEnum(q.Enum), ", ") + ")"
		}
		switch q.Type {
		case "integer":
			cmd.Flags().Int(flagName(q.Name), 0, usage)
		case "boolean":
			cmd.Flags().Bool(flagName(q.Name), false, usage)
		default:
			cmd.Flags().String(flagName(q.Name), "", usage)
		}
	}
	if meth.Request != "" {
		cmd.Flags().String("data", "", "JSON body: literal, @file, or - for stdin")
	}
	if isReport(meth) {
		cmd.Flags().String("query", "", "GAQL query, or @file to read it from a file")
	}
	if isReport(meth) {
		cmd.Flags().String("format", "json", "output format for the rows: json (Google's response as sent), csv or table")
	}
	if meth.PartialFailure {
		cmd.Flags().Bool("partial-failure", false, "apply the operations that succeed; exit 1 with kind partial_failure if any fail")
	}
	if meth.Paging != manifest.PagingNone {
		cmd.Flags().Bool("all", false, "follow page tokens and print one merged JSON array of rows")
		cmd.Flags().Int("max-pages", 100, "page cap for --all")
	}
	return cmd
}

// runMethod turns one parsed command into one API call. The guard runs
// before any network I/O, so a refused call has no side effects at all.
func (a *app) runMethod(cmd *cobra.Command, meth *manifest.Method, args []string) error {
	if meth.Blocked != "" {
		return a.execute(cmd, execSpec{meth: meth, req: &api.Request{Method: meth.HTTPMethod, Path: meth.Path}})
	}
	req, err := a.buildRequest(cmd, meth, args)
	if err != nil {
		return err
	}
	format := ""
	if isReport(meth) {
		if format = flagString(cmd, "format"); !validFormat(format) {
			return api.Usagef("--format wants json, csv or table, got %q", format)
		}
	}
	return a.execute(cmd, execSpec{
		meth: meth, req: req, canValidate: meth.ValidateOnly,
		partial: flagBool(cmd, "partial-failure"), all: flagBool(cmd, "all"), format: format,
	})
}

func (a *app) applyGlobalFlags(cmd *cobra.Command) {
	if flagBool(cmd, "verbose") {
		a.client.Verbose = a.stderr
	}
	if d, err := cmd.Flags().GetDuration("timeout"); err == nil {
		a.client.Timeout = d
	}
}
