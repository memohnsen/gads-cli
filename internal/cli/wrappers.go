package cli

// Hand-written wrappers for the everyday Search operations: list and get
// (GAQL), pause and enable, create and update of campaigns, ad groups,
// keywords, responsive search ads, assets, sitelinks, budgets and conversion
// actions. Each one only builds a request body or a query; the call itself
// goes through execute like every generated command, so the dry-run
// default, the gates, the budget cap and the audit log apply unchanged.

import (
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/config"
)

type obj = map[string]any

const (
	mCampaigns      = "customers.campaigns.mutate"
	mAdGroups       = "customers.adGroups.mutate"
	mAdGroupAds     = "customers.adGroupAds.mutate"
	mAds            = "customers.ads.mutate"
	mAdGroupCrit    = "customers.adGroupCriteria.mutate"
	mCampaignCrit   = "customers.campaignCriteria.mutate"
	mBudgets        = "customers.campaignBudgets.mutate"
	mAssets         = "customers.assets.mutate"
	mCampaignAssets = "customers.campaignAssets.mutate"
	mAdGroupAssets  = "customers.adGroupAssets.mutate"
	mConversions    = "customers.conversionActions.mutate"
	mGeneric        = "customers.googleAds.mutate"
	mSearch         = "customers.googleAds.search"
)

var (
	numericID  = regexp.MustCompile(`^[0-9]+$`)
	compoundID = regexp.MustCompile(`^([0-9]+)~([0-9]+)$`)
)

func needID(what, v string) error {
	if !numericID.MatchString(v) {
		return api.Usagef("%s must be a numeric ID, got %q", what, v)
	}
	return nil
}

// splitCompound reads "<adGroupId>~<id>", the form Google uses for ad group
// ads and ad group criteria.
func splitCompound(what, v string) (string, string, error) {
	m := compoundID.FindStringSubmatch(v)
	if m == nil {
		return "", "", api.Usagef("%s must be <adGroupId>~<id>, e.g. 123~456, got %q", what, v)
	}
	return m[1], m[2], nil
}

func rn(cid, collection, id string) string { return "customers/" + cid + "/" + collection + "/" + id }

func micros(flag, v string) (string, error) {
	m, err := config.ParseMoneyToMicros(v)
	if err != nil {
		return "", api.Usagef("--%s: %v", flag, err)
	}
	return fmt.Sprint(m), nil
}

// dispatch sends a mutate body to one manifest method through execute.
func (a *app) dispatch(cmd *cobra.Command, methodID string, body obj) error {
	meth := a.manifest.ByID(methodID)
	if meth == nil {
		return api.Usagef("this build's manifest lacks %s", methodID)
	}
	cid, err := a.customerID(cmd)
	if err != nil {
		return err
	}
	if err := a.applyLoginCustomerID(cmd); err != nil {
		return err
	}
	raw, err := jsonCompact(body)
	if err != nil {
		return api.Usagef("%v", err)
	}
	req := &api.Request{Method: meth.HTTPMethod, Path: strings.Replace(meth.Path, "{+customerId}", cid, 1), Body: raw, MethodID: meth.ID}
	return a.execute(cmd, execSpec{meth: meth, req: req, canValidate: meth.ValidateOnly})
}

// report runs a GAQL query through search, following every page.
func (a *app) report(cmd *cobra.Command, query string) error {
	meth := a.manifest.ByID(mSearch)
	if meth == nil {
		return api.Usagef("this build's manifest lacks %s", mSearch)
	}
	cid, err := a.customerID(cmd)
	if err != nil {
		return err
	}
	if err := a.applyLoginCustomerID(cmd); err != nil {
		return err
	}
	format := flagString(cmd, "format")
	if !validFormat(format) {
		return api.Usagef("--format wants json, csv or table, got %q", format)
	}
	raw, _ := jsonCompact(obj{"query": query})
	req := &api.Request{Method: meth.HTTPMethod, Path: strings.Replace(meth.Path, "{+customerId}", cid, 1), Body: raw, MethodID: meth.ID}
	return a.execute(cmd, execSpec{meth: meth, req: req, all: true, format: format})
}

func (a *app) wrapperCommands() []*cobra.Command {
	return []*cobra.Command{a.listCommand(), a.getCommand(), a.statusCommand("pause"), a.statusCommand("enable"),
		a.createCommand(), a.updateCommand(), a.linkCommand("link"), a.linkCommand("unlink")}
}

// ---- list and get ----------------------------------------------------------

type reportKind struct {
	name, single, from, fields string
	status                     string // status field, for --include-removed
	campaign, adGroup          string // filter fields
	where                      string // fixed condition
	idWhere                    func(id string) (string, error)
}

func simpleID(field string) func(string) (string, error) {
	return func(id string) (string, error) {
		if err := needID("id", id); err != nil {
			return "", err
		}
		return field + " = " + id, nil
	}
}

func compoundWhere(childField string) func(string) (string, error) {
	return func(id string) (string, error) {
		ag, child, err := splitCompound("id", id)
		if err != nil {
			return "", err
		}
		return "ad_group.id = " + ag + " AND " + childField + " = " + child, nil
	}
}

