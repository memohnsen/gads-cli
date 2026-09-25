package main

import (
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

var testResults = map[string]string{"customers.operations.list": "operations"}

func build(t *testing.T, id string) manifest.Method {
	t.Helper()
	d := realDiscovery(t)
	m, err := BuildMethod(d, methodDef(t, d, id), nil, testResults)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBuildCampaignsMutate(t *testing.T) {
	m := build(t, "customers.campaigns.mutate")
	if m.Path != "v25/customers/{+customerId}/campaigns:mutate" || m.HTTPMethod != "POST" || !m.NeedsCustomer {
		t.Fatalf("%+v", m)
	}
	if !m.ValidateOnly || !m.PartialFailure || m.Paging != manifest.PagingNone {
		t.Fatalf("flags %+v", m)
	}
	if m.Mutate == nil || m.Mutate.OperationsField != "operations" || m.Mutate.Resource != "Campaign" {
		t.Fatalf("mutate %+v", m.Mutate)
	}
	if len(m.PathParams) != 1 || m.PathParams[0].Name != "customerId" || m.PathParams[0].Pattern == "" {
		t.Fatalf("path params %+v", m.PathParams)
	}
}

func TestBuildCustomerMutateSingleOperation(t *testing.T) {
	m := build(t, "customers.mutate")
	if m.Mutate == nil || m.Mutate.OperationsField != "operation" || m.Mutate.Resource != "Customer" {
		t.Fatalf("mutate %+v", m.Mutate)
	}
}

func TestBuildGenericMutate(t *testing.T) {
	m := build(t, "customers.googleAds.mutate")
	if m.Mutate == nil || m.Mutate.OperationsField != "mutateOperations" || m.Mutate.Resource != "" {
		t.Fatalf("mutate %+v", m.Mutate)
	}
}

func TestBuildSearchPaging(t *testing.T) {
	m := build(t, "customers.googleAds.search")
	if m.Paging != manifest.PagingBodyToken || m.ResultsField != "results" || m.Mutate != nil {
		t.Fatalf("%+v", m)
	}
}

func TestBuildQueryTokenPaging(t *testing.T) {
	m := build(t, "customers.operations.list")
	if m.Paging != manifest.PagingQueryToken || m.ResultsField != "operations" || m.NeedsCustomer {
		t.Fatalf("%+v", m)
	}
	if len(m.PathParams) != 1 || m.PathParams[0].Name != "name" || !strings.Contains(m.PathParams[0].Pattern, "operations") {
		t.Fatalf("path params %+v", m.PathParams)
	}
	names := map[string]bool{}
	for _, q := range m.QueryParams {
		names[q.Name] = true
	}
	if !names["pageToken"] || !names["filter"] {
		t.Fatalf("query params %+v", m.QueryParams)
	}
}

func TestBuildResultsFieldNeedsOverride(t *testing.T) {
	d := realDiscovery(t)
	_, err := BuildMethod(d, methodDef(t, d, "customers.operations.list"), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "results_fields") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildBatchJobsAddOperations(t *testing.T) {
	m := build(t, "customers.batchJobs.addOperations")
	if m.NeedsCustomer || m.Mutate == nil || m.Mutate.OperationsField != "mutateOperations" {
		t.Fatalf("%+v", m)
	}
	if m.PathParams[0].Name != "resourceName" || m.PathParams[0].Pattern != "^customers/[^/]+/batchJobs/[^/]+$" {
		t.Fatalf("path params %+v", m.PathParams)
	}
}

func TestBuildGET(t *testing.T) {
	m := build(t, "customers.listAccessibleCustomers")
	if m.HTTPMethod != "GET" || m.Request != "" || m.NeedsCustomer {
		t.Fatalf("%+v", m)
	}
}

func TestGenericOperations(t *testing.T) {
	d := realDiscovery(t)
	over := map[string]string{
		"bookCampaignsOperation":              "customers.reservations.bookCampaigns",
		"quoteCampaignsOperation":             "customers.reservations.quoteCampaigns",
		"recommendationSubscriptionOperation": "customers.recommendationSubscriptions.mutateRecommendationSubscription",
	}
	got, unmapped, err := GenericOperations(d, over)
	if err != nil || len(unmapped) != 0 {
		t.Fatalf("err %v unmapped %v", err, unmapped)
	}
	if got["campaignOperation"] != "customers.campaigns.mutate" || got["campaignBudgetOperation"] != "customers.campaignBudgets.mutate" {
		t.Fatalf("mapping %v", got)
	}
	if len(got) < 60 {
		t.Fatalf("only %d operation types", len(got))
	}
	_, unmapped, _ = GenericOperations(d, nil)
	if strings.Join(unmapped, ",") != "bookCampaignsOperation,quoteCampaignsOperation,recommendationSubscriptionOperation" {
		t.Fatalf("unmapped without overrides: %v", unmapped)
	}
}
