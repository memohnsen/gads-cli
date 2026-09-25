package main

import (
	"reflect"
	"testing"
)

func TestKebab(t *testing.T) {
	cases := map[string]string{
		"adGroupCriteria":             "ad-group-criteria",
		"AdGroupCriterionCustomizers": "ad-group-criterion-customizers",
		"listAccessibleCustomers":     "list-accessible-customers",
		"mutate":                      "mutate",
		"googleAdsFields":             "google-ads-fields",
	}
	for in, want := range cases {
		if got := kebab(in); got != want {
			t.Errorf("kebab(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommandPath(t *testing.T) {
	over := map[string]string{"customers.googleAds.search": "search", "customers.mutate": "customer mutate"}
	cases := map[string][]string{
		"customers.campaigns.mutate":        {"campaigns", "mutate"},
		"customers.generateKeywordIdeas":    {"generate-keyword-ideas"},
		"customers.listAccessibleCustomers": {"list-accessible-customers"},
		"googleAdsFields.search":            {"google-ads-fields", "search"},
		"listPlannableLocations":            {"list-plannable-locations"},
		"customers.googleAds.search":        {"search"},
		"customers.mutate":                  {"customer", "mutate"},
	}
	for id, want := range cases {
		if got := CommandPath(id, over); !reflect.DeepEqual(got, want) {
			t.Errorf("CommandPath(%q) = %v, want %v", id, got, want)
		}
	}
}