var reportKinds = []reportKind{
	{name: "campaigns", single: "campaign", from: "campaign",
		fields: "campaign.id, campaign.name, campaign.status, campaign.serving_status, campaign.advertising_channel_type, campaign.bidding_strategy_type, campaign.campaign_budget, campaign_budget.amount_micros, campaign.start_date_time, campaign.end_date_time",
		status: "campaign.status", campaign: "campaign.id", idWhere: simpleID("campaign.id")},
	{name: "ad-groups", single: "ad-group", from: "ad_group",
		fields: "ad_group.id, ad_group.name, ad_group.status, ad_group.type, ad_group.cpc_bid_micros, campaign.id, campaign.name",
		status: "ad_group.status", campaign: "campaign.id", adGroup: "ad_group.id", idWhere: simpleID("ad_group.id")},
	{name: "keywords", single: "keyword", from: "ad_group_criterion",
		fields: "ad_group_criterion.criterion_id, ad_group_criterion.keyword.text, ad_group_criterion.keyword.match_type, ad_group_criterion.status, ad_group_criterion.negative, ad_group_criterion.cpc_bid_micros, ad_group_criterion.final_urls, ad_group.id, campaign.id",
		status: "ad_group_criterion.status", campaign: "campaign.id", adGroup: "ad_group.id",
		where: "ad_group_criterion.type = 'KEYWORD'", idWhere: compoundWhere("ad_group_criterion.criterion_id")},
	{name: "negatives", single: "negative", from: "campaign_criterion",
		fields: "campaign_criterion.criterion_id, campaign_criterion.keyword.text, campaign_criterion.keyword.match_type, campaign_criterion.status, campaign.id",
		status: "campaign_criterion.status", campaign: "campaign.id",
		where: "campaign_criterion.type = 'KEYWORD' AND campaign_criterion.negative = TRUE", idWhere: simpleID("campaign_criterion.criterion_id")},
	{name: "ads", single: "ad", from: "ad_group_ad",
		fields: "ad_group_ad.ad.id, ad_group_ad.status, ad_group_ad.ad.type, ad_group_ad.ad.final_urls, ad_group_ad.ad.responsive_search_ad.headlines, ad_group_ad.ad.responsive_search_ad.descriptions, ad_group_ad.ad.responsive_search_ad.path1, ad_group_ad.ad.responsive_search_ad.path2, ad_group_ad.policy_summary.approval_status, ad_group.id, campaign.id",
		status: "ad_group_ad.status", campaign: "campaign.id", adGroup: "ad_group.id", idWhere: compoundWhere("ad_group_ad.ad.id")},
	{name: "budgets", single: "budget", from: "campaign_budget",
		fields: "campaign_budget.id, campaign_budget.name, campaign_budget.amount_micros, campaign_budget.status, campaign_budget.delivery_method, campaign_budget.explicitly_shared, campaign_budget.reference_count",
		status: "campaign_budget.status", idWhere: simpleID("campaign_budget.id")},
	{name: "assets", single: "asset", from: "asset",
		fields:  "asset.id, asset.name, asset.type, asset.text_asset.text, asset.image_asset.full_size.url, asset.final_urls",
		idWhere: simpleID("asset.id")},
	{name: "sitelinks", single: "sitelink", from: "asset",
		fields: "asset.id, asset.sitelink_asset.link_text, asset.sitelink_asset.description1, asset.sitelink_asset.description2, asset.final_urls",
		where:  "asset.type = 'SITELINK'", idWhere: simpleID("asset.id")},
	{name: "sitelink-links", single: "", from: "campaign_asset",
		fields: "campaign_asset.campaign, campaign_asset.asset, campaign_asset.status, campaign.id, asset.id, asset.sitelink_asset.link_text",
		status: "campaign_asset.status", campaign: "campaign.id", where: "campaign_asset.field_type = 'SITELINK'"},
	{name: "conversion-actions", single: "conversion-action", from: "conversion_action",
		fields: "conversion_action.id, conversion_action.name, conversion_action.status, conversion_action.type, conversion_action.category, conversion_action.primary_for_goal, conversion_action.counting_type",
		status: "conversion_action.status", idWhere: simpleID("conversion_action.id")},
}

func (k reportKind) query(conds []string) string {
	q := "SELECT " + k.fields + " FROM " + k.from
	if k.where != "" {
		conds = append([]string{k.where}, conds...)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	return q
}

func addFormat(cmd *cobra.Command) {
	cmd.Flags().String("format", "json", "output format: json (one array of rows), csv or table")
	cmd.Flags().Int("max-pages", 100, "page cap; hitting it ends the run as incomplete")
}

func (a *app) listCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "list", Short: "List campaigns, ad groups, keywords, ads, budgets, assets, sitelinks or conversion actions (GAQL)", RunE: groupRunE}
	for _, k := range reportKinds {
		sub := &cobra.Command{
			Use: k.name, Short: "List " + strings.ReplaceAll(k.name, "-", " ") + " (prints the GAQL it runs with --show-query)", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				var conds []string
				for _, f := range []struct{ flag, field string }{{"campaign-id", k.campaign}, {"ad-group-id", k.adGroup}} {
					v := flagString(cmd, f.flag)
					if v == "" || f.field == "" {
						continue
					}
					if err := needID("--"+f.flag, v); err != nil {
						return err
					}
					conds = append(conds, f.field+" = "+v)
				}
				if k.status != "" && !flagBool(cmd, "include-removed") {
					conds = append(conds, k.status+" != 'REMOVED'")
				}
				q := k.query(conds)
				if flagBool(cmd, "show-query") {
					fmt.Fprintln(a.stdout, q)
					return nil
				}
				return a.report(cmd, q)
			},
		}
		if k.campaign != "" {
			sub.Flags().String("campaign-id", "", "only this campaign")
		}
		if k.adGroup != "" {
			sub.Flags().String("ad-group-id", "", "only this ad group")
		}
		if k.status != "" {
			sub.Flags().Bool("include-removed", false, "include removed items")
		}
		sub.Flags().Bool("show-query", false, "print the GAQL instead of running it")
		addFormat(sub)
		cmd.AddCommand(sub)
	}
	return cmd
}

