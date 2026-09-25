package manifest

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func gz(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(raw)
	zw.Close()
	return buf.Bytes()
}

func testManifest() *Manifest {
	return &Manifest{
		SchemaVersion: SchemaVersion, APIVersion: "v25",
		Methods: []Method{
			{ID: "customers.campaigns.mutate", HTTPMethod: "POST", Path: "v25/customers/{+customerId}/campaigns:mutate",
				PathParams: []Param{{Name: "customerId", Pattern: "^[^/]+$"}}},
			{ID: "customers.operations.list", HTTPMethod: "GET", Path: "v25/{+name}",
				PathParams: []Param{{Name: "name", Pattern: "^customers/[^/]+/operations$"}}},
			{ID: "customers.operations.get", HTTPMethod: "GET", Path: "v25/{+name}",
				PathParams: []Param{{Name: "name", Pattern: "^customers/[^/]+/operations/[^/]+$"}}},
			{ID: "customers.batchJobs.run", HTTPMethod: "POST", Path: "v25/{+resourceName}:run",
				PathParams: []Param{{Name: "resourceName", Pattern: "^customers/[^/]+/batchJobs/[^/]+$"}}},
			{ID: "customers.listAccessibleCustomers", HTTPMethod: "GET", Path: "v25/customers:listAccessibleCustomers"},
		},
		Schemas: map[string]Schema{
			"GoogleAdsGoogleadsV25Resources__Campaign":                {},
			"GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings": {},
			"GoogleAdsGoogleadsV25Services__Thing":                    {},
			"GoogleAdsGoogleadsV25Resources__Thing":                   {},
			"GoogleLongrunning__Operation":                            {},
		},
	}
}

func TestParseRoundTrip(t *testing.T) {
	m := testManifest()
	got, err := Parse(gz(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Methods, m.Methods) || got.APIVersion != "v25" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestParseRejectsOtherSchemaVersion(t *testing.T) {
	_, err := Parse(gz(t, map[string]any{"schema_version": 99}))
	if err == nil || !strings.Contains(err.Error(), "schema_version 99") {
		t.Fatalf("err = %v", err)
	}
}

func TestByID(t *testing.T) {
	m := testManifest()
	if got := m.ByID("customers.batchJobs.run"); got == nil || got.HTTPMethod != "POST" {
		t.Fatalf("got %+v", got)
	}
	if m.ByID("nope") != nil {
		t.Fatal("unknown id must be nil")
	}
}

func TestFind(t *testing.T) {
	m := testManifest()
	cases := []struct{ method, path, want string }{
		{"POST", "v25/customers/1234567890/campaigns:mutate", "customers.campaigns.mutate"},
		{"POST", "/v25/customers/1234567890/campaigns:mutate", "customers.campaigns.mutate"},
		// another API version maps onto this version's method
		{"POST", "v24/customers/1234567890/campaigns:mutate", "customers.campaigns.mutate"},
		// the Discovery pattern tells list and get apart on the same template
		{"GET", "v25/customers/1/operations", "customers.operations.list"},
		{"GET", "v25/customers/1/operations/abc", "customers.operations.get"},
		{"POST", "v25/customers/1/batchJobs/7:run", "customers.batchJobs.run"},
		{"GET", "v25/customers:listAccessibleCustomers", "customers.listAccessibleCustomers"},
		{"GET", "v25/customers/1234567890/campaigns:mutate", ""}, // wrong HTTP method
		{"POST", "v25/customers/1/unknown:mutate", ""},
		{"POST", "v25/customers/1/batchJobs/7/extra:run", ""},
	}
	for _, c := range cases {
		got := m.Find(c.method, c.path)
		id := ""
		if got != nil {
			id = got.ID
		}
		if id != c.want {
			t.Errorf("Find(%s %s) = %q, want %q", c.method, c.path, id, c.want)
		}
	}
}

func TestShortName(t *testing.T) {
	cases := map[string]string{
		"GoogleAdsGoogleadsV25Resources__Campaign":                "Campaign",
		"GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings": "Campaign_NetworkSettings",
		"GoogleAdsGoogleadsV25Common__ResponsiveSearchAdInfo":     "ResponsiveSearchAdInfo",
		"GoogleAdsGoogleadsV25Services__MutateOperation":          "MutateOperation",
		"GoogleLongrunning__Operation":                            "GoogleLongrunning__Operation",
	}
	for in, want := range cases {
		if got := ShortName(in); got != want {
			t.Errorf("ShortName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLookupSchema(t *testing.T) {
	m := testManifest()
	if id, _ := m.LookupSchema("Campaign"); id != "GoogleAdsGoogleadsV25Resources__Campaign" {
		t.Fatalf("short name: %q", id)
	}
	if id, _ := m.LookupSchema("Campaign_NetworkSettings"); id != "GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings" {
		t.Fatalf("nested short name: %q", id)
	}
	if id, _ := m.LookupSchema("GoogleAdsGoogleadsV25Services__Thing"); id != "GoogleAdsGoogleadsV25Services__Thing" {
		t.Fatalf("full name: %q", id)
	}
	id, cands := m.LookupSchema("Thing")
	if id != "" || len(cands) != 2 {
		t.Fatalf("ambiguous: id %q candidates %v", id, cands)
	}
	if id, cands := m.LookupSchema("Nope"); id != "" || len(cands) != 0 {
		t.Fatalf("unknown: %q %v", id, cands)
	}
}

func TestRiskRankAndForce(t *testing.T) {
	if !(RiskRank(RiskRead) < RiskRank(RiskWrite) && RiskRank(RiskWrite) < RiskRank(RiskSpend)) {
		t.Fatal("rank order")
	}
	for _, r := range []string{RiskDelete, RiskSpend, RiskAdmin} {
		if !NeedsForce(r) || RiskRank(r) != RiskRank(RiskSpend) {
			t.Fatalf("%s must need --force at the top rank", r)
		}
	}
	if NeedsForce(RiskRead) || NeedsForce(RiskWrite) {
		t.Fatal("read and write need no --force")
	}
}
