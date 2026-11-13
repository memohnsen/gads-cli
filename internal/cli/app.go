// Package cli builds the googleads command tree from the embedded manifest
// and turns process arguments into one API call, or one merged page walk.
//
// Two rules shape everything here: stdout carries the API response and
// nothing else, and every failure leaves as one line of JSON on stderr so an
// agent can parse it without heuristics.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/auth"
	"github.com/memohnsen/gads-cli/internal/config"
	"github.com/memohnsen/gads-cli/internal/manifest"
)

type app struct {
	manifest *manifest.Manifest
	client   *api.Client
	res      config.Resolved
	// configErr is why the configuration could not be resolved. Only
	// version and the config commands run without it, so a person can find
	// and repair the file; every other command reports it.
	configErr      error
	stdout, stderr io.Writer
	stdin          io.Reader
	// cacheDir locates the token cache; nil means os.UserCacheDir.
	cacheDir func() (string, error)
	// creds caches the credential of each role once loaded.
	creds map[string]*auth.Credential
	// tokenOverride replaces the token source of a role; a test seam.
	tokenOverride func(role string) func(context.Context) (string, error)
	// prompt and isTerminal are seams for `init`, the one interactive
	// command; nil in production, where init uses the real terminal.
	prompt     prompter
	isTerminal func() bool
	// openBrowser is the seam for `auth login`; nil means auth.OpenBrowser.
	openBrowser func(url string) error
}

// Execute is the process entry point: it loads the embedded manifest and the
// resolved config, runs the command tree and returns the exit code
// (0 success, 1 API or network failure, 2 usage error or guardrail refusal).
func Execute(args []string) int {
	m, err := manifest.Load()
	if err != nil {
		return (&app{stderr: os.Stderr}).renderError(api.Usagef("%v: this build's embedded manifest is unreadable, reinstall googleads", err))
	}
	// Ctrl-C and SIGTERM cancel the request in flight and any retry wait.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := &app{manifest: m, stdout: os.Stdout, stderr: os.Stderr, stdin: os.Stdin}
	a.configure(os.Getenv, contextSleeper(ctx))
	return a.runContext(ctx, args)
}

// configure resolves the config file and the environment and builds the
// client. A failure is kept in configErr rather than returned: the root
// command reports it for every command that needs the configuration.
func (a *app) configure(getenv func(string) string, sleep func(time.Duration)) {
	a.res, a.configErr = config.Resolve(getenv)
	a.client = &api.Client{
		BaseURL: a.res.APIBase, LoginCustomerID: a.res.LoginCustomerID, ReadOnly: a.res.ReadOnly,
		AllowCustomBase: a.res.AllowCustomBase, TokenSource: a.tokenSourceFor(config.RoleReader), Sleep: sleep,
	}
}

// requireConfig is the root's persistent pre-run: with an unresolvable
// configuration only version, help and the config commands run.
func (a *app) requireConfig(cmd *cobra.Command, args []string) error {
	if a.configErr == nil {
		return nil
	}
	for c := cmd; c.HasParent(); c = c.Parent() {
		if !c.Parent().HasParent() { // c is a top-level command
			switch c.Name() {
			case "version", "config", "help":
				return nil
			}
		}
	}
	return api.Usagef("cannot load the configuration: %v", a.configErr)
}

// tokenSourceFor returns the token source of one credential role. The
// credential is loaded on the first API call, so a missing or broken one
// cannot break commands that never call the API. It is nil when the role has
// no credential source, which the client reports as a usage error.
func (a *app) tokenSourceFor(role string) func(context.Context) (string, error) {
	if a.tokenOverride != nil {
		return a.tokenOverride(role)
	}
	if !a.res.Credential(role).Configured() {
		return nil
	}
	return func(ctx context.Context) (string, error) {
		cred, err := a.loadCredential(ctx, role)
		if err != nil {
			return "", err
		}
		return a.newSource(cred).Token(ctx)
	}
}

func (a *app) newSource(cred *auth.Credential) *auth.Source {
	return &auth.Source{Key: cred.Key, User: cred.User, TokenURL: a.res.TokenURL,
		AllowCustomBase: a.res.AllowCustomBase, CacheDir: a.cacheDir}
}

// loadCredential loads and caches the credential of role from its
// configured source.
func (a *app) loadCredential(ctx context.Context, role string) (*auth.Credential, error) {
	if c, ok := a.creds[role]; ok {
		return c, nil
	}
	c, err := auth.LoadCredential(ctx, a.res.Credential(role))
	if err != nil {
		return nil, err
	}
	if a.creds == nil {
		a.creds = map[string]*auth.Credential{}
	}
	a.creds[role] = c
	return c, nil
}

// credentialIdentity is the kind and identity of a role's credential once it
// has been loaded, for the audit log; never a secret.
func (a *app) credentialIdentity(role string) (kind, identity string) {
	if c, ok := a.creds[role]; ok {
		return c.Kind(), c.Identity()
	}
	return "", ""
}

func credentialHint(role string) string { return auth.SetupHint(role) }

// contextSleeper returns a sleep that gives up as soon as ctx is done.
func contextSleeper(ctx context.Context) func(time.Duration) {
	return func(d time.Duration) {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
		}
	}
}

func (a *app) run(args []string) int { return a.runContext(context.Background(), args) }

func (a *app) runContext(ctx context.Context, args []string) int {
	root := a.newRoot()
	root.SetArgs(args)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		return a.renderError(err)
	}
	return 0
}

// renderError writes err as one line of JSON on stderr and returns the exit
// code: 2 for usage errors and guardrail refusals (nothing was sent), 1 for
// everything the API or the network produced.
func (a *app) renderError(err error) int {
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		apiErr = api.Usagef("%v", err) // anything else is a cobra parse or usage failure
	}
	raw, mErr := jsonCompact(apiErr)
	if mErr != nil {
		raw, mErr = jsonCompact(&api.Error{Kind: apiErr.Kind, Message: apiErr.Message, Status: apiErr.Status, RequestID: apiErr.RequestID})
		if mErr != nil {
			raw = []byte(`{"error":"internal: cannot render error","kind":"usage","details":null}`)
		}
	}
	fmt.Fprintln(a.stderr, string(raw))
	if apiErr.IsLocal() {
		return 2
	}
	return 1
}