func (a *app) getCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "get", Short: "Get one campaign, ad group, keyword, ad, budget, asset or conversion action (GAQL)", RunE: groupRunE}
	for _, k := range reportKinds {
		if k.single == "" {
			continue
		}
		idForm := "<id>"
		if k.name == "keywords" || k.name == "ads" {
			idForm = "<adGroupId>~<id>"
		}
		sub := &cobra.Command{
			Use: k.single + " " + idForm, Short: "Get one " + strings.ReplaceAll(k.single, "-", " "), Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cond, err := k.idWhere(args[0])
				if err != nil {
					return err
				}
				return a.report(cmd, k.query([]string{cond}))
			},
		}
		addFormat(sub)
		cmd.AddCommand(sub)
	}
	return cmd
}

// ---- pause and enable ------------------------------------------------------

// statusTarget maps a kind to its mutate method and the resource name its ID
// form gives.
func statusTarget(kind, cid, id string) (method, name string, err error) {
	switch kind {
	case "campaign":
		return mCampaigns, rn(cid, "campaigns", id), needID("campaign ID", id)
	case "ad-group":
		return mAdGroups, rn(cid, "adGroups", id), needID("ad group ID", id)
	case "ad":
		ag, ad, err := splitCompound("ad ID", id)
		return mAdGroupAds, rn(cid, "adGroupAds", ag+"~"+ad), err
	case "keyword":
		ag, c, err := splitCompound("keyword ID", id)
		return mAdGroupCrit, rn(cid, "adGroupCriteria", ag+"~"+c), err
	}
	return "", "", api.Usagef("unknown kind %q", kind)
}

func (a *app) statusCommand(verb string) *cobra.Command {
	status, short := "PAUSED", "Pause a campaign, ad group, ad or keyword (a write: dry run unless --apply)"
	if verb == "enable" {
		status, short = "ENABLED", "Enable a campaign, ad group, ad or keyword (spend: --apply --confirm-spend)"
	}
	cmd := &cobra.Command{Use: verb, Short: short, RunE: groupRunE}
	for _, k := range []struct{ kind, idForm string }{
		{"campaign", "<campaignId>"}, {"ad-group", "<adGroupId>"}, {"ad", "<adGroupId>~<adId>"}, {"keyword", "<adGroupId>~<criterionId>"},
	} {
		cmd.AddCommand(&cobra.Command{
			Use: k.kind + " " + k.idForm, Short: strings.ToUpper(verb[:1]) + verb[1:] + " one " + strings.ReplaceAll(k.kind, "-", " "), Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cid, err := a.customerID(cmd)
				if err != nil {
					return err
				}
				method, name, err := statusTarget(k.kind, cid, args[0])
				if err != nil {
					return err
				}
				return a.dispatch(cmd, method, obj{"operations": []any{obj{
					"update": obj{"resourceName": name, "status": status}, "updateMask": "status"}}})
			},
		})
	}
	return cmd
}

// ---- create ----------------------------------------------------------------

func (a *app) createCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "create", Short: "Create campaigns, ad groups, keywords, RSAs, assets, sitelinks, budgets, conversion actions (always PAUSED)", RunE: groupRunE}
	cmd.AddCommand(a.createCampaign(), a.createAdGroup(), a.createKeywords(), a.createRSA(), a.createSitelink(),
		a.createTextAsset(), a.createImageAsset(), a.createBudget(), a.createConversionAction())
	return cmd
}

// biddingFlags adds the bidding choice to a campaign command.
func biddingFlags(cmd *cobra.Command, def string) {
	cmd.Flags().String("bidding", def, "bid strategy: manual-cpc, maximize-clicks, maximize-conversions or maximize-conversion-value")
	cmd.Flags().String("max-cpc", "", "maximize-clicks: CPC bid ceiling, in the account currency")
	cmd.Flags().String("target-cpa", "", "maximize-conversions: target cost per action, in the account currency")
	cmd.Flags().Float64("target-roas", 0, "maximize-conversion-value: target return on ad spend (e.g. 3.5 for 350%)")
}

// bidding returns the campaign field and value for --bidding.
func bidding(cmd *cobra.Command) (string, any, error) {
	switch b := flagString(cmd, "bidding"); b {
	case "manual-cpc":
		return "manualCpc", obj{}, nil
	case "maximize-clicks":
		v := obj{}
		if c := flagString(cmd, "max-cpc"); c != "" {
			m, err := micros("max-cpc", c)
			if err != nil {
				return "", nil, err
			}
			v["cpcBidCeilingMicros"] = m
		}
		return "targetSpend", v, nil
	case "maximize-conversions":
		v := obj{}
		if c := flagString(cmd, "target-cpa"); c != "" {
			m, err := micros("target-cpa", c)
			if err != nil {
				return "", nil, err
			}
			v["targetCpaMicros"] = m
		}
		return "maximizeConversions", v, nil
	case "maximize-conversion-value":
		v := obj{}
		if r, _ := cmd.Flags().GetFloat64("target-roas"); r > 0 {
			v["targetRoas"] = r
		}
		return "maximizeConversionValue", v, nil
	default:
		return "", nil, api.Usagef("--bidding wants manual-cpc, maximize-clicks, maximize-conversions or maximize-conversion-value, got %q", b)
	}
}

