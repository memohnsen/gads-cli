package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/memohnsen/gads-cli/internal/manifest"
)

// Overrides is tools/genmanifest/overrides.json.
type Overrides struct {
	Commands          map[string]string `json:"commands"`           // method id -> command path
	Skiplist          map[string]string `json:"skiplist"`           // method id -> reason it is not generated
	Blocked           map[string]string `json:"blocked"`            // method id -> reason the CLI refuses it
	GenericOperations map[string]string `json:"generic_operations"` // operation type -> method id
	ResultsFields     map[string]string `json:"results_fields"`     // method id -> repeated field --all merges
}

// LoadOverrides reads the override table, rejecting unknown keys.
func LoadOverrides(path string) (*Overrides, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var o Overrides
	if err := dec.Decode(&o); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &o, nil
}

// Proposal is a machine-guessed risk class awaiting review.
type Proposal struct{ ID, HTTPMethod, Path, Proposed string }

// reserved are the built-in commands no generated command may shadow.
var reserved = set("api", "auth", "commands", "completion", "config", "help", "init", "schema", "version",
	// hand-written wrappers (internal/cli/wrappers.go)
	"list", "get", "pause", "enable", "create", "update", "link", "unlink")

// Build assembles the manifest. It collects every problem it finds and
// reports them together, so one run shows all the work needed. Methods that
// are not in the reviewed table come back as proposals instead of being
// guessed into the manifest.
func Build(specPath, overridesPath, classificationsPath string) (*manifest.Manifest, []Proposal, error) {
	d, err := LoadDiscovery(specPath)
	if err != nil {
		return nil, nil, err
	}
	ov, err := LoadOverrides(overridesPath)
	if err != nil {
		return nil, nil, err
	}
	table, err := LoadClassifications(classificationsPath)
	if err != nil {
		return nil, nil, err
	}

	var problems []string
	var proposals []Proposal
	var methods []manifest.Method
	ids := map[string]bool{}
	seen := map[string]string{} // command path -> method id

	for _, id := range sortedKeys(table) {
		if !validRisk(table[id]) {
			problems = append(problems, fmt.Sprintf("classification %q has risk %q; want one of %s",
				id, table[id], strings.Join(riskClasses, ", ")))
		}
	}
	for _, def := range d.AllMethods() {
		id := MethodID(def.ID)
		ids[id] = true
		if reason, skip := ov.Skiplist[id]; skip {
			if strings.TrimSpace(reason) == "" {
				problems = append(problems, "skiplist entry "+id+" has an empty reason")
			}
			if _, classified := table[id]; classified {
				problems = append(problems, "skiplisted method "+id+" must not be classified")
			}
			continue
		}
		if def.SupportsMediaUpload {
			problems = append(problems, id+" needs the media upload protocol; skiplist it with a reason")
			continue
		}
		m, err := BuildMethod(d, def, ov.Commands, ov.ResultsFields)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		key := strings.Join(m.Command, " ")
		if prev, dup := seen[key]; dup {
			problems = append(problems, fmt.Sprintf("command collision %q: %s and %s", key, prev, id))
			continue
		}
		seen[key] = id
		if reserved[m.Command[0]] {
			problems = append(problems, fmt.Sprintf("command %q of %s shadows a built-in command", key, id))
			continue
		}
		risk, ok := table[id]
		if !ok {
			proposals = append(proposals, Proposal{id, def.HTTPMethod, def.Path, proposeRisk(id, def.HTTPMethod)})
			continue
		}
		m.Risk = risk
		if reason, blocked := ov.Blocked[id]; blocked {
			if strings.TrimSpace(reason) == "" {
				problems = append(problems, "blocked entry "+id+" has an empty reason")
			}
			m.Blocked = reason
		}
		methods = append(methods, m)
	}
	// cobra cannot hang a command under a name that is itself a command
	for key, id := range seen {
		for other := range seen {
			if strings.HasPrefix(other, key+" ") {
				problems = append(problems, fmt.Sprintf("command %q (%s) is also a group of %q", key, id, other))
			}
		}
	}
	stale := func(kind string, entries map[string]string) {
		for id := range entries {
			if !ids[id] {
				problems = append(problems, fmt.Sprintf("%s entry %q matches no method in the document; remove it", kind, id))
			}
		}
	}
	stale("skiplist", ov.Skiplist)
	stale("blocked", ov.Blocked)
	stale("commands", ov.Commands)
	stale("results_fields", ov.ResultsFields)
	stale("classification", table)
	for typ, id := range ov.GenericOperations {
		if !ids[id] {
			problems = append(problems, fmt.Sprintf("generic_operations %s -> %q names no method", typ, id))
		}
	}
	generic, unmapped, err := GenericOperations(d, ov.GenericOperations)
	if err != nil {
		problems = append(problems, err.Error())
	}
	for _, typ := range unmapped {
		problems = append(problems, "generic operation type "+typ+" maps to no method; add it to generic_operations")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, proposals, fmt.Errorf("generator problems:\n  %s", strings.Join(problems, "\n  "))
	}
	sort.Slice(methods, func(i, j int) bool {
		return strings.Join(methods[i].Command, " ") < strings.Join(methods[j].Command, " ")
	})
	return &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, APIVersion: d.Version, DiscoveryRevision: d.Revision,
		Methods: methods, Schemas: SummarizeSchemas(d), GenericOperations: generic,
	}, proposals, nil
}
