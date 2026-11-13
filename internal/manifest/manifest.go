// Package manifest describes the Google Ads API surface the CLI exposes.
//
// tools/genmanifest builds it from the vendored Discovery document, gzips it
// and embeds it into the binary. At runtime the CLI parses it to build the
// command tree, validate arguments, print help and classify calls by risk.
package manifest

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// SchemaVersion is the manifest format this build understands.
const SchemaVersion = 1

// Risk classes. delete, spend and admin are gated: applying them needs
// --apply --confirm-spend.
const (
	RiskRead   = "read"
	RiskWrite  = "write"
	RiskDelete = "delete"
	RiskSpend  = "spend"
	RiskAdmin  = "admin"
)

// Paging styles.
const (
	PagingNone       = "none"
	PagingBodyToken  = "body-token"  // pageToken travels in the JSON request body
	PagingQueryToken = "query-token" // pageToken travels as a query parameter
)

// Manifest is everything the CLI knows about the API.
type Manifest struct {
	SchemaVersion     int               `json:"schema_version"`
	APIVersion        string            `json:"api_version"`
	DiscoveryRevision string            `json:"discovery_revision"`
	Methods           []Method          `json:"methods"`
	Schemas           map[string]Schema `json:"schemas"`
	// GenericOperations maps each operation type of the generic mutate
	// (campaignOperation, ...) to the id of the method that owns the same
	// operation, whose risk class and resource apply to it.
	GenericOperations map[string]string `json:"generic_operations"`
}

// Method is one API method and the command that calls it.
type Method struct {
	Command     []string `json:"command"`
	ID          string   `json:"id"`
	HTTPMethod  string   `json:"http_method"`
	Path        string   `json:"path"` // "v25/customers/{+customerId}/campaigns:mutate"
	Description string   `json:"description,omitempty"`
	Risk        string   `json:"risk"`
	// Blocked, when set, is why the CLI refuses this method outright.
	Blocked        string  `json:"blocked,omitempty"`
	NeedsCustomer  bool    `json:"needs_customer"`
	PathParams     []Param `json:"path_params,omitempty"` // in path order, customerId included
	QueryParams    []Param `json:"query_params,omitempty"`
	Request        string  `json:"request,omitempty"`  // schema id of the request body
	Response       string  `json:"response,omitempty"` // schema id of the response
	ValidateOnly   bool    `json:"validate_only"`
	PartialFailure bool    `json:"partial_failure"`
	Paging         string  `json:"paging"`
	ResultsField   string  `json:"results_field,omitempty"` // repeated response field --all merges
	Mutate         *Mutate `json:"mutate,omitempty"`
}

// Mutate says where a mutate request carries its operations.
type Mutate struct {
	// OperationsField is "operations" (array), "operation" (single object)
	// or "mutateOperations" (generic mutate and batch jobs).
	OperationsField string `json:"operations_field"`
	// Resource is the short schema name the operations create or update
	// ("Campaign"); empty for the generic mutate.
	Resource string `json:"resource,omitempty"`
}

// Param is a path or query parameter.
type Param struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Pattern  string   `json:"pattern,omitempty"` // Discovery's anchored regexp for the value
	Doc      string   `json:"doc,omitempty"`
	Enum     []string `json:"enum,omitempty"`
}

// Schema is the compact field table of one Discovery schema.
type Schema struct {
	Fields []Field `json:"fields"`
}

