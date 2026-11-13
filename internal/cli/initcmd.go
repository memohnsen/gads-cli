package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/auth"
	"github.com/memohnsen/gads-cli/internal/config"
	"github.com/memohnsen/gads-cli/internal/manifest"
)

func (a *app) initCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Interactive setup: service-account key, customer, budget cap (needs a terminal)",
		Long: "Interactive setup. Asks for the path of a service-account key file, the customer ID and a budget\n" +
			"cap, checks the key against Google with two read-only calls, and saves everything to the config\n" +
			"file (0600). Needs a terminal: agents configure googleads with `googleads config set` or the\n" +
			"environment instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			isTTY := a.isTerminal
			if isTTY == nil {
				isTTY = stdioIsTerminal
			}
			if !isTTY() {
				return api.Usagef("googleads init is interactive and needs a terminal; use `googleads config set ...` instead")
			}
			p := a.prompt
			if p == nil {
				p = newTermPrompter(os.Stdin, a.stdout)
			}
			return a.runInit(cmd.Context(), p)
		},
	}
}

func (a *app) runInit(ctx context.Context, p prompter) error {
	out := a.stdout
	fmt.Fprintln(out, "googleads setup: you need a service-account key file and your Google Ads customer ID.")
	for _, name := range []string{"GOOGLE_ADS_JSON_KEY_FILE_PATH", "GOOGLE_ADS_CUSTOMER_ID", "GOOGLE_ADS_LOGIN_CUSTOMER_ID", "GOOGLE_ADS_READ_ONLY"} {
		if a.res.FromEnv[name] {
			fmt.Fprintf(out, "Note: %s is set in this shell and wins over what you save here.\n", name)
		}
	}
	key, keyJSON, keyPath, err := a.askKey(p)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Key for %s (project %s).\n", key.ClientEmail, key.ProjectID)
	customer, err := askValid(p, out, "Google Ads customer ID (e.g. 123-456-7890): ", config.NormalizeCustomerID)
	if err != nil {
		return err
	}
	name, currency, zone, err := a.verify(ctx, key, customer)
	if err != nil {
		return err // nothing is saved; the error carries Google's reason and a hint
	}
	fmt.Fprintf(out, "Connected to %s (currency %s, time zone %s).\n", name, currency, zone)
	capMicros, err := askValid(p, out,
		fmt.Sprintf("Budget cap: the highest daily budget googleads may ever set, in %s (e.g. 30): ", currency),
		config.ParseMoneyToMicros)
	if err != nil {
		return err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, keyJSON); err != nil {
		return api.Usagef("%v", err)
	}
	if err := a.updateConfig(func(c *config.Config) {
		c.ServiceAccountKey, c.CustomerID, c.BudgetCapMicros = compact.Bytes(), customer, capMicros
	}); err != nil {
		return err
	}
	fmt.Fprintf(out, "Saved. The key now lives in the config file; delete the downloaded copy: %s\n", keyPath)
	return nil
}

// askKey asks for the key file until one reads and validates, three times at most.
func (a *app) askKey(p prompter) (*auth.Key, []byte, string, error) {
	for range 3 {
		path, err := ask(p, "Path to the service-account key file (JSON): ", false)
		if err != nil {
			return nil, nil, "", api.Usagef("init: %v", err)
		}
		path = expandHome(path)
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(a.stdout, "Cannot read %s: %v\n", path, err)
			continue
		}
		raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
		key, err := auth.ParseKey(raw)
		if err != nil {
			fmt.Fprintln(a.stdout, err)
			continue
		}
		return key, raw, path, nil
	}
	return nil, nil, "", api.Usagef("init: no usable service-account key after 3 attempts")
}

// askValid asks until parse accepts the answer, three times at most.
func askValid[T any](p prompter, out io.Writer, prompt string, parse func(string) (T, error)) (T, error) {
	var zero T
	for range 3 {
		s, err := ask(p, prompt, false)
		if err != nil {
			return zero, api.Usagef("init: %v", err)
		}
		v, err := parse(s)
		if err == nil {
			return v, nil
		}
		fmt.Fprintln(out, err)
	}
	return zero, api.Usagef("init: no valid answer after 3 attempts")
}

// expandHome turns a leading ~ into the home directory, as a shell would,
// and drops the quotes a pasted path often carries.
func expandHome(path string) string {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// verify proves the key reaches the customer with two read-only calls and
// returns the account's name, currency and time zone.
func (a *app) verify(ctx context.Context, key *auth.Key, customer string) (string, string, string, error) {
	list, search := a.manifest.ByID("customers.listAccessibleCustomers"), a.manifest.ByID("customers.googleAds.search")
	if list == nil || search == nil {
		return "", "", "", api.Usagef("init: this build's manifest lacks the verification methods; reinstall googleads")
	}
	client := *a.client
	client.TokenSource = a.newSource(key).Token
	if _, err := client.Do(ctx, api.Request{Method: list.HTTPMethod, Path: list.Path, Risk: manifest.RiskRead, MethodID: list.ID}); err != nil {
		return "", "", "", err
	}
	body, err := jsonCompact(map[string]string{"query": "SELECT customer.descriptive_name, customer.currency_code, customer.time_zone FROM customer LIMIT 1"})
	if err != nil {
		return "", "", "", api.Usagef("%v", err)
	}
	resp, err := client.Do(ctx, api.Request{Method: search.HTTPMethod, Path: strings.Replace(search.Path, "{+customerId}", customer, 1),
		Body: body, Risk: manifest.RiskRead, MethodID: search.ID})
	if err != nil {
		return "", "", "", err
	}
	var r struct {
		Results []struct {
			Customer struct {
				DescriptiveName string `json:"descriptiveName"`
				CurrencyCode    string `json:"currencyCode"`
				TimeZone        string `json:"timeZone"`
			} `json:"customer"`
		} `json:"results"`
	}
	if err := json.Unmarshal(resp.Body, &r); err != nil || len(r.Results) == 0 {
		return "", "", "", api.Usagef("init: the account check returned no customer row: %.200s", resp.Body)
	}
	c := r.Results[0].Customer
	return c.DescriptiveName, c.CurrencyCode, c.TimeZone, nil
}