func (a *app) createCampaign() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "campaign",
		Short: "Create a PAUSED Search campaign (with a new budget, or on an existing one)",
		Long: "Create a PAUSED Search campaign in one request: optionally a new daily budget (--budget-amount,\n" +
			"spend-class, held to the budget cap), the campaign, and its location targets. Without --apply it\n" +
			"is a dry run. A new budget makes the call spend-class: dry-run it with --validate-only, apply it\n" +
			"with --apply --confirm-spend. With --budget-id it is a plain write.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cid, err := a.customerID(cmd)
			if err != nil {
				return err
			}
			name := flagString(cmd, "name")
			if name == "" {
				return api.Usagef("--name is required")
			}
			budgetID, budgetAmount := flagString(cmd, "budget-id"), flagString(cmd, "budget-amount")
			if (budgetID == "") == (budgetAmount == "") {
				return api.Usagef("give exactly one of --budget-id (an existing budget) or --budget-amount (a new daily budget)")
			}
			var ops []any
			budget := rn(cid, "campaignBudgets", budgetID)
			if budgetAmount != "" {
				m, err := micros("budget-amount", budgetAmount)
				if err != nil {
					return err
				}
				budget = rn(cid, "campaignBudgets", "-1")
				ops = append(ops, obj{"campaignBudgetOperation": obj{"create": obj{
					"resourceName": budget, "name": name + " budget", "amountMicros": m,
					"deliveryMethod": "STANDARD", "explicitlyShared": false}}})
			} else if err := needID("--budget-id", budgetID); err != nil {
				return err
			}
			field, strategy, err := bidding(cmd)
			if err != nil {
				return err
			}
			eu := "DOES_NOT_CONTAIN_EU_POLITICAL_ADVERTISING"
			if flagBool(cmd, "eu-political-advertising") {
				eu = "CONTAINS_EU_POLITICAL_ADVERTISING"
			}
			campaign := rn(cid, "campaigns", "-2")
			c := obj{
				"resourceName": campaign, "name": name, "status": "PAUSED", "advertisingChannelType": "SEARCH",
				"campaignBudget": budget, field: strategy, "containsEuPoliticalAdvertising": eu,
				"networkSettings": obj{"targetGoogleSearch": true, "targetSearchNetwork": flagBool(cmd, "search-partners"),
					"targetContentNetwork": flagBool(cmd, "display-network"), "targetPartnerSearchNetwork": false},
			}
			if v := flagString(cmd, "final-url-suffix"); v != "" {
				c["finalUrlSuffix"] = v
			}
			ops = append(ops, obj{"campaignOperation": obj{"create": c}})
			locs, _ := cmd.Flags().GetStringSlice("location")
			for _, l := range locs {
				if err := needID("--location", l); err != nil {
					return err
				}
				ops = append(ops, obj{"campaignCriterionOperation": obj{"create": obj{
					"campaign": campaign, "location": obj{"geoTargetConstant": "geoTargetConstants/" + l}}}})
			}
			return a.dispatch(cmd, mGeneric, obj{"mutateOperations": ops})
		},
	}
	cmd.Flags().String("name", "", "campaign name (required)")
	cmd.Flags().String("budget-id", "", "existing campaign budget ID")
	cmd.Flags().String("budget-amount", "", "create a new daily budget of this amount, in the account currency")
	biddingFlags(cmd, "manual-cpc")
	cmd.Flags().Bool("search-partners", false, "also serve on Google search partners")
	cmd.Flags().Bool("display-network", false, "also serve on the Display Network")
	cmd.Flags().StringSlice("location", nil, "geo target constant ID to target, repeatable (e.g. 2840 for the United States)")
	cmd.Flags().Bool("eu-political-advertising", false, "declare that the campaign contains EU political advertising")
	cmd.Flags().String("final-url-suffix", "", "final URL suffix (tracking parameters)")
	return cmd
}

func (a *app) createAdGroup() *cobra.Command {
	cmd := &cobra.Command{
		Use: "ad-group", Short: "Create a PAUSED Search ad group", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cid, err := a.customerID(cmd)
			if err != nil {
				return err
			}
			camp, name := flagString(cmd, "campaign-id"), flagString(cmd, "name")
			if err := needID("--campaign-id", camp); err != nil {
				return err
			}
			if name == "" {
				return api.Usagef("--name is required")
			}
			g := obj{"campaign": rn(cid, "campaigns", camp), "name": name, "status": "PAUSED", "type": "SEARCH_STANDARD"}
			if v := flagString(cmd, "cpc-bid"); v != "" {
				m, err := micros("cpc-bid", v)
				if err != nil {
					return err
				}
				g["cpcBidMicros"] = m
			}
			return a.dispatch(cmd, mAdGroups, obj{"operations": []any{obj{"create": g}}})
		},
	}
	cmd.Flags().String("campaign-id", "", "campaign ID (required)")
	cmd.Flags().String("name", "", "ad group name (required)")
	cmd.Flags().String("cpc-bid", "", "default max CPC, in the account currency")
	return cmd
}

var matchTypes = map[string]bool{"EXACT": true, "PHRASE": true, "BROAD": true}

