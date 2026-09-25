package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// riskClasses are the only classes the CLI understands. A typo in
// classifications.json must fail the build rather than ship as an
// unrecognized, and therefore unguarded, value.
var riskClasses = []string{manifest.RiskRead, manifest.RiskWrite, manifest.RiskDelete, manifest.RiskSpend, manifest.RiskAdmin}

func validRisk(r string) bool {
	for _, k := range riskClasses {
		if r == k {
			return true
		}
	}
	return false
}

func set(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

var (
	proposeSpend = set("customers.experiments.promoteExperiment", "customers.experiments.graduateExperiment",
		"customers.experiments.scheduleExperiment", "customers.campaignDrafts.promote", "customers.recommendations.apply",
		"customers.recommendationSubscriptions.mutateRecommendationSubscription", "customers.reservations.bookCampaigns")
	proposeDelete = set("customers.operations.delete", "customers.adGroupAds.removeAutomaticallyCreatedAssets",
		"customers.removeCampaignAutomaticallyCreatedAsset", "customers.youTubeVideoUploads.remove")
	proposeWrite = set("customers.recommendations.dismiss", "customers.experiments.endExperiment", "customers.operations.cancel",
		"customers.campaigns.enablePMaxBrandGuidelines", "customers.smartCampaignSettings.generatePMaxDraftCampaign",
		"customers.youTubeVideoUploads.update")
	adminMutateResources = set("customerUserAccesses", "customerUserAccessInvitations", "customerClientLinks",
		"customerManagerLinks", "billingSetups", "accountBudgetProposals", "accountLinks", "batchJobs")
	readVerb   = regexp.MustCompile(`^(search|get|list|generate|suggest|fetch|quote|wait)`)
	adminWords = regexp.MustCompile(`(?i)(upload|link|invitation|offlineUserData|batchJobs|identityVerification|incentive|leadConversation|leadFeedback|multiPartyAuth|createCustomerClient)`)
)

// proposeRisk guesses a class for a method nobody has classified yet. It is
// a starting point for review, never the final word: only
// classifications.json decides what ships.
func proposeRisk(id, httpMethod string) string {
	segs := strings.Split(id, ".")
	verb := segs[len(segs)-1]
	switch {
	case proposeSpend[id]:
		return manifest.RiskSpend
	case proposeDelete[id]:
		return manifest.RiskDelete
	case proposeWrite[id]:
		return manifest.RiskWrite
	case httpMethod == "GET", readVerb.MatchString(verb):
		return manifest.RiskRead
	case verb == "mutate" && len(segs) >= 2 && adminMutateResources[segs[len(segs)-2]]:
		return manifest.RiskAdmin
	case adminWords.MatchString(id):
		return manifest.RiskAdmin
	case verb == "mutate":
		return manifest.RiskWrite
	case strings.HasPrefix(verb, "remove"), httpMethod == "DELETE":
		return manifest.RiskDelete
	}
	return manifest.RiskWrite
}

// LoadClassifications reads the reviewed method id -> risk table.
func LoadClassifications(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}
