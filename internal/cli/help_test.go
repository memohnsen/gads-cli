package cli

import (
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

func help(t *testing.T, args ...string) string {
	t.Helper()
	a, out, errb := testApp(t, nil, config.Resolved{})
	if code := a.run(append(args, "--help")); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	return out.String()
}

func TestHelpShowsResourceFieldsAndExample(t *testing.T) {
	h := help(t, "campaigns", "mutate")
	for _, want := range []string{
		"POST v25/customers/{+customerId}/campaigns:mutate (risk: write)",
		"status string (one of: ENABLED, PAUSED, REMOVED): The status of the campaign.",
		"id int64 as string (read-only)",
		"networkSettings Campaign_NetworkSettings",
		`Example body:`, `{"operations":[{"create":{}}]}`,
		"--validate-only",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("help lacks %q:\n%s", want, h)
		}
	}
}

func TestHelpGenericMutateListsOperationTypes(t *testing.T) {
	if h := help(t, "mutate"); !strings.Contains(h, "campaignOperation -> Campaign") {
		t.Fatalf("help:\n%s", h)
	}
}

func TestHelpSearchHasExampleAndPaging(t *testing.T) {
	h := help(t, "search")
	if !strings.Contains(h, `--query "SELECT`) || !strings.Contains(h, "merged JSON array of the results rows") {
		t.Fatalf("help:\n%s", h)
	}
}

func TestHelpBlocked(t *testing.T) {
	if h := help(t, "recommendations", "apply"); !strings.Contains(h, "BLOCKED") {
		t.Fatalf("help:\n%s", h)
	}
}