func (a *app) createKeywords() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keywords",
		Short: "Add PAUSED keywords to an ad group, or negative keywords to an ad group or campaign",
		Long: "Add keywords. Positive keywords go to --ad-group-id and are created PAUSED. With --negative they\n" +
			"are negative keywords, on --ad-group-id or on --campaign-id (negatives have no status).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cid, err := a.customerID(cmd)
			if err != nil {
				return err
			}
			texts, _ := cmd.Flags().GetStringArray("keyword")
			if len(texts) == 0 {
				return api.Usagef("give at least one --keyword")
			}
			mt := strings.ToUpper(flagString(cmd, "match-type"))
			if !matchTypes[mt] {
				return api.Usagef("--match-type wants EXACT, PHRASE or BROAD, got %q", mt)
			}
			negative := flagBool(cmd, "negative")
			ag, camp := flagString(cmd, "ad-group-id"), flagString(cmd, "campaign-id")
			bid := flagString(cmd, "cpc-bid")
			switch {
			case camp != "" && ag != "":
				return api.Usagef("give --ad-group-id or --campaign-id, not both")
			case camp != "" && !negative:
				return api.Usagef("--campaign-id takes only negative keywords (--negative); positive keywords belong to an ad group")
			case camp == "" && ag == "":
				return api.Usagef("--ad-group-id is required (or --campaign-id with --negative)")
			case negative && bid != "":
				return api.Usagef("negative keywords take no --cpc-bid")
			}
			var ops []any
			for _, text := range texts {
				kw := obj{"text": text, "matchType": mt}
				if camp != "" {
					if err := needID("--campaign-id", camp); err != nil {
						return err
					}
					ops = append(ops, obj{"create": obj{"campaign": rn(cid, "campaigns", camp), "negative": true, "keyword": kw}})
					continue
				}
				if err := needID("--ad-group-id", ag); err != nil {
					return err
				}
				c := obj{"adGroup": rn(cid, "adGroups", ag), "keyword": kw}
				if negative {
					c["negative"] = true
				} else {
					c["status"] = "PAUSED"
					if bid != "" {
						m, err := micros("cpc-bid", bid)
						if err != nil {
							return err
						}
						c["cpcBidMicros"] = m
					}
					if u := flagString(cmd, "final-url"); u != "" {
						c["finalUrls"] = []string{u}
					}
				}
				ops = append(ops, obj{"create": c})
			}
			method := mAdGroupCrit
			if camp != "" {
				method = mCampaignCrit
			}
			return a.dispatch(cmd, method, obj{"operations": ops})
		},
	}
	cmd.Flags().String("ad-group-id", "", "ad group ID")
	cmd.Flags().String("campaign-id", "", "campaign ID (negative keywords only)")
	cmd.Flags().StringArray("keyword", nil, "keyword text, repeatable")
	cmd.Flags().String("match-type", "PHRASE", "EXACT, PHRASE or BROAD")
	cmd.Flags().Bool("negative", false, "add negative keywords")
	cmd.Flags().String("cpc-bid", "", "max CPC for these keywords, in the account currency")
	cmd.Flags().String("final-url", "", "keyword-level final URL")
	return cmd
}

func textAssets(texts []string) []any {
	out := make([]any, 0, len(texts))
	for _, t := range texts {
		out = append(out, obj{"text": t})
	}
	return out
}

func rsaFlags(cmd *cobra.Command) {
	cmd.Flags().StringArray("headline", nil, "headline (max 30 characters), repeatable: 3 to 15")
	cmd.Flags().StringArray("description", nil, "description (max 90 characters), repeatable: 2 to 4")
	cmd.Flags().String("final-url", "", "landing page URL")
	cmd.Flags().String("path1", "", "display URL path 1 (max 15 characters)")
	cmd.Flags().String("path2", "", "display URL path 2 (max 15 characters)")
}

func checkRSA(headlines, descriptions []string, partial bool) error {
	if (!partial || len(headlines) > 0) && (len(headlines) < 3 || len(headlines) > 15) {
		return api.Usagef("a responsive search ad takes 3 to 15 --headline values, got %d", len(headlines))
	}
	if (!partial || len(descriptions) > 0) && (len(descriptions) < 2 || len(descriptions) > 4) {
		return api.Usagef("a responsive search ad takes 2 to 4 --description values, got %d", len(descriptions))
	}
	return nil
}

func (a *app) createRSA() *cobra.Command {
	cmd := &cobra.Command{
		Use: "rsa", Short: "Create a PAUSED responsive search ad", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cid, err := a.customerID(cmd)
			if err != nil {
				return err
			}
			ag := flagString(cmd, "ad-group-id")
			if err := needID("--ad-group-id", ag); err != nil {
				return err
			}
			hs, _ := cmd.Flags().GetStringArray("headline")
			ds, _ := cmd.Flags().GetStringArray("description")
			if err := checkRSA(hs, ds, false); err != nil {
				return err
			}
			url := flagString(cmd, "final-url")
			if url == "" {
				return api.Usagef("--final-url is required")
			}
			rsa := obj{"headlines": textAssets(hs), "descriptions": textAssets(ds)}
			for _, p := range []string{"path1", "path2"} {
				if v := flagString(cmd, p); v != "" {
					rsa[p] = v
				}
			}
			return a.dispatch(cmd, mAdGroupAds, obj{"operations": []any{obj{"create": obj{
				"adGroup": rn(cid, "adGroups", ag), "status": "PAUSED",
				"ad": obj{"finalUrls": []string{url}, "responsiveSearchAd": rsa}}}}})
		},
	}
	cmd.Flags().String("ad-group-id", "", "ad group ID (required)")
	rsaFlags(cmd)
	return cmd
}

func sitelinkAsset(cmd *cobra.Command, resourceName string) (obj, error) {
	text, url := flagString(cmd, "link-text"), flagString(cmd, "final-url")
	if text == "" || url == "" {
		return nil, api.Usagef("--link-text and --final-url are required")
	}
	sl := obj{"linkText": text}
	for _, d := range []string{"description1", "description2"} {
		if v := flagString(cmd, d); v != "" {
			sl[d] = v
		}
	}
	a := obj{"sitelinkAsset": sl, "finalUrls": []string{url}}
	if resourceName != "" {
		a["resourceName"] = resourceName
	}
	return a, nil
}

