package main

import (
	"testing"
)

// The proposal rules reproduce today's reviewed table. When a reviewer
// overrides a proposal, add the method to the explicit sets in classify.go so
// the rules keep describing the table they seed.
func TestProposalsMatchReviewedTable(t *testing.T) {
	table, err := LoadClassifications("classifications.json")
	if err != nil {
		t.Fatal(err)
	}
	d := realDiscovery(t)
	for _, def := range d.AllMethods() {
		id := MethodID(def.ID)
		want, ok := table[id]
		if !ok {
			continue
		}
		if got := proposeRisk(id, def.HTTPMethod); got != want {
			t.Errorf("proposeRisk(%s) = %s, table says %s", id, got, want)
		}
	}
}

func TestValidRisk(t *testing.T) {
	for _, r := range []string{"read", "write", "delete", "spend", "admin"} {
		if !validRisk(r) {
			t.Errorf("%s must be valid", r)
		}
	}
	if validRisk("send") || validRisk("Write") {
		t.Fatal("unknown classes must be invalid")
	}
}
