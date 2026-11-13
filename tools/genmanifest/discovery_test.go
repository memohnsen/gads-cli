package main

import (
	"regexp"
	"strings"
	"sync"
	"testing"
)

var (
	realOnce sync.Once
	realDoc  *Discovery
	realErr  error
)

// realDiscovery loads the vendored document once per test binary.
func realDiscovery(t *testing.T) *Discovery {
	t.Helper()
	realOnce.Do(func() { realDoc, realErr = LoadDiscovery(vendoredSpecPath) })
	if realErr != nil {
		t.Fatal(realErr)
	}
	return realDoc
}

func methodDef(t *testing.T, d *Discovery, id string) MethodDef {
	t.Helper()
	for _, m := range d.AllMethods() {
		if MethodID(m.ID) == id {
			return m
		}
	}
	t.Fatalf("no method %s", id)
	return MethodDef{}
}

func TestLoadDiscovery(t *testing.T) {
	d := realDiscovery(t)
	if !regexp.MustCompile(`^v[0-9]+$`).MatchString(d.Version) || d.Revision == "" {
		t.Fatalf("version %q revision %q", d.Version, d.Revision)
	}
	all := d.AllMethods()
	if len(all) < 150 {
		t.Fatalf("%d methods", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].ID > all[i].ID {
			t.Fatal("AllMethods is not sorted")
		}
	}
	if MethodID("googleads.customers.campaigns.mutate") != "customers.campaigns.mutate" {
		t.Fatal("MethodID")
	}
}

func TestStripMarkersAndFirstSentence(t *testing.T) {
	if got := stripMarkers("Immutable. The resource name. More."); got != "The resource name. More." {
		t.Fatalf("stripMarkers = %q", got)
	}
	if got := stripMarkers("Output only. Required. Text"); got != "Text" {
		t.Fatalf("stacked markers = %q", got)
	}
	if got := firstSentence("One  two.\nThree.", 200); got != "One two." {
		t.Fatalf("firstSentence = %q", got)
	}
	if got := firstSentence(strings.Repeat("a", 300), 20); len([]rune(got)) != 20 || !strings.HasSuffix(got, "...") {
		t.Fatalf("truncation = %q", got)
	}
}

func TestSummarizeCampaign(t *testing.T) {
	d := realDiscovery(t)
	prefix := "GoogleAdsGoogleads" + strings.ToUpper(d.Version[:1]) + d.Version[1:]
	s := SummarizeSchemas(d)[prefix+"Resources__Campaign"]
	byName := map[string]int{}
	for i, f := range s.Fields {
		byName[f.Name] = i
	}
	status := s.Fields[byName["status"]]
	if status.Type != "string" || !contains(status.Enum, "PAUSED") {
		t.Fatalf("status %+v", status)
	}
	if id := s.Fields[byName["id"]]; !id.ReadOnly || id.Format != "int64" {
		t.Fatalf("id %+v", id)
	}
	if rn := s.Fields[byName["resourceName"]]; !rn.Immutable || strings.HasPrefix(rn.Doc, "Immutable") {
		t.Fatalf("resourceName %+v", rn)
	}
	if ns := s.Fields[byName["networkSettings"]]; ns.Type != "object" || ns.Ref != prefix+"Resources_Campaign_NetworkSettings" {
		t.Fatalf("networkSettings %+v", ns)
	}
	for i := 1; i < len(s.Fields); i++ {
		if s.Fields[i-1].Name > s.Fields[i].Name {
			t.Fatal("fields are not sorted")
		}
	}
}

// Google stacks markers ("Required. Immutable. ..."). stripMarkers drops all
// of them from the doc, so each one must become a flag.
func TestSummarizeStackedMarkers(t *testing.T) {
	s := summarize(SchemaDef{Properties: map[string]PropertyDef{
		"appId":     {Type: "string", Description: "Required. Output only. The app."},
		"fieldType": {Type: "string", Description: "Required. Immutable. Role that the asset takes."},
	}})
	app, ft := s.Fields[0], s.Fields[1]
	if !app.Required || !app.ReadOnly || app.Immutable || app.Doc != "The app." {
		t.Fatalf("appId %+v", app)
	}
	if !ft.Required || !ft.Immutable || ft.ReadOnly || ft.Doc != "Role that the asset takes." {
		t.Fatalf("fieldType %+v", ft)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// vendoredSpecPath is the one Discovery document in spec/, whatever its version.
var vendoredSpecPath = func() string {
	p, err := VendoredSpec("../../spec")
	if err != nil {
		return "../../spec/missing.discovery.json"
	}
	return p
}()

func mustVendoredSpec(t *testing.T) string {
	t.Helper()
	p, err := VendoredSpec("../../spec")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