// linkOp builds the campaign or ad group asset link for an asset.
func linkOp(cmd *cobra.Command, cid, asset, fieldType string) (string, obj, error) {
	camp, ag := flagString(cmd, "campaign-id"), flagString(cmd, "ad-group-id")
	switch {
	case camp != "" && ag != "":
		return "", nil, api.Usagef("give --campaign-id or --ad-group-id, not both")
	case camp != "":
		if err := needID("--campaign-id", camp); err != nil {
			return "", nil, err
		}
		return "campaignAssetOperation", obj{"campaign": rn(cid, "campaigns", camp), "asset": asset, "fieldType": fieldType}, nil
	case ag != "":
		if err := needID("--ad-group-id", ag); err != nil {
			return "", nil, err
		}
		return "adGroupAssetOperation", obj{"adGroup": rn(cid, "adGroups", ag), "asset": asset, "fieldType": fieldType}, nil
	}
	return "", nil, nil
}

func (a *app) createSitelink() *cobra.Command {
	cmd := &cobra.Command{
		Use: "sitelink", Short: "Create a sitelink asset, optionally linked to a campaign or ad group", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cid, err := a.customerID(cmd)
			if err != nil {
				return err
			}
			asset := rn(cid, "assets", "-1")
			sl, err := sitelinkAsset(cmd, asset)
			if err != nil {
				return err
			}
			ops := []any{obj{"assetOperation": obj{"create": sl}}}
			typ, link, err := linkOp(cmd, cid, asset, "SITELINK")
			if err != nil {
				return err
			}
			if link != nil {
				ops = append(ops, obj{typ: obj{"create": link}})
			}
			return a.dispatch(cmd, mGeneric, obj{"mutateOperations": ops})
		},
	}
	cmd.Flags().String("link-text", "", "sitelink text (max 25 characters, required)")
	cmd.Flags().String("final-url", "", "sitelink URL (required)")
	cmd.Flags().String("description1", "", "description line 1 (max 35 characters)")
	cmd.Flags().String("description2", "", "description line 2 (max 35 characters)")
	cmd.Flags().String("campaign-id", "", "link the sitelink to this campaign")
	cmd.Flags().String("ad-group-id", "", "link the sitelink to this ad group")
	return cmd
}

func (a *app) createTextAsset() *cobra.Command {
	cmd := &cobra.Command{
		Use: "text-asset", Short: "Create a text asset", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			text := flagString(cmd, "text")
			if text == "" {
				return api.Usagef("--text is required")
			}
			asset := obj{"textAsset": obj{"text": text}}
			if n := flagString(cmd, "name"); n != "" {
				asset["name"] = n
			}
			return a.dispatch(cmd, mAssets, obj{"operations": []any{obj{"create": asset}}})
		},
	}
	cmd.Flags().String("text", "", "asset text (required)")
	cmd.Flags().String("name", "", "asset name")
	return cmd
}

// maxImageBytes is Google's limit for an image asset.
const maxImageBytes = 5 << 20

func (a *app) createImageAsset() *cobra.Command {
	cmd := &cobra.Command{
		Use: "image-asset", Short: "Create an image asset from a local file (PNG, JPEG or GIF, at most 5 MB)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, name := flagString(cmd, "file"), flagString(cmd, "name")
			if path == "" || name == "" {
				return api.Usagef("--file and --name are required")
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return api.Usagef("--file: %v", err)
			}
			if len(raw) > maxImageBytes {
				return api.Usagef("--file is %d bytes; image assets are at most 5 MB", len(raw))
			}
			return a.dispatch(cmd, mAssets, obj{"operations": []any{obj{"create": obj{
				"name": name, "imageAsset": obj{"data": base64.StdEncoding.EncodeToString(raw)}}}}})
		},
	}
	cmd.Flags().String("file", "", "image file (required)")
	cmd.Flags().String("name", "", "asset name (required)")
	return cmd
}

func (a *app) createBudget() *cobra.Command {
	cmd := &cobra.Command{
		Use: "budget", Short: "Create a daily campaign budget (spend: held to the budget cap, --apply --confirm-spend)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, amount := flagString(cmd, "name"), flagString(cmd, "amount")
			if name == "" || amount == "" {
				return api.Usagef("--name and --amount are required")
			}
			m, err := micros("amount", amount)
			if err != nil {
				return err
			}
			return a.dispatch(cmd, mBudgets, obj{"operations": []any{obj{"create": obj{
				"name": name, "amountMicros": m, "deliveryMethod": "STANDARD", "explicitlyShared": flagBool(cmd, "shared")}}}})
		},
	}
	cmd.Flags().String("name", "", "budget name (required)")
	cmd.Flags().String("amount", "", "daily amount in the account currency (required)")
	cmd.Flags().Bool("shared", false, "make the budget shareable between campaigns")
	return cmd
}

func (a *app) createConversionAction() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conversion-action",
		Short: "Create a conversion action (secondary by default; --primary feeds bidding and is spend-class)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := flagString(cmd, "name")
			if name == "" {
				return api.Usagef("--name is required")
			}
			ca := obj{"name": name, "type": strings.ToUpper(flagString(cmd, "type")),
				"category": strings.ToUpper(flagString(cmd, "category")), "primaryForGoal": flagBool(cmd, "primary")}
			if v := flagString(cmd, "counting-type"); v != "" {
				ca["countingType"] = strings.ToUpper(v)
			}
			if v := flagString(cmd, "default-value"); v != "" {
				var f float64
				if _, err := fmt.Sscan(v, &f); err != nil || f < 0 {
					return api.Usagef("--default-value wants a number, got %q", v)
				}
				ca["valueSettings"] = obj{"defaultValue": f, "alwaysUseDefaultValue": flagBool(cmd, "always-use-default-value")}
			}
			return a.dispatch(cmd, mConversions, obj{"operations": []any{obj{"create": ca}}})
		},
	}
	cmd.Flags().String("name", "", "conversion action name (required)")
	cmd.Flags().String("type", "WEBPAGE", "conversion action type (WEBPAGE, UPLOAD_CLICKS, ...)")
	cmd.Flags().String("category", "DEFAULT", "category (DEFAULT, PURCHASE, SIGNUP, SUBMIT_LEAD_FORM, ...)")
	cmd.Flags().String("counting-type", "", "ONE_PER_CLICK or MANY_PER_CLICK")
	cmd.Flags().String("default-value", "", "default conversion value")
	cmd.Flags().Bool("always-use-default-value", false, "always use the default value")
	cmd.Flags().Bool("primary", false, "make it a primary action, used for bidding (spend-class)")
	return cmd
}

