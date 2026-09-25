package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

const mini = "testdata/mini-discovery.json"

func miniTables() (map[string]any, map[string]string) {
	over := map[string]any{"commands": map[string]string{
		"customers.googleAds.search": "search", "customers.googleAds.mutate": "mutate", "customers.mutate": "customer mutate",
	}}
	table := map[string]string{
		"customers.listAccessibleCustomers": "read", "customers.mutate": "write",
		"customers.googleAds.mutate": "write", "customers.googleAds.search": "read", "customers.campaigns.mutate": "write",
	}
	return over, table
}

func writeJSON(t *testing.T, dir, name string, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func buildMini(t *testing.T, over map[string]any, table map[string]string) (*manifest.Manifest, []Proposal, error) {
	t.Helper()
	dir := t.TempDir()
	return Build(mini, writeJSON(t, dir, "over.json", over), writeJSON(t, dir, "class.json", table))
}

func TestBuildMini(t *testing.T) {
	over, table := miniTables()
	m, props, err := buildMini(t, over, table)
	if err != nil || len(props) != 0 {
		t.Fatalf("err %v proposals %v", err, props)
	}
	if len(m.Methods) != 5 || m.APIVersion != "v25" || m.DiscoveryRevision != "20260101" {
		t.Fatalf("%d methods, %q %q", len(m.Methods), m.APIVersion, m.DiscoveryRevision)
	}
	if s := m.ByID("customers.googleAds.search"); s.Paging != manifest.PagingBodyToken || strings.Join(s.Command, " ") != "search" {
		t.Fatalf("search %+v", s)
	}
	if c := m.ByID("customers.mutate"); strings.Join(c.Command, " ") != "customer mutate" || c.Mutate.OperationsField != "operation" {
		t.Fatalf("customer mutate %+v", c)
	}
	if m.GenericOperations["campaignOperation"] != "customers.campaigns.mutate" {
		t.Fatalf("generic %v", m.GenericOperations)
	}
	if _, ok := m.Schemas["Campaign"]; !ok {
		t.Fatal("schemas missing")
	}
}

func TestBuildProposesUnclassified(t *testing.T) {
	over, table := miniTables()
	delete(table, "customers.campaigns.mutate")
	_, props, err := buildMini(t, over, table)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 1 || props[0].ID != "customers.campaigns.mutate" || props[0].Proposed != "write" {
		t.Fatalf("proposals %+v", props)
	}
}

func TestBuildProblems(t *testing.T) {
	cases := map[string]struct {
		edit func(over map[string]any, table map[string]string)
		want string
	}{
		"invalid risk":   {func(o map[string]any, tb map[string]string) { tb["customers.mutate"] = "wirte" }, `risk "wirte"`},
		"stale class":    {func(o map[string]any, tb map[string]string) { tb["customers.gone.mutate"] = "write" }, `classification entry "customers.gone.mutate"`},
		"stale skiplist": {func(o map[string]any, tb map[string]string) { o["skiplist"] = map[string]string{"nope.x": "why"} }, `skiplist entry "nope.x"`},
		"empty skip reason": {func(o map[string]any, tb map[string]string) {
			o["skiplist"] = map[string]string{"customers.campaigns.mutate": " "}
			delete(tb, "customers.campaigns.mutate")
		}, "empty reason"},
		"empty block reason": {func(o map[string]any, tb map[string]string) { o["blocked"] = map[string]string{"customers.mutate": ""} }, "empty reason"},
		"collision": {func(o map[string]any, tb map[string]string) {
			o["commands"].(map[string]string)["customers.mutate"] = "mutate"
		}, "command collision"},
		"reserved": {func(o map[string]any, tb map[string]string) {
			o["commands"].(map[string]string)["customers.googleAds.search"] = "config"
		}, "built-in"},
		"leaf and group": {func(o map[string]any, tb map[string]string) {
			o["commands"].(map[string]string)["customers.googleAds.search"] = "customer"
		}, "also a group"},
		"stale generic": {func(o map[string]any, tb map[string]string) {
			o["generic_operations"] = map[string]string{"campaignOperation": "customers.nothing.mutate"}
		}, "names no method"},
	}
	for name, c := range cases {
		over, table := miniTables()
		c.edit(over, table)
		_, _, err := buildMini(t, over, table)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, c.want)
		}
	}
}

func TestBuildRealDocument(t *testing.T) {
	m, props, err := Build("../../spec/googleads-v25.discovery.json", "overrides.json", "classifications.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 0 {
		t.Fatalf("unclassified methods: %+v", props)
	}
	if b := m.ByID("customers.recommendations.apply"); b == nil || b.Blocked == "" || b.Risk != manifest.RiskSpend {
		t.Fatalf("recommendations.apply %+v", b)
	}
	if b := m.ByID("customers.campaignBudgets.mutate"); b == nil || b.Mutate.Resource != "CampaignBudget" {
		t.Fatalf("campaignBudgets.mutate %+v", b)
	}
	if m.ByID("media.upload") != nil {
		t.Fatal("media.upload must be skiplisted")
	}
	if len(m.GenericOperations) < 60 {
		t.Fatalf("%d generic operation types", len(m.GenericOperations))
	}
}
