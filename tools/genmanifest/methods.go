package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/memohnsen/gads-cli/internal/manifest"
)

var pathPlaceholder = regexp.MustCompile(`\{\+?([A-Za-z0-9_]+)\}`)

// BuildMethod converts one Discovery method into a manifest method. Build
// fills in Risk and Blocked from the reviewed tables.
func BuildMethod(d *Discovery, def MethodDef, commandOverrides, resultsFields map[string]string) (manifest.Method, error) {
	id := MethodID(def.ID)
	m := manifest.Method{
		Command: CommandPath(id, commandOverrides), ID: id, HTTPMethod: def.HTTPMethod, Path: def.Path,
		Description: firstParagraph(stripMarkers(def.Description), 600), Paging: manifest.PagingNone,
	}
	for _, match := range pathPlaceholder.FindAllStringSubmatch(def.Path, -1) {
		name := match[1]
		p, ok := def.Parameters[name]
		if !ok || p.Location != "path" {
			return m, fmt.Errorf("%s: path placeholder {%s} has no path parameter", id, name)
		}
		if name == "customerId" {
			m.NeedsCustomer = true
		}
		m.PathParams = append(m.PathParams, manifest.Param{
			Name: name, Type: p.Type, Required: true, Pattern: p.Pattern,
			Doc: firstSentence(stripMarkers(p.Description), 200),
		})
	}
	for _, name := range sortedKeys(def.Parameters) {
		p := def.Parameters[name]
		if p.Location != "query" {
			continue
		}
		m.QueryParams = append(m.QueryParams, manifest.Param{
			Name: name, Type: p.Type, Required: p.Required, Enum: p.Enum,
			Doc: firstSentence(stripMarkers(p.Description), 200),
		})
	}
	var req, resp map[string]PropertyDef
	if def.Request != nil {
		s, ok := d.Schemas[def.Request.Ref]
		if !ok {
			return m, fmt.Errorf("%s: request schema %q missing", id, def.Request.Ref)
		}
		m.Request, req = def.Request.Ref, s.Properties
		_, m.ValidateOnly = req["validateOnly"]
		_, m.PartialFailure = req["partialFailure"]
	}
	if def.Response != nil {
		m.Response, resp = def.Response.Ref, d.Schemas[def.Response.Ref].Properties
	}
	if _, ok := resp["nextPageToken"]; ok {
		qp, queryToken := def.Parameters["pageToken"]
		switch _, bodyToken := req["pageToken"]; {
		case bodyToken:
			m.Paging = manifest.PagingBodyToken
		case queryToken && qp.Location == "query":
			m.Paging = manifest.PagingQueryToken
		}
		if m.Paging != manifest.PagingNone {
			f, err := resultsField(id, resp, resultsFields)
			if err != nil {
				return m, err
			}
			m.ResultsField = f
		}
	}
	mut, err := mutateInfo(d, id, req)
	if err != nil {
		return m, err
	}
	m.Mutate = mut
	return m, nil
}

// resultsField picks the repeated response field that holds the rows --all
// merges: an override, else "results", else the only repeated field.
func resultsField(id string, resp map[string]PropertyDef, overrides map[string]string) (string, error) {
	if f, ok := overrides[id]; ok {
		if p, ok := resp[f]; !ok || p.Type != "array" {
			return "", fmt.Errorf("%s: results_fields override %q is not a repeated response field", id, f)
		}
		return f, nil
	}
	var repeated []string
	for _, name := range sortedKeys(resp) {
		if resp[name].Type == "array" {
			repeated = append(repeated, name)
		}
	}
	for _, r := range repeated {
		if r == "results" {
			return r, nil
		}
	}
	if len(repeated) == 1 {
		return repeated[0], nil
	}
	return "", fmt.Errorf("%s: cannot tell which repeated field holds the rows (%s); add it to results_fields",
		id, strings.Join(repeated, ", "))
}

// mutateInfo records where a mutate request carries its operations.
func mutateInfo(d *Discovery, id string, req map[string]PropertyDef) (*manifest.Mutate, error) {
	if _, ok := req["mutateOperations"]; ok {
		return &manifest.Mutate{OperationsField: "mutateOperations"}, nil
	}
	if !strings.HasSuffix(id, ".mutate") {
		return nil, nil
	}
	for _, field := range []string{"operations", "operation"} {
		p, ok := req[field]
		if !ok {
			continue
		}
		ref := p.Ref
		if p.Items != nil {
			ref = p.Items.Ref
		}
		res, err := operationResource(d, ref)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		return &manifest.Mutate{OperationsField: field, Resource: res}, nil
	}
	return nil, fmt.Errorf("%s: mutate request has no operations field", id)
}

// operationResource returns the short name of the resource an operation
// schema creates or updates ("Campaign" for CampaignOperation), or "" when
// the operation carries no resource body.
func operationResource(d *Discovery, opRef string) (string, error) {
	op, ok := d.Schemas[opRef]
	if !ok {
		return "", fmt.Errorf("operation schema %q missing", opRef)
	}
	for _, f := range []string{"create", "update"} {
		if p, ok := op.Properties[f]; ok && p.Ref != "" {
			return manifest.ShortName(p.Ref), nil
		}
	}
	return "", nil
}

// GenericOperations maps each operation type of the generic mutate
// (campaignOperation, ...) to the per-resource mutate method that owns the
// same operation schema. overrides covers types no mutate method owns. The
// second result lists types left unmapped, for Build to report.
func GenericOperations(d *Discovery, overrides map[string]string) (map[string]string, []string, error) {
	var generic *MethodDef
	byOpRef := map[string]string{}
	for _, def := range d.AllMethods() {
		id := MethodID(def.ID)
		if id == "customers.googleAds.mutate" {
			g := def
			generic = &g
			continue
		}
		if !strings.HasSuffix(id, ".mutate") || def.Request == nil {
			continue
		}
		req := d.Schemas[def.Request.Ref].Properties
		for _, field := range []string{"operations", "operation"} {
			if p, ok := req[field]; ok {
				ref := p.Ref
				if p.Items != nil {
					ref = p.Items.Ref
				}
				byOpRef[ref] = id
			}
		}
	}
	if generic == nil || generic.Request == nil {
		return nil, nil, fmt.Errorf("customers.googleAds.mutate not found in the document")
	}
	ops, ok := d.Schemas[generic.Request.Ref].Properties["mutateOperations"]
	if !ok || ops.Items == nil {
		return nil, nil, fmt.Errorf("generic mutate request has no mutateOperations")
	}
	types := d.Schemas[ops.Items.Ref].Properties
	out := map[string]string{}
	var unmapped []string
	for _, typ := range sortedKeys(types) {
		if id, ok := overrides[typ]; ok {
			out[typ] = id
			continue
		}
		if id, ok := byOpRef[types[typ].Ref]; ok {
			out[typ] = id
			continue
		}
		unmapped = append(unmapped, typ)
	}
	return out, unmapped, nil
}
