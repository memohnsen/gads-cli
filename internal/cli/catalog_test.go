package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

func TestCommandsJSON(t *testing.T) {
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"commands", "--json"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var cat struct {
		SchemaVersion     int              `json:"schema_version"`
		APIVersion        string           `json:"api_version"`
		DiscoveryRevision string           `json:"discovery_revision"`
		Commands          []map[string]any `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &cat); err != nil {
		t.Fatal(err)
	}
	if cat.SchemaVersion != 1 || cat.APIVersion != "v25" || cat.DiscoveryRevision != "20260923" || len(cat.Commands) != 7 {
		t.Fatalf("%+v", cat)
	}
	by := map[string]map[string]any{}
	for _, c := range cat.Commands {
		by[c["command"].(string)] = c
	}
	if b := by["recommendations apply"]; b["blocked"] != true || b["blocked_reason"] == nil || b["risk"] != "spend" {
		t.Fatalf("blocked entry %v", b)
	}
	cm := by["campaigns mutate"]
	if cm["resource"] != "Campaign" || cm["validate_only"] != true || cm["needs_customer"] != true ||
		cm["request"] != "MutateCampaignsRequest" || cm["id"] != "customers.campaigns.mutate" {
		t.Fatalf("campaigns mutate %v", cm)
	}
	if _, ok := cm["path_params"]; ok {
		t.Fatal("customerId is a flag, not a positional: no path_params")
	}
	ol := by["operations list"]
	if ol["paging"] != "query-token" || ol["results_field"] != "operations" || len(ol["path_params"].([]any)) != 1 {
		t.Fatalf("operations list %v", ol)
	}
}

func TestCommandsText(t *testing.T) {
	a, out, _ := testApp(t, nil, config.Resolved{})
	a.run([]string{"commands"})
	if !strings.Contains(out.String(), "campaigns mutate") || !strings.Contains(out.String(), "[write]") || !strings.Contains(out.String(), "blocked") {
		t.Fatalf("%s", out.String())
	}
}

func TestSchemaCommand(t *testing.T) {
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"schema", "Campaign"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "Campaign (GoogleAdsGoogleadsV25Resources__Campaign)") || !strings.Contains(out.String(), "status string") {
		t.Fatalf("%s", out.String())
	}
	a, out, _ = testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"schema", "Campaign_NetworkSettings", "--json"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var s struct {
		Name   string           `json:"name"`
		ID     string           `json:"id"`
		Fields []manifest.Field `json:"fields"`
	}
	if err := json.Unmarshal(out.Bytes(), &s); err != nil || s.Name != "Campaign_NetworkSettings" || s.Fields[0].Name != "targetGoogleSearch" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestSchemaAmbiguousAndUnknown(t *testing.T) {
	a, _, errb := testApp(t, nil, config.Resolved{})
	a.manifest.Schemas["GoogleAdsGoogleadsV25Services__Campaign"] = manifest.Schema{}
	if code := a.run([]string{"schema", "Campaign"}); code != 2 || !strings.Contains(errb.String(), "GoogleAdsGoogleadsV25Services__Campaign") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	a, _, errb = testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"schema", "Nope"}); code != 2 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestSchemaJSONKeepsAngleBracketsLiteral(t *testing.T) {
	a, out, _ := testApp(t, nil, config.Resolved{})
	a.manifest.Schemas["GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings"] = manifest.Schema{
		Fields: []manifest.Field{{Name: "x", Type: "string", Doc: "a < b & c > d"}}}
	if code := a.run([]string{"schema", "Campaign_NetworkSettings", "--json"}); code != 0 || !strings.Contains(out.String(), "a < b & c > d") {
		t.Fatalf("exit %d stdout %q", code, out.String())
	}
}
