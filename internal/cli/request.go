package cli

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/config"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

var unsafePath = regexp.MustCompile(`[?#%\s\\]`)

// buildRequest turns flags and arguments into a request. The guard sees the
// body built here before the CLI sets the fields it owns.
func (a *app) buildRequest(cmd *cobra.Command, meth *manifest.Method, args []string) (*api.Request, error) {
	path, next := meth.Path, 0
	for _, p := range meth.PathParams {
		var val string
		if p.Name == "customerId" {
			id, err := a.customerID(cmd)
			if err != nil {
				return nil, err
			}
			val = id
		} else {
			val = args[next]
			next++
			if err := validatePathValue(p, val); err != nil {
				return nil, err
			}
		}
		// {+name} is reserved expansion: slashes stay as they are.
		path = strings.Replace(path, "{+"+p.Name+"}", val, 1)
		path = strings.Replace(path, "{"+p.Name+"}", url.PathEscape(val), 1)
	}
	q := url.Values{}
	for _, param := range meth.QueryParams {
		fn := flagName(param.Name)
		if !cmd.Flags().Changed(fn) {
			continue // only what the caller asked for goes on the wire
		}
		switch param.Type {
		case "integer":
			v, _ := cmd.Flags().GetInt(fn)
			q.Set(param.Name, strconv.Itoa(v))
		case "boolean":
			v, _ := cmd.Flags().GetBool(fn)
			q.Set(param.Name, strconv.FormatBool(v))
		default:
			q.Set(param.Name, flagString(cmd, fn))
		}
	}
	req := &api.Request{Method: meth.HTTPMethod, Path: path, Query: q, Risk: meth.Risk, MethodID: meth.ID}
	if meth.Request != "" {
		body, err := a.readJSONBody(cmd)
		if err != nil {
			return nil, err
		}
		if gaql := flagString(cmd, "query"); isReport(meth) && gaql != "" {
			if body != nil {
				return nil, api.Usagef("--query and --data cannot be combined: put the query in the body, or use --query alone")
			}
			text, err := readQuery(gaql)
			if err != nil {
				return nil, err
			}
			if body, err = jsonCompact(map[string]string{"query": text}); err != nil {
				return nil, api.Usagef("%v", err)
			}
		}
		req.Body = body
	}
	if err := a.applyLoginCustomerID(cmd); err != nil {
		return nil, err
	}
	return req, nil
}

// applyLoginCustomerID lets --login-customer-id override the configured
// manager account for this call.
func (a *app) applyLoginCustomerID(cmd *cobra.Command) error {
	v := flagString(cmd, "login-customer-id")
	if v == "" {
		return nil
	}
	id, err := config.NormalizeCustomerID(v)
	if err != nil {
		return api.Usagef("--login-customer-id: %v", err)
	}
	a.client.LoginCustomerID = id
	return nil
}

// customerID resolves the customer: the flag, else the environment or the
// config file (already normalized by config.Resolve).
func (a *app) customerID(cmd *cobra.Command) (string, error) {
	if v := flagString(cmd, "customer-id"); v != "" {
		id, err := config.NormalizeCustomerID(v)
		if err != nil {
			return "", api.Usagef("--customer-id: %v", err)
		}
		return id, nil
	}
	if a.res.CustomerID != "" {
		return a.res.CustomerID, nil
	}
	return "", api.Usagef("no customer ID: pass --customer-id, set GOOGLE_ADS_CUSTOMER_ID, or run `googleads config set customer-id <id>`")
}

// validatePathValue keeps a positional inside the path Google defines for
// it: no query or fragment characters, no empty, "." or ".." segments, and a
// match against the Discovery pattern.
func validatePathValue(p manifest.Param, v string) error {
	if v == "" || unsafePath.MatchString(v) {
		return api.Usagef("%s %q contains characters that cannot go into a URL path", p.Name, v)
	}
	for _, seg := range strings.Split(v, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return api.Usagef(`%s %q has an empty, "." or ".." segment`, p.Name, v)
		}
	}
	if p.Pattern != "" {
		if re, err := regexp.Compile(p.Pattern); err == nil && !re.MatchString(v) {
			return api.Usagef("%s %q does not have the expected form %s", p.Name, v, p.Pattern)
		}
	}
	return nil
}

// readQuery returns the GAQL text: literal, or from a file with @path.
func readQuery(v string) (string, error) {
	if !strings.HasPrefix(v, "@") {
		return v, nil
	}
	raw, err := os.ReadFile(v[1:])
	if err != nil {
		return "", api.Usagef("reading --query: %v", err)
	}
	if raw, err = decodeText(raw, "the --query file"); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// setOwnedFields sets validateOnly and partialFailure when their flags are
// given, removing either spelling of the same field first so Google never
// sees it twice. Without either flag the body goes out byte for byte.
func setOwnedFields(body []byte, validateOnly, partialFailure bool) ([]byte, error) {
	if !validateOnly && !partialFailure {
		return body, nil
	}
	obj := map[string]any{}
	if len(body) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if err := dec.Decode(&obj); err != nil {
			return nil, api.Usagef("--data must be a JSON object to combine it with --validate-only or --partial-failure: %v", err)
		}
	}
	if validateOnly {
		delete(obj, "validate_only")
		obj["validateOnly"] = true
	}
	if partialFailure {
		delete(obj, "partial_failure")
		obj["partialFailure"] = true
	}
	return jsonCompact(obj)
}

// jsonCompact marshals v without HTML escaping, so GAQL such as
// "metrics.clicks > 0" goes out as written.
func jsonCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
