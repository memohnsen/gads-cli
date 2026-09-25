// Command genmanifest turns the vendored Discovery document into the
// manifest embedded in the binary. See Build in build.go.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Discovery is the part of Google's Discovery document the generator reads.
type Discovery struct {
	Kind      string               `json:"kind"`
	Version   string               `json:"version"`
	Revision  string               `json:"revision"`
	Resources map[string]Resource  `json:"resources"`
	Schemas   map[string]SchemaDef `json:"schemas"`
}

// Resource is a node of the Discovery resource tree.
type Resource struct {
	Methods   map[string]MethodDef `json:"methods"`
	Resources map[string]Resource  `json:"resources"`
}

// MethodDef is one Discovery method.
type MethodDef struct {
	ID                  string              `json:"id"`
	Path                string              `json:"path"`
	HTTPMethod          string              `json:"httpMethod"`
	Description         string              `json:"description"`
	Parameters          map[string]ParamDef `json:"parameters"`
	Request             *Ref                `json:"request"`
	Response            *Ref                `json:"response"`
	SupportsMediaUpload bool                `json:"supportsMediaUpload"`
}

// Ref points at a schema.
type Ref struct {
	Ref string `json:"$ref"`
}

// ParamDef is a method parameter.
type ParamDef struct {
	Type        string   `json:"type"`
	Location    string   `json:"location"`
	Required    bool     `json:"required"`
	Pattern     string   `json:"pattern"`
	Format      string   `json:"format"`
	Description string   `json:"description"`
	Enum        []string `json:"enum"`
}

// SchemaDef is a Discovery schema.
type SchemaDef struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Properties map[string]PropertyDef `json:"properties"`
}

// PropertyDef is a schema property.
type PropertyDef struct {
	Type                 string       `json:"type"`
	Ref                  string       `json:"$ref"`
	Format               string       `json:"format"`
	Description          string       `json:"description"`
	ReadOnly             bool         `json:"readOnly"`
	Enum                 []string     `json:"enum"`
	Items                *PropertyDef `json:"items"`
	AdditionalProperties *PropertyDef `json:"additionalProperties"`
}

// LoadDiscovery reads and decodes a vendored Discovery document.
func LoadDiscovery(path string) (*Discovery, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d Discovery
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if d.Kind != "discovery#restDescription" {
		return nil, fmt.Errorf("%s: kind %q is not a Discovery document", path, d.Kind)
	}
	return &d, nil
}

// MethodID is Discovery's id without the service prefix:
// "googleads.customers.campaigns.mutate" becomes "customers.campaigns.mutate".
// It contains no API version, so ids survive a version change.
func MethodID(discoveryID string) string { return strings.TrimPrefix(discoveryID, "googleads.") }

// AllMethods returns every method in the document, sorted by Discovery id.
func (d *Discovery) AllMethods() []MethodDef {
	var out []MethodDef
	var walk func(map[string]Resource)
	walk = func(res map[string]Resource) {
		for _, r := range res {
			for _, m := range r.Methods {
				out = append(out, m)
			}
			walk(r.Resources)
		}
	}
	walk(d.Resources)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
