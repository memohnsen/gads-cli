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

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/auth"
	"github.com/wir-drei-digital/google-ads-cli/internal/config"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

type app struct {
	manifest       *manifest.Manifest
	client         *api.Client
	res            config.Resolved
	stdout, stderr io.Writer
	stdin          io.Reader
	// cacheDir locates the token cache; nil means os.UserCacheDir.
	cacheDir func() (string, error)
	// prompt and isTerminal are seams for `init`, the one interactive
	// command; nil in production, where init uses the real terminal.
	prompt     prompter
	isTerminal func() bool
}

// Execute is the process entry point: it loads the embedded manifest and the
// resolved config, runs the command tree and returns the exit code
// (0 success, 1 API or network failure, 2 usage error).
func Execute(args []string) int {
	m, err := manifest.Load()
	if err != nil {
		return (&app{stderr: os.Stderr}).renderError(api.Usagef("%v: this build's embedded manifest is unreadable, reinstall googleads", err))
	}
	res, err := config.Resolve(os.Getenv)
	if err != nil {
		return (&app{stderr: os.Stderr}).renderError(api.Usagef("%v", err))
	}
	// Ctrl-C and SIGTERM cancel the request in flight and any retry wait.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := &app{manifest: m, res: res, stdout: os.Stdout, stderr: os.Stderr, stdin: os.Stdin}
	a.client = &api.Client{
		BaseURL: res.APIBase, LoginCustomerID: res.LoginCustomerID, ReadOnly: res.ReadOnly,
		AllowCustomBase: res.AllowCustomBase, TokenSource: a.tokenSource(), Sleep: contextSleeper(ctx),
	}
	return a.runContext(ctx, args)
}

// tokenSource loads the key on the first API call, so a missing or broken
// key file cannot break commands that never call the API. It is nil when no
// key is configured, which the client reports as a usage error.
func (a *app) tokenSource() func(context.Context) (string, error) {
	if a.res.KeySource() == "" {
		return nil
	}
	var src *auth.Source
	return func(ctx context.Context) (string, error) {
		if src == nil {
			key, err := a.loadKey()
			if err != nil {
				return "", err
			}
			src = a.newSource(key)
		}
		return src.Token(ctx)
	}
}

func (a *app) newSource(key *auth.Key) *auth.Source {
	return &auth.Source{Key: key, TokenURL: a.res.TokenURL, AllowCustomBase: a.res.AllowCustomBase, CacheDir: a.cacheDir}
}

// loadKey reads and validates the configured key: the file named by
// GOOGLE_ADS_JSON_KEY_FILE_PATH, else the copy in the config file.
func (a *app) loadKey() (*auth.Key, error) {
	raw := a.res.KeyJSON
	if a.res.KeyPath != "" {
		b, err := os.ReadFile(a.res.KeyPath)
		if err != nil {
			return nil, api.Usagef("reading GOOGLE_ADS_JSON_KEY_FILE_PATH: %v", err)
		}
		raw = b
	}
	k, err := auth.ParseKey(raw)
	if err != nil {
		return nil, api.Usagef("%v", err)
	}
	return k, nil
}

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
// code: 2 for usage errors, 1 for everything the API or the network produced.
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
	if apiErr.Kind == api.KindUsage {
		return 2
	}
	return 1
}