// Field is one field of a schema.
type Field struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`          // string, integer, number, boolean, object, map
	Ref       string   `json:"ref,omitempty"` // schema id for object fields
	Repeated  bool     `json:"repeated,omitempty"`
	Format    string   `json:"format,omitempty"` // int64, double, ...
	Enum      []string `json:"enum,omitempty"`
	ReadOnly  bool     `json:"read_only,omitempty"`
	Required  bool     `json:"required,omitempty"`
	Immutable bool     `json:"immutable,omitempty"`
	Doc       string   `json:"doc,omitempty"`
}

// Parse gunzips and decodes a manifest, rejecting any other schema version.
func Parse(gz []byte) (*Manifest, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if m.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("manifest: schema_version %d, want %d", m.SchemaVersion, SchemaVersion)
	}
	return &m, nil
}

// ByID returns the method with the given id, or nil.
func (m *Manifest) ByID(id string) *Method {
	for i := range m.Methods {
		if m.Methods[i].ID == id {
			return &m.Methods[i]
		}
	}
	return nil
}

var (
	versionPrefix = regexp.MustCompile(`^v[0-9]+/`)
	placeholder   = regexp.MustCompile(`\{\+?([A-Za-z0-9_]+)\}`)
)

// Find returns the method whose HTTP method matches and whose path template
// matches path. The leading API version segment is ignored on both sides, so
// a raw call to another version maps onto this version's method and inherits
// its risk class. Each placeholder matches its Discovery pattern (anchors
// removed) or one segment. When several templates match, the one with the
// most literal characters wins. It returns nil when nothing matches.
func (m *Manifest) Find(httpMethod, path string) *Method {
	p := versionPrefix.ReplaceAllString(strings.TrimPrefix(path, "/"), "")
	var best *Method
	bestLiteral := -1
	for i := range m.Methods {
		meth := &m.Methods[i]
		if meth.HTTPMethod != httpMethod {
			continue
		}
		re, literal := meth.pathRegexp()
		if re != nil && re.MatchString(p) && literal > bestLiteral {
			best, bestLiteral = meth, literal
		}
	}
	return best
}

// pathRegexp compiles the method's path template, without its version
// segment, and counts its literal characters.
func (meth *Method) pathRegexp() (*regexp.Regexp, int) {
	tmpl := versionPrefix.ReplaceAllString(meth.Path, "")
	var b strings.Builder
	b.WriteString("^")
	literal, last := 0, 0
	for _, loc := range placeholder.FindAllStringSubmatchIndex(tmpl, -1) {
		b.WriteString(regexp.QuoteMeta(tmpl[last:loc[0]]))
		literal += loc[0] - last
		b.WriteString("(?:" + meth.paramPattern(tmpl[loc[2]:loc[3]]) + ")")
		last = loc[1]
	}
	b.WriteString(regexp.QuoteMeta(tmpl[last:]))
	literal += len(tmpl) - last
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, 0
	}
	return re, literal
}

func (meth *Method) paramPattern(name string) string {
	for _, p := range meth.PathParams {
		if p.Name == name && p.Pattern != "" {
			return strings.TrimSuffix(strings.TrimPrefix(p.Pattern, "^"), "$")
		}
	}
	return "[^/]+"
}

var schemaPrefix = regexp.MustCompile(`^GoogleAdsGoogleadsV[0-9]+[A-Z][a-z]+_+`)

// ShortName drops Google's package prefix from a schema id:
// "GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings" becomes
// "Campaign_NetworkSettings". Ids outside the Google Ads packages keep their name.
func ShortName(schemaID string) string {
	return schemaPrefix.ReplaceAllString(schemaID, "")
}

// LookupSchema resolves a full schema id or a short name. It returns the id
// when exactly one schema matches, and the sorted candidates when the short
// name is ambiguous. Both are empty when nothing matches.
func (m *Manifest) LookupSchema(name string) (string, []string) {
	if _, ok := m.Schemas[name]; ok {
		return name, nil
	}
	var hits []string
	for id := range m.Schemas {
		if ShortName(id) == name {
			hits = append(hits, id)
		}
	}
	sort.Strings(hits)
	if len(hits) == 1 {
		return hits[0], nil
	}
	return "", hits
}

// RiskRank orders classes: read < write < delete, spend, admin.
func RiskRank(risk string) int {
	switch risk {
	case RiskRead:
		return 0
	case RiskWrite:
		return 1
	default:
		return 2
	}
}

// NeedsConfirm reports whether a class is gated: applying it needs
// --apply --confirm-spend.
func NeedsConfirm(risk string) bool { return RiskRank(risk) == 2 }
