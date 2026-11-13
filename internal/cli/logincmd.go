package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/auth"
	"github.com/memohnsen/gads-cli/internal/config"
)

func (a *app) loginCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Create a user OAuth credential in a browser and store it in 1Password or a 0600 file (one time, by a person)",
		Long: "Run Google's installed-app OAuth flow once and store the result as an authorized_user\n" +
			"credential for a role. Use it where service-account keys are unavailable, for example when an\n" +
			"organisation policy blocks key creation. The credential acts with the Google Ads access of the\n" +
			"person who signs in; read-only mode, dry runs and every --apply/--confirm-spend gate apply to it\n" +
			"exactly as to a service account.\n\n" +
			"It needs the JSON of a \"Desktop app\" OAuth client (Cloud Console, APIs & Services, Credentials),\n" +
			"read from a chmod 600 file (--client-file) or 1Password (--client-op-ref). It listens on\n" +
			"127.0.0.1, sends the browser to Google's consent page for the Google Ads scope only, checks the\n" +
			"state, exchanges the code with PKCE and stores client ID, client secret and refresh token:\n" +
			"  - with --op-ref op://<vault>/<item>/<field>, in that 1Password field (created if missing, the\n" +
			"    secret passed to op on stdin), and op_<role>_ref is set to it;\n" +
			"  - otherwise in <config dir>/gads/credentials/<role>.json, mode 0600.\n\n" +
			"--no-browser prints only the consent URL on stdout, for a machine without a browser: open it\n" +
			"elsewhere, then paste the address the browser lands on (or forward --port over SSH).\n" +
			"No secret is printed. Examples:\n" +
			"  googleads auth login --client-file ~/client.json\n" +
			"  googleads auth login --role writer --client-op-ref op://<vault>/<client item>/<field> \\\n" +
			"      --op-ref op://<vault>/<credential item>/credential --no-browser",
		Args: cobra.NoArgs,
		RunE: a.runLogin,
	}
	roleFlag(cmd)
	f := cmd.Flags()
	f.String("client-file", "", "OAuth client JSON (Desktop app) downloaded from the Cloud Console; must be chmod 600")
	f.String("client-op-ref", "", "1Password reference to the OAuth client JSON, instead of --client-file")
	f.String("op-ref", "", "store the credential in this 1Password field (op://<vault>/<item>/<field>) instead of a 0600 file")
	f.Bool("no-browser", false, "print only the consent URL and accept the pasted redirect address; for machines without a browser")
	f.Int("port", 0, "loopback port for the redirect (0 picks a free one); fix it to forward it over SSH")
	f.Duration("wait", 5*time.Minute, "how long to wait for the browser")
	return cmd
}

func (a *app) runLogin(cmd *cobra.Command, args []string) error {
	role, err := roleOf(cmd)
	if err != nil {
		return err
	}
	var store auth.OpRef
	storeRef := flagString(cmd, "op-ref")
	if storeRef != "" {
		if store, err = auth.ParseOpRef(storeRef); err != nil {
			return api.Usagef("--op-ref: %v", err)
		}
	}
	port, _ := cmd.Flags().GetInt("port")
	if port < 0 || port > 65535 {
		return api.Usagef("--port wants 0 to 65535, got %d", port)
	}
	wait, _ := cmd.Flags().GetDuration("wait")
	noBrowser, _ := cmd.Flags().GetBool("no-browser")
	client, err := auth.LoadClient(cmd.Context(), expandHome(flagString(cmd, "client-file")), flagString(cmd, "client-op-ref"))
	if err != nil {
		return err
	}
	l := &auth.Login{
		Client: client, TokenURL: a.res.TokenURL, AllowCustomBase: a.res.AllowCustomBase,
		Port: port, NoBrowser: noBrowser, OpenBrowser: a.openBrowser,
		Stdout: a.stdout, Stderr: a.stderr, Wait: wait,
	}
	if noBrowser {
		l.Paste = a.stdin
	}
	cred, err := l.Run(cmd.Context())
	if err != nil {
		return err
	}
	raw, err := auth.MarshalUserCredential(cred)
	if err != nil {
		return api.Usagef("%v", err)
	}
	where := ""
	if storeRef != "" {
		if err := auth.WriteOp(cmd.Context(), store, raw, cred.ClientSecret, cred.RefreshToken); err != nil {
			return api.Usagef("storing the %s credential in 1Password: %v", role, err)
		}
		if err := a.updateConfig(func(c *config.Config) {
			if role == config.RoleWriter {
				c.OpWriterRef = store.String()
			} else {
				c.OpReaderRef = store.String()
			}
		}); err != nil {
			return err
		}
		where = "stored in 1Password at " + store.String() + " and set as op_" + role + "_ref"
		if c := a.res.Credential(role); c.Source == config.SourceOp && c.EnvName != "" && c.OpRef != store.String() {
			fmt.Fprintf(a.stderr, "note: %s is set in this environment and wins over op_%s_ref\n", c.EnvName, role)
		}
	} else {
		p, err := config.CredentialPath(role)
		if err != nil {
			return api.Usagef("%v", err)
		}
		if err := config.WriteFileAtomic(p, raw); err != nil {
			return api.Usagef("%v", err)
		}
		where = "saved to " + p + " (mode 0600)"
		if c := a.res.Credential(role); c.Source == config.SourceOp || c.Source == config.SourceEnv {
			fmt.Fprintf(a.stderr, "note: the %s credential from %s wins over the stored file\n", role, c.Source)
		}
	}
	fmt.Fprintf(a.stdout, "%s credential (user OAuth, client %s) %s; the client secret and refresh token are not shown\n",
		role, auth.ClientIDSuffix(cred.ClientID), where)
	return nil
}