// ---- update ----------------------------------------------------------------

// update builds one update operation from the flags that were given: each
// entry maps a changed flag to its field path and value.
type change struct {
	path  string // update mask path, snake_case
	field []string
	value any
}

func updateOp(name string, changes []change) (obj, error) {
	if len(changes) == 0 {
		return nil, api.Usagef("nothing to update: give at least one field flag")
	}
	body := obj{"resourceName": name}
	var mask []string
	for _, c := range changes {
		cur := body
		for _, f := range c.field[:len(c.field)-1] {
			next, ok := cur[f].(obj)
			if !ok {
				next = obj{}
				cur[f] = next
			}
			cur = next
		}
		cur[c.field[len(c.field)-1]] = c.value
		mask = append(mask, c.path)
	}
	sort.Strings(mask)
	return obj{"operations": []any{obj{"update": body, "updateMask": strings.Join(mask, ",")}}}, nil
}

func moneyChange(cmd *cobra.Command, flag, path string, field ...string) ([]change, error) {
	if !cmd.Flags().Changed(flag) {
		return nil, nil
	}
	m, err := micros(flag, flagString(cmd, flag))
	if err != nil {
		return nil, err
	}
	return []change{{path, field, m}}, nil
}

func stringChange(cmd *cobra.Command, flag, path string, field ...string) []change {
	if !cmd.Flags().Changed(flag) {
		return nil
	}
	return []change{{path, field, flagString(cmd, flag)}}
}

