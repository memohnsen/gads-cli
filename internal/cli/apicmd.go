package cli

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/manifest"
)

var validMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// apiCommand is the escape hatch for anything the generated tree lacks. It is
// not a bypass: a path the manifest knows, in any API version, gets that
// method's class, payload check and block; an unknown mutation is admin.
func (a *app) apiCommand() *cobra.Command {
	var queries, headers []string
	cmd := &cobra.Command{
		Use:   "api <METHOD> <path>",
		Short: "Raw authenticated request (escape hatch); the guardrails still apply",
		Long: "Raw authenticated request. The path is relative to https://googleads.googleapis.com/,\n" +
			"for example v26/customers/1234567890/googleAds:search, without a query string (use --query k=v).\n\n" +
			"It is not a bypass. A path that matches a known method, in any API version, inherits its risk\n" +
			"class, the payload check, the budget cap, the block list and the dry-run default: a write is\n" +
			"sent with validateOnly unless --apply is given, and a gated one needs --apply --confirm-spend.\n" +
			"An unmatched POST, PUT, PATCH or DELETE is admin-class: it has no known dry run, so it needs\n" +
			"--apply --confirm-spend, and every amountMicros or totalAmountMicros anywhere in its body is\n" +
			"held to the budget cap, which no flag overrides. Read-only mode allows GET, read-class methods\n" +
			"and dry runs. Every write attempt is audited. Authorization, a developer-token header, the method-override headers\n" +
			"(X-HTTP-Method-Override, X-HTTP-Method, X-Method-Override) and the credential query parameters\n" +
			"access_token, oauth_token and key cannot be set. GET and DELETE take no --data. Each path\n" +
			"segment must be non-empty and must not be \".\" or \"..\" or contain %, whitespace or a\n" +
			"backslash.\n\n" +
			"The path carries the customer ID, so api does not use --customer-id.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			method := strings.ToUpper(args[0])
			if !validMethods[method] {
				return api.Usagef("method %q not allowed (GET, POST, PUT, PATCH, DELETE)", args[0])
			}
			path := strings.TrimLeft(args[1], "/")
			if path == "" || strings.ContainsAny(path, "?#") {
				return api.Usagef("path must be relative, e.g. v26/customers/1234567890/googleAds:search, with no query string (use --query k=v)")
			}
			// Before Find: a spelling Find does not recognise, such as %3A
			// for ":" or a ".." segment, would otherwise go out as an
			// unmatched call that the payload check never sees.
			if err := checkPathSegments("path", path); err != nil {
				return err
			}
			q := url.Values{}
			for _, kv := range queries {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return api.Usagef("--query wants k=v, got %q", kv)
				}
				q.Add(k, v)
			}
			hdr := http.Header{}
			for _, kv := range headers {
				k, v, ok := strings.Cut(kv, ":")
				if !ok || strings.TrimSpace(k) == "" {
					return api.Usagef("--header wants k:v, got %q", kv)
				}
				hdr.Add(strings.TrimSpace(k), strings.TrimSpace(v))
			}
			body, err := a.readJSONBody(cmd)
			if err != nil {
				return err
			}
			target := a.manifest.Find(method, path)
			unmatched := target == nil
			if unmatched {
				risk := manifest.RiskAdmin
				if method == "GET" {
					risk = manifest.RiskRead
				}
				target = &manifest.Method{ID: "raw " + method + " " + path, Command: []string{"api", method, path}, HTTPMethod: method, Risk: risk}
			}
			if err := a.applyLoginCustomerID(cmd); err != nil {
				return err
			}
			return a.execute(cmd, execSpec{
				meth: target, unmatched: unmatched, forceRead: method == "GET",
				canValidate: !unmatched && target.ValidateOnly,
				req:         &api.Request{Method: method, Path: path, Query: q, Body: body, MethodID: target.ID, Headers: hdr},
			})
		},
	}
	cmd.Flags().String("data", "", "JSON body: literal, @file, or - for stdin")
	cmd.Flags().StringArrayVar(&queries, "query", nil, "query parameter k=v (repeatable)")
	cmd.Flags().StringArrayVar(&headers, "header", nil, "extra header k:v (repeatable; Authorization cannot be set)")
	return cmd
}
