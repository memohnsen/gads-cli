package manifest

import "testing"

func TestLoadEmbedded(t *testing.T) {
	m, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if m.APIVersion != "v26" || len(m.Methods) < 150 {
		t.Fatalf("api %q, %d methods", m.APIVersion, len(m.Methods))
	}
	s := m.ByID("customers.googleAds.search")
	if s == nil || s.Paging != PagingBodyToken || s.Risk != RiskRead {
		t.Fatalf("search %+v", s)
	}
	if f := m.Find("POST", "v26/customers/1234567890/campaignBudgets:mutate"); f == nil || f.ID != "customers.campaignBudgets.mutate" {
		t.Fatalf("Find on the real manifest: %+v", f)
	}
}