func (a *app) updateCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "update", Short: "Update campaigns, ad groups, keywords, RSAs, budgets and conversion actions", RunE: groupRunE}

	campaign := &cobra.Command{
		Use: "campaign <campaignId>", Short: "Update a campaign's name, budget or bidding (budget and bidding are spend-class)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cid, err := a.customerID(cmd)
			if err != nil {
				return err
			}
			if err := needID("campaign ID", args[0]); err != nil {
				return err
			}
			changes := stringChange(cmd, "name", "name", "name")
			if cmd.Flags().Changed("budget-id") {
				b := flagString(cmd, "budget-id")
				if err := needID("--budget-id", b); err != nil {
					return err
				}
				changes = append(changes, change{"campaign_budget", []string{"campaignBudget"}, rn(cid, "campaignBudgets", b)})
			}
			if cmd.Flags().Changed("bidding") {
				field, strategy, err := bidding(cmd)
				if err != nil {
					return err
				}
				snake := map[string]string{"manualCpc": "manual_cpc", "targetSpend": "target_spend",
					"maximizeConversions": "maximize_conversions", "maximizeConversionValue": "maximize_conversion_value"}[field]
				changes = append(changes, change{snake, []string{field}, strategy})
			}
			body, err := updateOp(rn(cid, "campaigns", args[0]), changes)
			if err != nil {
				return err
			}
			return a.dispatch(cmd, mCampaigns, body)
		},
	}
	campaign.Flags().String("name", "", "new name")
	campaign.Flags().String("budget-id", "", "move the campaign to this budget (spend-class)")
	biddingFlags(campaign, "")

	adGroup := &cobra.Command{
		Use: "ad-group <adGroupId>", Short: "Update an ad group's name or default bid (a bid is spend-class)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cid, err := a.customerID(cmd)
			if err != nil {
				return err
			}
			if err := needID("ad group ID", args[0]); err != nil {
				return err
			}
			changes := stringChange(cmd, "name", "name", "name")
			bid, err := moneyChange(cmd, "cpc-bid", "cpc_bid_micros", "cpcBidMicros")
			if err != nil {
				return err
			}
			body, err := updateOp(rn(cid, "adGroups", args[0]), append(changes, bid...))
			if err != nil {
				return err
			}
			return a.dispatch(cmd, mAdGroups, body)
		},
	}
	adGroup.Flags().String("name", "", "new name")
	adGroup.Flags().String("cpc-bid", "", "new default max CPC, in the account currency (spend-class)")

	keyword := &cobra.Command{
		Use: "keyword <adGroupId>~<criterionId>", Short: "Update a keyword's bid (spend-class) or final URL", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cid, err := a.customerID(cmd)
			if err != nil {
				return err
			}
			ag, c, err := splitCompound("keyword ID", args[0])
			if err != nil {
				return err
			}
			changes, err := moneyChange(cmd, "cpc-bid", "cpc_bid_micros", "cpcBidMicros")
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("final-url") {
				changes = append(changes, change{"final_urls", []string{"finalUrls"}, []string{flagString(cmd, "final-url")}})
			}
			body, err := updateOp(rn(cid, "adGroupCriteria", ag+"~"+c), changes)
			if err != nil {
				return err
			}
			return a.dispatch(cmd, mAdGroupCrit, body)
		},
	}
	keyword.Flags().String("cpc-bid", "", "new max CPC, in the account currency (spend-class)")
	keyword.Flags().String("final-url", "", "new keyword-level final URL")

	rsa := &cobra.Command{
		Use:   "rsa <adId>",
		Short: "Replace an RSA's headlines, descriptions, final URL or paths",
		Long: "Update a responsive search ad through the ads service. --headline and --description replace the\n" +
			"whole list (3-15 headlines, 2-4 descriptions). The ad ID is the ad's own ID (ad_group_ad.ad.id).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cid, err := a.customerID(cmd)
			if err != nil {
				return err
			}
			if err := needID("ad ID", args[0]); err != nil {
				return err
			}
			hs, _ := cmd.Flags().GetStringArray("headline")
			ds, _ := cmd.Flags().GetStringArray("description")
			if err := checkRSA(hs, ds, true); err != nil {
				return err
			}
			var changes []change
			if len(hs) > 0 {
				changes = append(changes, change{"responsive_search_ad.headlines", []string{"responsiveSearchAd", "headlines"}, textAssets(hs)})
			}
			if len(ds) > 0 {
				changes = append(changes, change{"responsive_search_ad.descriptions", []string{"responsiveSearchAd", "descriptions"}, textAssets(ds)})
			}
			changes = append(changes, stringChange(cmd, "path1", "responsive_search_ad.path1", "responsiveSearchAd", "path1")...)
			changes = append(changes, stringChange(cmd, "path2", "responsive_search_ad.path2", "responsiveSearchAd", "path2")...)
			if cmd.Flags().Changed("final-url") {
				changes = append(changes, change{"final_urls", []string{"finalUrls"}, []string{flagString(cmd, "final-url")}})
			}
			body, err := updateOp(rn(cid, "ads", args[0]), changes)
			if err != nil {
				return err
			}
			return a.dispatch(cmd, mAds, body)
		},
	}
	rsaFlags(rsa)

	budget := &cobra.Command{
		Use: "budget <budgetId>", Short: "Update a budget's name or daily amount (an amount is spend-class and held to the cap)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cid, err := a.customerID(cmd)
			if err != nil {
				return err
			}
			if err := needID("budget ID", args[0]); err != nil {
				return err
			}
			changes := stringChange(cmd, "name", "name", "name")
			amt, err := moneyChange(cmd, "amount", "amount_micros", "amountMicros")
			if err != nil {
				return err
			}
			body, err := updateOp(rn(cid, "campaignBudgets", args[0]), append(changes, amt...))
			if err != nil {
				return err
			}
			return a.dispatch(cmd, mBudgets, body)
		},
	}
	budget.Flags().String("name", "", "new name")
	budget.Flags().String("amount", "", "new daily amount, in the account currency (spend-class)")

	conv := &cobra.Command{
		Use: "conversion-action <conversionActionId>", Short: "Update a conversion action's name or primary/secondary status (making it primary is spend-class)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cid, err := a.customerID(cmd)
			if err != nil {
				return err
			}
			if err := needID("conversion action ID", args[0]); err != nil {
				return err
			}
			changes := stringChange(cmd, "name", "name", "name")
			if cmd.Flags().Changed("primary") {
				changes = append(changes, change{"primary_for_goal", []string{"primaryForGoal"}, flagBool(cmd, "primary")})
			}
			body, err := updateOp(rn(cid, "conversionActions", args[0]), changes)
			if err != nil {
				return err
			}
			return a.dispatch(cmd, mConversions, body)
		},
	}
	conv.Flags().String("name", "", "new name")
	conv.Flags().Bool("primary", false, "--primary=true makes it primary for bidding (spend-class), --primary=false secondary")

	cmd.AddCommand(campaign, adGroup, keyword, rsa, budget, conv)
	return cmd
}

// ---- link and unlink -------------------------------------------------------

func (a *app) linkCommand(verb string) *cobra.Command {
	short := "Link an existing sitelink asset to a campaign or ad group"
	if verb == "unlink" {
		short = "Unlink a sitelink asset from a campaign or ad group (removes the link: delete-class)"
	}
	cmd := &cobra.Command{Use: verb, Short: short, RunE: groupRunE}
	sub := &cobra.Command{
		Use: "sitelink <assetId>", Short: short, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cid, err := a.customerID(cmd)
			if err != nil {
				return err
			}
			if err := needID("asset ID", args[0]); err != nil {
				return err
			}
			typ, link, err := linkOp(cmd, cid, rn(cid, "assets", args[0]), "SITELINK")
			if err != nil {
				return err
			}
			if link == nil {
				return api.Usagef("give --campaign-id or --ad-group-id")
			}
			method := mCampaignAssets
			if typ == "adGroupAssetOperation" {
				method = mAdGroupAssets
			}
			if verb == "link" {
				return a.dispatch(cmd, method, obj{"operations": []any{obj{"create": link}}})
			}
			var name string
			if c, ok := link["campaign"].(string); ok {
				name = rn(cid, "campaignAssets", strings.TrimPrefix(c, "customers/"+cid+"/campaigns/")+"~"+args[0]+"~SITELINK")
			} else {
				g := link["adGroup"].(string)
				name = rn(cid, "adGroupAssets", strings.TrimPrefix(g, "customers/"+cid+"/adGroups/")+"~"+args[0]+"~SITELINK")
			}
			return a.dispatch(cmd, method, obj{"operations": []any{obj{"remove": name}}})
		},
	}
	sub.Flags().String("campaign-id", "", "campaign ID")
	sub.Flags().String("ad-group-id", "", "ad group ID")
	cmd.AddCommand(sub)
	return cmd
}
