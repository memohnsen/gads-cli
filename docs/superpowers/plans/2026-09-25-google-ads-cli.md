# googleads (Google Ads CLI) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `googleads`, a single static Go binary that exposes every method of the Google Ads API v25 as a command, authenticates with a Google Cloud service-account key, and refuses to switch on spend or exceed a budget cap without an explicit, visible decision.

**Architecture:** A build-time generator turns Google's vendored Discovery document into an embedded, gzipped manifest (methods, schemas, risk classes, generic mutate mapping). At runtime cobra builds the command tree from the manifest; `internal/guard` inspects every mutate body before anything is sent; `internal/api` sends REST/JSON requests with a bearer token from `internal/auth` (JWT bearer grant, cached on disk).

**Tech Stack:** Go 1.26, `github.com/spf13/cobra` v1.10.2, `golang.org/x/oauth2` v0.36.0 (package `jwt`), `golang.org/x/term` v0.45.0, goreleaser, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-25-google-ads-cli-design.md`. Read it before your task; this plan argues from it.

**Reference implementation:** `/Users/daniel/Development/bexio-cli`, a sibling CLI by the same author. Where a step says "copy X from bexio-cli", copy the file and make exactly the listed changes. Never import anything from bexio-cli: the repositories share no module.

## Global Constraints

- Repo root: `/Users/daniel/Development/google-ads-cli`. Run every command from there.
- Module `github.com/wir-drei-digital/google-ads-cli`; binary `googleads`; `go 1.26` in `go.mod`; must build with `CGO_ENABLED=0`.
- Dependencies: only `github.com/spf13/cobra v1.10.2`, `golang.org/x/oauth2 v0.36.0`, `golang.org/x/term v0.45.0` and their indirect requirements. Add them with `go get <module>@<version>` when a task first needs them, then `go mod tidy`.
- API version pinned to `v25`; the Discovery document is vendored at `spec/googleads-v25.discovery.json`.
- Method ids are Discovery's `id` without the `googleads.` prefix (`customers.campaigns.mutate`, `listPlannableLocations`).
- API host `googleads.googleapis.com` over HTTPS; token URL `https://oauth2.googleapis.com/token`. Any other host only with `GOOGLE_ADS_ALLOW_CUSTOM_BASE=1`; non-HTTPS refused except on loopback (`localhost`, `127.0.0.1`, `::1`).
- stdout carries only the API response (or a config/auth command's own result); every failure is exactly one line of JSON on stderr with keys `kind`, `error`, `status` (omitted when no response), `details`, `request_id` (omitted when unknown). Exit codes: `0` success, `1` API or network error, `2` usage error, which includes every guardrail refusal.
- Secrets never come from argv. The service-account key comes from stdin (`config set service-account-key`), from a file named by `GOOGLE_ADS_JSON_KEY_FILE_PATH`, or from a file path typed into `init`.
- No `developer-token` header, ever.
- `--force` is a per-call flag only. The budget cap lives only in the config file: no environment variable, no flag.
- Risk classes are exactly `read`, `write`, `delete`, `spend`, `admin`; `delete`, `spend` and `admin` need `--force`.
- `go vet ./...` clean; `go test ./...` passes on Linux, macOS and Windows without network access. The only exception is `e2e/live_test.go`, which skips unless `GOOGLE_ADS_LIVE=1`.
- All code, identifiers, comments and docs in English. Prose in `README.md`, `SECURITY.md`, `docs/agents.md` and in error messages contains no em dash character; use a colon, a semicolon, a comma or a new sentence.
- Commit at the end of every task. Every commit message ends with a blank line followed by `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

These inputs are implied by the spec but not spelled out there. Each has a test in the task that owns the code.

1. **Customer IDs pasted from the Google Ads UI** with dashes or spaces (`123-456-7890`, ` 123 456 7890 `) in `--customer-id`, `--login-customer-id`, `GOOGLE_ADS_CUSTOMER_ID`, `GOOGLE_ADS_LOGIN_CUSTOMER_ID` and `config set`: all normalize to ten digits, and the URL path and the `login-customer-id` header carry digits only. Tests: Task 5 (`TestNormalizeCustomerID`, `TestResolveNormalizesEnvCustomerID`), Task 9 (`TestCustomerIDFlagWithDashes`, `TestLoginCustomerIDFlagSetsHeader`).
2. **A `--data @file.json` written on Windows**: a UTF-8 byte order mark is stripped and accepted; a UTF-16 file (the PowerShell 5 default) fails with a usage error that says to save the file as UTF-8, not with "not valid JSON". Tests: Task 9 (`TestDataFileWithUTF8BOMAccepted`, `TestDataFileUTF16Refused`).
3. **`googleads init` given a key path that starts with `~/`**, as people paste it: expanded to the home directory. Test: Task 11 (`TestInitExpandsHomeInKeyPath`).
4. **`config set budget-cap 29,61`** (Swiss and German decimal comma): refused with a message that says to use a dot, never read as 2961 or 29. Tests: Task 5 (`TestParseMoneyToMicros`), Task 11 (`TestConfigSetBudgetCapComma`).
5. **Budget amounts in a mutate body written loosely**: `" 29610000"`, `"+29610000"`, `2.961e7`, `"29610000.0"` are refused with a message to write a plain whole number of micros, never compared loosely against the cap. Test: Task 8 (`TestAmountForms`).

## File Structure

```
google-ads-cli/
├── go.mod, go.sum, LICENSE, Makefile, .gitignore, .gitattributes, .goreleaser.yaml
├── .github/workflows/ci.yml, .github/workflows/release.yml
├── README.md, SECURITY.md, docs/agents.md
├── cmd/googleads/main.go                 # hands argv to internal/cli
├── internal/manifest/manifest.go         # types, Parse, ByID, Find, ShortName, LookupSchema, RiskRank, NeedsForce
├── internal/manifest/embed.go            # //go:embed manifest.json.gz, Load
├── internal/manifest/manifest.json.gz    # generated, committed
├── internal/config/config.go             # config file, Resolve, NormalizeCustomerID, ParseMoneyToMicros, FormatMicros
├── internal/auth/key.go                  # ParseKey
├── internal/auth/source.go               # Source.Token: disk cache + JWT bearer exchange
├── internal/api/errors.go                # Error, kinds, kindForStatus
├── internal/api/client.go                # Client.Do: host guard, headers, retries
├── internal/api/google.go                # GoogleAdsFailure parsing, messages, hints, retry delay, PartialFailure
├── internal/api/retry.go                 # classifyTransport, retryAfter, jitter
├── internal/guard/json.go                # strict JSON parse: duplicate keys, both spellings, camelCase keys
├── internal/guard/guard.go               # Check: blocked, classes, payload rules, cap, force, read-only
├── internal/cli/app.go                   # Execute, app, renderError, client and token wiring
├── internal/cli/root.go, version.go
├── internal/cli/generated.go             # command tree, leaf commands, runMethod
├── internal/cli/request.go               # buildRequest: customer, path params, query, --query, owned fields
├── internal/cli/body.go                  # readJSONBody: stdin/@file/literal, BOM, UTF-16
├── internal/cli/output.go, all.go
├── internal/cli/help.go                  # long help, field tables, example bodies
├── internal/cli/catalog.go, schemacmd.go, apicmd.go
├── internal/cli/configcmd.go, authcmd.go, initcmd.go, prompt.go
├── tools/fetchspec/main.go
├── tools/genmanifest/discovery.go, naming.go, schemas.go, methods.go, classify.go, build.go, main.go
├── tools/genmanifest/overrides.json, classifications.json, testdata/
├── spec/googleads-v25.discovery.json, spec/PROVENANCE.json
└── e2e/helpers_test.go, e2e/smoke_test.go, e2e/live_test.go
```

---

### Task 1: Repository scaffold and manifest package

**Files:**
- Create: `go.mod`, `LICENSE`, `.gitignore`, `.gitattributes`, `Makefile`
- Create: `internal/manifest/manifest.go`
- Test: `internal/manifest/manifest_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (package `manifest`, used by every later task):

```go
const SchemaVersion = 1
const (RiskRead = "read"; RiskWrite = "write"; RiskDelete = "delete"; RiskSpend = "spend"; RiskAdmin = "admin")
const (PagingNone = "none"; PagingBodyToken = "body-token"; PagingQueryToken = "query-token")
type Manifest struct {
	SchemaVersion     int               `json:"schema_version"`
	APIVersion        string            `json:"api_version"`
	DiscoveryRevision string            `json:"discovery_revision"`
	Methods           []Method          `json:"methods"`
	Schemas           map[string]Schema `json:"schemas"`
	GenericOperations map[string]string `json:"generic_operations"`
}
type Method struct { Command []string; ID, HTTPMethod, Path, Description, Risk, Blocked string; NeedsCustomer bool; PathParams, QueryParams []Param; Request, Response string; ValidateOnly, PartialFailure bool; Paging, ResultsField string; Mutate *Mutate }
type Mutate struct { OperationsField, Resource string }
type Param struct { Name, Type string; Required bool; Pattern, Doc string; Enum []string }
type Schema struct { Fields []Field }
type Field struct { Name, Type, Ref string; Repeated bool; Format string; Enum []string; ReadOnly, Required, Immutable bool; Doc string }
func Parse(gz []byte) (*Manifest, error)
func (m *Manifest) ByID(id string) *Method
func (m *Manifest) Find(httpMethod, path string) *Method
func ShortName(schemaID string) string
func (m *Manifest) LookupSchema(name string) (id string, candidates []string)
func RiskRank(risk string) int
func NeedsForce(risk string) bool
```

- [ ] **Step 1: Create the scaffold files**

`go.mod`:

```
module github.com/wir-drei-digital/google-ads-cli

go 1.26
```

`LICENSE`: copy `/Users/daniel/Development/bexio-cli/LICENSE` verbatim (MIT, "Copyright (c) 2026 wir drei digital").

`.gitignore`:

```
bin/
dist/
*.test
coverage.out

# `go build ./cmd/googleads` drops the binary here; never commit it.
/googleads
/googleads.exe

# Superpowers working artifacts: process scratch, not repo content.
.superpowers/
```

`.gitattributes`:

```
# Text files are LF in the repository and on checkout, on every platform:
# the generator's golden test byte-compares a file joined with "\n".
* text=auto eol=lf

# The embedded manifest is gzip; never rewrite its bytes.
internal/manifest/manifest.json.gz binary
```

`Makefile` (recipe lines start with a real tab character):

```make
.PHONY: build test generate update-spec check

VERSION ?= v25

build:
	go build -o bin/googleads ./cmd/googleads

test:
	go test ./...

generate:
	go run ./tools/genmanifest

update-spec:
	go run ./tools/fetchspec -version $(VERSION)

check:
	go vet ./...
	go run ./tools/genmanifest -check
	go test ./...
```

- [ ] **Step 2: Write the failing tests**

`internal/manifest/manifest_test.go`:

```go
package manifest

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func gz(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(raw)
	zw.Close()
	return buf.Bytes()
}

func testManifest() *Manifest {
	return &Manifest{
		SchemaVersion: SchemaVersion, APIVersion: "v25",
		Methods: []Method{
			{ID: "customers.campaigns.mutate", HTTPMethod: "POST", Path: "v25/customers/{+customerId}/campaigns:mutate",
				PathParams: []Param{{Name: "customerId", Pattern: "^[^/]+$"}}},
			{ID: "customers.operations.list", HTTPMethod: "GET", Path: "v25/{+name}",
				PathParams: []Param{{Name: "name", Pattern: "^customers/[^/]+/operations$"}}},
			{ID: "customers.operations.get", HTTPMethod: "GET", Path: "v25/{+name}",
				PathParams: []Param{{Name: "name", Pattern: "^customers/[^/]+/operations/[^/]+$"}}},
			{ID: "customers.batchJobs.run", HTTPMethod: "POST", Path: "v25/{+resourceName}:run",
				PathParams: []Param{{Name: "resourceName", Pattern: "^customers/[^/]+/batchJobs/[^/]+$"}}},
			{ID: "customers.listAccessibleCustomers", HTTPMethod: "GET", Path: "v25/customers:listAccessibleCustomers"},
		},
		Schemas: map[string]Schema{
			"GoogleAdsGoogleadsV25Resources__Campaign":                 {},
			"GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings":  {},
			"GoogleAdsGoogleadsV25Services__Thing":                     {},
			"GoogleAdsGoogleadsV25Resources__Thing":                    {},
			"GoogleLongrunning__Operation":                             {},
		},
	}
}

func TestParseRoundTrip(t *testing.T) {
	m := testManifest()
	got, err := Parse(gz(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Methods, m.Methods) || got.APIVersion != "v25" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestParseRejectsOtherSchemaVersion(t *testing.T) {
	_, err := Parse(gz(t, map[string]any{"schema_version": 99}))
	if err == nil || !strings.Contains(err.Error(), "schema_version 99") {
		t.Fatalf("err = %v", err)
	}
}

func TestByID(t *testing.T) {
	m := testManifest()
	if got := m.ByID("customers.batchJobs.run"); got == nil || got.HTTPMethod != "POST" {
		t.Fatalf("got %+v", got)
	}
	if m.ByID("nope") != nil {
		t.Fatal("unknown id must be nil")
	}
}

func TestFind(t *testing.T) {
	m := testManifest()
	cases := []struct{ method, path, want string }{
		{"POST", "v25/customers/1234567890/campaigns:mutate", "customers.campaigns.mutate"},
		{"POST", "/v25/customers/1234567890/campaigns:mutate", "customers.campaigns.mutate"},
		// another API version maps onto this version's method
		{"POST", "v24/customers/1234567890/campaigns:mutate", "customers.campaigns.mutate"},
		// the Discovery pattern tells list and get apart on the same template
		{"GET", "v25/customers/1/operations", "customers.operations.list"},
		{"GET", "v25/customers/1/operations/abc", "customers.operations.get"},
		{"POST", "v25/customers/1/batchJobs/7:run", "customers.batchJobs.run"},
		{"GET", "v25/customers:listAccessibleCustomers", "customers.listAccessibleCustomers"},
		{"GET", "v25/customers/1234567890/campaigns:mutate", ""}, // wrong HTTP method
		{"POST", "v25/customers/1/unknown:mutate", ""},
		{"POST", "v25/customers/1/batchJobs/7/extra:run", ""},
	}
	for _, c := range cases {
		got := m.Find(c.method, c.path)
		id := ""
		if got != nil {
			id = got.ID
		}
		if id != c.want {
			t.Errorf("Find(%s %s) = %q, want %q", c.method, c.path, id, c.want)
		}
	}
}

func TestShortName(t *testing.T) {
	cases := map[string]string{
		"GoogleAdsGoogleadsV25Resources__Campaign":                "Campaign",
		"GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings": "Campaign_NetworkSettings",
		"GoogleAdsGoogleadsV25Common__ResponsiveSearchAdInfo":     "ResponsiveSearchAdInfo",
		"GoogleAdsGoogleadsV25Services__MutateOperation":          "MutateOperation",
		"GoogleLongrunning__Operation":                            "GoogleLongrunning__Operation",
	}
	for in, want := range cases {
		if got := ShortName(in); got != want {
			t.Errorf("ShortName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLookupSchema(t *testing.T) {
	m := testManifest()
	if id, _ := m.LookupSchema("Campaign"); id != "GoogleAdsGoogleadsV25Resources__Campaign" {
		t.Fatalf("short name: %q", id)
	}
	if id, _ := m.LookupSchema("Campaign_NetworkSettings"); id != "GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings" {
		t.Fatalf("nested short name: %q", id)
	}
	if id, _ := m.LookupSchema("GoogleAdsGoogleadsV25Services__Thing"); id != "GoogleAdsGoogleadsV25Services__Thing" {
		t.Fatalf("full name: %q", id)
	}
	id, cands := m.LookupSchema("Thing")
	if id != "" || len(cands) != 2 {
		t.Fatalf("ambiguous: id %q candidates %v", id, cands)
	}
	if id, cands := m.LookupSchema("Nope"); id != "" || len(cands) != 0 {
		t.Fatalf("unknown: %q %v", id, cands)
	}
}

func TestRiskRankAndForce(t *testing.T) {
	if !(RiskRank(RiskRead) < RiskRank(RiskWrite) && RiskRank(RiskWrite) < RiskRank(RiskSpend)) {
		t.Fatal("rank order")
	}
	for _, r := range []string{RiskDelete, RiskSpend, RiskAdmin} {
		if !NeedsForce(r) || RiskRank(r) != RiskRank(RiskSpend) {
			t.Fatalf("%s must need --force at the top rank", r)
		}
	}
	if NeedsForce(RiskRead) || NeedsForce(RiskWrite) {
		t.Fatal("read and write need no --force")
	}
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./internal/manifest/`
Expected: FAIL (package does not compile: undefined `Manifest`, `Parse`, and so on).

- [ ] **Step 4: Implement the package**

`internal/manifest/manifest.go`:

```go
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

// Risk classes. delete, spend and admin need --force.
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

// NeedsForce reports whether a class needs --force.
func NeedsForce(risk string) bool { return RiskRank(risk) == 2 }
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `go test ./internal/manifest/ && go vet ./...`
Expected: PASS, vet silent.

- [ ] **Step 6: Commit**

```bash
git add go.mod LICENSE .gitignore .gitattributes Makefile internal/manifest
git commit -m "Scaffold repo and manifest package

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: fetchspec and the vendored Discovery document

**Files:**
- Create: `tools/fetchspec/main.go`
- Test: `tools/fetchspec/main_test.go`
- Create (by running the tool): `spec/googleads-v25.discovery.json`, `spec/PROVENANCE.json`

**Interfaces:**
- Consumes: nothing.
- Produces: the vendored document at `spec/googleads-v25.discovery.json`, which Tasks 3 and 4 read. `make update-spec VERSION=vNN` refreshes it.

- [ ] **Step 1: Write the failing tests**

`tools/fetchspec/main_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(kind, version string, methods int) []byte {
	ms := map[string]any{}
	for i := 0; i < methods; i++ {
		ms["m"+string(rune('a'+i))] = map[string]any{"id": "googleads.x"}
	}
	doc := map[string]any{
		"kind": kind, "version": version, "revision": "20260923",
		"resources": map[string]any{"customers": map[string]any{
			"methods":   ms,
			"resources": map[string]any{"campaigns": map[string]any{"methods": map[string]any{"mutate": map[string]any{}}}},
		}},
		"schemas": map[string]any{"A": map[string]any{}, "B": map[string]any{}},
	}
	raw, _ := json.Marshal(doc)
	return raw
}

func TestValidateAccepts(t *testing.T) {
	p, err := validate(fixture("discovery#restDescription", "v25", 3), "v25", 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if p.Methods != 4 || p.Schemas != 2 || p.Revision != "20260923" || len(p.SHA256) != 64 {
		t.Fatalf("provenance %+v", p)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]struct {
		raw     []byte
		wantErr string
	}{
		"not json":      {[]byte("<html>"), "not JSON"},
		"wrong kind":    {fixture("something", "v25", 3), "kind"},
		"wrong version": {fixture("discovery#restDescription", "v24", 3), "version"},
		"too few":       {fixture("discovery#restDescription", "v25", 1), "methods"},
	}
	for name, c := range cases {
		_, err := validate(c.raw, "v25", 4, 2)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, c.wantErr)
		}
	}
}

func TestRunWritesSpecAndProvenance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/$discovery/rest" || r.URL.Query().Get("version") != "v25" {
			http.NotFound(w, r)
			return
		}
		w.Write(fixture("discovery#restDescription", "v25", 3))
	}))
	defer srv.Close()
	dir := t.TempDir()
	if _, err := run(srv.Client(), srv.URL, "v25", dir, time.Unix(0, 0), 4, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "googleads-v25.discovery.json")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "PROVENANCE.json"))
	if err != nil || !strings.Contains(string(raw), `"revision": "20260923"`) {
		t.Fatalf("provenance: %s %v", raw, err)
	}
}

func TestRunWritesNothingOnBadDocument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture("discovery#restDescription", "v24", 3))
	}))
	defer srv.Close()
	dir := t.TempDir()
	if _, err := run(srv.Client(), srv.URL, "v25", dir, time.Now(), 4, 2); err == nil {
		t.Fatal("want error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("wrote %d files", len(entries))
	}
}

func TestRunHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := run(srv.Client(), srv.URL, "v26", t.TempDir(), time.Now(), 4, 2); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./tools/fetchspec/`
Expected: FAIL (undefined `validate`, `run`).

- [ ] **Step 3: Implement the tool**

`tools/fetchspec/main.go`:

```go
// Command fetchspec downloads Google's Discovery document for one Google Ads
// API version, validates it, and writes it into spec/ with a provenance
// record. Nothing is written when validation fails, so a broken download can
// never replace the vendored document.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type document struct {
	Kind      string                     `json:"kind"`
	Version   string                     `json:"version"`
	Revision  string                     `json:"revision"`
	Resources map[string]json.RawMessage `json:"resources"`
	Schemas   map[string]json.RawMessage `json:"schemas"`
}

type resource struct {
	Methods   map[string]json.RawMessage `json:"methods"`
	Resources map[string]json.RawMessage `json:"resources"`
}

// Provenance records where the vendored document came from.
type Provenance struct {
	SourceURL   string `json:"source_url"`
	RetrievedAt string `json:"retrieved_at"`
	SHA256      string `json:"sha256"`
	Revision    string `json:"revision"`
	Methods     int    `json:"methods"`
	Schemas     int    `json:"schemas"`
}

func countMethods(res map[string]json.RawMessage) (int, error) {
	n := 0
	for name, raw := range res {
		var r resource
		if err := json.Unmarshal(raw, &r); err != nil {
			return 0, fmt.Errorf("resource %s: %w", name, err)
		}
		sub, err := countMethods(r.Resources)
		if err != nil {
			return 0, err
		}
		n += len(r.Methods) + sub
	}
	return n, nil
}

// validate checks that raw is a Discovery document for version with enough
// methods and schemas, and returns its provenance fields.
func validate(raw []byte, version string, minMethods, minSchemas int) (Provenance, error) {
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		return Provenance{}, fmt.Errorf("not JSON: %w", err)
	}
	if d.Kind != "discovery#restDescription" {
		return Provenance{}, fmt.Errorf("kind %q, want discovery#restDescription", d.Kind)
	}
	if d.Version != version {
		return Provenance{}, fmt.Errorf("version %q, want %q", d.Version, version)
	}
	n, err := countMethods(d.Resources)
	if err != nil {
		return Provenance{}, err
	}
	if n < minMethods {
		return Provenance{}, fmt.Errorf("%d methods, want at least %d", n, minMethods)
	}
	if len(d.Schemas) < minSchemas {
		return Provenance{}, fmt.Errorf("%d schemas, want at least %d", len(d.Schemas), minSchemas)
	}
	sum := sha256.Sum256(raw)
	return Provenance{SHA256: hex.EncodeToString(sum[:]), Revision: d.Revision, Methods: n, Schemas: len(d.Schemas)}, nil
}

func fetch(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func run(client *http.Client, base, version, outDir string, now time.Time, minMethods, minSchemas int) (Provenance, error) {
	url := base + "/$discovery/rest?version=" + version
	raw, err := fetch(client, url)
	if err != nil {
		return Provenance{}, err
	}
	prov, err := validate(raw, version, minMethods, minSchemas)
	if err != nil {
		return Provenance{}, fmt.Errorf("refusing to write %s: %w", url, err)
	}
	prov.SourceURL = url
	prov.RetrievedAt = now.UTC().Format(time.RFC3339)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Provenance{}, err
	}
	if err := os.WriteFile(filepath.Join(outDir, "googleads-"+version+".discovery.json"), raw, 0o644); err != nil {
		return Provenance{}, err
	}
	pj, err := json.MarshalIndent(prov, "", "  ")
	if err != nil {
		return Provenance{}, err
	}
	if err := os.WriteFile(filepath.Join(outDir, "PROVENANCE.json"), append(pj, '\n'), 0o644); err != nil {
		return Provenance{}, err
	}
	return prov, nil
}

func main() {
	version := flag.String("version", "v25", "Google Ads API version to fetch")
	base := flag.String("base", "https://googleads.googleapis.com", "Discovery host")
	outDir := flag.String("out", "spec", "output directory")
	flag.Parse()
	prov, err := run(&http.Client{Timeout: 60 * time.Second}, *base, *version, *outDir, time.Now(), 150, 500)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s/googleads-%s.discovery.json: revision %s, %d methods, %d schemas\n",
		*outDir, *version, prov.Revision, prov.Methods, prov.Schemas)
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./tools/fetchspec/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Vendor the real document (needs network)**

Run: `make update-spec`
Expected: `wrote spec/googleads-v25.discovery.json: revision 2026MMDD, 175 methods, 1489 schemas`. A later revision or a slightly different count is fine; record the numbers in your report. If the download fails, stop and report the error; do not hand-craft the file.

- [ ] **Step 6: Commit**

```bash
git add tools/fetchspec spec
git commit -m "Add fetchspec and vendor the v25 Discovery document

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 3: Generator part 1: Discovery parsing, naming, schemas, methods

**Files:**
- Create: `tools/genmanifest/discovery.go`, `tools/genmanifest/naming.go`, `tools/genmanifest/schemas.go`, `tools/genmanifest/methods.go`
- Test: `tools/genmanifest/discovery_test.go`, `tools/genmanifest/naming_test.go`, `tools/genmanifest/methods_test.go`

**Interfaces:**
- Consumes: `manifest.Method`, `manifest.Param`, `manifest.Schema`, `manifest.Field`, `manifest.Mutate`, `manifest.ShortName`, `manifest.Paging*` (Task 1); `spec/googleads-v25.discovery.json` (Task 2).
- Produces (package `main` in `tools/genmanifest`, used by Task 4):

```go
type Discovery struct { Kind, Version, Revision string; Resources map[string]Resource; Schemas map[string]SchemaDef }
func LoadDiscovery(path string) (*Discovery, error)
func MethodID(discoveryID string) string                 // strips "googleads."
func (d *Discovery) AllMethods() []MethodDef              // sorted by Discovery id
func CommandPath(id string, overrides map[string]string) []string
func SummarizeSchemas(d *Discovery) map[string]manifest.Schema
func BuildMethod(d *Discovery, def MethodDef, commandOverrides, resultsFields map[string]string) (manifest.Method, error) // Risk and Blocked left empty
func GenericOperations(d *Discovery, overrides map[string]string) (map[string]string, []string, error)
func sortedKeys[V any](m map[string]V) []string
func firstSentence(s string, limit int) string
func stripMarkers(s string) string
```

- [ ] **Step 1: Write the failing tests**

`tools/genmanifest/naming_test.go`:

```go
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
```

`tools/genmanifest/discovery_test.go`:

```go
package main

import (
	"strings"
	"sync"
	"testing"
)

var (
	realOnce sync.Once
	realDoc  *Discovery
	realErr  error
)

// realDiscovery loads the vendored document once per test binary.
func realDiscovery(t *testing.T) *Discovery {
	t.Helper()
	realOnce.Do(func() { realDoc, realErr = LoadDiscovery("../../spec/googleads-v25.discovery.json") })
	if realErr != nil {
		t.Fatal(realErr)
	}
	return realDoc
}

func methodDef(t *testing.T, d *Discovery, id string) MethodDef {
	t.Helper()
	for _, m := range d.AllMethods() {
		if MethodID(m.ID) == id {
			return m
		}
	}
	t.Fatalf("no method %s", id)
	return MethodDef{}
}

func TestLoadDiscovery(t *testing.T) {
	d := realDiscovery(t)
	if d.Version != "v25" || d.Revision == "" {
		t.Fatalf("version %q revision %q", d.Version, d.Revision)
	}
	all := d.AllMethods()
	if len(all) < 150 {
		t.Fatalf("%d methods", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].ID > all[i].ID {
			t.Fatal("AllMethods is not sorted")
		}
	}
	if MethodID("googleads.customers.campaigns.mutate") != "customers.campaigns.mutate" {
		t.Fatal("MethodID")
	}
}

func TestStripMarkersAndFirstSentence(t *testing.T) {
	if got := stripMarkers("Immutable. The resource name. More."); got != "The resource name. More." {
		t.Fatalf("stripMarkers = %q", got)
	}
	if got := stripMarkers("Output only. Required. Text"); got != "Text" {
		t.Fatalf("stacked markers = %q", got)
	}
	if got := firstSentence("One  two.\nThree.", 200); got != "One two." {
		t.Fatalf("firstSentence = %q", got)
	}
	if got := firstSentence(strings.Repeat("a", 300), 20); len([]rune(got)) != 20 || !strings.HasSuffix(got, "...") {
		t.Fatalf("truncation = %q", got)
	}
}

func TestSummarizeCampaign(t *testing.T) {
	s := SummarizeSchemas(realDiscovery(t))["GoogleAdsGoogleadsV25Resources__Campaign"]
	byName := map[string]int{}
	for i, f := range s.Fields {
		byName[f.Name] = i
	}
	status := s.Fields[byName["status"]]
	if status.Type != "string" || !contains(status.Enum, "PAUSED") {
		t.Fatalf("status %+v", status)
	}
	if id := s.Fields[byName["id"]]; !id.ReadOnly || id.Format != "int64" {
		t.Fatalf("id %+v", id)
	}
	if rn := s.Fields[byName["resourceName"]]; !rn.Immutable || strings.HasPrefix(rn.Doc, "Immutable") {
		t.Fatalf("resourceName %+v", rn)
	}
	if ns := s.Fields[byName["networkSettings"]]; ns.Type != "object" || ns.Ref != "GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings" {
		t.Fatalf("networkSettings %+v", ns)
	}
	for i := 1; i < len(s.Fields); i++ {
		if s.Fields[i-1].Name > s.Fields[i].Name {
			t.Fatal("fields are not sorted")
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
```

`tools/genmanifest/methods_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./tools/genmanifest/`
Expected: FAIL (undefined identifiers).

- [ ] **Step 3: Implement**

`tools/genmanifest/discovery.go`:

```go
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
```

`tools/genmanifest/naming.go`:

```go
package main

import (
	"strings"
	"unicode"
)

// CommandPath derives the command path for a method id: drop a leading
// "customers" segment and kebab-case each remaining one, so
// "customers.adGroupCriteria.mutate" becomes ["ad-group-criteria", "mutate"].
// An override (a space-separated path) wins.
func CommandPath(id string, overrides map[string]string) []string {
	if o, ok := overrides[id]; ok {
		return strings.Fields(o)
	}
	segs := strings.Split(id, ".")
	if len(segs) > 1 && segs[0] == "customers" {
		segs = segs[1:]
	}
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = kebab(s)
	}
	return out
}

// kebab turns camelCase (or a capitalized name) into kebab-case:
// "AdGroupCriterionCustomizers" becomes "ad-group-criterion-customizers".
func kebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
```

`tools/genmanifest/schemas.go`:

```go
package main

import (
	"strings"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// markers are the prefixes Google puts at the start of descriptions. The
// field flags carry them, so the one-line doc does not repeat them.
var markers = []string{"Required.", "Immutable.", "Output only.", "Optional."}

// SummarizeSchemas turns every Discovery schema into the compact field table
// the CLI prints for `schema <Name>` and in --help, keyed by schema id.
func SummarizeSchemas(d *Discovery) map[string]manifest.Schema {
	out := make(map[string]manifest.Schema, len(d.Schemas))
	for id, def := range d.Schemas {
		out[id] = summarize(def)
	}
	return out
}

func summarize(def SchemaDef) manifest.Schema {
	names := sortedKeys(def.Properties)
	fields := make([]manifest.Field, 0, len(names))
	for _, name := range names {
		p := def.Properties[name]
		f := manifest.Field{Name: name}
		t := p
		if p.Type == "array" && p.Items != nil {
			f.Repeated = true
			t = *p.Items
		}
		switch {
		case t.Ref != "":
			f.Type, f.Ref = "object", t.Ref
		case t.Type == "object" && t.AdditionalProperties != nil:
			f.Type = "map"
		default:
			f.Type, f.Format, f.Enum = t.Type, t.Format, t.Enum
		}
		desc := strings.TrimSpace(p.Description)
		f.ReadOnly = p.ReadOnly || strings.HasPrefix(desc, "Output only.")
		f.Required = strings.HasPrefix(desc, "Required.")
		f.Immutable = strings.HasPrefix(desc, "Immutable.")
		f.Doc = firstSentence(stripMarkers(desc), 200)
		fields = append(fields, f)
	}
	return manifest.Schema{Fields: fields}
}

// stripMarkers removes leading description markers, however many are stacked.
func stripMarkers(s string) string {
	s = strings.TrimSpace(s)
	for changed := true; changed; {
		changed = false
		for _, m := range markers {
			if strings.HasPrefix(s, m) {
				s = strings.TrimSpace(strings.TrimPrefix(s, m))
				changed = true
			}
		}
	}
	return s
}

// firstSentence collapses s to one line and keeps its first sentence,
// cut at limit runes.
func firstSentence(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}
	return truncate(s, limit)
}

// firstParagraph collapses the first paragraph of s to one line, cut at limit runes.
func firstParagraph(s string, limit int) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n\n"); i >= 0 {
		s = s[:i]
	}
	return truncate(strings.Join(strings.Fields(s), " "), limit)
}

func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return strings.TrimSpace(string(r[:limit-3])) + "..."
}
```

`tools/genmanifest/methods.go`:

```go
package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
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
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./tools/genmanifest/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/genmanifest
git commit -m "genmanifest: read Discovery methods, schemas and mutate shapes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Generator part 2: reviewed tables, Build, main, golden list, embedded manifest

**Files:**
- Create: `tools/genmanifest/classify.go`, `tools/genmanifest/build.go`, `tools/genmanifest/main.go`
- Create: `tools/genmanifest/overrides.json`, `tools/genmanifest/classifications.json`
- Create: `tools/genmanifest/testdata/mini-discovery.json`, `tools/genmanifest/testdata/golden_commands.txt` (generated)
- Create: `internal/manifest/embed.go`, `internal/manifest/manifest.json.gz` (generated)
- Test: `tools/genmanifest/build_test.go`, `tools/genmanifest/golden_test.go`, `tools/genmanifest/classify_test.go`, `internal/manifest/embed_test.go`

**Interfaces:**
- Consumes: everything Task 3 produces; `manifest.*` from Task 1.
- Produces: `manifest.Load() (*Manifest, error)` backed by the embedded, generated manifest. `make generate` rebuilds it; `go run ./tools/genmanifest -check` fails on drift. Later tasks rely on these method ids existing with these classes: `customers.googleAds.search` (read, body-token paging, command `search`), `customers.googleAds.searchStream` (read, `search-stream`), `customers.googleAds.mutate` (write, `mutate`), `customers.campaigns.mutate`, `customers.campaignBudgets.mutate` (write), `customers.listAccessibleCustomers` (read, GET), `customers.recommendations.apply` (blocked).

- [ ] **Step 1: Write the reviewed tables**

`tools/genmanifest/overrides.json`:

```json
{
  "commands": {
    "customers.googleAds.search": "search",
    "customers.googleAds.searchStream": "search-stream",
    "customers.googleAds.mutate": "mutate",
    "customers.mutate": "customer mutate"
  },
  "skiplist": {
    "media.upload": "YouTube video upload uses Google's media upload protocol, which this client does not implement"
  },
  "blocked": {
    "customers.recommendations.apply": "applying a recommendation can set a budget to an amount Google chooses, which the budget cap cannot check; read recommendations with search and carry out a worthwhile one through mutate",
    "customers.recommendationSubscriptions.mutateRecommendationSubscription": "an auto-apply subscription lets Google change budgets later, outside any check",
    "customers.reservations.bookCampaigns": "booking a reservation commits spend that the budget cap cannot check"
  },
  "generic_operations": {
    "bookCampaignsOperation": "customers.reservations.bookCampaigns",
    "quoteCampaignsOperation": "customers.reservations.quoteCampaigns",
    "recommendationSubscriptionOperation": "customers.recommendationSubscriptions.mutateRecommendationSubscription"
  },
  "results_fields": {
    "customers.operations.list": "operations"
  }
}
```

`tools/genmanifest/classifications.json`: exactly this content. It is the reviewed risk table; it decides what needs `--force`. Do not change a class without saying so in your report.

```json
{
  "audienceInsights.listInsightsEligibleDates": "read",
  "customers.AdGroupCriterionCustomizers.mutate": "write",
  "customers.CampaignGoalConfigs.mutate": "write",
  "customers.CustomerCustomizers.mutate": "write",
  "customers.Goals.mutate": "write",
  "customers.accountBudgetProposals.mutate": "admin",
  "customers.accountLinks.create": "admin",
  "customers.accountLinks.mutate": "admin",
  "customers.adGroupAdLabels.mutate": "write",
  "customers.adGroupAds.mutate": "write",
  "customers.adGroupAds.removeAutomaticallyCreatedAssets": "delete",
  "customers.adGroupAssetSets.mutate": "write",
  "customers.adGroupAssets.mutate": "write",
  "customers.adGroupBidModifiers.mutate": "write",
  "customers.adGroupCriteria.mutate": "write",
  "customers.adGroupCriterionLabels.mutate": "write",
  "customers.adGroupCustomizers.mutate": "write",
  "customers.adGroupLabels.mutate": "write",
  "customers.adGroups.mutate": "write",
  "customers.adParameters.mutate": "write",
  "customers.ads.mutate": "write",
  "customers.assetGenerations.generateImages": "read",
  "customers.assetGenerations.generateText": "read",
  "customers.assetGroupAssets.mutate": "write",
  "customers.assetGroupListingGroupFilters.mutate": "write",
  "customers.assetGroupSignals.mutate": "write",
  "customers.assetGroups.mutate": "write",
  "customers.assetSetAssets.mutate": "write",
  "customers.assetSets.mutate": "write",
  "customers.assets.mutate": "write",
  "customers.audiences.mutate": "write",
  "customers.batchJobs.addOperations": "admin",
  "customers.batchJobs.listResults": "read",
  "customers.batchJobs.mutate": "admin",
  "customers.batchJobs.run": "admin",
  "customers.biddingDataExclusions.mutate": "write",
  "customers.biddingSeasonalityAdjustments.mutate": "write",
  "customers.biddingStrategies.mutate": "write",
  "customers.billingSetups.mutate": "admin",
  "customers.campaignAssetSets.mutate": "write",
  "customers.campaignAssets.mutate": "write",
  "customers.campaignBidModifiers.mutate": "write",
  "customers.campaignBudgets.mutate": "write",
  "customers.campaignConversionGoals.mutate": "write",
  "customers.campaignCriteria.mutate": "write",
  "customers.campaignCustomizers.mutate": "write",
  "customers.campaignDrafts.listAsyncErrors": "read",
  "customers.campaignDrafts.mutate": "write",
  "customers.campaignDrafts.promote": "spend",
  "customers.campaignGroups.mutate": "write",
  "customers.campaignLabels.mutate": "write",
  "customers.campaignSharedSets.mutate": "write",
  "customers.campaigns.enablePMaxBrandGuidelines": "write",
  "customers.campaigns.mutate": "write",
  "customers.conversionActions.mutate": "write",
  "customers.conversionCustomVariables.mutate": "write",
  "customers.conversionGoalCampaignConfigs.mutate": "write",
  "customers.conversionValueRuleSets.mutate": "write",
  "customers.conversionValueRules.mutate": "write",
  "customers.createCustomerClient": "admin",
  "customers.customAudiences.mutate": "write",
  "customers.customConversionGoals.mutate": "write",
  "customers.customInterests.mutate": "write",
  "customers.customerAssetSets.mutate": "write",
  "customers.customerAssets.mutate": "write",
  "customers.customerClientLinks.mutate": "admin",
  "customers.customerConversionGoals.mutate": "write",
  "customers.customerLabels.mutate": "write",
  "customers.customerManagerLinks.moveManagerLink": "admin",
  "customers.customerManagerLinks.mutate": "admin",
  "customers.customerNegativeCriteria.mutate": "write",
  "customers.customerSkAdNetworkConversionValueSchemas.mutate": "write",
  "customers.customerUserAccessInvitations.mutate": "admin",
  "customers.customerUserAccesses.mutate": "admin",
  "customers.customizerAttributes.mutate": "write",
  "customers.dataLinks.create": "admin",
  "customers.dataLinks.remove": "admin",
  "customers.dataLinks.update": "admin",
  "customers.experimentArms.mutate": "write",
  "customers.experiments.endExperiment": "write",
  "customers.experiments.graduateExperiment": "spend",
  "customers.experiments.listExperimentAsyncErrors": "read",
  "customers.experiments.mutate": "write",
  "customers.experiments.promoteExperiment": "spend",
  "customers.experiments.scheduleExperiment": "spend",
  "customers.generateAdGroupThemes": "read",
  "customers.generateAudienceCompositionInsights": "read",
  "customers.generateAudienceDefinition": "read",
  "customers.generateAudienceOverlapInsights": "read",
  "customers.generateBenchmarksMetrics": "read",
  "customers.generateCreatorInsights": "read",
  "customers.generateInsightsFinderReport": "read",
  "customers.generateKeywordForecastMetrics": "read",
  "customers.generateKeywordHistoricalMetrics": "read",
  "customers.generateKeywordIdeas": "read",
  "customers.generateReachForecast": "read",
  "customers.generateShareablePreviews": "read",
  "customers.generateSuggestedTargetingInsights": "read",
  "customers.generateTargetingSuggestionMetrics": "read",
  "customers.generateTrendingInsights": "read",
  "customers.getIdentityVerification": "read",
  "customers.googleAds.mutate": "write",
  "customers.googleAds.search": "read",
  "customers.googleAds.searchStream": "read",
  "customers.incentives.applyIncentive": "admin",
  "customers.invoices.list": "read",
  "customers.keywordPlanAdGroupKeywords.mutate": "write",
  "customers.keywordPlanAdGroups.mutate": "write",
  "customers.keywordPlanCampaignKeywords.mutate": "write",
  "customers.keywordPlanCampaigns.mutate": "write",
  "customers.keywordPlans.mutate": "write",
  "customers.labels.mutate": "write",
  "customers.listAccessibleCustomers": "read",
  "customers.localServices.appendLeadConversation": "admin",
  "customers.localServicesLeads.provideLeadFeedback": "admin",
  "customers.multiPartyAuthReview.resolve": "admin",
  "customers.mutate": "write",
  "customers.offlineUserDataJobs.addOperations": "admin",
  "customers.offlineUserDataJobs.create": "admin",
  "customers.offlineUserDataJobs.run": "admin",
  "customers.operations.cancel": "write",
  "customers.operations.delete": "delete",
  "customers.operations.get": "read",
  "customers.operations.list": "read",
  "customers.operations.wait": "read",
  "customers.paymentsAccounts.list": "read",
  "customers.productLinkInvitations.create": "admin",
  "customers.productLinkInvitations.remove": "admin",
  "customers.productLinkInvitations.update": "admin",
  "customers.productLinks.create": "admin",
  "customers.productLinks.remove": "admin",
  "customers.recommendationSubscriptions.mutateRecommendationSubscription": "spend",
  "customers.recommendations.apply": "spend",
  "customers.recommendations.dismiss": "write",
  "customers.recommendations.generate": "read",
  "customers.remarketingActions.mutate": "write",
  "customers.removeCampaignAutomaticallyCreatedAsset": "delete",
  "customers.reservations.bookCampaigns": "spend",
  "customers.reservations.quoteCampaigns": "read",
  "customers.searchAudienceInsightsAttributes": "read",
  "customers.sharedCriteria.mutate": "write",
  "customers.sharedSets.mutate": "write",
  "customers.smartCampaignSettings.generatePMaxDraftCampaign": "write",
  "customers.smartCampaignSettings.getSmartCampaignStatus": "read",
  "customers.smartCampaignSettings.mutate": "write",
  "customers.startIdentityVerification": "admin",
  "customers.suggestBrands": "read",
  "customers.suggestKeywordThemes": "read",
  "customers.suggestSmartCampaignAd": "read",
  "customers.suggestSmartCampaignBudgetOptions": "read",
  "customers.suggestTravelAssets": "read",
  "customers.thirdPartyAppAnalyticsLinks.regenerateShareableLinkId": "admin",
  "customers.uploadCallConversions": "admin",
  "customers.uploadClickConversions": "admin",
  "customers.uploadConversionAdjustments": "admin",
  "customers.uploadUserData": "admin",
  "customers.userListCustomerTypes.mutate": "write",
  "customers.userLists.mutate": "write",
  "customers.youTubeVideoUploads.remove": "delete",
  "customers.youTubeVideoUploads.update": "write",
  "generateConversionRates": "read",
  "geoTargetConstants.suggest": "read",
  "googleAdsFields.get": "read",
  "googleAdsFields.search": "read",
  "incentives.fetchIncentive": "read",
  "keywordThemeConstants.suggest": "read",
  "listBenchmarksAvailableDates": "read",
  "listBenchmarksLocations": "read",
  "listBenchmarksProducts": "read",
  "listBenchmarksSources": "read",
  "listPlannableLocations": "read",
  "listPlannableProducts": "read",
  "listPlannableUserInterests": "read",
  "listPlannableUserLists": "read"
}
```

If the vendored document (Task 2) is a newer revision with methods this table lacks, Build reports them as proposals: run `go run ./tools/genmanifest -write-proposals`, apply `proposeRisk`'s answers, and list every added id and class in your report for review.

`tools/genmanifest/testdata/mini-discovery.json`:

```json
{
  "kind": "discovery#restDescription", "version": "v25", "revision": "20260101",
  "resources": {
    "customers": {
      "methods": {
        "listAccessibleCustomers": {"id": "googleads.customers.listAccessibleCustomers", "path": "v25/customers:listAccessibleCustomers", "httpMethod": "GET", "response": {"$ref": "ListResp"}},
        "mutate": {"id": "googleads.customers.mutate", "path": "v25/customers/{+customerId}:mutate", "httpMethod": "POST",
          "parameters": {"customerId": {"type": "string", "location": "path", "required": true, "pattern": "^[^/]+$"}},
          "request": {"$ref": "MutateCustomerRequest"}, "response": {"$ref": "Empty"}}
      },
      "resources": {
        "googleAds": {"methods": {
          "mutate": {"id": "googleads.customers.googleAds.mutate", "path": "v25/customers/{+customerId}/googleAds:mutate", "httpMethod": "POST",
            "parameters": {"customerId": {"type": "string", "location": "path", "required": true, "pattern": "^[^/]+$"}},
            "request": {"$ref": "MutateGoogleAdsRequest"}, "response": {"$ref": "Empty"}},
          "search": {"id": "googleads.customers.googleAds.search", "path": "v25/customers/{+customerId}/googleAds:search", "httpMethod": "POST",
            "parameters": {"customerId": {"type": "string", "location": "path", "required": true, "pattern": "^[^/]+$"}},
            "request": {"$ref": "SearchRequest"}, "response": {"$ref": "SearchResponse"}}
        }},
        "campaigns": {"methods": {
          "mutate": {"id": "googleads.customers.campaigns.mutate", "path": "v25/customers/{+customerId}/campaigns:mutate", "httpMethod": "POST",
            "parameters": {"customerId": {"type": "string", "location": "path", "required": true, "pattern": "^[^/]+$"}},
            "request": {"$ref": "MutateCampaignsRequest"}, "response": {"$ref": "Empty"}}
        }}
      }
    }
  },
  "schemas": {
    "Empty": {"id": "Empty", "type": "object", "properties": {}},
    "ListResp": {"id": "ListResp", "type": "object", "properties": {"resourceNames": {"type": "array", "items": {"type": "string"}}}},
    "Campaign": {"id": "Campaign", "type": "object", "properties": {"name": {"type": "string"}, "status": {"type": "string", "enum": ["UNSPECIFIED", "UNKNOWN", "ENABLED", "PAUSED", "REMOVED"]}}},
    "Customer": {"id": "Customer", "type": "object", "properties": {"id": {"type": "string", "format": "int64", "readOnly": true}}},
    "CampaignOperation": {"id": "CampaignOperation", "type": "object", "properties": {"create": {"$ref": "Campaign"}, "update": {"$ref": "Campaign"}, "remove": {"type": "string"}, "updateMask": {"type": "string"}}},
    "CustomerOperation": {"id": "CustomerOperation", "type": "object", "properties": {"update": {"$ref": "Customer"}, "updateMask": {"type": "string"}}},
    "MutateCampaignsRequest": {"id": "MutateCampaignsRequest", "type": "object", "properties": {"operations": {"type": "array", "items": {"$ref": "CampaignOperation"}}, "validateOnly": {"type": "boolean"}, "partialFailure": {"type": "boolean"}}},
    "MutateCustomerRequest": {"id": "MutateCustomerRequest", "type": "object", "properties": {"operation": {"$ref": "CustomerOperation"}, "validateOnly": {"type": "boolean"}}},
    "MutateOperation": {"id": "MutateOperation", "type": "object", "properties": {"campaignOperation": {"$ref": "CampaignOperation"}}},
    "MutateGoogleAdsRequest": {"id": "MutateGoogleAdsRequest", "type": "object", "properties": {"mutateOperations": {"type": "array", "items": {"$ref": "MutateOperation"}}, "validateOnly": {"type": "boolean"}, "partialFailure": {"type": "boolean"}}},
    "SearchRequest": {"id": "SearchRequest", "type": "object", "properties": {"query": {"type": "string"}, "pageToken": {"type": "string"}, "validateOnly": {"type": "boolean"}}},
    "SearchResponse": {"id": "SearchResponse", "type": "object", "properties": {"results": {"type": "array", "items": {"$ref": "Row"}}, "nextPageToken": {"type": "string"}}},
    "Row": {"id": "Row", "type": "object", "properties": {"campaign": {"$ref": "Campaign"}}}
  }
}
```

- [ ] **Step 2: Write the failing tests**

`tools/genmanifest/build_test.go`:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

const mini = "testdata/mini-discovery.json"

func miniTables() (map[string]any, map[string]string) {
	over := map[string]any{"commands": map[string]string{
		"customers.googleAds.search": "search", "customers.googleAds.mutate": "mutate", "customers.mutate": "customer mutate",
	}}
	table := map[string]string{
		"customers.listAccessibleCustomers": "read", "customers.mutate": "write",
		"customers.googleAds.mutate": "write", "customers.googleAds.search": "read", "customers.campaigns.mutate": "write",
	}
	return over, table
}

func writeJSON(t *testing.T, dir, name string, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func buildMini(t *testing.T, over map[string]any, table map[string]string) (*manifest.Manifest, []Proposal, error) {
	t.Helper()
	dir := t.TempDir()
	return Build(mini, writeJSON(t, dir, "over.json", over), writeJSON(t, dir, "class.json", table))
}

func TestBuildMini(t *testing.T) {
	over, table := miniTables()
	m, props, err := buildMini(t, over, table)
	if err != nil || len(props) != 0 {
		t.Fatalf("err %v proposals %v", err, props)
	}
	if len(m.Methods) != 5 || m.APIVersion != "v25" || m.DiscoveryRevision != "20260101" {
		t.Fatalf("%d methods, %q %q", len(m.Methods), m.APIVersion, m.DiscoveryRevision)
	}
	if s := m.ByID("customers.googleAds.search"); s.Paging != manifest.PagingBodyToken || strings.Join(s.Command, " ") != "search" {
		t.Fatalf("search %+v", s)
	}
	if c := m.ByID("customers.mutate"); strings.Join(c.Command, " ") != "customer mutate" || c.Mutate.OperationsField != "operation" {
		t.Fatalf("customer mutate %+v", c)
	}
	if m.GenericOperations["campaignOperation"] != "customers.campaigns.mutate" {
		t.Fatalf("generic %v", m.GenericOperations)
	}
	if _, ok := m.Schemas["Campaign"]; !ok {
		t.Fatal("schemas missing")
	}
}

func TestBuildProposesUnclassified(t *testing.T) {
	over, table := miniTables()
	delete(table, "customers.campaigns.mutate")
	_, props, err := buildMini(t, over, table)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 1 || props[0].ID != "customers.campaigns.mutate" || props[0].Proposed != "write" {
		t.Fatalf("proposals %+v", props)
	}
}

func TestBuildProblems(t *testing.T) {
	cases := map[string]struct {
		edit func(over map[string]any, table map[string]string)
		want string
	}{
		"invalid risk":     {func(o map[string]any, tb map[string]string) { tb["customers.mutate"] = "wirte" }, `risk "wirte"`},
		"stale class":      {func(o map[string]any, tb map[string]string) { tb["customers.gone.mutate"] = "write" }, `classification entry "customers.gone.mutate"`},
		"stale skiplist":   {func(o map[string]any, tb map[string]string) { o["skiplist"] = map[string]string{"nope.x": "why"} }, `skiplist entry "nope.x"`},
		"empty skip reason": {func(o map[string]any, tb map[string]string) {
			o["skiplist"] = map[string]string{"customers.campaigns.mutate": " "}
			delete(tb, "customers.campaigns.mutate")
		}, "empty reason"},
		"empty block reason": {func(o map[string]any, tb map[string]string) { o["blocked"] = map[string]string{"customers.mutate": ""} }, "empty reason"},
		"collision": {func(o map[string]any, tb map[string]string) {
			o["commands"].(map[string]string)["customers.mutate"] = "mutate"
		}, "command collision"},
		"reserved": {func(o map[string]any, tb map[string]string) {
			o["commands"].(map[string]string)["customers.googleAds.search"] = "config"
		}, "built-in"},
		"leaf and group": {func(o map[string]any, tb map[string]string) {
			o["commands"].(map[string]string)["customers.googleAds.search"] = "customer"
		}, "also a group"},
		"stale generic": {func(o map[string]any, tb map[string]string) {
			o["generic_operations"] = map[string]string{"campaignOperation": "customers.nothing.mutate"}
		}, "names no method"},
	}
	for name, c := range cases {
		over, table := miniTables()
		c.edit(over, table)
		_, _, err := buildMini(t, over, table)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, c.want)
		}
	}
}

func TestBuildRealDocument(t *testing.T) {
	m, props, err := Build("../../spec/googleads-v25.discovery.json", "overrides.json", "classifications.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 0 {
		t.Fatalf("unclassified methods: %+v", props)
	}
	if b := m.ByID("customers.recommendations.apply"); b == nil || b.Blocked == "" || b.Risk != manifest.RiskSpend {
		t.Fatalf("recommendations.apply %+v", b)
	}
	if b := m.ByID("customers.campaignBudgets.mutate"); b == nil || b.Mutate.Resource != "CampaignBudget" {
		t.Fatalf("campaignBudgets.mutate %+v", b)
	}
	if m.ByID("media.upload") != nil {
		t.Fatal("media.upload must be skiplisted")
	}
	if len(m.GenericOperations) < 60 {
		t.Fatalf("%d generic operation types", len(m.GenericOperations))
	}
}
```

`tools/genmanifest/classify_test.go`:

```go
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
```

`tools/genmanifest/golden_test.go`:

```go
package main

import (
	"os"
	"strings"
	"testing"
)

// The golden list pins every command path with its method id, risk class and
// blocked state. A diff here is a public API change: review it, then
// regenerate with UPDATE_GOLDEN=1 go test ./tools/genmanifest.
func TestGoldenCommands(t *testing.T) {
	m, _, err := Build("../../spec/googleads-v25.discovery.json", "overrides.json", "classifications.json")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, meth := range m.Methods {
		line := strings.Join(meth.Command, " ") + "\t" + meth.ID + "\t" + meth.Risk
		if meth.Blocked != "" {
			line += "\tblocked"
		}
		b.WriteString(line + "\n")
	}
	const golden = "testdata/golden_commands.txt"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Fatal("command list differs from testdata/golden_commands.txt; review, then UPDATE_GOLDEN=1")
	}
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./tools/genmanifest/`
Expected: FAIL (undefined `Build`, `Proposal`, `proposeRisk`, `validRisk`, `LoadClassifications`).

- [ ] **Step 4: Implement classify.go, build.go and main.go**

`tools/genmanifest/classify.go`:

```go
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
```

`tools/genmanifest/build.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
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
var reserved = set("api", "auth", "commands", "completion", "config", "help", "init", "schema", "version")

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
```

`tools/genmanifest/main.go`: copy `/Users/daniel/Development/bexio-cli/tools/genmanifest/main.go` and change:
1. Drop the `Proposal` type and the `Build` function from the copy (they live in `build.go`), and the unused `sort` import.
2. The import path becomes `github.com/wir-drei-digital/google-ads-cli/internal/manifest`.
3. Flag defaults: `-spec` = `spec/googleads-v25.discovery.json`, `-overrides` = `tools/genmanifest/overrides.json`, `-classifications` = `tools/genmanifest/classifications.json`, `-out` = `internal/manifest/manifest.json.gz`. Update the usage strings to say "Discovery document" instead of "OpenAPI spec".
4. In the proposals branch, `merged[p.ID] = p.Proposed` (the field is `ID`, not `OperationID`), and the "unclassified" line prints `p.ID, p.HTTPMethod, p.Path, p.Proposed`.
5. The final line prints `wrote %s: %d methods, %d bytes gz` with `len(m.Methods)`.

Keep `encode`, `-check` (compare the decompressed JSON, print "manifest drift: run `make generate` and commit") and `-write-proposals` exactly as in bexio-cli.

- [ ] **Step 5: Generate and pin the golden list**

Run: `go vet ./tools/genmanifest/ && make generate && UPDATE_GOLDEN=1 go test ./tools/genmanifest/ -run TestGoldenCommands`
Expected: `wrote internal/manifest/manifest.json.gz: 174 methods, <N> bytes gz`, then PASS. Open `testdata/golden_commands.txt` and confirm it has 174 lines, three of them ending in `blocked`, and that `search`, `search-stream`, `mutate` and `customer mutate` appear.

- [ ] **Step 6: Embed the manifest**

`internal/manifest/embed.go`:

```go
package manifest

import _ "embed"

//go:embed manifest.json.gz
var embedded []byte

// Load parses the manifest embedded in this binary.
func Load() (*Manifest, error) { return Parse(embedded) }
```

`internal/manifest/embed_test.go`:

```go
package manifest

import "testing"

func TestLoadEmbedded(t *testing.T) {
	m, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if m.APIVersion != "v25" || len(m.Methods) < 150 {
		t.Fatalf("api %q, %d methods", m.APIVersion, len(m.Methods))
	}
	s := m.ByID("customers.googleAds.search")
	if s == nil || s.Paging != PagingBodyToken || s.Risk != RiskRead {
		t.Fatalf("search %+v", s)
	}
	if f := m.Find("POST", "v25/customers/1234567890/campaignBudgets:mutate"); f == nil || f.ID != "customers.campaignBudgets.mutate" {
		t.Fatalf("Find on the real manifest: %+v", f)
	}
}
```

- [ ] **Step 7: Run everything**

Run: `go test ./... && go vet ./... && go run ./tools/genmanifest -check`
Expected: PASS; `manifest up to date`.

- [ ] **Step 8: Commit**

```bash
git add tools/genmanifest internal/manifest
git commit -m "genmanifest: reviewed tables, Build, golden list, embedded manifest

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 5: Config package

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (package `config`):

```go
const DefaultAPIBase = "https://googleads.googleapis.com"
const DefaultTokenURL = "https://oauth2.googleapis.com/token"
type Config struct { ServiceAccountKey json.RawMessage; CustomerID, LoginCustomerID string; ReadOnly bool; BudgetCapMicros int64 }
func Path() (string, error)            // <user config dir>/googleads/config.json
func Load() (Config, error)
func Save(c Config) error
type Resolved struct { KeyPath string; KeyJSON []byte; CustomerID, LoginCustomerID string; ReadOnly bool; BudgetCapMicros int64; APIBase, TokenURL string; AllowCustomBase bool; FromEnv map[string]bool }
func (r Resolved) KeySource() string   // "env", "config" or ""
func Resolve(getenv func(string) string) (Resolved, error)
func NormalizeCustomerID(s string) (string, error)
func ParseMoneyToMicros(s string) (int64, error)
func FormatMicros(micros int64) string
```

- [ ] **Step 1: Write the failing tests**

`internal/config/config_test.go`:

```go
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userConfigDir = old })
	return dir
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPathAndMissingFile(t *testing.T) {
	dir := setTestDir(t)
	p, err := Path()
	if err != nil || p != filepath.Join(dir, "googleads", "config.json") {
		t.Fatalf("path %q %v", p, err)
	}
	c, err := Load()
	if err != nil || c.CustomerID != "" || c.ServiceAccountKey != nil {
		t.Fatalf("missing file must load as zero: %+v %v", c, err)
	}
}

func TestSaveLoadRoundTripAndPerms(t *testing.T) {
	setTestDir(t)
	in := Config{ServiceAccountKey: json.RawMessage(`{"type":"service_account"}`), CustomerID: "1234567890",
		LoginCustomerID: "1112223333", ReadOnly: true, BudgetCapMicros: 29610000}
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil || string(out.ServiceAccountKey) != `{"type":"service_account"}` || out.CustomerID != in.CustomerID ||
		out.LoginCustomerID != in.LoginCustomerID || !out.ReadOnly || out.BudgetCapMicros != in.BudgetCapMicros {
		t.Fatalf("round trip %+v %v", out, err)
	}
	p, _ := Path()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("perms %v, want 0600", fi.Mode().Perm())
	}
}

func TestLoadCorrupt(t *testing.T) {
	setTestDir(t)
	p, _ := Path()
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte("{nope"), 0o600)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("err = %v", err)
	}
}

func TestNormalizeCustomerID(t *testing.T) {
	ok := map[string]string{
		"1234567890":       "1234567890",
		"123-456-7890":     "1234567890",
		" 123 456 7890 ":   "1234567890",
		"123 - 456 - 7890": "1234567890",
	}
	for in, want := range ok {
		if got, err := NormalizeCustomerID(in); err != nil || got != want {
			t.Errorf("NormalizeCustomerID(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "123-456-789", "12345678901", "abc-def-ghij", "123_456_7890"} {
		if _, err := NormalizeCustomerID(bad); err == nil {
			t.Errorf("NormalizeCustomerID(%q) must fail", bad)
		}
	}
}

func TestParseMoneyToMicros(t *testing.T) {
	ok := map[string]int64{"30": 30000000, "29.61": 29610000, "0.000001": 1, " 5.5 ": 5500000, "1000000": 1000000000000}
	for in, want := range ok {
		if got, err := ParseMoneyToMicros(in); err != nil || got != want {
			t.Errorf("ParseMoneyToMicros(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	bad := map[string]string{
		"29,61":      "dot", // the Swiss and German decimal comma must never be misread
		"1,000":      "dot",
		"0":          "greater than 0",
		"0.0":        "greater than 0",
		"-5":         "want an amount",
		"1e3":        "want an amount",
		"29.1234567": "want an amount",
		"":           "want an amount",
		"1000001":    "too large",
	}
	for in, want := range bad {
		if _, err := ParseMoneyToMicros(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseMoneyToMicros(%q) err = %v, want it to contain %q", in, err, want)
		}
	}
}

func TestFormatMicros(t *testing.T) {
	cases := map[int64]string{30000000: "30", 29610000: "29.61", 1: "0.000001", 1500000: "1.5", 0: "0"}
	for in, want := range cases {
		if got := FormatMicros(in); got != want {
			t.Errorf("FormatMicros(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveDefaults(t *testing.T) {
	setTestDir(t)
	r, err := Resolve(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.APIBase != DefaultAPIBase || r.TokenURL != DefaultTokenURL || r.KeySource() != "" || r.ReadOnly || r.FromEnv != nil {
		t.Fatalf("%+v", r)
	}
}

func TestResolveFileValues(t *testing.T) {
	setTestDir(t)
	Save(Config{ServiceAccountKey: json.RawMessage(`{}`), CustomerID: "1234567890", BudgetCapMicros: 30000000, ReadOnly: true})
	r, err := Resolve(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.KeySource() != "config" || r.CustomerID != "1234567890" || r.BudgetCapMicros != 30000000 || !r.ReadOnly {
		t.Fatalf("%+v", r)
	}
}

func TestResolveNormalizesEnvCustomerID(t *testing.T) {
	setTestDir(t)
	Save(Config{CustomerID: "1111111111"})
	r, err := Resolve(env(map[string]string{
		"GOOGLE_ADS_CUSTOMER_ID": "123-456-7890", "GOOGLE_ADS_LOGIN_CUSTOMER_ID": " 999 888 7777 ",
		"GOOGLE_ADS_JSON_KEY_FILE_PATH": "/keys/k.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if r.CustomerID != "1234567890" || r.LoginCustomerID != "9998887777" {
		t.Fatalf("customer %q login %q", r.CustomerID, r.LoginCustomerID)
	}
	if r.KeyPath != "/keys/k.json" || r.KeySource() != "env" {
		t.Fatalf("key %+v", r)
	}
	if !r.FromEnv["GOOGLE_ADS_CUSTOMER_ID"] || !r.FromEnv["GOOGLE_ADS_JSON_KEY_FILE_PATH"] {
		t.Fatalf("FromEnv %v", r.FromEnv)
	}
}

func TestResolveRejectsBadCustomerID(t *testing.T) {
	setTestDir(t)
	if _, err := Resolve(env(map[string]string{"GOOGLE_ADS_CUSTOMER_ID": "12345"})); err == nil ||
		!strings.Contains(err.Error(), "customer ID") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveReadOnlyValues(t *testing.T) {
	setTestDir(t)
	for v, want := range map[string]bool{"1": true, "true": true, "TRUE": true, "0": false, "no": false} {
		r, _ := Resolve(env(map[string]string{"GOOGLE_ADS_READ_ONLY": v}))
		if r.ReadOnly != want {
			t.Errorf("GOOGLE_ADS_READ_ONLY=%s: %v", v, r.ReadOnly)
		}
	}
}

func TestBudgetCapHasNoEnvironmentOverride(t *testing.T) {
	setTestDir(t)
	Save(Config{BudgetCapMicros: 30000000})
	r, _ := Resolve(env(map[string]string{"GOOGLE_ADS_BUDGET_CAP": "999", "GOOGLE_ADS_BUDGET_CAP_MICROS": "999"}))
	if r.BudgetCapMicros != 30000000 {
		t.Fatalf("cap %d: the environment must not move the cap", r.BudgetCapMicros)
	}
}

func TestResolveBaseOverrides(t *testing.T) {
	setTestDir(t)
	r, _ := Resolve(env(map[string]string{
		"GOOGLE_ADS_API_BASE": "http://127.0.0.1:9/", "GOOGLE_ADS_TOKEN_URL": "http://127.0.0.1:9/token",
		"GOOGLE_ADS_ALLOW_CUSTOM_BASE": "1",
	}))
	if r.APIBase != "http://127.0.0.1:9" || r.TokenURL != "http://127.0.0.1:9/token" || !r.AllowCustomBase {
		t.Fatalf("%+v", r)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/config/`
Expected: FAIL (undefined identifiers).

- [ ] **Step 3: Implement**

`internal/config/config.go`:

```go
// Package config resolves the CLI's settings from the config file and the
// environment. Environment variables win, except for the budget cap, which
// only the config file can set: an agent's environment must not be able to
// raise the limit a person chose.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DefaultAPIBase is the Google Ads API host.
const DefaultAPIBase = "https://googleads.googleapis.com"

// DefaultTokenURL is Google's OAuth 2.0 token endpoint.
const DefaultTokenURL = "https://oauth2.googleapis.com/token"

// userConfigDir is swapped in tests.
var userConfigDir = os.UserConfigDir

// Config is the on-disk configuration.
type Config struct {
	ServiceAccountKey json.RawMessage `json:"service_account_key,omitempty"`
	CustomerID        string          `json:"customer_id,omitempty"`
	LoginCustomerID   string          `json:"login_customer_id,omitempty"`
	ReadOnly          bool            `json:"read_only,omitempty"`
	BudgetCapMicros   int64           `json:"budget_cap_micros,omitempty"`
}

// Path returns <os.UserConfigDir()>/googleads/config.json.
func Path() (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "googleads", "config.json"), nil
}

// Load reads the config file. A missing file is the zero Config; a corrupt
// file is an error.
func Load() (Config, error) {
	p, err := Path()
	if err != nil {
		return Config{}, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("%s is corrupt: %w", p, err)
	}
	return c, nil
}

// Save writes the config file with 0600 perms inside a 0700 directory,
// through a temp file and a rename, so a reader never sees a partial file.
func Save(c Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// Same directory as the target: rename is atomic only within a
	// filesystem, and CreateTemp already makes the file 0600.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	// fsync before the rename: rename is atomic, not durable.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return err
	}
	// Best effort: make the rename itself durable. Windows cannot open a
	// directory for this, which is fine.
	if d, err := os.Open(filepath.Dir(p)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// Resolved is the effective configuration after the environment overlay.
type Resolved struct {
	// KeyPath names the key file (GOOGLE_ADS_JSON_KEY_FILE_PATH); KeyJSON is
	// the key stored in the config file. KeyPath wins.
	KeyPath         string
	KeyJSON         []byte
	CustomerID      string // ten digits, or ""
	LoginCustomerID string // ten digits, or ""
	ReadOnly        bool
	BudgetCapMicros int64 // 0 means no cap is configured
	APIBase         string
	TokenURL        string
	AllowCustomBase bool
	// FromEnv names the GOOGLE_ADS_* variables that were set non-empty.
	// Names only, never values.
	FromEnv map[string]bool
}

// KeySource reports where the key comes from: "env", "config" or "".
func (r Resolved) KeySource() string {
	switch {
	case r.KeyPath != "":
		return "env"
	case len(r.KeyJSON) > 0:
		return "config"
	}
	return ""
}

// Resolve loads the config file and overlays the environment.
func Resolve(getenv func(string) string) (Resolved, error) {
	c, err := Load()
	if err != nil {
		return Resolved{}, err
	}
	r := Resolved{
		KeyJSON: c.ServiceAccountKey, ReadOnly: c.ReadOnly, BudgetCapMicros: c.BudgetCapMicros,
		APIBase: DefaultAPIBase, TokenURL: DefaultTokenURL,
	}
	env := func(name string) string {
		v := strings.TrimSpace(getenv(name))
		if v != "" {
			if r.FromEnv == nil {
				r.FromEnv = map[string]bool{}
			}
			r.FromEnv[name] = true
		}
		return v
	}
	r.KeyPath = env("GOOGLE_ADS_JSON_KEY_FILE_PATH")
	customer, login := c.CustomerID, c.LoginCustomerID
	if v := env("GOOGLE_ADS_CUSTOMER_ID"); v != "" {
		customer = v
	}
	if v := env("GOOGLE_ADS_LOGIN_CUSTOMER_ID"); v != "" {
		login = v
	}
	if customer != "" {
		if r.CustomerID, err = NormalizeCustomerID(customer); err != nil {
			return Resolved{}, fmt.Errorf("customer ID: %w", err)
		}
	}
	if login != "" {
		if r.LoginCustomerID, err = NormalizeCustomerID(login); err != nil {
			return Resolved{}, fmt.Errorf("login customer ID: %w", err)
		}
	}
	if v := env("GOOGLE_ADS_READ_ONLY"); v == "1" || strings.EqualFold(v, "true") {
		r.ReadOnly = true
	}
	if v := env("GOOGLE_ADS_API_BASE"); v != "" {
		r.APIBase = strings.TrimRight(v, "/")
	}
	if v := env("GOOGLE_ADS_TOKEN_URL"); v != "" {
		r.TokenURL = v
	}
	if env("GOOGLE_ADS_ALLOW_CUSTOM_BASE") == "1" {
		r.AllowCustomBase = true
	}
	return r, nil
}

var tenDigits = regexp.MustCompile(`^[0-9]{10}$`)

// NormalizeCustomerID accepts a customer ID as Google Ads displays it
// ("123-456-7890") or as bare digits, with stray spaces, and returns the ten
// digits the API wants.
func NormalizeCustomerID(s string) (string, error) {
	d := strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s))
	if !tenDigits.MatchString(d) {
		return "", fmt.Errorf("want 10 digits such as 123-456-7890, got %q", s)
	}
	return d, nil
}

var moneyPattern = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]{1,6}))?$`)

// maxUnits bounds amounts far below int64 micros overflow.
const maxUnits = 1_000_000

// ParseMoneyToMicros reads an amount in currency units ("29.61") and returns
// micros (29610000). Only a dot separates decimals, at most six places; zero,
// negatives, exponents and thousands separators are refused.
func ParseMoneyToMicros(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, ",") {
		return 0, fmt.Errorf("use a dot as the decimal separator and no thousands separator (29.61, not 29,61), got %q", s)
	}
	m := moneyPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("want an amount such as 30 or 29.61 (at most six decimal places), got %q", s)
	}
	units, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || units > maxUnits || (units == maxUnits && strings.Trim(m[2], "0") != "") {
		return 0, fmt.Errorf("amount %q is too large (at most %d)", s, maxUnits)
	}
	frac, _ := strconv.ParseInt(m[2]+strings.Repeat("0", 6-len(m[2])), 10, 64)
	micros := units*1_000_000 + frac
	if micros == 0 {
		return 0, errors.New("amount must be greater than 0")
	}
	return micros, nil
}

// FormatMicros renders micros as currency units without trailing zeros:
// 29610000 becomes "29.61", 30000000 becomes "30".
func FormatMicros(micros int64) string {
	units, frac := micros/1_000_000, micros%1_000_000
	if frac == 0 {
		return strconv.FormatInt(units, 10)
	}
	return strings.TrimRight(fmt.Sprintf("%d.%06d", units, frac), "0")
}
```

Note on `ParseMoneyToMicros("")`: `m[2]` is empty when there is no fraction; `strconv.ParseInt("000000")` is 0.

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/config/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "Add config: file, environment overlay, customer IDs, money parsing

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: API client

**Files:**
- Create: `internal/api/errors.go`, `internal/api/client.go`, `internal/api/google.go`, `internal/api/retry.go`
- Test: `internal/api/client_test.go`, `internal/api/google_test.go`

**Interfaces:**
- Consumes: `manifest.RiskRead` (Task 1).
- Produces (package `api`, used by Tasks 7 to 12):

```go
const (KindAuth, KindForbidden, KindNotFound, KindValidation, KindRateLimited, KindServer, KindTransport, KindOutcomeUnknown, KindIncomplete, KindUsage, KindPartialFailure, KindConflict string)
type Error struct { Kind, Message string; Status int; Details any; RequestID string } // JSON: kind, error, status, details, request_id
func (e *Error) Error() string
func Usagef(format string, a ...any) *Error
type Client struct { BaseURL, LoginCustomerID string; ReadOnly, AllowCustomBase bool; TokenSource func(context.Context) (string, error); Timeout, RetryBudget time.Duration; MaxAttempts int; Verbose io.Writer; HTTP *http.Client; Sleep func(time.Duration) }
func StaticToken(tok string) func(context.Context) (string, error)
type Request struct { Method, Path string; Query url.Values; Body []byte; Risk string; ValidateOnly bool; MethodID string; Headers http.Header }
type Response struct { Status int; Header http.Header; Body []byte }
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) // error is always *Error
func PartialFailure(r Request, resp *Response) *Error                  // nil when nothing failed
```

- [ ] **Step 1: Write the failing tests**

`internal/api/client_test.go`:

```go
package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var slept []time.Duration
	return &Client{BaseURL: srv.URL, TokenSource: StaticToken("tok"), AllowCustomBase: true,
		Sleep: func(d time.Duration) { slept = append(slept, d) }}, &slept
}

func asErr(t *testing.T, err error) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not *Error", err)
	}
	return e
}

var search = Request{Method: "POST", Path: "v25/customers/1234567890/googleAds:search",
	Body: []byte(`{"query":"SELECT customer.id FROM customer"}`), Risk: manifest.RiskRead, MethodID: "customers.googleAds.search"}

func TestDoSendsHeadersAndJoinsPath(t *testing.T) {
	var got *http.Request
	var body string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Write([]byte(`{"results":[]}`))
	})
	c.LoginCustomerID = "1112223333"
	resp, err := c.Do(context.Background(), search)
	if err != nil || resp.Status != 200 || string(resp.Body) != `{"results":[]}` {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if got.URL.Path != "/v25/customers/1234567890/googleAds:search" {
		t.Fatalf("path %q", got.URL.Path)
	}
	if got.Header.Get("Authorization") != "Bearer tok" || got.Header.Get("Accept") != "application/json" ||
		got.Header.Get("Content-Type") != "application/json" || got.Header.Get("login-customer-id") != "1112223333" {
		t.Fatalf("headers %v", got.Header)
	}
	if got.Header.Get("developer-token") != "" {
		t.Fatal("the developer-token header must never be sent")
	}
	if body != string(search.Body) {
		t.Fatalf("body %q", body)
	}
}

func TestNoLoginCustomerIDHeaderWhenUnset(t *testing.T) {
	var got http.Header
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { got = r.Header.Clone(); w.Write([]byte(`{}`)) })
	if _, err := c.Do(context.Background(), search); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["Login-Customer-Id"]; ok {
		t.Fatal("login-customer-id sent while unset")
	}
}

func TestGuardRefusals(t *testing.T) {
	cases := map[string]struct {
		c    Client
		r    Request
		want string
	}{
		"no credential":   {Client{BaseURL: "https://googleads.googleapis.com"}, search, "no service-account key"},
		"foreign host":    {Client{BaseURL: "https://evil.example", TokenSource: StaticToken("t")}, search, "refusing to send credentials to evil.example"},
		"plain http":      {Client{BaseURL: "http://example.com", TokenSource: StaticToken("t"), AllowCustomBase: true}, search, "non-HTTPS"},
		"read-only write": {Client{BaseURL: "https://googleads.googleapis.com", TokenSource: StaticToken("t"), ReadOnly: true}, Request{Method: "POST", Path: "v25/x:mutate", Risk: manifest.RiskWrite}, "read-only"},
		"credential param": {Client{BaseURL: "https://googleads.googleapis.com", TokenSource: StaticToken("t")},
			Request{Method: "GET", Path: "v25/x", Risk: manifest.RiskRead, Query: url.Values{"access_token": {"x"}}}, "access_token"},
	}
	for name, tc := range cases {
		_, err := tc.c.Do(context.Background(), tc.r)
		if e := asErr(t, err); e.Kind != KindUsage || !strings.Contains(e.Message, tc.want) {
			t.Errorf("%s: %+v", name, e)
		}
	}
}

func TestReadOnlyAllowsValidateOnly(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	c.ReadOnly = true
	_, err := c.Do(context.Background(), Request{Method: "POST", Path: "v25/customers/1/campaigns:mutate",
		Body: []byte(`{}`), Risk: manifest.RiskSpend, ValidateOnly: true})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCallerCannotOverrideAuthorization(t *testing.T) {
	var auth string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { auth = r.Header.Get("Authorization"); w.Write([]byte(`{}`)) })
	r := search
	r.Headers = http.Header{"Authorization": {"Bearer stolen"}, "X-Extra": {"1"}}
	if _, err := c.Do(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" {
		t.Fatalf("Authorization %q", auth)
	}
}

func TestRedirectRefused(t *testing.T) {
	var target int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&target, 1) }))
	defer other.Close()
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect) })
	_, err := c.Do(context.Background(), search)
	e := asErr(t, err)
	if e.Kind != KindValidation || !strings.Contains(e.Message, other.URL) || atomic.LoadInt32(&target) != 0 {
		t.Fatalf("%+v, target hit %d times", e, target)
	}
}

func Test429HonorsRetryDelay(t *testing.T) {
	var n int32
	c, slept := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			w.Write([]byte(quotaBody("5s")))
			return
		}
		w.Write([]byte(`{}`))
	})
	if _, err := c.Do(context.Background(), search); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 1 || (*slept)[0] < 5*time.Second || (*slept)[0] > 6*time.Second {
		t.Fatalf("slept %v", *slept)
	}
}

func Test429BeyondBudgetFailsAtOnce(t *testing.T) {
	c, slept := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		w.Write([]byte(quotaBody("3600s")))
	})
	_, err := c.Do(context.Background(), search)
	e := asErr(t, err)
	if e.Kind != KindRateLimited || !strings.Contains(e.Message, "retry budget") || len(*slept) != 0 {
		t.Fatalf("%+v slept %v", e, *slept)
	}
}

func TestReadRetriesServerErrorsEvenAsPOST(t *testing.T) {
	var n int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{}`))
	})
	if _, err := c.Do(context.Background(), search); err != nil || atomic.LoadInt32(&n) != 3 {
		t.Fatalf("err %v attempts %d", err, n)
	}
}

func TestMutationIsNeverReplayed(t *testing.T) {
	var n int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&n, 1); w.WriteHeader(503) })
	_, err := c.Do(context.Background(), Request{Method: "POST", Path: "v25/customers/1/campaigns:mutate", Body: []byte(`{}`), Risk: manifest.RiskWrite})
	if e := asErr(t, err); e.Kind != KindServer || atomic.LoadInt32(&n) != 1 {
		t.Fatalf("%+v after %d attempts", e, n)
	}
}

func TestDroppedConnection(t *testing.T) {
	var n int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	})
	_, err := c.Do(context.Background(), Request{Method: "POST", Path: "v25/customers/1/campaigns:mutate", Body: []byte(`{}`), Risk: manifest.RiskWrite})
	if e := asErr(t, err); e.Kind != KindOutcomeUnknown {
		t.Fatalf("write: %+v", e)
	}
	atomic.StoreInt32(&n, 0)
	_, err = c.Do(context.Background(), search)
	if e := asErr(t, err); e.Kind != KindTransport || atomic.LoadInt32(&n) != 3 {
		t.Fatalf("read: %+v after %d attempts", e, n)
	}
}

func TestTokenSourceErrorKeepsItsKind(t *testing.T) {
	c := &Client{BaseURL: "https://googleads.googleapis.com", TokenSource: func(context.Context) (string, error) {
		return "", &Error{Kind: KindAuth, Message: "token exchange refused: invalid_grant"}
	}}
	_, err := c.Do(context.Background(), search)
	if e := asErr(t, err); e.Kind != KindAuth || !strings.Contains(e.Message, "invalid_grant") {
		t.Fatalf("%+v", e)
	}
}
```

`internal/api/google_test.go`:

```go
package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func quotaBody(delay string) string {
	return `{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.ads.googleads.v25.errors.GoogleAdsFailure","errors":[{"errorCode":{"quotaError":"RESOURCE_TEMPORARILY_EXHAUSTED"},"message":"Too many requests.","details":{"quotaErrorDetails":{"rateScope":"ACCOUNT","retryDelay":"` + delay + `"}}}],"requestId":"rq-429"}]}}`
}

func failureBody(code, value, message, path string) string {
	loc := ""
	if path != "" {
		loc = `,"location":{"fieldPathElements":[{"fieldName":"mutate_operations","index":0},{"fieldName":"campaign_operation"},{"fieldName":"create"},{"fieldName":"` + path + `"}]}`
	}
	return `{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.ads.googleads.v25.errors.GoogleAdsFailure","errors":[{"errorCode":{"` + code + `":"` + value + `"},"message":"` + message + `"` + loc + `}],"requestId":"rq-1"}]}}`
}

func respond(status int, body string, header map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}
}

func TestGoogleAdsFailureMessage(t *testing.T) {
	c, _ := testClient(t, respond(400, failureBody("fieldError", "REQUIRED", "The required field was not present.", "contains_eu_political_advertising"), nil))
	_, err := c.Do(context.Background(), Request{Method: "POST", Path: "v25/customers/1/googleAds:mutate", Body: []byte(`{}`), Risk: "write"})
	e := asErr(t, err)
	want := "POST v25/customers/1/googleAds:mutate: HTTP 400: fieldError.REQUIRED: The required field was not present. (mutate_operations[0].campaign_operation.create.contains_eu_political_advertising)"
	if e.Kind != KindValidation || e.Message != want {
		t.Fatalf("got  %q\nwant %q", e.Message, want)
	}
	if e.RequestID != "rq-1" || e.Status != 400 {
		t.Fatalf("%+v", e)
	}
	if _, ok := e.Details.(map[string]any); !ok {
		t.Fatalf("details %T", e.Details)
	}
}

func TestRequestIDHeaderWins(t *testing.T) {
	c, _ := testClient(t, respond(404, `{"error":{"code":404,"message":"not found","status":"NOT_FOUND"}}`, map[string]string{"request-id": "hdr-7"}))
	_, err := c.Do(context.Background(), search)
	if e := asErr(t, err); e.Kind != KindNotFound || e.RequestID != "hdr-7" || !strings.HasSuffix(e.Message, "HTTP 404: not found") {
		t.Fatalf("%+v", e)
	}
}

func TestHints(t *testing.T) {
	cases := map[string]struct {
		status   int
		body     string
		methodID string
		want     string
	}{
		"test access":  {403, failureBody("authorizationError", "CLOUD_PROJECT_NOT_APPROVED_FOR_PRODUCTION", "x", ""), "customers.googleAds.search", "Test access"},
		"no access":    {403, failureBody("authorizationError", "USER_PERMISSION_DENIED", "x", ""), "customers.googleAds.search", "Access and security"},
		"eu political": {400, failureBody("campaignError", "EU_POLITICAL_ADVERTISING_DECLARATION_REQUIRED", "x", ""), "customers.googleAds.mutate", "missing_eu_political_advertising_declaration"},
		"planning":     {403, `{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`, "customers.generateKeywordIdeas", "Basic access"},
	}
	for name, tc := range cases {
		c, _ := testClient(t, respond(tc.status, tc.body, nil))
		r := search
		r.MethodID = tc.methodID
		_, err := c.Do(context.Background(), r)
		if e := asErr(t, err); !strings.Contains(e.Message, "hint: ") || !strings.Contains(e.Message, tc.want) {
			t.Errorf("%s: %q", name, e.Message)
		}
	}
}

func TestConflictKinds(t *testing.T) {
	c, _ := testClient(t, respond(409, `{"error":{"code":409,"message":"aborted","status":"ABORTED"}}`, nil))
	if _, err := c.Do(context.Background(), search); asErr(t, err).Kind != KindConflict {
		t.Fatal("409 must be conflict")
	}
	c, _ = testClient(t, respond(400, failureBody("databaseError", "CONCURRENT_MODIFICATION", "Multiple requests were attempting to modify the same resource at once.", ""), nil))
	if _, err := c.Do(context.Background(), search); asErr(t, err).Kind != KindConflict {
		t.Fatal("CONCURRENT_MODIFICATION must be conflict")
	}
}

func TestPartialFailure(t *testing.T) {
	r := Request{Method: "POST", Path: "v25/customers/1/campaigns:mutate"}
	failed := &Response{Status: 200, Header: http.Header{}, Body: []byte(`{"results":[{},{"resourceName":"customers/1/campaigns/2"}],"partialFailureError":{"code":3,"message":"Multiple errors in details.","details":[{"@type":"type.googleapis.com/google.ads.googleads.v25.errors.GoogleAdsFailure","errors":[{"errorCode":{"fieldError":"REQUIRED"},"message":"The required field was not present.","location":{"fieldPathElements":[{"fieldName":"operations","index":0}]}}]}]}}`)}
	e := PartialFailure(r, failed)
	if e == nil || e.Kind != KindPartialFailure || !strings.Contains(e.Message, "fieldError.REQUIRED") {
		t.Fatalf("%+v", e)
	}
	ok := &Response{Status: 200, Header: http.Header{}, Body: []byte(`{"results":[{"resourceName":"customers/1/campaigns/2"}]}`)}
	if PartialFailure(r, ok) != nil {
		t.Fatal("no partialFailureError means no error")
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/api/`
Expected: FAIL (undefined identifiers).

- [ ] **Step 3: Implement**

`internal/api/errors.go`:

```go
package api

import "fmt"

// Error kinds. Every error the CLI reports carries exactly one.
const (
	KindAuth           = "auth"
	KindForbidden      = "forbidden"
	KindNotFound       = "not_found"
	KindValidation     = "validation"
	KindRateLimited    = "rate_limited"
	KindServer         = "server"
	KindTransport      = "transport"
	KindOutcomeUnknown = "outcome_unknown"
	KindIncomplete     = "incomplete"
	KindUsage          = "usage"
	KindPartialFailure = "partial_failure"
	KindConflict       = "conflict"
)

// Error is the single error type the CLI reports, shaped for JSON output.
type Error struct {
	Kind      string `json:"kind"`
	Message   string `json:"error"`
	Status    int    `json:"status,omitempty"`
	Details   any    `json:"details"`
	RequestID string `json:"request_id,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// Usagef builds a caller error: something wrong before any request is sent.
func Usagef(format string, a ...any) *Error {
	return &Error{Kind: KindUsage, Message: fmt.Sprintf(format, a...)}
}

func kindForStatus(status int) string {
	switch {
	case status == 401:
		return KindAuth
	case status == 403:
		return KindForbidden
	case status == 404 || status == 410:
		return KindNotFound
	case status == 409:
		return KindConflict
	case status == 429:
		return KindRateLimited
	case status >= 500:
		return KindServer
	default:
		return KindValidation
	}
}
```

`internal/api/retry.go`:

```go
package api

import (
	"errors"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"time"
)

// classifyTransport maps a network failure. A dial or DNS failure never
// reached Google, and a read-safe call changes nothing, so both are safe to
// retry. Anything else may have been applied: the caller must verify state.
func classifyTransport(err error, readSafe bool) *Error {
	var opErr *net.OpError
	var dnsErr *net.DNSError
	presend := (errors.As(err, &opErr) && opErr.Op == "dial") || errors.As(err, &dnsErr)
	if readSafe || presend {
		return &Error{Kind: KindTransport, Message: err.Error()}
	}
	return &Error{Kind: KindOutcomeUnknown,
		Message: "the request may or may not have been applied: " + err.Error() + "; verify state before retrying"}
}

// retryAfter returns the Retry-After header as a duration, or 0.
func retryAfter(h http.Header) time.Duration {
	if secs, err := strconv.Atoi(h.Get("Retry-After")); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// jitter spreads retries so concurrent callers do not resynchronize.
func jitter() time.Duration { return time.Duration(rand.Int63n(int64(500 * time.Millisecond))) }
```

`internal/api/google.go`:

```go
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// googleFailure is what the CLI reads from a Google error body:
// {"error":{"code","message","status","details":[{"@type":"...GoogleAdsFailure","errors":[...],"requestId"}]}}.
type googleFailure struct {
	Message   string
	Status    string
	Errors    []adsError
	RequestID string
}

type adsError struct {
	ErrorCode map[string]string `json:"errorCode"`
	Message   string            `json:"message"`
	Location  *struct {
		FieldPathElements []struct {
			FieldName string `json:"fieldName"`
			Index     *int   `json:"index"`
		} `json:"fieldPathElements"`
	} `json:"location"`
	Details *struct {
		QuotaErrorDetails *struct {
			RetryDelay string `json:"retryDelay"`
		} `json:"quotaErrorDetails"`
	} `json:"details"`
}

type rpcStatus struct {
	Message string            `json:"message"`
	Status  string            `json:"status"`
	Details []json.RawMessage `json:"details"`
}

func parseFailure(body []byte) googleFailure {
	var env struct {
		Error rpcStatus `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil {
		return googleFailure{}
	}
	return fromStatus(env.Error)
}

func fromStatus(s rpcStatus) googleFailure {
	f := googleFailure{Message: s.Message, Status: s.Status}
	for _, raw := range s.Details {
		var d struct {
			Type      string     `json:"@type"`
			Errors    []adsError `json:"errors"`
			RequestID string     `json:"requestId"`
		}
		if json.Unmarshal(raw, &d) != nil || !strings.HasSuffix(d.Type, ".GoogleAdsFailure") {
			continue
		}
		f.Errors = append(f.Errors, d.Errors...)
		if f.RequestID == "" {
			f.RequestID = d.RequestID
		}
	}
	return f
}

// code renders an error code as "category.VALUE": {"fieldError":"REQUIRED"}
// becomes "fieldError.REQUIRED".
func (e adsError) code() string {
	for k, v := range e.ErrorCode {
		return k + "." + v
	}
	return ""
}

// path renders the field path: mutate_operations[0].campaign_operation.create.name.
func (e adsError) path() string {
	if e.Location == nil {
		return ""
	}
	var parts []string
	for _, el := range e.Location.FieldPathElements {
		p := el.FieldName
		if el.Index != nil {
			p += "[" + strconv.Itoa(*el.Index) + "]"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ".")
}

// summary is the part of the message after "HTTP <code>": Google's first
// error with its field path, else Google's own message.
func (f googleFailure) summary() string {
	if len(f.Errors) > 0 {
		e := f.Errors[0]
		s := ": " + e.code() + ": " + e.Message
		if p := e.path(); p != "" {
			s += " (" + p + ")"
		}
		if n := len(f.Errors) - 1; n > 0 {
			s += fmt.Sprintf(" (+%d more in details)", n)
		}
		return s
	}
	if f.Message != "" {
		return ": " + f.Message
	}
	return ""
}

func (f googleFailure) has(value string) bool {
	for _, e := range f.Errors {
		for _, v := range e.ErrorCode {
			if v == value {
				return true
			}
		}
	}
	return false
}

func (f googleFailure) concurrent() bool { return f.has("CONCURRENT_MODIFICATION") }

func (f googleFailure) retryDelay() (time.Duration, bool) {
	for _, e := range f.Errors {
		if e.Details != nil && e.Details.QuotaErrorDetails != nil {
			if d, err := time.ParseDuration(e.Details.QuotaErrorDetails.RetryDelay); err == nil && d >= 0 {
				return d, true
			}
		}
	}
	return 0, false
}

var planningMethod = regexp.MustCompile(`(?i)(keyword|reach|audience|insight|plannable|benchmark)`)

// hint appends the next step for the errors people hit while setting up.
func hint(r Request, f googleFailure, status int) string {
	switch {
	case f.has("CLOUD_PROJECT_NOT_APPROVED_FOR_PRODUCTION"):
		return "; hint: the Google Cloud project still has Test access, which reaches test accounts only; apply for Explorer or Basic access on the project's Google Ads API page in the Cloud Console"
	case f.has("USER_PERMISSION_DENIED"):
		return "; hint: the service account has no access to this customer; add its email in Google Ads under Admin, Access and security, or set login-customer-id to the manager account it was added to"
	case f.has("EU_POLITICAL_ADVERTISING_DECLARATION_REQUIRED"):
		return `; hint: a campaign in this account lacks the EU political advertising declaration; find it with: googleads search --query "SELECT campaign.id, campaign.name FROM campaign WHERE campaign.missing_eu_political_advertising_declaration = TRUE"`
	case status == http.StatusForbidden && planningMethod.MatchString(r.MethodID):
		return "; hint: Explorer access blocks planning services (Keyword Planner, reach, audience insights); Basic access is required"
	}
	return ""
}

// PartialFailure inspects a 2xx response to a request sent with
// partialFailure: a non-empty partialFailureError means some operations
// failed. It returns nil when nothing failed.
func PartialFailure(r Request, resp *Response) *Error {
	var env struct {
		PartialFailureError json.RawMessage `json:"partialFailureError"`
	}
	if json.Unmarshal(resp.Body, &env) != nil || len(env.PartialFailureError) == 0 || string(env.PartialFailureError) == "null" {
		return nil
	}
	var s rpcStatus
	if json.Unmarshal(env.PartialFailureError, &s) != nil || (s.Message == "" && len(s.Details) == 0) {
		return nil
	}
	var details any
	json.Unmarshal(env.PartialFailureError, &details)
	f := fromStatus(s)
	rid := resp.Header.Get("request-id")
	if rid == "" {
		rid = f.RequestID
	}
	return &Error{
		Kind:      KindPartialFailure,
		Message:   fmt.Sprintf("%s %s: some operations failed%s; the full response is on stdout", r.Method, r.Path, f.summary()),
		Status:    resp.Status,
		Details:   details,
		RequestID: rid,
	}
}
```

`internal/api/client.go`: start from `/Users/daniel/Development/bexio-cli/internal/api/client.go` and rewrite it to this:

```go
// Package api is the HTTP client for the Google Ads REST API. It owns the
// host guard, the headers, the retry policy and the error shape.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// Client performs API requests. BaseURL and TokenSource are required;
// Timeout, RetryBudget and MaxAttempts fall back to defaults.
type Client struct {
	BaseURL                   string
	LoginCustomerID           string // sent as login-customer-id when set
	ReadOnly, AllowCustomBase bool
	// TokenSource supplies the bearer token for each attempt; nil means no
	// credential is configured. Return a bare *Error to choose the kind.
	TokenSource func(context.Context) (string, error)
	Timeout     time.Duration // per attempt; default 60s
	RetryBudget time.Duration // total wait on 429; default 60s
	MaxAttempts int           // attempts for read-safe transient failures; default 3
	Verbose     io.Writer     // nil = silent; never receives a credential
	HTTP        *http.Client
	Sleep       func(time.Duration)
}

// StaticToken adapts a fixed token to the TokenSource seam.
func StaticToken(tok string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return tok, nil }
}

// Request is one API call.
type Request struct {
	Method string
	Path   string // relative to BaseURL: "v25/customers/1234567890/googleAds:search"
	Query  url.Values
	Body   []byte
	// Risk is the class the guard settled on; ValidateOnly says the
	// --validate-only flag was given, so Google applies nothing.
	Risk         string
	ValidateOnly bool
	MethodID     string // manifest method id, for hints; "" for raw calls
	// Headers are applied after the defaults. Authorization is the
	// client's alone and cannot be set here.
	Headers http.Header
}

// Response is a successful (2xx) response.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// readSafe reports whether the call cannot change anything: a read, or a
// validate-only call. Read-only mode allows exactly these, and only these
// are retried after a transient failure.
func (r Request) readSafe() bool { return r.Risk == manifest.RiskRead || r.ValidateOnly }

var credentialParams = []string{"access_token", "oauth_token", "key"}

func isLoopback(host string) bool { return host == "localhost" || host == "127.0.0.1" || host == "::1" }

func (c *Client) logf(format string, a ...any) {
	if c.Verbose != nil {
		fmt.Fprintf(c.Verbose, format+"\n", a...)
	}
}

// guard rejects a request locally, before anything is sent.
func (c *Client) guard(r Request) *Error {
	if c.TokenSource == nil {
		return Usagef("no service-account key: set GOOGLE_ADS_JSON_KEY_FILE_PATH, store one with `googleads config set service-account-key < key.json`, or run `googleads init`")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" {
		return Usagef("invalid API base URL %q", c.BaseURL)
	}
	host := u.Hostname()
	if host != "googleads.googleapis.com" && !c.AllowCustomBase {
		return Usagef("refusing to send credentials to %s; set GOOGLE_ADS_ALLOW_CUSTOM_BASE=1 if intentional", host)
	}
	if u.Scheme != "https" && !isLoopback(host) {
		return Usagef("refusing non-HTTPS API base %s", c.BaseURL)
	}
	if c.ReadOnly && !r.readSafe() {
		return Usagef("read-only mode (GOOGLE_ADS_READ_ONLY) blocks %s %s; --validate-only calls are still allowed", r.Method, r.Path)
	}
	for _, k := range credentialParams {
		if r.Query.Has(k) {
			return Usagef("query parameter %q carries a credential; the client owns authentication", k)
		}
	}
	return nil
}

// refuseRedirects never follows a redirect: Go keeps Authorization across a
// same-host https to http hop, and a 307/308 would replay a mutation.
func refuseRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// attempt performs one round trip; Do wraps it with the retry loop.
func (c *Client) attempt(ctx context.Context, r Request) (*Response, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tok, err := c.TokenSource(ctx)
	if err != nil {
		var apiErr *Error
		if errors.As(err, &apiErr) {
			return nil, apiErr
		}
		return nil, &Error{Kind: KindAuth, Message: "obtaining an access token: " + err.Error()}
	}
	u := strings.TrimRight(c.BaseURL, "/") + "/" + strings.TrimLeft(r.Path, "/")
	if len(r.Query) > 0 {
		u += "?" + r.Query.Encode()
	}
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u, body)
	if err != nil {
		return nil, &Error{Kind: KindUsage, Message: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.LoginCustomerID != "" {
		req.Header.Set("login-customer-id", c.LoginCustomerID)
	}
	for k, vs := range r.Headers {
		if http.CanonicalHeaderKey(k) == "Authorization" {
			continue
		}
		req.Header[http.CanonicalHeaderKey(k)] = vs
	}
	c.logf("> %s %s", r.Method, r.Path)
	httpClient := &http.Client{}
	if c.HTTP != nil {
		cp := *c.HTTP
		httpClient = &cp
	}
	httpClient.CheckRedirect = refuseRedirects
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	c.logf("< %d (%d bytes)", resp.StatusCode, len(raw))
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: raw}, nil
}

func errorDetails(resp *Response) any {
	if len(resp.Body) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(resp.Body, &v) == nil {
		return v
	}
	return string(resp.Body)
}

// errorFor builds the error for a non-2xx response.
func (c *Client) errorFor(r Request, resp *Response, f googleFailure, extra string) *Error {
	kind := kindForStatus(resp.Status)
	if f.concurrent() {
		kind = KindConflict
	}
	rid := resp.Header.Get("request-id")
	if rid == "" {
		rid = f.RequestID
	}
	return &Error{
		Kind:      kind,
		Message:   fmt.Sprintf("%s %s: HTTP %d%s%s%s", r.Method, r.Path, resp.Status, f.summary(), extra, hint(r, f, resp.Status)),
		Status:    resp.Status,
		Details:   errorDetails(resp),
		RequestID: rid,
	}
}

// Do sends a request with the retry policy: 429 is retried for every call
// within the retry budget, waiting as long as Google asks; network failures
// and 5xx are retried for read-safe calls only. A mutation is never replayed.
// The error is always *Error.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	if err := c.guard(r); err != nil {
		return nil, err
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	budget := c.RetryBudget
	if budget == 0 {
		budget = 60 * time.Second
	}
	maxAttempts := c.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 3
	}
	var waited time.Duration
	transientTries := 0
	backoff := time.Second
	for {
		resp, err := c.attempt(ctx, r)
		if err != nil {
			var apiErr *Error
			if errors.As(err, &apiErr) {
				return nil, apiErr
			}
			e := classifyTransport(err, r.readSafe())
			if e.Kind == KindTransport && r.readSafe() {
				transientTries++
				if transientTries < maxAttempts {
					sleep(backoff)
					backoff *= 2
					continue
				}
			}
			return nil, e
		}
		switch {
		case resp.Status >= 200 && resp.Status < 300:
			return resp, nil
		case resp.Status >= 300 && resp.Status < 400:
			return nil, &Error{
				Kind: KindValidation, Status: resp.Status, Details: errorDetails(resp), RequestID: resp.Header.Get("request-id"),
				Message: fmt.Sprintf("%s %s: HTTP %d redirect to %q refused: googleads never follows redirects "+
					"(they can strip HTTPS off the token and replay a mutation); check GOOGLE_ADS_API_BASE",
					r.Method, r.Path, resp.Status, resp.Header.Get("Location")),
			}
		case resp.Status == http.StatusTooManyRequests:
			f := parseFailure(resp.Body)
			wait := backoff
			if d, ok := f.retryDelay(); ok {
				wait = max(d, backoff)
			} else if ra := retryAfter(resp.Header); ra > 0 {
				wait = max(ra, backoff)
			}
			wait += jitter()
			if waited+wait > budget {
				return nil, c.errorFor(r, resp, f, fmt.Sprintf("; Google asks to wait %s, beyond the %s retry budget",
					wait.Round(time.Second), budget))
			}
			waited += wait
			c.logf("* 429, waiting %s", wait)
			sleep(wait)
			backoff *= 2
			continue
		case resp.Status >= 500 && r.readSafe():
			transientTries++
			if transientTries < maxAttempts {
				sleep(backoff)
				backoff *= 2
				continue
			}
		}
		return nil, c.errorFor(r, resp, parseFailure(resp.Body), "")
	}
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/api/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "Add the API client: host guard, headers, retries, Google Ads errors

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 7: Service-account auth

**Files:**
- Create: `internal/auth/key.go`, `internal/auth/source.go`
- Test: `internal/auth/key_test.go`, `internal/auth/source_test.go`

**Interfaces:**
- Consumes: `api.Error`, `api.Usagef`, `api.KindAuth`, `api.KindTransport` (Task 6).
- Produces (package `auth`, used by Tasks 9, 11 and 12):

```go
const Scope = "https://www.googleapis.com/auth/adwords"
type Key struct { Type, ProjectID, PrivateKeyID, PrivateKey, ClientEmail string } // JSON: type, project_id, private_key_id, private_key, client_email
func ParseKey(raw []byte) (*Key, error)
type Source struct { Key *Key; TokenURL string; AllowCustomBase bool; HTTP *http.Client; CacheDir func() (string, error); Now func() time.Time }
func (s *Source) Token(ctx context.Context) (string, error) // error is always *api.Error
func (s *Source) CachedUntil() time.Time                    // zero when nothing is cached
```

- [ ] **Step 1: Add the dependency**

Run: `go get golang.org/x/oauth2@v0.36.0`

- [ ] **Step 2: Write the failing tests**

`internal/auth/key_test.go`:

```go
package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseKeyAcceptsServiceAccount(t *testing.T) {
	k, err := ParseKey(keyJSON(t, "kid-1"))
	if err != nil {
		t.Fatal(err)
	}
	if k.ClientEmail != "gads@proj.iam.gserviceaccount.com" || k.PrivateKeyID != "kid-1" || k.ProjectID != "proj" {
		t.Fatalf("%+v", k)
	}
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, keyJSON(t, "kid-1")...)
	if _, err := ParseKey(withBOM); err != nil {
		t.Fatalf("a UTF-8 BOM must be accepted: %v", err)
	}
}

func TestParseKeyNamesOtherCredentials(t *testing.T) {
	var full map[string]any
	json.Unmarshal(keyJSON(t, "kid-1"), &full)
	noEmail := map[string]any{}
	for k, v := range full {
		noEmail[k] = v
	}
	delete(noEmail, "client_email")
	badPEM := map[string]any{}
	for k, v := range full {
		badPEM[k] = v
	}
	badPEM["private_key"] = "not a key"
	cases := map[string]struct {
		v    any
		want string
	}{
		"installed app":   {map[string]any{"installed": map[string]any{"client_id": "x"}}, "OAuth client file"},
		"web app":         {map[string]any{"web": map[string]any{"client_id": "x"}}, "OAuth client file"},
		"user credential": {map[string]any{"type": "authorized_user", "refresh_token": "x"}, "user credential"},
		"no type":         {map[string]any{"client_email": "x"}, `no "type"`},
		"other type":      {map[string]any{"type": "external_account"}, "external_account"},
		"missing email":   {noEmail, "client_email"},
		"bad pem":         {badPEM, "not PEM"},
	}
	for name, c := range cases {
		raw, _ := json.Marshal(c.v)
		if _, err := ParseKey(raw); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	if _, err := ParseKey([]byte("not json")); err == nil {
		t.Error("garbage must fail")
	}
}
```

`internal/auth/source_test.go`:

```go
package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
)

var (
	keyOnce sync.Once
	testRSA *rsa.PrivateKey
)

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testRSA = k
	})
	return testRSA
}

// keyJSON is a service-account key file whose own token_uri points at an
// attacker: the client must ignore it.
func keyJSON(t *testing.T, keyID string) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(rsaKey(t))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "proj", "private_key_id": keyID,
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "gads@proj.iam.gserviceaccount.com", "client_id": "1",
		"token_uri": "https://attacker.example/token",
	})
	return raw
}

type tokenServer struct {
	*httptest.Server
	calls  atomic.Int32
	mu     sync.Mutex
	claims map[string]any
	kid    string
}

func newTokenServer(t *testing.T, refuse bool) *tokenServer {
	t.Helper()
	ts := &tokenServer{}
	pub := &rsaKey(t).PublicKey
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := ts.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		r.ParseForm()
		parts := strings.Split(r.Form.Get("assertion"), ".")
		if refuse || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || len(parts) != 3 {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`))
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) != nil {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant","error_description":"bad signature"}`))
			return
		}
		hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
		body, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var h map[string]any
		json.Unmarshal(hdr, &h)
		ts.mu.Lock()
		ts.kid, _ = h["kid"].(string)
		ts.claims = nil
		json.Unmarshal(body, &ts.claims)
		ts.mu.Unlock()
		fmt.Fprintf(w, `{"access_token":"at-%d","expires_in":3600,"token_type":"Bearer"}`, n)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func newSource(t *testing.T, ts *tokenServer, keyID, cacheDir string) *Source {
	t.Helper()
	k, err := ParseKey(keyJSON(t, keyID))
	if err != nil {
		t.Fatal(err)
	}
	return &Source{Key: k, TokenURL: ts.URL + "/token", AllowCustomBase: true, HTTP: ts.Client(),
		CacheDir: func() (string, error) { return cacheDir, nil }}
}

func TestTokenSendsAValidAssertion(t *testing.T) {
	ts := newTokenServer(t, false)
	s := newSource(t, ts, "kid-1", t.TempDir())
	tok, err := s.Token(context.Background())
	if err != nil || tok != "at-1" {
		t.Fatalf("tok %q err %v", tok, err)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	c := ts.claims
	if c["iss"] != "gads@proj.iam.gserviceaccount.com" || c["scope"] != Scope || c["aud"] != ts.URL+"/token" {
		t.Fatalf("claims %v", c)
	}
	if exp, iat := c["exp"].(float64), c["iat"].(float64); exp-iat != 3600 {
		t.Fatalf("lifetime %v", exp-iat)
	}
	if _, ok := c["sub"]; ok {
		t.Fatal("no subject: the service account acts as itself")
	}
	if ts.kid != "kid-1" {
		t.Fatalf("kid %q", ts.kid)
	}
}

func TestTokenIsCachedAndKeyFileTokenURIIgnored(t *testing.T) {
	ts := newTokenServer(t, false)
	s := newSource(t, ts, "kid-1", t.TempDir())
	for i := 0; i < 3; i++ {
		if tok, err := s.Token(context.Background()); err != nil || tok != "at-1" {
			t.Fatalf("call %d: %q %v", i, tok, err)
		}
	}
	if ts.calls.Load() != 1 {
		t.Fatalf("%d exchanges, want 1", ts.calls.Load())
	}
	if until := s.CachedUntil(); time.Until(until) < 55*time.Minute {
		t.Fatalf("cached until %v", until)
	}
}

func TestTokenReMintsNearExpiry(t *testing.T) {
	ts := newTokenServer(t, false)
	s := newSource(t, ts, "kid-1", t.TempDir())
	s.Token(context.Background())
	s.Now = func() time.Time { return time.Now().Add(59*time.Minute + 30*time.Second) }
	if tok, _ := s.Token(context.Background()); tok != "at-2" {
		t.Fatalf("tok %q: less than a minute left must re-mint", tok)
	}
}

func TestCacheKeyedByKeyID(t *testing.T) {
	ts := newTokenServer(t, false)
	dir := t.TempDir()
	newSource(t, ts, "kid-1", dir).Token(context.Background())
	if tok, _ := newSource(t, ts, "kid-2", dir).Token(context.Background()); tok != "at-2" {
		t.Fatalf("a rotated key must not reuse the old key's token: %q", tok)
	}
}

func TestCorruptCacheIgnoredAndPerms(t *testing.T) {
	ts := newTokenServer(t, false)
	dir := t.TempDir()
	p := filepath.Join(dir, "googleads", "token.json")
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte("{garbage"), 0o600)
	if _, err := newSource(t, ts, "kid-1", dir).Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("cache perms %v", fi.Mode().Perm())
	}
}

func TestTokenURLPinned(t *testing.T) {
	k, _ := ParseKey(keyJSON(t, "kid-1"))
	cases := map[string]Source{
		"foreign host": {Key: k, TokenURL: "https://evil.example/token"},
		"plain http":   {Key: k, TokenURL: "http://example.com/token", AllowCustomBase: true},
	}
	for name, s := range cases {
		s.CacheDir = func() (string, error) { return t.TempDir(), nil }
		_, err := s.Token(context.Background())
		var e *api.Error
		if !errors.As(err, &e) || e.Kind != api.KindUsage {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestRefusedExchangeIsAuthError(t *testing.T) {
	ts := newTokenServer(t, true)
	_, err := newSource(t, ts, "kid-1", t.TempDir()).Token(context.Background())
	var e *api.Error
	if !errors.As(err, &e) || e.Kind != api.KindAuth || !strings.Contains(e.Message, "invalid_grant") {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./internal/auth/`
Expected: FAIL (undefined identifiers).

- [ ] **Step 4: Implement**

`internal/auth/key.go`:

```go
// Package auth turns a Google Cloud service-account key into access tokens
// for the Google Ads API: a signed JWT exchanged at Google's token endpoint
// (RFC 7523), cached on disk until shortly before it expires.
package auth

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
)

// Scope is the OAuth scope of the Google Ads API.
const Scope = "https://www.googleapis.com/auth/adwords"

// Key is the part of a service-account key file the CLI uses.
type Key struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ParseKey validates a service-account key file. When the file is some
// other Google credential it says which, because "invalid key" sends people
// to the wrong page of the Cloud Console.
func ParseKey(raw []byte) (*Key, error) {
	raw = bytes.TrimPrefix(raw, utf8BOM)
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("not a JSON key file: %w", err)
	}
	for _, kind := range []string{"installed", "web"} {
		if _, ok := probe[kind]; ok {
			return nil, fmt.Errorf("this is an OAuth client file (%s app), not a service-account key: create a key under IAM & Admin, Service accounts, Keys", kind)
		}
	}
	var k Key
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, fmt.Errorf("not a service-account key: %w", err)
	}
	switch k.Type {
	case "service_account":
	case "authorized_user":
		return nil, errors.New("this is a user credential (authorized_user), not a service-account key")
	case "":
		return nil, errors.New(`not a service-account key: the file has no "type" field`)
	default:
		return nil, fmt.Errorf("credential type %q is not a service-account key", k.Type)
	}
	if k.ClientEmail == "" || k.PrivateKeyID == "" || k.PrivateKey == "" {
		return nil, errors.New("the service-account key lacks client_email, private_key_id or private_key")
	}
	if _, err := parseRSA(k.PrivateKey); err != nil {
		return nil, fmt.Errorf("service-account key: %w", err)
	}
	return &k, nil
}

func parseRSA(pemKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, errors.New("private_key is not PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private_key is not an RSA key")
		}
		return rk, nil
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}
```

`internal/auth/source.go`:

```go
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
)

// Source mints and caches access tokens for one key.
type Source struct {
	Key             *Key
	TokenURL        string // pinned to Google unless AllowCustomBase
	AllowCustomBase bool
	HTTP            *http.Client            // nil: a client with a 30s timeout
	CacheDir        func() (string, error) // nil: os.UserCacheDir
	Now             func() time.Time        // nil: time.Now
}

// minValidity is how much lifetime a cached token needs left to be reused.
const minValidity = 60 * time.Second

type cacheEntry struct {
	AccessToken string    `json:"access_token"`
	Expiry      time.Time `json:"expiry"`
}

type cacheFile struct {
	Entries map[string]cacheEntry `json:"entries"`
}

// Token returns a valid access token. The error is always *api.Error:
// usage for a bad configuration, auth when Google refuses the key,
// transport when the token endpoint cannot be reached.
func (s *Source) Token(ctx context.Context) (string, error) {
	if err := checkTokenURL(s.TokenURL, s.AllowCustomBase); err != nil {
		return "", err
	}
	now := s.now()
	if e, ok := s.readCache()[s.cacheKey()]; ok && e.Expiry.Sub(now) >= minValidity {
		return e.AccessToken, nil
	}
	tok, err := s.mint(ctx)
	if err != nil {
		return "", err
	}
	s.writeCache(cacheEntry{AccessToken: tok.AccessToken, Expiry: tok.Expiry}, now)
	return tok.AccessToken, nil
}

// CachedUntil reports when the cached token for this key expires; zero when
// nothing is cached.
func (s *Source) CachedUntil() time.Time { return s.readCache()[s.cacheKey()].Expiry }

func (s *Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Source) mint(ctx context.Context) (*oauth2.Token, error) {
	cfg := &jwt.Config{
		Email:        s.Key.ClientEmail,
		PrivateKey:   []byte(s.Key.PrivateKey),
		PrivateKeyID: s.Key.PrivateKeyID,
		Scopes:       []string{Scope},
		TokenURL:     s.TokenURL, // never the key file's token_uri
		Expires:      time.Hour,
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	if s.HTTP != nil {
		cp := *s.HTTP
		hc = &cp
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	tok, err := cfg.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, hc)).Token()
	if err == nil {
		return tok, nil
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		msg := "token exchange refused"
		if re.ErrorCode != "" {
			msg += ": " + re.ErrorCode
		}
		if re.ErrorDescription != "" {
			msg += ": " + re.ErrorDescription
		}
		return nil, &api.Error{Kind: api.KindAuth, Message: msg + " (was the key deleted or disabled in the Cloud Console?)"}
	}
	return nil, &api.Error{Kind: api.KindTransport, Message: "token exchange: " + err.Error()}
}

func checkTokenURL(raw string, allowCustom bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return api.Usagef("invalid token URL %q", raw)
	}
	host := u.Hostname()
	if host != "oauth2.googleapis.com" && !allowCustom {
		return api.Usagef("refusing to send the signed key assertion to %s; set GOOGLE_ADS_ALLOW_CUSTOM_BASE=1 if intentional", host)
	}
	if u.Scheme != "https" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return api.Usagef("refusing non-HTTPS token URL %s", raw)
	}
	return nil
}

func (s *Source) cacheKey() string {
	return s.Key.ClientEmail + "|" + s.Key.PrivateKeyID + "|" + s.TokenURL
}

func (s *Source) cachePath() (string, error) {
	dirFn := s.CacheDir
	if dirFn == nil {
		dirFn = os.UserCacheDir
	}
	dir, err := dirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "googleads", "token.json"), nil
}

// readCache returns the cached entries; a missing or corrupt cache is empty.
func (s *Source) readCache() map[string]cacheEntry {
	p, err := s.cachePath()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var f cacheFile
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	return f.Entries
}

// writeCache stores e for this key and drops expired entries. Best effort:
// a cache that cannot be written only costs the next call an exchange.
func (s *Source) writeCache(e cacheEntry, now time.Time) {
	if e.Expiry.IsZero() {
		return
	}
	p, err := s.cachePath()
	if err != nil {
		return
	}
	entries := map[string]cacheEntry{}
	for k, v := range s.readCache() {
		if v.Expiry.After(now) {
			entries[k] = v
		}
	}
	entries[s.cacheKey()] = e
	raw, err := json.Marshal(cacheFile{Entries: entries})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".token-*.json")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() != nil {
		return
	}
	os.Rename(tmp.Name(), p)
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `go mod tidy && go test ./internal/auth/ && go vet ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/auth
git commit -m "Add service-account auth: key validation, JWT exchange, token cache

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Guard

**Files:**
- Create: `internal/guard/json.go`, `internal/guard/guard.go`
- Test: `internal/guard/json_test.go`, `internal/guard/guard_test.go`

**Interfaces:**
- Consumes: `manifest.Manifest`, `manifest.Method`, `manifest.Mutate`, `manifest.RiskRank`, `manifest.NeedsForce` (Task 1); `config.FormatMicros` (Task 5); `api.Usagef` (Task 6).
- Produces (package `guard`, used by Tasks 9 and 10):

```go
type Options struct { Force, ValidateOnly, ReadOnly bool; CapMicros int64 }
type Finding struct { Operation int; Class, Reason string }
type Decision struct { Class string; Findings []Finding }
func Check(m *manifest.Manifest, meth *manifest.Method, body []byte, opt Options) (Decision, error) // a refusal is *api.Error, kind usage
```

- [ ] **Step 1: Write the failing tests**

`internal/guard/json_test.go`:

```go
package guard

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseStrictNormalizesKeys(t *testing.T) {
	obj, err := parseStrict([]byte(`{"mutate_operations":[{"campaign_budget_operation":{"create":{"amount_micros":"5"}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	ops := obj["mutateOperations"].([]any)
	create := ops[0].(map[string]any)["campaignBudgetOperation"].(map[string]any)["create"].(map[string]any)
	if create["amountMicros"] != "5" {
		t.Fatalf("%v", create)
	}
	obj, _ = parseStrict([]byte(`{"n":2.5e3}`))
	if n, ok := obj["n"].(json.Number); !ok || n.String() != "2.5e3" {
		t.Fatal("numbers must stay json.Number with their original text")
	}
}

func TestParseStrictRefusals(t *testing.T) {
	for body, want := range map[string]string{
		`{"a":1,"a":2}`:          "duplicate key",
		`{"x":{"b_c":1,"bC":2}}`: "both spellings",
		`[1,2]`:                  "must be a JSON object",
		`{"a":1} {"b":2}`:        "trailing data",
		`{"a":`:                  "",
	} {
		_, err := parseStrict([]byte(body))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", body, err, want)
		}
	}
}

func TestToCamel(t *testing.T) {
	for in, want := range map[string]string{
		"amount_micros": "amountMicros", "contains_eu_political_advertising": "containsEuPoliticalAdvertising",
		"amountMicros": "amountMicros", "status": "status",
	} {
		if got := toCamel(in); got != want {
			t.Errorf("toCamel(%q) = %q", in, got)
		}
	}
}
```

`internal/guard/guard_test.go`:

```go
package guard

import (
	"errors"
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

const cap30 = 30000000

func testManifest() *manifest.Manifest {
	mut := func(field, res string) *manifest.Mutate { return &manifest.Mutate{OperationsField: field, Resource: res} }
	return &manifest.Manifest{
		Methods: []manifest.Method{
			{ID: "customers.googleAds.search", Command: []string{"search"}, Risk: "read"},
			{ID: "customers.googleAds.mutate", Command: []string{"mutate"}, Risk: "write", Mutate: mut("mutateOperations", "")},
			{ID: "customers.campaigns.mutate", Command: []string{"campaigns", "mutate"}, Risk: "write", Mutate: mut("operations", "Campaign")},
			{ID: "customers.campaignBudgets.mutate", Command: []string{"campaign-budgets", "mutate"}, Risk: "write", Mutate: mut("operations", "CampaignBudget")},
			{ID: "customers.adGroups.mutate", Command: []string{"ad-groups", "mutate"}, Risk: "write", Mutate: mut("operations", "AdGroup")},
			{ID: "customers.mutate", Command: []string{"customer", "mutate"}, Risk: "write", Mutate: mut("operation", "Customer")},
			{ID: "customers.customerUserAccesses.mutate", Command: []string{"customer-user-accesses", "mutate"}, Risk: "admin", Mutate: mut("operation", "CustomerUserAccess")},
			{ID: "customers.batchJobs.addOperations", Command: []string{"batch-jobs", "add-operations"}, Risk: "admin", Mutate: mut("mutateOperations", "")},
			{ID: "customers.recommendations.apply", Command: []string{"recommendations", "apply"}, Risk: "spend", Blocked: "applies amounts Google chooses"},
			{ID: "customers.reservations.bookCampaigns", Command: []string{"reservations", "book-campaigns"}, Risk: "spend", Blocked: "commits spend"},
		},
		GenericOperations: map[string]string{
			"campaignOperation":       "customers.campaigns.mutate",
			"campaignBudgetOperation": "customers.campaignBudgets.mutate",
			"adGroupOperation":        "customers.adGroups.mutate",
			"bookCampaignsOperation":  "customers.reservations.bookCampaigns",
		},
	}
}

func check(id, body string, opt Options) (Decision, error) {
	m := testManifest()
	return Check(m, m.ByID(id), []byte(body), opt)
}

func TestCheck(t *testing.T) {
	withCap := Options{CapMicros: cap30}
	cases := []struct {
		name, id, body string
		opt            Options
		wantClass      string // expected class when the call is allowed
		wantErr        string // expected substring of a usage refusal
	}{
		{"read method", "customers.googleAds.search", `{"query":"SELECT customer.id FROM customer"}`, Options{}, "read", ""},
		{"paused campaign and budget", "customers.googleAds.mutate", `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"amountMicros":"29610000"}}},{"campaignOperation":{"create":{"status":"PAUSED","name":"x"}}}]}`, withCap, "write", ""},
		{"campaign without status", "customers.googleAds.mutate", `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x"}}}]}`, withCap, "", "Google's default is ENABLED"},
		{"campaign without status, forced", "customers.googleAds.mutate", `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x"}}}]}`, Options{Force: true}, "spend", ""},
		{"campaign without status, validate-only", "customers.googleAds.mutate", `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x"}}}]}`, Options{ValidateOnly: true}, "spend", ""},
		{"numeric status is not PAUSED", "customers.campaigns.mutate", `{"operations":[{"create":{"status":3}}]}`, Options{}, "", "needs --force"},
		{"enable campaign", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","status":"ENABLED"},"updateMask":"status"}]}`, Options{}, "", "status set to ENABLED"},
		{"pause campaign", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","status":"PAUSED"},"updateMask":"status"}]}`, Options{}, "write", ""},
		{"status only in the mask", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2"},"updateMask":"status"}]}`, Options{}, "", "needs --force"},
		{"move to another budget", "customers.campaigns.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaigns/2","campaignBudget":"customers/1/campaignBudgets/9"},"updateMask":"campaign_budget"}]}`, Options{}, "", "another budget"},
		{"snake_case spelling", "customers.googleAds.mutate", `{"mutate_operations":[{"campaign_operation":{"create":{"status":"ENABLED"}}}]}`, Options{}, "", "needs --force"},
		{"duplicate key", "customers.campaigns.mutate", `{"operations":[{"create":{"status":"PAUSED","status":"ENABLED"}}]}`, Options{Force: true}, "", "duplicate key"},
		{"both spellings", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"amountMicros":"1","amount_micros":"999999999999"}}]}`, Options{Force: true, CapMicros: cap30}, "", "both spellings"},
		{"budget at the cap", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"30000000"}}]}`, withCap, "write", ""},
		{"budget one micro above", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"30000001"}}]}`, withCap, "", "exceeds the configured cap"},
		{"above the cap, forced", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"30000001"}}]}`, Options{Force: true, CapMicros: cap30}, "", "exceeds"},
		{"above the cap, validate-only", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"30000001"}}]}`, Options{ValidateOnly: true, CapMicros: cap30}, "", "exceeds"},
		{"no cap configured", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"amountMicros":"1000000"}}]}`, Options{Force: true}, "", "no budget cap"},
		{"budget update within the cap", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaignBudgets/9","amountMicros":"5000000"},"updateMask":"amount_micros"}]}`, withCap, "", "budget amount changed to 5"},
		{"budget update within the cap, forced", "customers.campaignBudgets.mutate", `{"operations":[{"update":{"resourceName":"customers/1/campaignBudgets/9","amountMicros":"5000000"},"updateMask":"amount_micros"}]}`, Options{Force: true, CapMicros: cap30}, "spend", ""},
		{"fixed total", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"totalAmountMicros":"1000000"}}]}`, Options{Force: true, CapMicros: cap30}, "", "fixed-total"},
		{"custom period", "customers.campaignBudgets.mutate", `{"operations":[{"create":{"period":"CUSTOM_PERIOD","amountMicros":"1"}}]}`, Options{Force: true, CapMicros: cap30}, "", "fixed-total"},
		{"remove", "customers.adGroups.mutate", `{"operations":[{"remove":"customers/1/adGroups/3"}]}`, Options{}, "", "removes AdGroup"},
		{"unknown generic type", "customers.googleAds.mutate", `{"mutateOperations":[{"fooOperation":{"create":{}}}]}`, Options{}, "", "unknown operation type fooOperation"},
		{"blocked method", "customers.recommendations.apply", `{"operations":[]}`, Options{Force: true, ValidateOnly: true}, "", "blocked"},
		{"blocked generic type", "customers.googleAds.mutate", `{"mutateOperations":[{"bookCampaignsOperation":{}}]}`, Options{Force: true}, "", "blocked"},
		{"batch job above the cap", "customers.batchJobs.addOperations", `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"amountMicros":"99000000"}}}]}`, Options{Force: true, CapMicros: cap30}, "", "exceeds"},
		{"batch job needs force", "customers.batchJobs.addOperations", `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"amountMicros":"1000000"}}}]}`, withCap, "", "admin-class"},
		{"batch job forced", "customers.batchJobs.addOperations", `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"amountMicros":"1000000"}}}]}`, Options{Force: true, CapMicros: cap30}, "admin", ""},
		{"admin method", "customers.customerUserAccesses.mutate", `{"operation":{"update":{"accessRole":"ADMIN"}}}`, Options{}, "", "admin-class"},
		{"single operation shape", "customers.mutate", `{"operation":{"update":{"autoTaggingEnabled":true},"updateMask":"auto_tagging_enabled"}}`, Options{}, "write", ""},
		{"read-only blocks writes", "customers.adGroups.mutate", `{"operations":[{"create":{"name":"x"}}]}`, Options{ReadOnly: true}, "", "read-only mode"},
		{"read-only allows validate-only", "customers.adGroups.mutate", `{"operations":[{"create":{"name":"x"}}]}`, Options{ReadOnly: true, ValidateOnly: true}, "write", ""},
		{"read-only allows reads", "customers.googleAds.search", `{"query":"x"}`, Options{ReadOnly: true}, "read", ""},
		{"not JSON", "customers.campaigns.mutate", `{"operations":`, Options{Force: true}, "", "cannot read"},
		{"operations not an array", "customers.campaigns.mutate", `{"operations":{"create":{}}}`, Options{Force: true}, "", "must be a JSON array"},
		{"validateOnly in the body does not count", "customers.campaigns.mutate", `{"validateOnly":true,"operations":[{"create":{"status":"ENABLED"}}]}`, Options{}, "", "needs --force"},
	}
	for _, c := range cases {
		d, err := check(c.id, c.body, c.opt)
		if c.wantErr != "" {
			var e *api.Error
			if !errors.As(err, &e) || e.Kind != api.KindUsage || !strings.Contains(e.Message, c.wantErr) {
				t.Errorf("%s: err = %v, want a usage error containing %q", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil || d.Class != c.wantClass {
			t.Errorf("%s: class %q err %v, want class %q", c.name, d.Class, err, c.wantClass)
		}
	}
}

func TestAmountForms(t *testing.T) {
	for _, amount := range []string{`" 29610000"`, `"+29610000"`, `2.961e7`, `"29610000.0"`, `29610000.0`, `-1`, `"abc"`, `true`} {
		body := `{"operations":[{"create":{"amountMicros":` + amount + `}}]}`
		_, err := check("customers.campaignBudgets.mutate", body, Options{Force: true, CapMicros: cap30})
		if err == nil || !strings.Contains(err.Error(), "micros") {
			t.Errorf("amount %s: err = %v, want a refusal about micros", amount, err)
		}
	}
	for _, amount := range []string{`29610000`, `"29610000"`} {
		body := `{"operations":[{"create":{"amountMicros":` + amount + `}}]}`
		if _, err := check("customers.campaignBudgets.mutate", body, Options{CapMicros: cap30}); err != nil {
			t.Errorf("amount %s: %v", amount, err)
		}
	}
}

func TestFindingsNameEveryOperation(t *testing.T) {
	body := `{"mutateOperations":[{"adGroupOperation":{"remove":"customers/1/adGroups/3"}},{"campaignOperation":{"create":{"name":"x"}}}]}`
	_, err := check("customers.googleAds.mutate", body, Options{})
	if err == nil || !strings.Contains(err.Error(), "operation 0: removes AdGroup") || !strings.Contains(err.Error(), "operation 1: campaign created") {
		t.Fatalf("err = %v", err)
	}
	d, err := check("customers.googleAds.mutate", body, Options{Force: true})
	if err != nil || len(d.Findings) != 2 {
		t.Fatalf("findings %+v err %v", d.Findings, err)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/guard/`
Expected: FAIL (undefined identifiers).

- [ ] **Step 3: Implement**

`internal/guard/json.go`:

```go
package guard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// parseStrict decodes a JSON object and refuses what could make the guard
// and Google read different values: a key given twice, or one field given in
// both spellings Google accepts (amountMicros and amount_micros). Keys come
// back in lowerCamelCase and numbers as json.Number.
func parseStrict(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec, "")
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after the JSON body")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("the body must be a JSON object")
	}
	return obj, nil
}

func parseValue(dec *json.Decoder, path string) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil // string, json.Number, bool or nil
	}
	switch delim {
	case '{':
		obj := map[string]any{}
		raw := map[string]string{} // camelCase key -> key as written
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key := kt.(string)
			camel := toCamel(key)
			if prev, dup := raw[camel]; dup {
				if prev == key {
					return nil, fmt.Errorf("duplicate key %q at %s", key, where(path))
				}
				return nil, fmt.Errorf("field %q given in both spellings (%q and %q) at %s", camel, prev, key, where(path))
			}
			raw[camel] = key
			v, err := parseValue(dec, join(path, camel))
			if err != nil {
				return nil, err
			}
			obj[camel] = v
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return obj, nil
	case '[':
		arr := []any{}
		for i := 0; dec.More(); i++ {
			v, err := parseValue(dec, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	}
	return nil, fmt.Errorf("unexpected %v at %s", delim, where(path))
}

// toCamel converts snake_case to lowerCamelCase; other names pass through.
func toCamel(s string) string {
	if !strings.Contains(s, "_") {
		return s
	}
	parts := strings.Split(s, "_")
	var b strings.Builder
	b.WriteString(parts[0])
	for _, p := range parts[1:] {
		if p != "" {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return b.String()
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func where(path string) string {
	if path == "" {
		return "the top level"
	}
	return path
}
```

`internal/guard/guard.go`:

```go
// Package guard decides, before anything is sent, whether a call may run. It
// starts from the method's reviewed risk class, raises it by what each
// operation in a mutate body does, holds budget amounts to the configured
// cap, and applies --force, --validate-only, read-only mode and the blocked
// list. A refusal is an *api.Error of kind usage.
package guard

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/config"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// Options are the flags and settings that bear on the decision.
type Options struct {
	Force bool
	// ValidateOnly is the --validate-only flag. A validateOnly field inside
	// the body does not count: only the flag relaxes a gate.
	ValidateOnly bool
	ReadOnly     bool
	CapMicros    int64 // 0 means no cap is configured
}

// Finding is one reason a call needs --force.
type Finding struct {
	Operation int // index in the operation list; -1 for the method itself
	Class     string
	Reason    string
}

// Decision is the outcome for a call that may be sent.
type Decision struct {
	Class    string    // the highest class found; what the client is told
	Findings []Finding // every reason above write
}

// Check decides whether meth may be called with body.
func Check(m *manifest.Manifest, meth *manifest.Method, body []byte, opt Options) (Decision, error) {
	name := "googleads " + strings.Join(meth.Command, " ")
	if meth.Blocked != "" {
		return Decision{}, api.Usagef("%s is blocked in this version of googleads: %s", name, meth.Blocked)
	}
	d := Decision{Class: meth.Risk}
	if manifest.NeedsForce(meth.Risk) {
		d.Findings = append(d.Findings, Finding{Operation: -1, Class: meth.Risk, Reason: name + " is " + meth.Risk + "-class"})
	}
	if meth.Mutate != nil && len(body) > 0 {
		obj, err := parseStrict(body)
		if err != nil {
			return Decision{}, api.Usagef("refusing to send a mutate body the guard cannot read: %v", err)
		}
		if err := inspect(m, meth, obj, opt, &d); err != nil {
			return Decision{}, err
		}
	}
	if opt.ReadOnly && d.Class != manifest.RiskRead && !opt.ValidateOnly {
		return Decision{}, api.Usagef("read-only mode blocks %s (%s-class); --validate-only calls are still allowed", name, d.Class)
	}
	if manifest.NeedsForce(d.Class) && !opt.Force && !opt.ValidateOnly {
		return Decision{}, api.Usagef("%s needs --force: %s; re-run with --force to confirm, or with --validate-only to let Google check it without applying anything",
			name, describe(d.Findings))
	}
	return d, nil
}

func describe(fs []Finding) string {
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		if f.Operation < 0 {
			parts = append(parts, f.Reason)
		} else {
			parts = append(parts, fmt.Sprintf("operation %d: %s", f.Operation, f.Reason))
		}
	}
	return strings.Join(parts, "; ")
}

// raise lifts the decision's class and records why when the class needs --force.
func raise(d *Decision, op int, class, reason string) {
	if manifest.RiskRank(class) > manifest.RiskRank(d.Class) {
		d.Class = class
	}
	if manifest.NeedsForce(class) {
		d.Findings = append(d.Findings, Finding{Operation: op, Class: class, Reason: reason})
	}
}

// inspect walks the operations of a mutate body.
func inspect(m *manifest.Manifest, meth *manifest.Method, body map[string]any, opt Options, d *Decision) error {
	field := meth.Mutate.OperationsField
	raw, ok := body[field]
	if !ok {
		return nil // no operations: nothing is applied, Google rejects the request
	}
	var ops []any
	switch arr, isArr := raw.([]any); {
	case field == "operation":
		ops = []any{raw}
	case isArr:
		ops = arr
	default:
		return api.Usagef("%s must be a JSON array", field)
	}
	for i, op := range ops {
		obj, ok := op.(map[string]any)
		if !ok {
			return api.Usagef("operation %d is not a JSON object", i)
		}
		resource := meth.Mutate.Resource
		if field == "mutateOperations" {
			if len(obj) != 1 {
				return api.Usagef("operation %d must name exactly one operation type", i)
			}
			var typ string
			var inner any
			for k, v := range obj {
				typ, inner = k, v
			}
			target := m.ByID(m.GenericOperations[typ])
			if target == nil {
				raise(d, i, manifest.RiskAdmin, "unknown operation type "+typ)
				continue
			}
			if target.Blocked != "" {
				return api.Usagef("operation %d (%s) is blocked in this version of googleads: %s", i, typ, target.Blocked)
			}
			raise(d, i, target.Risk, typ+" is "+target.Risk+"-class")
			if obj, ok = inner.(map[string]any); !ok {
				return api.Usagef("operation %d (%s) is not a JSON object", i, typ)
			}
			resource = ""
			if target.Mutate != nil {
				resource = target.Mutate.Resource
			}
		}
		if err := inspectOperation(i, resource, obj, opt, d); err != nil {
			return err
		}
	}
	return nil
}

// inspectOperation applies the payload rules to one create, update or remove.
func inspectOperation(i int, resource string, op map[string]any, opt Options, d *Decision) error {
	if _, ok := op["remove"]; ok {
		what := resource
		if what == "" {
			what = "a resource"
		}
		raise(d, i, manifest.RiskDelete, "removes "+what)
		return nil
	}
	verb, obj := "create", asObject(op["create"])
	if obj == nil {
		verb, obj = "update", asObject(op["update"])
	}
	if obj == nil {
		return nil
	}
	mask := maskFields(op["updateMask"])
	set := func(field string) bool {
		_, present := obj[field]
		return present || (verb == "update" && mask[field])
	}
	switch resource {
	case "Campaign":
		status, isString := obj["status"].(string)
		paused := isString && status == "PAUSED"
		switch {
		case verb == "create" && !paused:
			raise(d, i, manifest.RiskSpend, "campaign created without status PAUSED (Google's default is ENABLED)")
		case verb == "update" && set("status") && !paused:
			raise(d, i, manifest.RiskSpend, "campaign status set to "+show(obj["status"]))
		}
		if verb == "update" && set("campaignBudget") {
			raise(d, i, manifest.RiskSpend, "campaign moved to another budget")
		}
	case "CampaignBudget":
		if set("totalAmountMicros") || obj["period"] == "CUSTOM_PERIOD" {
			return api.Usagef("operation %d: fixed-total budgets (totalAmountMicros, period CUSTOM_PERIOD) are refused in this version; the budget cap is a daily cap", i)
		}
		if set("amountMicros") {
			amount, err := micros(obj["amountMicros"])
			if err != nil {
				return api.Usagef("operation %d: amountMicros %v", i, err)
			}
			if opt.CapMicros == 0 {
				return api.Usagef("operation %d sets a budget amount, but no budget cap is configured; a person sets one with: googleads config set budget-cap <amount>", i)
			}
			if amount > opt.CapMicros {
				return api.Usagef("operation %d: budget amount %s (%d micros) exceeds the configured cap of %s (%d micros); --force does not override the cap",
					i, config.FormatMicros(amount), amount, config.FormatMicros(opt.CapMicros), opt.CapMicros)
			}
			if verb == "update" {
				raise(d, i, manifest.RiskSpend, "budget amount changed to "+config.FormatMicros(amount))
			}
		}
	}
	return nil
}

func asObject(v any) map[string]any {
	o, _ := v.(map[string]any)
	return o
}

// maskFields returns the top-level fields an updateMask names, in camelCase.
func maskFields(v any) map[string]bool {
	s, _ := v.(string)
	out := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			top, _, _ := strings.Cut(p, ".")
			out[toCamel(top)] = true
		}
	}
	return out
}

func show(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return "a default through updateMask"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

var digitsOnly = regexp.MustCompile(`^[0-9]+$`)

// micros reads an amount the way Google accepts it, a JSON number or a
// decimal string, but only in its plain form: digits only. Anything else is
// refused rather than interpreted, so the cap compares exactly what Google
// would use.
func micros(v any) (int64, error) {
	var s string
	switch t := v.(type) {
	case nil:
		return 0, nil // cleared through updateMask
	case json.Number:
		s = t.String()
	case string:
		s = t
	default:
		return 0, fmt.Errorf("must be a whole number of micros, got %v", t)
	}
	if !digitsOnly.MatchString(s) {
		return 0, fmt.Errorf("must be written as a plain whole number of micros (digits only), got %q", s)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("is out of range for micros: %q", s)
	}
	return n, nil
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/guard/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/guard
git commit -m "Add the guard: payload risk check, budget cap, force and read-only gates

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 9: CLI core: command tree, requests, paging, help

**Files:**
- Create: `cmd/googleads/main.go`
- Create: `internal/cli/app.go`, `internal/cli/root.go`, `internal/cli/version.go`, `internal/cli/generated.go`, `internal/cli/request.go`, `internal/cli/body.go`, `internal/cli/output.go`, `internal/cli/all.go`, `internal/cli/help.go`
- Test: `internal/cli/app_test.go`, `internal/cli/help_test.go`

**Interfaces:**
- Consumes: `manifest.*` (Tasks 1, 4), `config.Resolved`, `config.Resolve`, `config.NormalizeCustomerID` (Task 5), `api.*` (Task 6), `auth.ParseKey`, `auth.Key`, `auth.Source` (Task 7), `guard.Check`, `guard.Options` (Task 8).
- Produces (package `cli`, used by Tasks 10 to 12):

```go
func Execute(args []string) int
type app struct { manifest *manifest.Manifest; client *api.Client; res config.Resolved; stdout, stderr io.Writer; stdin io.Reader; cacheDir func() (string, error) }
func (a *app) run(args []string) int
func (a *app) renderError(err error) int
func (a *app) newRoot() *cobra.Command          // Tasks 10 and 11 add their commands to its AddCommand call
func (a *app) loadKey() (*auth.Key, error)
func (a *app) newSource(key *auth.Key) *auth.Source
func (a *app) readJSONBody(cmd *cobra.Command) ([]byte, error)
func (a *app) writeResponse(resp *api.Response, outputPath string) error
func (a *app) applyGlobalFlags(cmd *cobra.Command)
func groupRunE(cmd *cobra.Command, args []string) error
func flagBool(cmd *cobra.Command, name string) bool
func flagString(cmd *cobra.Command, name string) string
func writeFieldTable(w io.Writer, fields []manifest.Field)
func cleanDoc(doc string) string
func jsonCompact(v any) ([]byte, error)
func sortedKeys[V any](m map[string]V) []string
var Version = "dev"
```

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/spf13/cobra@v1.10.2`

- [ ] **Step 2: Write the failing tests**

`internal/cli/app_test.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/config"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// testManifest is a small hand-built manifest with every shape the CLI handles.
func testManifest() *manifest.Manifest {
	mut := func(field, res string) *manifest.Mutate { return &manifest.Mutate{OperationsField: field, Resource: res} }
	cid := manifest.Param{Name: "customerId", Type: "string", Required: true, Pattern: "^[^/]+$"}
	return &manifest.Manifest{
		SchemaVersion: 1, APIVersion: "v25", DiscoveryRevision: "20260923",
		Methods: []manifest.Method{
			{Command: []string{"search"}, ID: "customers.googleAds.search", HTTPMethod: "POST",
				Path: "v25/customers/{+customerId}/googleAds:search", Risk: "read", NeedsCustomer: true,
				PathParams: []manifest.Param{cid}, Request: "SearchRequest", ValidateOnly: true,
				Paging: manifest.PagingBodyToken, ResultsField: "results", Description: "Returns all rows that match the search query."},
			{Command: []string{"mutate"}, ID: "customers.googleAds.mutate", HTTPMethod: "POST",
				Path: "v25/customers/{+customerId}/googleAds:mutate", Risk: "write", NeedsCustomer: true,
				PathParams: []manifest.Param{cid}, Request: "MutateGoogleAdsRequest", ValidateOnly: true, PartialFailure: true,
				Paging: manifest.PagingNone, Mutate: mut("mutateOperations", "")},
			{Command: []string{"campaigns", "mutate"}, ID: "customers.campaigns.mutate", HTTPMethod: "POST",
				Path: "v25/customers/{+customerId}/campaigns:mutate", Risk: "write", NeedsCustomer: true,
				PathParams: []manifest.Param{cid}, Request: "MutateCampaignsRequest", ValidateOnly: true, PartialFailure: true,
				Paging: manifest.PagingNone, Mutate: mut("operations", "Campaign")},
			{Command: []string{"operations", "list"}, ID: "customers.operations.list", HTTPMethod: "GET", Path: "v25/{+name}",
				Risk: "read", PathParams: []manifest.Param{{Name: "name", Type: "string", Required: true, Pattern: "^customers/[^/]+/operations$"}},
				QueryParams: []manifest.Param{{Name: "pageSize", Type: "integer"}, {Name: "pageToken", Type: "string"}},
				Paging: manifest.PagingQueryToken, ResultsField: "operations"},
			{Command: []string{"recommendations", "apply"}, ID: "customers.recommendations.apply", HTTPMethod: "POST",
				Path: "v25/customers/{+customerId}/recommendations:apply", Risk: "spend", Blocked: "applies amounts Google chooses",
				NeedsCustomer: true, PathParams: []manifest.Param{cid}, Request: "ApplyRecommendationRequest", Paging: manifest.PagingNone},
			{Command: []string{"list-accessible-customers"}, ID: "customers.listAccessibleCustomers", HTTPMethod: "GET",
				Path: "v25/customers:listAccessibleCustomers", Risk: "read", Paging: manifest.PagingNone},
		},
		Schemas: map[string]manifest.Schema{
			"GoogleAdsGoogleadsV25Resources__Campaign": {Fields: []manifest.Field{
				{Name: "id", Type: "string", Format: "int64", ReadOnly: true, Doc: "The ID of the campaign."},
				{Name: "name", Type: "string", Doc: "The name of the campaign."},
				{Name: "networkSettings", Type: "object", Ref: "GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings"},
				{Name: "status", Type: "string", Enum: []string{"UNSPECIFIED", "UNKNOWN", "ENABLED", "PAUSED", "REMOVED"}, Doc: "The status of the campaign."},
			}},
			"GoogleAdsGoogleadsV25Resources_Campaign_NetworkSettings": {Fields: []manifest.Field{{Name: "targetGoogleSearch", Type: "boolean"}}},
			"SearchRequest": {Fields: []manifest.Field{{Name: "pageToken", Type: "string"}, {Name: "query", Type: "string", Required: true}}},
		},
		GenericOperations: map[string]string{"campaignOperation": "customers.campaigns.mutate"},
	}
}

type fakeServer struct {
	*httptest.Server
	hits    atomic.Int32
	mu      sync.Mutex
	bodies  []string
	uris    []string
	headers []http.Header
}

func newFakeServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body string)) *fakeServer {
	t.Helper()
	fs := &fakeServer{}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		fs.mu.Lock()
		fs.bodies = append(fs.bodies, string(b))
		fs.uris = append(fs.uris, r.URL.RequestURI())
		fs.headers = append(fs.headers, r.Header.Clone())
		fs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		handler(w, r, string(b))
	}))
	t.Cleanup(fs.Close)
	return fs
}

func reply(body string) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, _ string) { w.Write([]byte(body)) }
}

func testApp(t *testing.T, srv *fakeServer, res config.Resolved) (*app, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errb bytes.Buffer
	base := "https://googleads.googleapis.com"
	if srv != nil {
		base = srv.URL
	}
	a := &app{manifest: testManifest(), res: res, stdout: &out, stderr: &errb, stdin: strings.NewReader("")}
	a.client = &api.Client{BaseURL: base, AllowCustomBase: true, TokenSource: api.StaticToken("tok"),
		ReadOnly: res.ReadOnly, LoginCustomerID: res.LoginCustomerID, Sleep: func(time.Duration) {}}
	return a, &out, &errb
}

func errJSON(t *testing.T, errb *bytes.Buffer) map[string]any {
	t.Helper()
	if n := strings.Count(strings.TrimSuffix(errb.String(), "\n"), "\n"); n != 0 {
		t.Fatalf("stderr spans %d extra lines: %q", n, errb.String())
	}
	var e map[string]any
	if err := json.Unmarshal(errb.Bytes(), &e); err != nil {
		t.Fatalf("stderr is not JSON: %q", errb.String())
	}
	return e
}

func writeFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const pausedCampaign = `{"operations":[{"create":{"name":"x","status":"PAUSED"}}]}`

func TestVersion(t *testing.T) {
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"version"}); code != 0 || !strings.Contains(out.String(), "Google Ads API v25") {
		t.Fatalf("exit %d out %q", code, out.String())
	}
}

func TestUnknownCommandAndBareGroupAreUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"nope"}, {"campaigns"}, {}} {
		a, _, errb := testApp(t, nil, config.Resolved{})
		if code := a.run(args); code != 2 || errJSON(t, errb)["kind"] != "usage" {
			t.Errorf("%v: exit %d stderr %q", args, code, errb.String())
		}
	}
}

func TestCustomerIDFlagWithDashes(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[]}`))
	a, out, errb := testApp(t, srv, config.Resolved{})
	code := a.run([]string{"search", "--customer-id", "123-456-7890", "--query", "SELECT customer.id FROM customer WHERE metrics.clicks > 0"})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if srv.uris[0] != "/v25/customers/1234567890/googleAds:search" {
		t.Fatalf("uri %q", srv.uris[0])
	}
	if srv.bodies[0] != `{"query":"SELECT customer.id FROM customer WHERE metrics.clicks > 0"}` {
		t.Fatalf("body %q", srv.bodies[0])
	}
	if out.String() != "{\"results\":[]}\n" {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestCustomerFromConfigAndMissingCustomer(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, _ := testApp(t, srv, config.Resolved{CustomerID: "1112223333"})
	if code := a.run([]string{"search", "--query", "SELECT customer.id FROM customer"}); code != 0 || srv.uris[0] != "/v25/customers/1112223333/googleAds:search" {
		t.Fatalf("exit %d uris %v", code, srv.uris)
	}
	a, _, errb := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"search", "--query", "x"}); code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), "customer") {
		t.Fatalf("exit %d stderr %q", code, errb.String())
	}
	if srv.hits.Load() != 1 {
		t.Fatal("a call without a customer must not reach the network")
	}
}

func TestQueryAndDataConflict(t *testing.T) {
	a, _, errb := testApp(t, nil, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--query", "x", "--data", `{"query":"y"}`}); code != 2 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

func TestQueryFromFile(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	p := writeFile(t, "q.gaql", append([]byte{0xEF, 0xBB, 0xBF}, []byte("SELECT campaign.id FROM campaign\n")...))
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--query", "@" + p}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if srv.bodies[0] != `{"query":"SELECT campaign.id FROM campaign"}` {
		t.Fatalf("body %q", srv.bodies[0])
	}
}

func TestDataFileWithUTF8BOMAccepted(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[]}`))
	p := writeFile(t, "body.json", append([]byte{0xEF, 0xBB, 0xBF}, []byte(pausedCampaign)...))
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"campaigns", "mutate", "--data", "@" + p}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if srv.bodies[0] != pausedCampaign {
		t.Fatalf("body %q", srv.bodies[0])
	}
}

func TestDataFileUTF16Refused(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	p := writeFile(t, "body.json", []byte{0xFF, 0xFE, '{', 0, '}', 0})
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	code := a.run([]string{"campaigns", "mutate", "--data", "@" + p})
	msg, _ := errJSON(t, errb)["error"].(string)
	if code != 2 || !strings.Contains(msg, "UTF-16") || !strings.Contains(msg, "UTF-8") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d msg %q", code, msg)
	}
}

func TestSpendNeedsForceAndNeverReachesTheNetwork(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[{}]}`))
	enable := `{"operations":[{"create":{"name":"x"}}]}`
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	code := a.run([]string{"campaigns", "mutate", "--data", enable})
	if code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), "PAUSED") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d stderr %q hits %d", code, errb.String(), srv.hits.Load())
	}
	a, _, errb = testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"campaigns", "mutate", "--data", enable, "--force"}); code != 0 || srv.hits.Load() != 1 {
		t.Fatalf("forced: exit %d stderr %q", code, errb.String())
	}
}

func TestValidateOnlySetsTheFieldAndSkipsForce(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	code := a.run([]string{"campaigns", "mutate", "--validate-only", "--data", `{"validate_only":false,"operations":[{"create":{"name":"x"}}]}`})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	b := srv.bodies[0]
	if !strings.Contains(b, `"validateOnly":true`) || strings.Contains(b, "validate_only") || !strings.Contains(b, `"operations"`) {
		t.Fatalf("body %q", b)
	}
}

func TestPartialFailureExitsOneWithTheResponseOnStdout(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[{}],"partialFailureError":{"code":3,"message":"Multiple errors in details.","details":[{"@type":"type.googleapis.com/google.ads.googleads.v25.errors.GoogleAdsFailure","errors":[{"errorCode":{"fieldError":"REQUIRED"},"message":"The required field was not present."}]}]}}`))
	a, out, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	code := a.run([]string{"campaigns", "mutate", "--partial-failure", "--data", pausedCampaign})
	if code != 1 || errJSON(t, errb)["kind"] != "partial_failure" || !strings.Contains(out.String(), "partialFailureError") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out.String(), errb.String())
	}
	if !strings.Contains(srv.bodies[0], `"partialFailure":true`) {
		t.Fatalf("body %q", srv.bodies[0])
	}
}

func TestBlockedMethodRefused(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{})
	code := a.run([]string{"recommendations", "apply", "--force", "--data", `{"operations":[]}`})
	if code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), "blocked") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d stderr %q", code, errb.String())
	}
}

func TestReadOnlyAllowsValidateOnly(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	res := config.Resolved{CustomerID: "1234567890", ReadOnly: true}
	a, _, _ := testApp(t, srv, res)
	if code := a.run([]string{"campaigns", "mutate", "--data", pausedCampaign}); code != 2 {
		t.Fatalf("read-only write: exit %d", code)
	}
	a, _, errb := testApp(t, srv, res)
	if code := a.run([]string{"campaigns", "mutate", "--validate-only", "--data", pausedCampaign}); code != 0 {
		t.Fatalf("read-only validate-only: exit %d %s", code, errb.String())
	}
}

func TestAllBodyTokenMerges(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, body string) {
		if strings.Contains(body, `"pageToken":"p2"`) {
			w.Write([]byte(`{"results":[3]}`))
			return
		}
		w.Write([]byte(`{"results":[1,2],"nextPageToken":"p2"}`))
	})
	a, out, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--all", "--query", "SELECT campaign.id FROM campaign"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if out.String() != "[1,2,3]\n" {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestAllQueryTokenMerges(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.URL.Query().Get("pageToken") == "t" {
			w.Write([]byte(`{"operations":[{"name":"b"}]}`))
			return
		}
		w.Write([]byte(`{"operations":[{"name":"a"}],"nextPageToken":"t"}`))
	})
	a, out, errb := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"operations", "list", "customers/1234567890/operations", "--all"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if out.String() != `[{"name":"a"},{"name":"b"}]`+"\n" || !strings.HasPrefix(srv.uris[0], "/v25/customers/1234567890/operations") {
		t.Fatalf("stdout %q uris %v", out.String(), srv.uris)
	}
}

func TestAllMaxPagesIsIncomplete(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[1],"nextPageToken":"more"}`))
	a, out, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	code := a.run([]string{"search", "--all", "--max-pages", "2", "--query", "x"})
	if code != 1 || errJSON(t, errb)["kind"] != "incomplete" || out.String() != "[1,1]\n" {
		t.Fatalf("exit %d stdout %q", code, out.String())
	}
}

func TestAllRefusesSummaryRow(t *testing.T) {
	a, _, _ := testApp(t, nil, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--all", "--data", `{"query":"x","searchSettings":{"returnSummaryRow":true}}`}); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestPositionalValidation(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	for _, name := range []string{"customers/1/../x", "customers/1234567890/other", "customers/1/operations?x=1"} {
		a, _, errb := testApp(t, srv, config.Resolved{})
		if code := a.run([]string{"operations", "list", name}); code != 2 {
			t.Errorf("%q: exit %d %s", name, code, errb.String())
		}
	}
	if srv.hits.Load() != 0 {
		t.Fatal("an invalid path parameter must not reach the network")
	}
}

func TestLoginCustomerIDFlagSetsHeader(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--query", "x", "--login-customer-id", "111-222-3333"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if got := srv.headers[0].Get("login-customer-id"); got != "1112223333" {
		t.Fatalf("login-customer-id %q", got)
	}
}

func TestTopLevelLeafCommand(t *testing.T) {
	srv := newFakeServer(t, reply(`{"resourceNames":["customers/1234567890"]}`))
	a, out, errb := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"list-accessible-customers"}); code != 0 || srv.uris[0] != "/v25/customers:listAccessibleCustomers" {
		t.Fatalf("exit %d uris %v %s", code, srv.uris, errb.String())
	}
	if !strings.Contains(out.String(), "customers/1234567890") {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestAPIErrorIsOneJSONLine(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		w.Header().Set("request-id", "rq-9")
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":400,"message":"bad","status":"INVALID_ARGUMENT"}}`))
	})
	a, _, errb := testApp(t, srv, config.Resolved{CustomerID: "1234567890"})
	if code := a.run([]string{"search", "--query", "x"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	e := errJSON(t, errb)
	if e["kind"] != "validation" || e["request_id"] != "rq-9" || e["status"] != float64(400) {
		t.Fatalf("%v", e)
	}
}
```

`internal/cli/help_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

func help(t *testing.T, args ...string) string {
	t.Helper()
	a, out, errb := testApp(t, nil, config.Resolved{})
	if code := a.run(append(args, "--help")); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	return out.String()
}

func TestHelpShowsResourceFieldsAndExample(t *testing.T) {
	h := help(t, "campaigns", "mutate")
	for _, want := range []string{
		"POST v25/customers/{+customerId}/campaigns:mutate (risk: write)",
		"status string (one of: ENABLED, PAUSED, REMOVED): The status of the campaign.",
		"id int64 as string (read-only)",
		"networkSettings Campaign_NetworkSettings",
		`Example body:`, `{"operations":[{"create":{}}]}`,
		"--validate-only",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("help lacks %q:\n%s", want, h)
		}
	}
}

func TestHelpGenericMutateListsOperationTypes(t *testing.T) {
	if h := help(t, "mutate"); !strings.Contains(h, "campaignOperation -> Campaign") {
		t.Fatalf("help:\n%s", h)
	}
}

func TestHelpSearchHasExampleAndPaging(t *testing.T) {
	h := help(t, "search")
	if !strings.Contains(h, `--query "SELECT`) || !strings.Contains(h, "merged JSON array of the results rows") {
		t.Fatalf("help:\n%s", h)
	}
}

func TestHelpBlocked(t *testing.T) {
	if h := help(t, "recommendations", "apply"); !strings.Contains(h, "BLOCKED") {
		t.Fatalf("help:\n%s", h)
	}
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./internal/cli/`
Expected: FAIL (undefined identifiers).

- [ ] **Step 4: Implement**

`cmd/googleads/main.go`:

```go
// Command googleads is the CLI entry point: it hands the process arguments
// to the cli package and turns its result into an exit code.
package main

import (
	"os"

	"github.com/wir-drei-digital/google-ads-cli/internal/cli"
)

func main() { os.Exit(cli.Execute(os.Args[1:])) }
```

`internal/cli/app.go`:

```go
// Package cli builds the googleads command tree from the embedded manifest
// and turns process arguments into one API call, or one merged page walk.
//
// Two rules shape everything here: stdout carries the API response and
// nothing else, and every failure leaves as one line of JSON on stderr so an
// agent can parse it without heuristics.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/auth"
	"github.com/wir-drei-digital/google-ads-cli/internal/config"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

type app struct {
	manifest       *manifest.Manifest
	client         *api.Client
	res            config.Resolved
	stdout, stderr io.Writer
	stdin          io.Reader
	// cacheDir locates the token cache; nil means os.UserCacheDir.
	cacheDir func() (string, error)
}

// Execute is the process entry point: it loads the embedded manifest and the
// resolved config, runs the command tree and returns the exit code
// (0 success, 1 API or network failure, 2 usage error).
func Execute(args []string) int {
	m, err := manifest.Load()
	if err != nil {
		return (&app{stderr: os.Stderr}).renderError(api.Usagef("%v: this build's embedded manifest is unreadable, reinstall googleads", err))
	}
	res, err := config.Resolve(os.Getenv)
	if err != nil {
		return (&app{stderr: os.Stderr}).renderError(api.Usagef("%v", err))
	}
	// Ctrl-C and SIGTERM cancel the request in flight and any retry wait.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := &app{manifest: m, res: res, stdout: os.Stdout, stderr: os.Stderr, stdin: os.Stdin}
	a.client = &api.Client{
		BaseURL: res.APIBase, LoginCustomerID: res.LoginCustomerID, ReadOnly: res.ReadOnly,
		AllowCustomBase: res.AllowCustomBase, TokenSource: a.tokenSource(), Sleep: contextSleeper(ctx),
	}
	return a.runContext(ctx, args)
}

// tokenSource loads the key on the first API call, so a missing or broken
// key file cannot break commands that never call the API. It is nil when no
// key is configured, which the client reports as a usage error.
func (a *app) tokenSource() func(context.Context) (string, error) {
	if a.res.KeySource() == "" {
		return nil
	}
	var src *auth.Source
	return func(ctx context.Context) (string, error) {
		if src == nil {
			key, err := a.loadKey()
			if err != nil {
				return "", err
			}
			src = a.newSource(key)
		}
		return src.Token(ctx)
	}
}

func (a *app) newSource(key *auth.Key) *auth.Source {
	return &auth.Source{Key: key, TokenURL: a.res.TokenURL, AllowCustomBase: a.res.AllowCustomBase, CacheDir: a.cacheDir}
}

// loadKey reads and validates the configured key: the file named by
// GOOGLE_ADS_JSON_KEY_FILE_PATH, else the copy in the config file.
func (a *app) loadKey() (*auth.Key, error) {
	raw := a.res.KeyJSON
	if a.res.KeyPath != "" {
		b, err := os.ReadFile(a.res.KeyPath)
		if err != nil {
			return nil, api.Usagef("reading GOOGLE_ADS_JSON_KEY_FILE_PATH: %v", err)
		}
		raw = b
	}
	k, err := auth.ParseKey(raw)
	if err != nil {
		return nil, api.Usagef("%v", err)
	}
	return k, nil
}

// contextSleeper returns a sleep that gives up as soon as ctx is done.
func contextSleeper(ctx context.Context) func(time.Duration) {
	return func(d time.Duration) {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
		}
	}
}

func (a *app) run(args []string) int { return a.runContext(context.Background(), args) }

func (a *app) runContext(ctx context.Context, args []string) int {
	root := a.newRoot()
	root.SetArgs(args)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		return a.renderError(err)
	}
	return 0
}

// renderError writes err as one line of JSON on stderr and returns the exit
// code: 2 for usage errors, 1 for everything the API or the network produced.
func (a *app) renderError(err error) int {
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		apiErr = api.Usagef("%v", err) // anything else is a cobra parse or usage failure
	}
	raw, mErr := json.Marshal(apiErr)
	if mErr != nil {
		raw, mErr = json.Marshal(&api.Error{Kind: apiErr.Kind, Message: apiErr.Message, Status: apiErr.Status, RequestID: apiErr.RequestID})
		if mErr != nil {
			raw = []byte(`{"error":"internal: cannot render error","kind":"usage","details":null}`)
		}
	}
	fmt.Fprintln(a.stderr, string(raw))
	if apiErr.Kind == api.KindUsage {
		return 2
	}
	return 1
}
```

`internal/cli/root.go`:

```go
package cli

import (
	"time"

	"github.com/spf13/cobra"
)

func (a *app) newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "googleads",
		Short: "CLI for the Google Ads API, built for agents: JSON in, JSON out",
		Long: "CLI for the Google Ads API, built for agents: JSON in, JSON out.\n\n" +
			"stdout carries the API response and nothing else; errors are one line of JSON\n" +
			"on stderr. Exit codes: 0 success, 1 API or network error, 2 usage error.\n\n" +
			"Run `googleads commands --json` for the machine-readable catalog of every method\n" +
			"and `googleads schema <Name>` for the fields of a resource.",
		// The root names no method. Printing help and exiting 0 would read as
		// success and break the stdout contract, so it is a usage error.
		RunE:          groupRunE,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	pf := root.PersistentFlags()
	pf.Bool("verbose", false, "log requests to stderr (credentials are never logged)")
	pf.Duration("timeout", 60*time.Second, "per-attempt HTTP timeout")
	pf.Bool("force", false, "confirm a delete-, spend- or admin-class call")
	pf.String("output", "", "write the response body to a file instead of stdout")
	pf.String("customer-id", "", "customer to act on, e.g. 123-456-7890 (default: GOOGLE_ADS_CUSTOMER_ID or the config file)")
	pf.String("login-customer-id", "", "manager account the access goes through; sets the login-customer-id header")
	root.AddCommand(a.versionCommand())
	a.addGeneratedCommands(root)
	return root
}
```

`internal/cli/version.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is the released version, set with -ldflags at build time.
var Version = "dev"

func (a *app) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version and the Google Ads API version it speaks",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(a.stdout, "googleads %s (Google Ads API %s, Discovery revision %s)\n",
				Version, a.manifest.APIVersion, a.manifest.DiscoveryRevision)
		},
	}
}
```

`internal/cli/generated.go`:

```go
package cli

import (
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/guard"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// flagName maps a camelCase API parameter to a kebab-case flag:
// "returnPartialSuccess" becomes "return-partial-success". The API name goes
// on the wire.
func flagName(param string) string {
	var b strings.Builder
	for i, r := range param {
		switch {
		case unicode.IsUpper(r):
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
		case r == '_':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func flagBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

func flagString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

// groupRunE answers a namespace invoked without a subcommand: that is a
// usage error, not a success that prints help.
func groupRunE(cmd *cobra.Command, args []string) error {
	var names []string
	for _, sub := range cmd.Commands() {
		if !sub.Hidden && sub.Name() != "help" && sub.Name() != "completion" {
			names = append(names, sub.Name())
		}
	}
	if len(names) > 12 {
		names = append(names[:12], "...")
	}
	return api.Usagef("%s needs a subcommand: %s", cmd.CommandPath(), strings.Join(names, ", "))
}

// addGeneratedCommands hangs one leaf per manifest method off root, creating
// the group commands its path implies. A one-element path is a leaf directly
// under root (search, mutate, generate-keyword-ideas).
func (a *app) addGeneratedCommands(root *cobra.Command) {
	groups := map[string]*cobra.Command{}
	for i := range a.manifest.Methods {
		meth := &a.manifest.Methods[i]
		parent := root
		for depth := 1; depth < len(meth.Command); depth++ {
			key := strings.Join(meth.Command[:depth], " ")
			g, ok := groups[key]
			if !ok {
				g = &cobra.Command{Use: meth.Command[depth-1], Short: meth.Command[depth-1] + " methods", RunE: groupRunE}
				groups[key] = g
				parent.AddCommand(g)
			}
			parent = g
		}
		parent.AddCommand(a.leafCommand(meth))
	}
}

// isReport marks the two methods that take GAQL through --query.
func isReport(meth *manifest.Method) bool {
	return meth.ID == "customers.googleAds.search" || meth.ID == "customers.googleAds.searchStream"
}

func (a *app) leafCommand(meth *manifest.Method) *cobra.Command {
	use := meth.Command[len(meth.Command)-1]
	positional := 0
	for _, p := range meth.PathParams {
		if p.Name != "customerId" {
			use += " <" + p.Name + ">"
			positional++
		}
	}
	short := firstLine(meth.Description)
	if meth.Blocked != "" {
		short = "[blocked] " + short
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(positional),
		RunE:  func(cmd *cobra.Command, args []string) error { return a.runMethod(cmd, meth, args) },
	}
	// Help is built on demand: rendering field tables for every command on
	// every run would cost more than the call itself.
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		c.Long = a.longHelp(meth)
		c.Parent().HelpFunc()(c, args)
	})
	for _, q := range meth.QueryParams {
		usage := cleanDoc(q.Doc)
		if len(q.Enum) > 0 {
			usage += " (one of: " + strings.Join(trimEnum(q.Enum), ", ") + ")"
		}
		switch q.Type {
		case "integer":
			cmd.Flags().Int(flagName(q.Name), 0, usage)
		case "boolean":
			cmd.Flags().Bool(flagName(q.Name), false, usage)
		default:
			cmd.Flags().String(flagName(q.Name), "", usage)
		}
	}
	if meth.Request != "" {
		cmd.Flags().String("data", "", "JSON body: literal, @file, or - for stdin")
	}
	if isReport(meth) {
		cmd.Flags().String("query", "", "GAQL query, or @file to read it from a file")
	}
	if meth.ValidateOnly {
		cmd.Flags().Bool("validate-only", false, "let Google check the request completely without applying anything")
	}
	if meth.PartialFailure {
		cmd.Flags().Bool("partial-failure", false, "apply the operations that succeed; exit 1 with kind partial_failure if any fail")
	}
	if meth.Paging != manifest.PagingNone {
		cmd.Flags().Bool("all", false, "follow page tokens and print one merged JSON array of rows")
		cmd.Flags().Int("max-pages", 100, "page cap for --all")
	}
	return cmd
}

// runMethod turns one parsed command into one API call. The guard runs
// before any network I/O, so a refused call has no side effects at all.
func (a *app) runMethod(cmd *cobra.Command, meth *manifest.Method, args []string) error {
	if meth.Blocked != "" {
		_, err := guard.Check(a.manifest, meth, nil, guard.Options{})
		return err
	}
	req, err := a.buildRequest(cmd, meth, args)
	if err != nil {
		return err
	}
	validateOnly, partial := flagBool(cmd, "validate-only"), flagBool(cmd, "partial-failure")
	dec, err := guard.Check(a.manifest, meth, req.Body, guard.Options{
		Force: flagBool(cmd, "force"), ValidateOnly: validateOnly,
		ReadOnly: a.res.ReadOnly, CapMicros: a.res.BudgetCapMicros,
	})
	if err != nil {
		return err
	}
	req.Risk, req.ValidateOnly = dec.Class, validateOnly
	if req.Body, err = setOwnedFields(req.Body, validateOnly, partial); err != nil {
		return err
	}
	a.applyGlobalFlags(cmd)
	if flagBool(cmd, "all") {
		return a.runAll(cmd, meth, *req)
	}
	resp, err := a.client.Do(cmd.Context(), *req)
	if err != nil {
		return err
	}
	if err := a.writeResponse(resp, flagString(cmd, "output")); err != nil {
		return err
	}
	if partial {
		if pf := api.PartialFailure(*req, resp); pf != nil {
			return pf
		}
	}
	return nil
}

func (a *app) applyGlobalFlags(cmd *cobra.Command) {
	if flagBool(cmd, "verbose") {
		a.client.Verbose = a.stderr
	}
	if d, err := cmd.Flags().GetDuration("timeout"); err == nil {
		a.client.Timeout = d
	}
}
```

`internal/cli/request.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/config"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

var unsafePath = regexp.MustCompile(`[?#%\s\\]`)

// buildRequest turns flags and arguments into a request. The guard sees the
// body built here before the CLI sets the fields it owns.
func (a *app) buildRequest(cmd *cobra.Command, meth *manifest.Method, args []string) (*api.Request, error) {
	path, next := meth.Path, 0
	for _, p := range meth.PathParams {
		var val string
		if p.Name == "customerId" {
			id, err := a.customerID(cmd)
			if err != nil {
				return nil, err
			}
			val = id
		} else {
			val = args[next]
			next++
			if err := validatePathValue(p, val); err != nil {
				return nil, err
			}
		}
		// {+name} is reserved expansion: slashes stay as they are.
		path = strings.Replace(path, "{+"+p.Name+"}", val, 1)
		path = strings.Replace(path, "{"+p.Name+"}", url.PathEscape(val), 1)
	}
	q := url.Values{}
	for _, param := range meth.QueryParams {
		fn := flagName(param.Name)
		if !cmd.Flags().Changed(fn) {
			continue // only what the caller asked for goes on the wire
		}
		switch param.Type {
		case "integer":
			v, _ := cmd.Flags().GetInt(fn)
			q.Set(param.Name, strconv.Itoa(v))
		case "boolean":
			v, _ := cmd.Flags().GetBool(fn)
			q.Set(param.Name, strconv.FormatBool(v))
		default:
			q.Set(param.Name, flagString(cmd, fn))
		}
	}
	req := &api.Request{Method: meth.HTTPMethod, Path: path, Query: q, Risk: meth.Risk, MethodID: meth.ID}
	if meth.Request != "" {
		body, err := a.readJSONBody(cmd)
		if err != nil {
			return nil, err
		}
		if gaql := flagString(cmd, "query"); isReport(meth) && gaql != "" {
			if body != nil {
				return nil, api.Usagef("--query and --data cannot be combined: put the query in the body, or use --query alone")
			}
			text, err := readQuery(gaql)
			if err != nil {
				return nil, err
			}
			if body, err = jsonCompact(map[string]string{"query": text}); err != nil {
				return nil, api.Usagef("%v", err)
			}
		}
		req.Body = body
	}
	if v := flagString(cmd, "login-customer-id"); v != "" {
		id, err := config.NormalizeCustomerID(v)
		if err != nil {
			return nil, api.Usagef("--login-customer-id: %v", err)
		}
		a.client.LoginCustomerID = id
	}
	return req, nil
}

// customerID resolves the customer: the flag, else the environment or the
// config file (already normalized by config.Resolve).
func (a *app) customerID(cmd *cobra.Command) (string, error) {
	if v := flagString(cmd, "customer-id"); v != "" {
		id, err := config.NormalizeCustomerID(v)
		if err != nil {
			return "", api.Usagef("--customer-id: %v", err)
		}
		return id, nil
	}
	if a.res.CustomerID != "" {
		return a.res.CustomerID, nil
	}
	return "", api.Usagef("no customer ID: pass --customer-id, set GOOGLE_ADS_CUSTOMER_ID, or run `googleads config set customer-id <id>`")
}

// validatePathValue keeps a positional inside the path Google defines for
// it: no query or fragment characters, no empty, "." or ".." segments, and a
// match against the Discovery pattern.
func validatePathValue(p manifest.Param, v string) error {
	if v == "" || unsafePath.MatchString(v) {
		return api.Usagef("%s %q contains characters that cannot go into a URL path", p.Name, v)
	}
	for _, seg := range strings.Split(v, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return api.Usagef(`%s %q has an empty, "." or ".." segment`, p.Name, v)
		}
	}
	if p.Pattern != "" {
		if re, err := regexp.Compile(p.Pattern); err == nil && !re.MatchString(v) {
			return api.Usagef("%s %q does not have the expected form %s", p.Name, v, p.Pattern)
		}
	}
	return nil
}

// readQuery returns the GAQL text: literal, or from a file with @path.
func readQuery(v string) (string, error) {
	if !strings.HasPrefix(v, "@") {
		return v, nil
	}
	raw, err := os.ReadFile(v[1:])
	if err != nil {
		return "", api.Usagef("reading --query: %v", err)
	}
	if raw, err = decodeText(raw, "the --query file"); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// setOwnedFields sets validateOnly and partialFailure when their flags are
// given, removing either spelling of the same field first so Google never
// sees it twice. Without either flag the body goes out byte for byte.
func setOwnedFields(body []byte, validateOnly, partialFailure bool) ([]byte, error) {
	if !validateOnly && !partialFailure {
		return body, nil
	}
	obj := map[string]any{}
	if len(body) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if err := dec.Decode(&obj); err != nil {
			return nil, api.Usagef("--data must be a JSON object to combine it with --validate-only or --partial-failure: %v", err)
		}
	}
	if validateOnly {
		delete(obj, "validate_only")
		obj["validateOnly"] = true
	}
	if partialFailure {
		delete(obj, "partial_failure")
		obj["partialFailure"] = true
	}
	return jsonCompact(obj)
}

// jsonCompact marshals v without HTML escaping, so GAQL such as
// "metrics.clicks > 0" goes out as written.
func jsonCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
```

`internal/cli/body.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
)

// maxJSONBody caps --data so a mistaken `--data @/dev/urandom` fails locally.
const maxJSONBody = 20 << 20

// readJSONBody resolves --data: "-" reads stdin, "@path" reads a file,
// anything else is the literal body. (nil, nil) when --data was not given.
func (a *app) readJSONBody(cmd *cobra.Command) ([]byte, error) {
	val := flagString(cmd, "data")
	if val == "" {
		return nil, nil
	}
	var raw []byte
	var err error
	switch {
	case val == "-":
		raw, err = io.ReadAll(io.LimitReader(a.stdin, maxJSONBody+1))
	case strings.HasPrefix(val, "@"):
		raw, err = os.ReadFile(val[1:])
	default:
		raw = []byte(val)
	}
	if err != nil {
		return nil, api.Usagef("reading --data: %v", err)
	}
	if len(raw) > maxJSONBody {
		return nil, api.Usagef("--data exceeds the 20 MB limit")
	}
	if raw, err = decodeText(raw, "--data"); err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, api.Usagef("--data is not valid JSON")
	}
	return raw, nil
}

// decodeText strips a UTF-8 byte order mark and refuses UTF-16, which
// PowerShell 5 writes by default, with a message that names the fix.
func decodeText(raw []byte, what string) ([]byte, error) {
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		return raw[3:], nil
	}
	if bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) || bytes.HasPrefix(raw, []byte{0xFE, 0xFF}) ||
		(len(raw) >= 2 && (raw[0] == 0 || raw[1] == 0)) {
		return nil, api.Usagef("%s is UTF-16 encoded; save it as UTF-8 (in PowerShell: Set-Content -Encoding utf8)", what)
	}
	return raw, nil
}
```

`internal/cli/output.go`:

```go
package cli

import (
	"os"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
)

// writeResponse puts the body on stdout, or in outputPath, as Google sent it.
// The one change is a trailing newline when the body lacks one.
func (a *app) writeResponse(resp *api.Response, outputPath string) error {
	if len(resp.Body) == 0 {
		return nil
	}
	if outputPath != "" {
		if err := os.WriteFile(outputPath, resp.Body, 0o644); err != nil {
			return api.Usagef("writing %s: %v", outputPath, err)
		}
		return nil
	}
	a.stdout.Write(resp.Body)
	if resp.Body[len(resp.Body)-1] != '\n' {
		a.stdout.Write([]byte("\n"))
	}
	return nil
}
```

`internal/cli/all.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// runAll follows page tokens and writes ONE JSON array of the merged rows.
// It is the only place the CLI reshapes a response, so each row reaches the
// output exactly as Google sent it. Hitting --max-pages is not silent
// success: the partial array is written and the run ends as incomplete.
func (a *app) runAll(cmd *cobra.Command, meth *manifest.Method, base api.Request) error {
	maxPages, _ := cmd.Flags().GetInt("max-pages")
	var body map[string]any
	if meth.Paging == manifest.PagingBodyToken {
		body = map[string]any{}
		if len(base.Body) > 0 {
			dec := json.NewDecoder(bytes.NewReader(base.Body))
			dec.UseNumber()
			if err := dec.Decode(&body); err != nil {
				return api.Usagef("--all needs a JSON object body: %v", err)
			}
		}
		if wantsSummaryRow(body) {
			return api.Usagef("--all merges rows only and would drop the summary row; page by hand or use search-stream when you need searchSettings.returnSummaryRow")
		}
		delete(body, "page_token")
	}
	var rows []json.RawMessage
	token, complete := "", false
	for page := 0; page < maxPages; page++ {
		req := base
		switch meth.Paging {
		case manifest.PagingBodyToken:
			if token != "" {
				body["pageToken"] = token
			}
			raw, err := jsonCompact(body)
			if err != nil {
				return api.Usagef("%v", err)
			}
			req.Body = raw
		case manifest.PagingQueryToken:
			q := url.Values{}
			for k, v := range base.Query {
				q[k] = append([]string{}, v...)
			}
			if token != "" {
				q.Set("pageToken", token)
			}
			req.Query = q
		}
		resp, err := a.client.Do(cmd.Context(), req)
		if err != nil {
			return err
		}
		var env map[string]json.RawMessage
		if err := json.Unmarshal(resp.Body, &env); err != nil {
			return api.Usagef("--all expects a JSON object response, got: %.100s", resp.Body)
		}
		var pageRows []json.RawMessage
		if raw, ok := env[meth.ResultsField]; ok {
			if err := json.Unmarshal(raw, &pageRows); err != nil {
				return api.Usagef("--all: %s is not an array", meth.ResultsField)
			}
		}
		rows = append(rows, pageRows...)
		token = ""
		if raw, ok := env["nextPageToken"]; ok {
			json.Unmarshal(raw, &token)
		}
		if token == "" {
			complete = true
			break
		}
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, r := range rows {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(r)
	}
	buf.WriteString("]\n")
	if err := a.writeResponse(&api.Response{Body: buf.Bytes()}, flagString(cmd, "output")); err != nil {
		return err
	}
	if !complete {
		return &api.Error{Kind: api.KindIncomplete, Message: "hit --max-pages before the last page; the output is partial"}
	}
	return nil
}

func wantsSummaryRow(body map[string]any) bool {
	for _, key := range []string{"searchSettings", "search_settings"} {
		if ss, ok := body[key].(map[string]any); ok {
			for _, f := range []string{"returnSummaryRow", "return_summary_row"} {
				if v, _ := ss[f].(bool); v {
					return true
				}
			}
		}
	}
	return false
}
```

`internal/cli/help.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// cleanDoc flattens a description into one plain line. pflag reads a
// back-quoted word in a usage string as the flag's value type.
func cleanDoc(doc string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(doc, "`", "")), " ")
}

// firstLine is the first sentence of a description, for a command's Short.
func firstLine(doc string) string {
	d := cleanDoc(doc)
	if i := strings.Index(d, ". "); i >= 0 {
		d = d[:i+1]
	}
	return d
}

// trimEnum drops UNSPECIFIED and UNKNOWN, which no request should send.
func trimEnum(values []string) []string {
	var out []string
	for _, v := range values {
		if v != "UNSPECIFIED" && v != "UNKNOWN" {
			out = append(out, v)
		}
	}
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

// fieldLine renders one field for help and `schema`, for example
//
//	status string (one of: ENABLED, PAUSED, REMOVED): The status of the campaign.
func fieldLine(f manifest.Field) string {
	t := f.Type
	switch {
	case f.Ref != "":
		t = manifest.ShortName(f.Ref)
	case f.Format == "int64" || f.Format == "uint64":
		t = "int64 as string"
	}
	if f.Repeated {
		t = "list of " + t
	}
	var notes []string
	if f.Required {
		notes = append(notes, "required")
	}
	if f.Immutable {
		notes = append(notes, "immutable")
	}
	if f.ReadOnly {
		notes = append(notes, "read-only")
	}
	if e := trimEnum(f.Enum); len(e) > 0 {
		notes = append(notes, "one of: "+strings.Join(e, ", "))
	}
	s := "  " + f.Name + " " + t
	if len(notes) > 0 {
		s += " (" + strings.Join(notes, "; ") + ")"
	}
	if f.Doc != "" {
		s += ": " + cleanDoc(f.Doc)
	}
	return s
}

func writeFieldTable(w io.Writer, fields []manifest.Field) {
	for _, f := range fields {
		fmt.Fprintln(w, fieldLine(f))
	}
}

// resourceSchemaID finds the schema of a resource short name, preferring the
// Resources package when the name is ambiguous.
func (a *app) resourceSchemaID(name string) string {
	id, candidates := a.manifest.LookupSchema(name)
	if id != "" {
		return id
	}
	for _, c := range candidates {
		if strings.Contains(c, "Resources__") {
			return c
		}
	}
	return ""
}

// longHelp renders what a caller needs without the API docs: the method,
// its risk, arguments, and for a mutate the fields of the resource.
func (a *app) longHelp(meth *manifest.Method) string {
	var b strings.Builder
	if meth.Blocked != "" {
		b.WriteString("BLOCKED in this version of googleads: " + meth.Blocked + "\n\n")
	}
	if meth.Description != "" {
		b.WriteString(cleanDoc(meth.Description) + "\n")
	}
	fmt.Fprintf(&b, "\n%s %s (risk: %s)\n", meth.HTTPMethod, meth.Path, meth.Risk)
	if meth.NeedsCustomer {
		b.WriteString("Customer: --customer-id, GOOGLE_ADS_CUSTOMER_ID or `googleads config set customer-id`.\n")
	}
	var positional []manifest.Param
	for _, p := range meth.PathParams {
		if p.Name != "customerId" {
			positional = append(positional, p)
		}
	}
	if len(positional) > 0 {
		b.WriteString("\nArguments:\n")
		for _, p := range positional {
			b.WriteString("  " + p.Name)
			if p.Pattern != "" {
				b.WriteString(" (form " + p.Pattern + ")")
			}
			if p.Doc != "" {
				b.WriteString(": " + cleanDoc(p.Doc))
			}
			b.WriteString("\n")
		}
	}
	if isReport(meth) {
		b.WriteString("\nExample:\n  googleads " + strings.Join(meth.Command, " ") +
			` --query "SELECT campaign.id, campaign.name, metrics.cost_micros FROM campaign WHERE segments.date DURING LAST_7_DAYS"` + "\n")
	}
	switch {
	case meth.Mutate != nil && meth.Mutate.OperationsField == "mutateOperations":
		b.WriteString("\nOperation types, one per entry of mutateOperations:\n")
		for _, typ := range sortedKeys(a.manifest.GenericOperations) {
			res := ""
			if t := a.manifest.ByID(a.manifest.GenericOperations[typ]); t != nil && t.Mutate != nil {
				res = t.Mutate.Resource
			}
			fmt.Fprintf(&b, "  %s -> %s\n", typ, res)
		}
		b.WriteString("\nRun `googleads schema <Resource>` for a resource's fields. Temporary resource names\n" +
			"(customers/<id>/campaigns/-1) link operations inside one request.\n")
	case meth.Mutate != nil && meth.Mutate.Resource != "":
		if id := a.resourceSchemaID(meth.Mutate.Resource); id != "" {
			fields := a.manifest.Schemas[id].Fields
			fmt.Fprintf(&b, "\n%s fields, for create and update (`googleads schema <Name>` shows nested types):\n", meth.Mutate.Resource)
			writeFieldTable(&b, fields)
			b.WriteString("\nExample body:\n  " + exampleBody(meth, fields) + "\n")
		}
	case meth.Request != "":
		if s, ok := a.manifest.Schemas[meth.Request]; ok && len(s.Fields) > 0 {
			b.WriteString("\nBody fields:\n")
			writeFieldTable(&b, s.Fields)
		}
	}
	if meth.ValidateOnly {
		b.WriteString("\n--validate-only: Google checks the request completely and applies nothing.\n")
	}
	if meth.Paging != manifest.PagingNone {
		fmt.Fprintf(&b, "--all follows page tokens and prints one merged JSON array of the %s rows.\n", meth.ResultsField)
	}
	return b.String()
}

// exampleBody renders a skeleton for a per-resource mutate: one create with
// the fields Google marks as required.
func exampleBody(meth *manifest.Method, fields []manifest.Field) string {
	create := map[string]any{}
	for _, f := range fields {
		if f.Required && !f.ReadOnly {
			create[f.Name] = placeholderValue(f)
		}
	}
	op := map[string]any{"create": create}
	body := map[string]any{"operations": []any{op}}
	if meth.Mutate.OperationsField == "operation" {
		body = map[string]any{"operation": op}
	}
	raw, _ := json.Marshal(body)
	return string(raw)
}

func placeholderValue(f manifest.Field) any {
	switch {
	case f.Repeated:
		return []any{}
	case f.Type == "object" || f.Type == "map":
		return map[string]any{}
	case f.Format == "int64" || f.Format == "uint64":
		return "0"
	case f.Type == "integer" || f.Type == "number":
		return 0
	case f.Type == "boolean":
		return false
	}
	if e := trimEnum(f.Enum); len(e) > 0 {
		return e[0]
	}
	return ""
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `go mod tidy && go test ./internal/cli/ && go vet ./... && go build ./cmd/googleads && ./googleads version && rm -f googleads`
Expected: PASS; the binary prints `googleads dev (Google Ads API v25, Discovery revision 2026MMDD)`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum cmd internal/cli
git commit -m "Add the CLI core: command tree, requests, paging, help

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 10: Catalog, schema lookup and the api escape hatch

**Files:**
- Create: `internal/cli/catalog.go`, `internal/cli/schemacmd.go`, `internal/cli/apicmd.go`
- Modify: `internal/cli/root.go` (the `root.AddCommand(...)` line)
- Test: `internal/cli/catalog_test.go`, `internal/cli/apicmd_test.go`

**Interfaces:**
- Consumes: everything Task 9 produces; `guard.Check` (Task 8); `manifest.Find`, `manifest.LookupSchema`, `manifest.ShortName` (Task 1).
- Produces: `googleads commands [--json]`, `googleads schema <Name> [--json]`, `googleads api <METHOD> <path>`. The catalog JSON is a public contract (`schema_version` 1): top-level keys `schema_version`, `api_version`, `discovery_revision`, `commands`; per command `command`, `id`, `http_method`, `path`, `risk`, `blocked`, `blocked_reason`, `summary`, `needs_customer`, `path_params`, `query_params`, `request`, `resource`, `validate_only`, `partial_failure`, `paging`, `results_field`.

- [ ] **Step 1: Write the failing tests**

`internal/cli/catalog_test.go`:

```go
package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

func TestCommandsJSON(t *testing.T) {
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"commands", "--json"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var cat struct {
		SchemaVersion     int              `json:"schema_version"`
		APIVersion        string           `json:"api_version"`
		DiscoveryRevision string           `json:"discovery_revision"`
		Commands          []map[string]any `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &cat); err != nil {
		t.Fatal(err)
	}
	if cat.SchemaVersion != 1 || cat.APIVersion != "v25" || cat.DiscoveryRevision != "20260923" || len(cat.Commands) != 6 {
		t.Fatalf("%+v", cat)
	}
	by := map[string]map[string]any{}
	for _, c := range cat.Commands {
		by[c["command"].(string)] = c
	}
	if b := by["recommendations apply"]; b["blocked"] != true || b["blocked_reason"] == nil || b["risk"] != "spend" {
		t.Fatalf("blocked entry %v", b)
	}
	cm := by["campaigns mutate"]
	if cm["resource"] != "Campaign" || cm["validate_only"] != true || cm["needs_customer"] != true ||
		cm["request"] != "MutateCampaignsRequest" || cm["id"] != "customers.campaigns.mutate" {
		t.Fatalf("campaigns mutate %v", cm)
	}
	if _, ok := cm["path_params"]; ok {
		t.Fatal("customerId is a flag, not a positional: no path_params")
	}
	ol := by["operations list"]
	if ol["paging"] != "query-token" || ol["results_field"] != "operations" || len(ol["path_params"].([]any)) != 1 {
		t.Fatalf("operations list %v", ol)
	}
}

func TestCommandsText(t *testing.T) {
	a, out, _ := testApp(t, nil, config.Resolved{})
	a.run([]string{"commands"})
	if !strings.Contains(out.String(), "campaigns mutate") || !strings.Contains(out.String(), "[write]") || !strings.Contains(out.String(), "blocked") {
		t.Fatalf("%s", out.String())
	}
}

func TestSchemaCommand(t *testing.T) {
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"schema", "Campaign"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "Campaign (GoogleAdsGoogleadsV25Resources__Campaign)") || !strings.Contains(out.String(), "status string") {
		t.Fatalf("%s", out.String())
	}
	a, out, _ = testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"schema", "Campaign_NetworkSettings", "--json"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var s struct {
		Name   string           `json:"name"`
		ID     string           `json:"id"`
		Fields []manifest.Field `json:"fields"`
	}
	if err := json.Unmarshal(out.Bytes(), &s); err != nil || s.Name != "Campaign_NetworkSettings" || s.Fields[0].Name != "targetGoogleSearch" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestSchemaAmbiguousAndUnknown(t *testing.T) {
	a, _, errb := testApp(t, nil, config.Resolved{})
	a.manifest.Schemas["GoogleAdsGoogleadsV25Services__Campaign"] = manifest.Schema{}
	if code := a.run([]string{"schema", "Campaign"}); code != 2 || !strings.Contains(errb.String(), "GoogleAdsGoogleadsV25Services__Campaign") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	a, _, errb = testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"schema", "Nope"}); code != 2 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}
```

`internal/cli/apicmd_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

func TestAPIMatchedPathInheritsTheGuard(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{})
	code := a.run([]string{"api", "POST", "v25/customers/1234567890/campaigns:mutate", "--data", `{"operations":[{"create":{"name":"x"}}]}`})
	if code != 2 || !strings.Contains(errb.String(), "PAUSED") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestAPIOtherVersionIsInspected(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{})
	code := a.run([]string{"api", "POST", "/v24/customers/1234567890/campaigns:mutate", "--data", `{"operations":[{"create":{"status":"ENABLED"}}]}`})
	if code != 2 || srv.hits.Load() != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestAPIUnmatchedMutationIsAdmin(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"api", "POST", "v25/customers/1/somethingNew:do", "--data", `{}`}); code != 2 || !strings.Contains(errb.String(), "admin") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	a, _, errb = testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"api", "POST", "v25/customers/1/somethingNew:do", "--data", `{}`, "--force"}); code != 0 || srv.hits.Load() != 1 {
		t.Fatalf("forced: exit %d %s", code, errb.String())
	}
}

func TestAPIBlockedPath(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, _, errb := testApp(t, srv, config.Resolved{})
	code := a.run([]string{"api", "POST", "v25/customers/1234567890/recommendations:apply", "--data", `{}`, "--force"})
	if code != 2 || !strings.Contains(errb.String(), "blocked") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestAPIRefusals(t *testing.T) {
	for _, args := range [][]string{
		{"api", "GET", "v25/customers:listAccessibleCustomers", "--query", "access_token=x"},
		{"api", "GET", "v25/customers:listAccessibleCustomers?key=x"},
		{"api", "TRACE", "v25/x"},
		{"api", "GET", ""},
	} {
		a, _, errb := testApp(t, nil, config.Resolved{})
		if code := a.run(args); code != 2 {
			t.Errorf("%v: exit %d %s", args, code, errb.String())
		}
	}
}

func TestAPIReadOnly(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[]}`))
	res := config.Resolved{ReadOnly: true}
	a, _, errb := testApp(t, srv, res)
	if code := a.run([]string{"api", "POST", "v25/customers/1234567890/googleAds:search", "--data", `{"query":"x"}`}); code != 0 {
		t.Fatalf("read-class POST in read-only mode: exit %d %s", code, errb.String())
	}
	a, _, _ = testApp(t, srv, res)
	if code := a.run([]string{"api", "GET", "v25/customers/1234567890/anythingElse"}); code != 0 {
		t.Fatalf("GET in read-only mode: exit %d", code)
	}
	a, _, _ = testApp(t, srv, res)
	if code := a.run([]string{"api", "POST", "v25/customers/1234567890/campaigns:mutate", "--data", pausedCampaign}); code != 2 {
		t.Fatalf("write in read-only mode: exit %d", code)
	}
}

func TestAPIAuthorizationIsTheClients(t *testing.T) {
	srv := newFakeServer(t, reply(`{}`))
	a, out, _ := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"api", "GET", "v25/customers:listAccessibleCustomers", "--header", "Authorization: Bearer stolen", "--header", "X-Test: 1"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := srv.headers[0].Get("Authorization"); got != "Bearer tok" || srv.headers[0].Get("X-Test") != "1" {
		t.Fatalf("headers %v", srv.headers[0])
	}
	if out.String() != "{}\n" {
		t.Fatalf("stdout %q", out.String())
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/cli/`
Expected: FAIL (unknown commands `commands`, `schema`, `api`).

- [ ] **Step 3: Implement**

`internal/cli/catalog.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// catalogEntry is one command as `googleads commands --json` publishes it:
// a stable projection of the manifest. schema_version is the contract a
// consumer pins to; new optional keys may appear under the same version.
type catalogEntry struct {
	Command        string           `json:"command"`
	ID             string           `json:"id"`
	HTTPMethod     string           `json:"http_method"`
	Path           string           `json:"path"`
	Risk           string           `json:"risk"`
	Blocked        bool             `json:"blocked,omitempty"`
	BlockedReason  string           `json:"blocked_reason,omitempty"`
	Summary        string           `json:"summary,omitempty"`
	NeedsCustomer  bool             `json:"needs_customer"`
	PathParams     []manifest.Param `json:"path_params,omitempty"` // positional; customerId is the --customer-id flag
	QueryParams    []manifest.Param `json:"query_params,omitempty"`
	Request        string           `json:"request,omitempty"`  // short schema name of the body
	Resource       string           `json:"resource,omitempty"` // for per-resource mutates
	ValidateOnly   bool             `json:"validate_only"`
	PartialFailure bool             `json:"partial_failure"`
	Paging         string           `json:"paging"`
	ResultsField   string           `json:"results_field,omitempty"`
}

func (a *app) commandsCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "commands",
		Short: "List every API command (--json for the machine-readable catalog)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !asJSON {
				for _, m := range a.manifest.Methods {
					tag := ""
					if m.Blocked != "" {
						tag = " blocked"
					}
					fmt.Fprintf(a.stdout, "%-60s %-6s [%s]%s\n", strings.Join(m.Command, " "), m.HTTPMethod, m.Risk, tag)
				}
				return nil
			}
			out := struct {
				SchemaVersion     int            `json:"schema_version"`
				APIVersion        string         `json:"api_version"`
				DiscoveryRevision string         `json:"discovery_revision"`
				Commands          []catalogEntry `json:"commands"`
			}{SchemaVersion: manifest.SchemaVersion, APIVersion: a.manifest.APIVersion,
				DiscoveryRevision: a.manifest.DiscoveryRevision, Commands: []catalogEntry{}}
			for _, m := range a.manifest.Methods {
				e := catalogEntry{
					Command: strings.Join(m.Command, " "), ID: m.ID, HTTPMethod: m.HTTPMethod, Path: m.Path, Risk: m.Risk,
					Blocked: m.Blocked != "", BlockedReason: m.Blocked, Summary: firstLine(m.Description),
					NeedsCustomer: m.NeedsCustomer, QueryParams: m.QueryParams, ValidateOnly: m.ValidateOnly,
					PartialFailure: m.PartialFailure, Paging: m.Paging, ResultsField: m.ResultsField,
				}
				for _, p := range m.PathParams {
					if p.Name != "customerId" {
						e.PathParams = append(e.PathParams, p)
					}
				}
				if m.Request != "" {
					e.Request = manifest.ShortName(m.Request)
				}
				if m.Mutate != nil {
					e.Resource = m.Mutate.Resource
				}
				out.Commands = append(out.Commands, e)
			}
			enc := json.NewEncoder(a.stdout)
			enc.SetEscapeHTML(false)
			return enc.Encode(out)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the machine-readable catalog")
	return cmd
}
```

`internal/cli/schemacmd.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

func (a *app) schemaCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "schema <Name>",
		Short: "Print the fields of a request or resource type, e.g. Campaign or Campaign_NetworkSettings",
		Long: "Print the fields of a request or resource type. Names are Google's schema names without the\n" +
			"GoogleAdsGoogleadsV25<Package> prefix (Campaign, Campaign_NetworkSettings, ResponsiveSearchAdInfo);\n" +
			"a full name works too. int64 fields travel as strings, amounts in micros.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, candidates := a.manifest.LookupSchema(args[0])
			if id == "" {
				if len(candidates) > 0 {
					return api.Usagef("schema name %q is ambiguous; use one of: %s", args[0], strings.Join(candidates, ", "))
				}
				return api.Usagef("no schema named %q; names look like Campaign, CampaignBudget, Campaign_NetworkSettings", args[0])
			}
			fields := a.manifest.Schemas[id].Fields
			if asJSON {
				return json.NewEncoder(a.stdout).Encode(struct {
					Name   string           `json:"name"`
					ID     string           `json:"id"`
					Fields []manifest.Field `json:"fields"`
				}{manifest.ShortName(id), id, fields})
			}
			fmt.Fprintf(a.stdout, "%s (%s)\n\n", manifest.ShortName(id), id)
			writeFieldTable(a.stdout, fields)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the field table as JSON")
	return cmd
}
```

`internal/cli/apicmd.go`:

```go
package cli

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/guard"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

var validMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// apiCommand is the escape hatch for anything the generated tree lacks. It is
// not a bypass: a path the manifest knows, in any API version, gets that
// method's class, payload check and block; an unknown mutation is admin.
func (a *app) apiCommand() *cobra.Command {
	var queries, headers []string
	cmd := &cobra.Command{
		Use:   "api <METHOD> <path>",
		Short: "Raw authenticated request (escape hatch); the guardrails still apply",
		Long: "Raw authenticated request. The path is relative to https://googleads.googleapis.com/,\n" +
			"for example v25/customers/1234567890/googleAds:search, without a query string (use --query k=v).\n\n" +
			"A path that matches a known method, in any API version, inherits its risk class, the payload\n" +
			"check, the budget cap and the block list. An unmatched POST, PUT, PATCH or DELETE is admin-class\n" +
			"and needs --force. Read-only mode allows GET and read-class methods. Authorization and the\n" +
			"credential query parameters access_token, oauth_token and key cannot be set.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			method := strings.ToUpper(args[0])
			if !validMethods[method] {
				return api.Usagef("method %q not allowed (GET, POST, PUT, PATCH, DELETE)", args[0])
			}
			path := strings.TrimLeft(args[1], "/")
			if path == "" || strings.ContainsAny(path, "?#") {
				return api.Usagef("path must be relative, e.g. v25/customers/1234567890/googleAds:search, with no query string (use --query k=v)")
			}
			q := url.Values{}
			for _, kv := range queries {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return api.Usagef("--query wants k=v, got %q", kv)
				}
				q.Add(k, v)
			}
			hdr := http.Header{}
			for _, kv := range headers {
				k, v, ok := strings.Cut(kv, ":")
				if !ok || strings.TrimSpace(k) == "" {
					return api.Usagef("--header wants k:v, got %q", kv)
				}
				hdr.Add(strings.TrimSpace(k), strings.TrimSpace(v))
			}
			body, err := a.readJSONBody(cmd)
			if err != nil {
				return err
			}
			target := a.manifest.Find(method, path)
			if target == nil {
				risk := manifest.RiskAdmin
				if method == "GET" {
					risk = manifest.RiskRead
				}
				target = &manifest.Method{ID: "raw " + method + " " + path, Command: []string{"api", method, path}, HTTPMethod: method, Risk: risk}
			}
			dec, err := guard.Check(a.manifest, target, body, guard.Options{
				Force: flagBool(cmd, "force"), ReadOnly: a.res.ReadOnly, CapMicros: a.res.BudgetCapMicros,
			})
			if err != nil {
				return err
			}
			risk := dec.Class
			if method == "GET" {
				risk = manifest.RiskRead
			}
			a.applyGlobalFlags(cmd)
			resp, err := a.client.Do(cmd.Context(), api.Request{
				Method: method, Path: path, Query: q, Body: body, Risk: risk, MethodID: target.ID, Headers: hdr,
			})
			if err != nil {
				return err
			}
			return a.writeResponse(resp, flagString(cmd, "output"))
		},
	}
	cmd.Flags().String("data", "", "JSON body: literal, @file, or - for stdin")
	cmd.Flags().StringArrayVar(&queries, "query", nil, "query parameter k=v (repeatable)")
	cmd.Flags().StringArrayVar(&headers, "header", nil, "extra header k:v (repeatable; Authorization cannot be set)")
	return cmd
}
```

Modify `internal/cli/root.go`: replace `root.AddCommand(a.versionCommand())` with

```go
	root.AddCommand(a.versionCommand(), a.commandsCommand(), a.schemaCommand(), a.apiCommand())
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/cli/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "Add the catalog, schema lookup and the guarded api escape hatch

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: config, auth and init commands

**Files:**
- Create: `internal/cli/prompt.go`, `internal/cli/configcmd.go`, `internal/cli/authcmd.go`, `internal/cli/initcmd.go`
- Modify: `internal/cli/app.go` (two fields on `app`), `internal/cli/root.go` (the `AddCommand` line)
- Test: `internal/cli/configcmd_test.go`, `internal/cli/authcmd_test.go`, `internal/cli/initcmd_test.go`

**Interfaces:**
- Consumes: Task 9's `app`, `loadKey`, `newSource`, `groupRunE`, `jsonCompact`; `config.*` (Task 5); `auth.ParseKey`, `auth.Source.CachedUntil` (Task 7); `api.*` (Task 6).
- Produces: `googleads config set|unset <key>`, `googleads config path`, `googleads auth status|token`, `googleads init`. `auth status` prints one line of JSON with keys `mode`, `source`, `client_email`, `private_key_id`, `project_id`, `token_cached_until`, `customer_id`, `login_customer_id`, `budget_cap`, `read_only`, `missing`, `hint`.

- [ ] **Step 1: Add the dependency**

Run: `go get golang.org/x/term@v0.45.0`

- [ ] **Step 2: Write the failing tests**

`internal/cli/configcmd_test.go`:

```go
package cli

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

// isolate points every per-user directory at a temp dir, so the tests never
// read or write the developer's real config file or token cache.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for k, v := range map[string]string{
		"HOME": home, "USERPROFILE": home,
		"XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_CACHE_HOME": filepath.Join(home, "cache"),
		"AppData": filepath.Join(home, "appdata"), "LocalAppData": filepath.Join(home, "localappdata"),
	} {
		t.Setenv(k, v)
	}
	return home
}

var (
	cliKeyOnce sync.Once
	cliKey     []byte
)

func testKeyJSON(t *testing.T) []byte {
	t.Helper()
	cliKeyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		der, _ := x509.MarshalPKCS8PrivateKey(k)
		cliKey, _ = json.Marshal(map[string]string{
			"type": "service_account", "project_id": "proj", "private_key_id": "kid-1",
			"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
			"client_email": "gads@proj.iam.gserviceaccount.com",
		})
	})
	return cliKey
}

func TestConfigSetServiceAccountKeyFromStdin(t *testing.T) {
	isolate(t)
	a, out, errb := testApp(t, nil, config.Resolved{})
	a.stdin = bytes.NewReader(testKeyJSON(t))
	if code := a.run([]string{"config", "set", "service-account-key"}); code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	c, _ := config.Load()
	if len(c.ServiceAccountKey) == 0 || !strings.Contains(out.String(), "gads@proj.iam.gserviceaccount.com") {
		t.Fatalf("stored %d bytes, stdout %q", len(c.ServiceAccountKey), out.String())
	}
	a, _, _ = testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "service-account-key", "secret-from-argv"}); code != 2 {
		t.Fatalf("a key from argv must be refused: exit %d", code)
	}
}

func TestConfigSetServiceAccountKeyRejectsOAuthClient(t *testing.T) {
	isolate(t)
	a, _, errb := testApp(t, nil, config.Resolved{})
	a.stdin = strings.NewReader(`{"installed":{"client_id":"x"}}`)
	if code := a.run([]string{"config", "set", "service-account-key"}); code != 2 || !strings.Contains(errb.String(), "OAuth client file") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestConfigSetCustomerIDs(t *testing.T) {
	isolate(t)
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "customer-id", "123-456-7890"}); code != 0 || !strings.Contains(out.String(), "customer_id = 1234567890") {
		t.Fatalf("exit %d %q", code, out.String())
	}
	a, _, _ = testApp(t, nil, config.Resolved{})
	a.run([]string{"config", "set", "login-customer-id", "111 222 3333"})
	if c, _ := config.Load(); c.CustomerID != "1234567890" || c.LoginCustomerID != "1112223333" {
		t.Fatalf("%+v", c)
	}
	a, _, _ = testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "customer-id", "12345"}); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestConfigSetBudgetCapComma(t *testing.T) {
	isolate(t)
	a, _, errb := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "budget-cap", "29,61"}); code != 2 || !strings.Contains(errb.String(), "dot") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "budget-cap", "29.61"}); code != 0 || !strings.Contains(out.String(), "29.61 (29610000 micros") {
		t.Fatalf("exit %d %q", code, out.String())
	}
	if c, _ := config.Load(); c.BudgetCapMicros != 29610000 {
		t.Fatalf("cap %d", c.BudgetCapMicros)
	}
}

func TestConfigUnsetAndPath(t *testing.T) {
	isolate(t)
	config.Save(config.Config{BudgetCapMicros: 1})
	a, out, _ := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "unset", "budget-cap"}); code != 0 || !strings.Contains(out.String(), "removed") {
		t.Fatalf("exit %d %q", code, out.String())
	}
	a, out, _ = testApp(t, nil, config.Resolved{})
	if a.run([]string{"config", "unset", "budget-cap"}); !strings.Contains(out.String(), "was not set") {
		t.Fatalf("%q", out.String())
	}
	a, out, _ = testApp(t, nil, config.Resolved{})
	a.run([]string{"config", "path"})
	if !strings.HasSuffix(strings.TrimSpace(out.String()), filepath.Join("googleads", "config.json")) {
		t.Fatalf("%q", out.String())
	}
}

func TestConfigEnvNameSuggestion(t *testing.T) {
	isolate(t)
	a, _, errb := testApp(t, nil, config.Resolved{})
	if code := a.run([]string{"config", "set", "GOOGLE_ADS_CUSTOMER_ID", "1"}); code != 2 || !strings.Contains(errb.String(), "config set customer-id") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}
```

`internal/cli/authcmd_test.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

// fakeGoogle serves a token endpoint and the two calls init makes.
func fakeGoogle(t *testing.T, searchStatus int) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/token":
			fmt.Fprintf(w, `{"access_token":"at-%d","expires_in":3600,"token_type":"Bearer"}`, n.Add(1))
		case r.URL.Path == "/v25/customers:listAccessibleCustomers":
			w.Write([]byte(`{"resourceNames":["customers/1234567890"]}`))
		case r.URL.Path == "/v25/customers/1234567890/googleAds:search" && searchStatus == 200:
			w.Write([]byte(`{"results":[{"customer":{"resourceName":"customers/1234567890","descriptiveName":"Muster AG","currencyCode":"CHF","timeZone":"Europe/Zurich"}}]}`))
		default:
			w.WriteHeader(403)
			w.Write([]byte(`{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.ads.googleads.v25.errors.GoogleAdsFailure","errors":[{"errorCode":{"authorizationError":"USER_PERMISSION_DENIED"},"message":"User doesn't have permission to access customer."}]}]}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func statusOf(t *testing.T, res config.Resolved) map[string]any {
	t.Helper()
	a, out, errb := testApp(t, nil, res)
	if code := a.run([]string{"auth", "status"}); code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	if strings.Contains(out.String(), "PRIVATE KEY") {
		t.Fatal("auth status must never print key material")
	}
	var s map[string]any
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAuthStatusNone(t *testing.T) {
	isolate(t)
	s := statusOf(t, config.Resolved{})
	missing := fmt.Sprint(s["missing"])
	if s["mode"] != "none" || !strings.Contains(missing, "service_account_key") || !strings.Contains(missing, "customer_id") {
		t.Fatalf("%v", s)
	}
}

func TestAuthStatusWithKey(t *testing.T) {
	isolate(t)
	s := statusOf(t, config.Resolved{KeyJSON: testKeyJSON(t), CustomerID: "1234567890", BudgetCapMicros: 29610000,
		TokenURL: config.DefaultTokenURL})
	if s["mode"] != "service_account" || s["source"] != "config" || s["client_email"] != "gads@proj.iam.gserviceaccount.com" ||
		s["budget_cap"] != "29.61" || s["missing"] != nil {
		t.Fatalf("%v", s)
	}
}

func TestAuthTokenPrintsAToken(t *testing.T) {
	isolate(t)
	srv := fakeGoogle(t, 200)
	res := config.Resolved{KeyJSON: testKeyJSON(t), TokenURL: srv.URL + "/token", AllowCustomBase: true}
	a, out, errb := testApp(t, nil, res)
	a.client.TokenSource = a.tokenSource()
	if code := a.run([]string{"auth", "token"}); code != 0 || strings.TrimSpace(out.String()) != "at-1" {
		t.Fatalf("exit %d out %q %s", code, out.String(), errb.String())
	}
}
```

`internal/cli/initcmd_test.go`:

```go
package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

type fakePrompter struct{ answers []string }

func (f *fakePrompter) Line(string) (string, error) {
	if len(f.answers) == 0 {
		return "", io.EOF
	}
	s := f.answers[0]
	f.answers = f.answers[1:]
	return s, nil
}
func (f *fakePrompter) Secret(p string) (string, error)     { return f.Line(p) }
func (f *fakePrompter) Confirm(string, bool) (bool, error) { return true, nil }

// initApp wires an app to a fake Google (token endpoint and API on one
// server) with a terminal and scripted answers.
func initApp(t *testing.T, searchStatus int, answers ...string) (*app, string) {
	t.Helper()
	home := isolate(t)
	srv := fakeGoogle(t, searchStatus)
	a, _, _ := testApp(t, &fakeServer{Server: srv}, config.Resolved{TokenURL: srv.URL + "/token", AllowCustomBase: true})
	a.isTerminal = func() bool { return true }
	a.prompt = &fakePrompter{answers: answers}
	return a, home
}

func TestInitRefusesWithoutTerminal(t *testing.T) {
	isolate(t)
	a, _, errb := testApp(t, nil, config.Resolved{})
	a.isTerminal = func() bool { return false }
	if code := a.run([]string{"init"}); code != 2 || !strings.Contains(errb.String(), "terminal") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestInitExpandsHomeInKeyPath(t *testing.T) {
	// The first cap answer uses a decimal comma and must be asked again.
	a, home := initApp(t, 200, "~/key.json", "123-456-7890", "29,61", "29.61")
	if err := os.WriteFile(filepath.Join(home, "key.json"), testKeyJSON(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := a.run([]string{"init"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	c, err := config.Load()
	if err != nil || len(c.ServiceAccountKey) == 0 || c.CustomerID != "1234567890" || c.BudgetCapMicros != 29610000 {
		t.Fatalf("saved %+v %v", c, err)
	}
	out := a.stdout.(interface{ String() string }).String()
	if !strings.Contains(out, "Muster AG") || !strings.Contains(out, "CHF") || !strings.Contains(out, "delete the downloaded copy") {
		t.Fatalf("stdout %q", out)
	}
}

func TestInitSavesNothingWhenVerificationFails(t *testing.T) {
	a, home := initApp(t, 403, "~/key.json", "123-456-7890", "29.61")
	os.WriteFile(filepath.Join(home, "key.json"), testKeyJSON(t), 0o600)
	if code := a.run([]string{"init"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if c, _ := config.Load(); c.CustomerID != "" || len(c.ServiceAccountKey) != 0 {
		t.Fatalf("a failed verification saved %+v", c)
	}
	if errOut := a.stderr.(interface{ String() string }).String(); !strings.Contains(errOut, "Access and security") {
		t.Fatalf("stderr %q", errOut)
	}
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./internal/cli/`
Expected: FAIL (unknown commands and undefined `prompter`, `isTerminal`, `prompt`).

- [ ] **Step 4: Implement**

`internal/cli/prompt.go`: copy `/Users/daniel/Development/bexio-cli/internal/cli/prompt.go` verbatim (the `prompter` interface, `termPrompter`, `newTermPrompter`, `Line`, `Secret`, `Confirm`, `ask`, `stdioIsTerminal`, `isTerminal`). In its comments replace every `bexio` with `googleads`. Nothing else changes.

Modify `internal/cli/app.go`: add two fields at the end of the `app` struct:

```go
	// prompt and isTerminal are seams for `init`, the one interactive
	// command; nil in production, where init uses the real terminal.
	prompt     prompter
	isTerminal func() bool
```

Modify `internal/cli/root.go`: the `AddCommand` line becomes

```go
	root.AddCommand(a.versionCommand(), a.commandsCommand(), a.schemaCommand(), a.apiCommand(),
		a.configCommand(), a.authCommand(), a.initCommand())
```

`internal/cli/configcmd.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/auth"
	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

func (a *app) configCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Manage the googleads config file", RunE: groupRunE}
	set := &cobra.Command{Use: "set", Short: "Set a config value", Args: cobra.ArbitraryArgs, RunE: keyGroupRunE("set")}
	set.AddCommand(
		&cobra.Command{
			Use:   "service-account-key",
			Short: "Read a service-account key (JSON) from stdin, check it and store it (0600)",
			Long: "Read a service-account key file from stdin, check it and store it in the config file (0600).\n\n" +
				"The key never comes from the command line, which every process on the machine can see:\n" +
				"  googleads config set service-account-key < key.json\n\n" +
				"Delete the downloaded key file afterwards. GOOGLE_ADS_JSON_KEY_FILE_PATH, when set, wins.",
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				raw, err := io.ReadAll(io.LimitReader(a.stdin, 1<<20))
				if err != nil {
					return api.Usagef("reading stdin: %v", err)
				}
				raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
				if len(bytes.TrimSpace(raw)) == 0 {
					return api.Usagef("no key on stdin; usage: googleads config set service-account-key < key.json")
				}
				key, err := auth.ParseKey(raw)
				if err != nil {
					return api.Usagef("%v", err)
				}
				var compact bytes.Buffer
				if err := json.Compact(&compact, raw); err != nil {
					return api.Usagef("%v", err)
				}
				if err := a.updateConfig(func(c *config.Config) { c.ServiceAccountKey = compact.Bytes() }); err != nil {
					return err
				}
				a.warnEnv("GOOGLE_ADS_JSON_KEY_FILE_PATH")
				fmt.Fprintf(a.stdout, "service-account key saved (%s, key id %s); delete the downloaded key file now\n",
					key.ClientEmail, key.PrivateKeyID)
				return nil
			},
		},
		a.customerIDSetter("customer-id", "customer to act on by default", "GOOGLE_ADS_CUSTOMER_ID",
			func(c *config.Config, v string) { c.CustomerID = v }),
		a.customerIDSetter("login-customer-id", "manager account the access goes through", "GOOGLE_ADS_LOGIN_CUSTOMER_ID",
			func(c *config.Config, v string) { c.LoginCustomerID = v }),
		&cobra.Command{
			Use:   "read-only <true|false>",
			Short: "Persist read-only mode",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				v, err := strconv.ParseBool(args[0])
				if err != nil {
					return api.Usagef("read-only wants true or false, got %q", args[0])
				}
				if err := a.updateConfig(func(c *config.Config) { c.ReadOnly = v }); err != nil {
					return err
				}
				fmt.Fprintf(a.stdout, "read_only = %v\n", v)
				return nil
			},
		},
		&cobra.Command{
			Use:   "budget-cap <amount>",
			Short: "Set the highest daily budget googleads may ever set, in the account's currency",
			Long: "Set the budget cap: the highest daily budget amount googleads accepts in any request, in the\n" +
				"account's currency (for example 30 or 29.61, a dot as the decimal separator). --force does not\n" +
				"override it, and no environment variable or flag can change it. Without a cap no budget amount\n" +
				"can be set at all.\n\n" +
				"Google may spend up to twice the daily budget on a single day and bills at most 30.4 times the\n" +
				"daily budget in a month.",
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				micros, err := config.ParseMoneyToMicros(args[0])
				if err != nil {
					return api.Usagef("budget-cap: %v", err)
				}
				if err := a.updateConfig(func(c *config.Config) { c.BudgetCapMicros = micros }); err != nil {
					return err
				}
				fmt.Fprintf(a.stdout, "budget_cap = %s (%d micros, in the account's currency)\n", config.FormatMicros(micros), micros)
				return nil
			},
		},
	)

	unset := &cobra.Command{Use: "unset", Short: "Remove a config value", Args: cobra.ArbitraryArgs, RunE: keyGroupRunE("unset")}
	for _, k := range []struct {
		use, short, done string
		stored           func(config.Config) bool
		clear            func(*config.Config)
	}{
		{"service-account-key", "Remove the stored service-account key from this machine",
			"service-account key removed from this machine; it stays valid until you delete it in the Google Cloud Console (IAM & Admin, Service accounts, Keys)",
			func(c config.Config) bool { return len(c.ServiceAccountKey) > 0 }, func(c *config.Config) { c.ServiceAccountKey = nil }},
		{"customer-id", "Remove the default customer", "customer-id removed",
			func(c config.Config) bool { return c.CustomerID != "" }, func(c *config.Config) { c.CustomerID = "" }},
		{"login-customer-id", "Remove the login customer", "login-customer-id removed",
			func(c config.Config) bool { return c.LoginCustomerID != "" }, func(c *config.Config) { c.LoginCustomerID = "" }},
		{"read-only", "Stop persisting read-only mode", "read-only removed",
			func(c config.Config) bool { return c.ReadOnly }, func(c *config.Config) { c.ReadOnly = false }},
		{"budget-cap", "Remove the budget cap", "budget-cap removed; no budget amount can be set until a new cap is configured",
			func(c config.Config) bool { return c.BudgetCapMicros != 0 }, func(c *config.Config) { c.BudgetCapMicros = 0 }},
	} {
		unset.AddCommand(&cobra.Command{
			Use: k.use, Short: k.short, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := config.Load()
				if err != nil {
					return api.Usagef("%v", err)
				}
				if !k.stored(c) {
					fmt.Fprintf(a.stdout, "%s was not set\n", k.use)
					return nil
				}
				k.clear(&c)
				if err := config.Save(c); err != nil {
					return api.Usagef("%v", err)
				}
				fmt.Fprintln(a.stdout, k.done)
				return nil
			},
		})
	}

	path := &cobra.Command{
		Use: "path", Short: "Print the config file location", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := config.Path()
			if err != nil {
				return api.Usagef("%v", err)
			}
			fmt.Fprintln(a.stdout, p)
			return nil
		},
	}
	cmd.AddCommand(set, unset, path)
	return cmd
}

func (a *app) customerIDSetter(use, what, envName string, assign func(*config.Config, string)) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <id>",
		Short: "Save the " + what + " (e.g. 123-456-7890)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := config.NormalizeCustomerID(args[0])
			if err != nil {
				return api.Usagef("%s: %v", use, err)
			}
			if err := a.updateConfig(func(c *config.Config) { assign(c, id) }); err != nil {
				return err
			}
			a.warnEnv(envName)
			fmt.Fprintf(a.stdout, "%s = %s\n", strings.ReplaceAll(use, "-", "_"), id)
			return nil
		},
	}
}

// updateConfig loads the config file, applies edit and saves it.
func (a *app) updateConfig(edit func(*config.Config)) error {
	c, err := config.Load()
	if err != nil {
		return api.Usagef("%v", err)
	}
	edit(&c)
	if err := config.Save(c); err != nil {
		return api.Usagef("%v", err)
	}
	return nil
}

// warnEnv says on stderr when an environment variable overrides what was
// just saved.
func (a *app) warnEnv(name string) {
	if a.res.FromEnv[name] {
		fmt.Fprintf(a.stderr, "note: %s is set in this environment and wins over the config file\n", name)
	}
}

// keyGroupRunE answers `config set` or `config unset` given something that is
// not one of their keys. An environment-variable name is the expected mistake,
// so it is answered with the key it plainly means.
func keyGroupRunE(verb string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			if k := suggestConfigKey(args[0]); k != "" {
				return api.Usagef("config keys are service-account-key, customer-id, login-customer-id, read-only and budget-cap, "+
					"not environment variable names; did you mean `googleads config %s %s`?", verb, k)
			}
		}
		return groupRunE(cmd, args)
	}
}

func suggestConfigKey(given string) string {
	switch strings.TrimPrefix(strings.ToLower(given), "google_ads_") {
	case "json_key_file_path", "service_account_key", "key":
		return "service-account-key"
	case "customer_id", "customerid":
		return "customer-id"
	case "login_customer_id", "logincustomerid":
		return "login-customer-id"
	case "read_only", "readonly":
		return "read-only"
	case "budget_cap", "budgetcap", "budget_cap_micros":
		return "budget-cap"
	}
	return ""
}
```

`internal/cli/authcmd.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

// authStatus is `googleads auth status`: what is configured, never a secret.
type authStatus struct {
	Mode             string   `json:"mode"` // service_account or none
	Source           string   `json:"source,omitempty"`
	ClientEmail      string   `json:"client_email,omitempty"`
	PrivateKeyID     string   `json:"private_key_id,omitempty"`
	ProjectID        string   `json:"project_id,omitempty"`
	TokenCachedUntil string   `json:"token_cached_until,omitempty"`
	CustomerID       string   `json:"customer_id,omitempty"`
	LoginCustomerID  string   `json:"login_customer_id,omitempty"`
	BudgetCap        string   `json:"budget_cap,omitempty"`
	ReadOnly         bool     `json:"read_only"`
	Missing          []string `json:"missing,omitempty"`
	Hint             string   `json:"hint,omitempty"`
}

func (a *app) authCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Inspect the configured credential", RunE: groupRunE}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "status",
			Short: "Show the configured credential as one line of JSON (offline; never prints a key or token)",
			Args:  cobra.NoArgs,
			RunE:  func(cmd *cobra.Command, args []string) error { return json.NewEncoder(a.stdout).Encode(a.authStatus()) },
		},
		&cobra.Command{
			Use:   "token",
			Short: "Print a valid access token to stdout (a credential: never log it)",
			Long: "Print a valid access token to stdout, for piping into another tool such as curl.\n" +
				"This writes a credential to stdout: do not log it or paste it anywhere.",
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if a.client.TokenSource == nil {
					return api.Usagef("no service-account key: set GOOGLE_ADS_JSON_KEY_FILE_PATH, store one with `googleads config set service-account-key < key.json`, or run `googleads init`")
				}
				tok, err := a.client.TokenSource(cmd.Context())
				if err != nil {
					return err
				}
				fmt.Fprintln(a.stdout, tok)
				return nil
			},
		},
	)
	return cmd
}

func (a *app) authStatus() authStatus {
	s := authStatus{Mode: "none", Source: a.res.KeySource(), CustomerID: a.res.CustomerID,
		LoginCustomerID: a.res.LoginCustomerID, ReadOnly: a.res.ReadOnly}
	if a.res.BudgetCapMicros > 0 {
		s.BudgetCap = config.FormatMicros(a.res.BudgetCapMicros)
	}
	var hints []string
	if s.Source == "" {
		s.Missing = append(s.Missing, "service_account_key")
		hints = append(hints, "run `googleads init`, or store a key with `googleads config set service-account-key < key.json`")
	} else {
		s.Mode = "service_account"
		if key, err := a.loadKey(); err != nil {
			s.Missing = append(s.Missing, "valid_service_account_key")
			hints = append(hints, err.Error())
		} else {
			s.ClientEmail, s.PrivateKeyID, s.ProjectID = key.ClientEmail, key.PrivateKeyID, key.ProjectID
			if until := a.newSource(key).CachedUntil(); until.After(time.Now()) {
				s.TokenCachedUntil = until.UTC().Format(time.RFC3339)
			}
		}
	}
	if s.CustomerID == "" {
		s.Missing = append(s.Missing, "customer_id")
		hints = append(hints, "set a customer with `googleads config set customer-id <id>` or pass --customer-id")
	}
	if s.BudgetCap == "" {
		hints = append(hints, "no budget cap: budget amounts cannot be set until one is configured with `googleads config set budget-cap <amount>`")
	}
	s.Hint = strings.Join(hints, "; ")
	return s
}
```

`internal/cli/initcmd.go`:

```go
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/auth"
	"github.com/wir-drei-digital/google-ads-cli/internal/config"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

func (a *app) initCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Interactive setup: service-account key, customer, budget cap (needs a terminal)",
		Long: "Interactive setup. Asks for the path of a service-account key file, the customer ID and a budget\n" +
			"cap, checks the key against Google with two read-only calls, and saves everything to the config\n" +
			"file (0600). Needs a terminal: agents configure googleads with `googleads config set` or the\n" +
			"environment instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			isTTY := a.isTerminal
			if isTTY == nil {
				isTTY = stdioIsTerminal
			}
			if !isTTY() {
				return api.Usagef("googleads init is interactive and needs a terminal; use `googleads config set ...` instead")
			}
			p := a.prompt
			if p == nil {
				p = newTermPrompter(os.Stdin, a.stdout)
			}
			return a.runInit(cmd.Context(), p)
		},
	}
}

func (a *app) runInit(ctx context.Context, p prompter) error {
	out := a.stdout
	fmt.Fprintln(out, "googleads setup: you need a service-account key file and your Google Ads customer ID.")
	for _, name := range []string{"GOOGLE_ADS_JSON_KEY_FILE_PATH", "GOOGLE_ADS_CUSTOMER_ID", "GOOGLE_ADS_LOGIN_CUSTOMER_ID", "GOOGLE_ADS_READ_ONLY"} {
		if a.res.FromEnv[name] {
			fmt.Fprintf(out, "Note: %s is set in this shell and wins over what you save here.\n", name)
		}
	}
	key, keyJSON, keyPath, err := a.askKey(p)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Key for %s (project %s).\n", key.ClientEmail, key.ProjectID)
	customer, err := askValid(p, out, "Google Ads customer ID (e.g. 123-456-7890): ", config.NormalizeCustomerID)
	if err != nil {
		return err
	}
	name, currency, zone, err := a.verify(ctx, key, customer)
	if err != nil {
		return err // nothing is saved; the error carries Google's reason and a hint
	}
	fmt.Fprintf(out, "Connected to %s (currency %s, time zone %s).\n", name, currency, zone)
	capMicros, err := askValid(p, out,
		fmt.Sprintf("Budget cap: the highest daily budget googleads may ever set, in %s (e.g. 30): ", currency),
		config.ParseMoneyToMicros)
	if err != nil {
		return err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, keyJSON); err != nil {
		return api.Usagef("%v", err)
	}
	if err := a.updateConfig(func(c *config.Config) {
		c.ServiceAccountKey, c.CustomerID, c.BudgetCapMicros = compact.Bytes(), customer, capMicros
	}); err != nil {
		return err
	}
	fmt.Fprintf(out, "Saved. The key now lives in the config file; delete the downloaded copy: %s\n", keyPath)
	return nil
}

// askKey asks for the key file until one reads and validates, three times at most.
func (a *app) askKey(p prompter) (*auth.Key, []byte, string, error) {
	for range 3 {
		path, err := ask(p, "Path to the service-account key file (JSON): ", false)
		if err != nil {
			return nil, nil, "", api.Usagef("init: %v", err)
		}
		path = expandHome(path)
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(a.stdout, "Cannot read %s: %v\n", path, err)
			continue
		}
		raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
		key, err := auth.ParseKey(raw)
		if err != nil {
			fmt.Fprintln(a.stdout, err)
			continue
		}
		return key, raw, path, nil
	}
	return nil, nil, "", api.Usagef("init: no usable service-account key after 3 attempts")
}

// askValid asks until parse accepts the answer, three times at most.
func askValid[T any](p prompter, out io.Writer, prompt string, parse func(string) (T, error)) (T, error) {
	var zero T
	for range 3 {
		s, err := ask(p, prompt, false)
		if err != nil {
			return zero, api.Usagef("init: %v", err)
		}
		v, err := parse(s)
		if err == nil {
			return v, nil
		}
		fmt.Fprintln(out, err)
	}
	return zero, api.Usagef("init: no valid answer after 3 attempts")
}

// expandHome turns a leading ~ into the home directory, as a shell would,
// and drops the quotes a pasted path often carries.
func expandHome(path string) string {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// verify proves the key reaches the customer with two read-only calls and
// returns the account's name, currency and time zone.
func (a *app) verify(ctx context.Context, key *auth.Key, customer string) (string, string, string, error) {
	list, search := a.manifest.ByID("customers.listAccessibleCustomers"), a.manifest.ByID("customers.googleAds.search")
	if list == nil || search == nil {
		return "", "", "", api.Usagef("init: this build's manifest lacks the verification methods; reinstall googleads")
	}
	client := *a.client
	client.TokenSource = a.newSource(key).Token
	if _, err := client.Do(ctx, api.Request{Method: list.HTTPMethod, Path: list.Path, Risk: manifest.RiskRead, MethodID: list.ID}); err != nil {
		return "", "", "", err
	}
	body, err := jsonCompact(map[string]string{"query": "SELECT customer.descriptive_name, customer.currency_code, customer.time_zone FROM customer LIMIT 1"})
	if err != nil {
		return "", "", "", api.Usagef("%v", err)
	}
	resp, err := client.Do(ctx, api.Request{Method: search.HTTPMethod, Path: strings.Replace(search.Path, "{+customerId}", customer, 1),
		Body: body, Risk: manifest.RiskRead, MethodID: search.ID})
	if err != nil {
		return "", "", "", err
	}
	var r struct {
		Results []struct {
			Customer struct {
				DescriptiveName string `json:"descriptiveName"`
				CurrencyCode    string `json:"currencyCode"`
				TimeZone        string `json:"timeZone"`
			} `json:"customer"`
		} `json:"results"`
	}
	if err := json.Unmarshal(resp.Body, &r); err != nil || len(r.Results) == 0 {
		return "", "", "", api.Usagef("init: the account check returned no customer row: %.200s", resp.Body)
	}
	c := r.Results[0].Customer
	return c.DescriptiveName, c.CurrencyCode, c.TimeZone, nil
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `go mod tidy && go test ./internal/cli/ && go vet ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/cli
git commit -m "Add config, auth and init commands

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 12: End-to-end smoke test and the manual live check

**Files:**
- Create: `e2e/helpers_test.go`, `e2e/smoke_test.go`, `e2e/live_test.go`

**Interfaces:**
- Consumes: the whole binary (`cmd/googleads`), `config.ParseMoneyToMicros` (Task 5).
- Produces: `go test ./e2e` (runs in CI) and `GOOGLE_ADS_LIVE=1 go test ./e2e -run TestLive -v` (manual, against a real account).

- [ ] **Step 1: Write the helpers**

`e2e/helpers_test.go`:

```go
// Package e2e drives the real binary as a subprocess: argv parsing, exit
// codes, stdout and stderr separation and the embedded manifest, exactly as
// a user gets them. Everything else in the repo tests in-process.
package e2e

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "googleads")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, "github.com/wir-drei-digital/google-ads-cli/cmd/googleads").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// run executes the binary with exactly env and returns stdout, stderr and the exit code.
func run(t *testing.T, bin string, env []string, stdin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

// scrubbedEnv drops every GOOGLE_ADS_* variable and points every per-user
// directory into a temp dir, so a developer's real key or config can never
// turn these tests green or red.
func scrubbedEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	home := t.TempDir()
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOOGLE_ADS_") {
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+home, "USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"), "XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		"AppData="+filepath.Join(home, "appdata"), "LocalAppData="+filepath.Join(home, "localappdata"))
	return append(env, extra...)
}

var (
	keyOnce sync.Once
	keyRaw  []byte
)

// writeKeyFile writes a throwaway service-account key and returns its path.
func writeKeyFile(t *testing.T) string {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		der, _ := x509.MarshalPKCS8PrivateKey(k)
		keyRaw, _ = json.Marshal(map[string]string{
			"type": "service_account", "project_id": "proj", "private_key_id": "kid-1",
			"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
			"client_email": "gads@proj.iam.gserviceaccount.com",
		})
	})
	p := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(p, keyRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeGoogle is one server for the token endpoint and the API.
type fakeGoogle struct {
	*httptest.Server
	mutates  atomic.Int32
	mu       sync.Mutex
	lastBody string
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	g := &fakeGoogle{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		b, _ := io.ReadAll(r.Body)
		switch {
		case r.URL.Path == "/token":
			w.Write([]byte(`{"access_token":"smoke-token","expires_in":3600,"token_type":"Bearer"}`))
			return
		case r.Header.Get("Authorization") != "Bearer smoke-token":
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"code":401,"message":"unauthenticated","status":"UNAUTHENTICATED"}}`))
			return
		}
		switch r.URL.Path {
		case "/v25/customers:listAccessibleCustomers":
			w.Write([]byte(`{"resourceNames":["customers/1234567890"]}`))
		case "/v25/customers/1234567890/googleAds:search":
			w.Write([]byte(`{"results":[{"customer":{"resourceName":"customers/1234567890","id":"1234567890"}}]}`))
		case "/v25/customers/1234567890/googleAds:mutate":
			g.mutates.Add(1)
			g.mu.Lock()
			g.lastBody = string(b)
			g.mu.Unlock()
			if strings.Contains(string(b), "FAIL_ME") {
				w.Header().Set("request-id", "rq-smoke")
				w.WriteHeader(400)
				w.Write([]byte(`{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.ads.googleads.v25.errors.GoogleAdsFailure","errors":[{"errorCode":{"fieldError":"REQUIRED"},"message":"The required field was not present.","location":{"fieldPathElements":[{"fieldName":"mutate_operations","index":0},{"fieldName":"campaign_operation"},{"fieldName":"create"},{"fieldName":"contains_eu_political_advertising"}]}}],"requestId":"rq-smoke"}]}}`))
				return
			}
			w.Write([]byte(`{"mutateOperationResponses":[]}`))
		default:
			w.WriteHeader(404)
			fmt.Fprintf(w, `{"error":{"code":404,"message":"no route %s","status":"NOT_FOUND"}}`, r.URL.Path)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *fakeGoogle) body() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastBody
}

// errLine parses the single JSON line a failure leaves on stderr.
func errLine(t *testing.T, stderr string) map[string]any {
	t.Helper()
	if strings.Count(strings.TrimSuffix(stderr, "\n"), "\n") != 0 {
		t.Fatalf("stderr is not one line: %q", stderr)
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(stderr), &e); err != nil {
		t.Fatalf("stderr is not JSON: %q", stderr)
	}
	return e
}
```

- [ ] **Step 2: Write the smoke test**

`e2e/smoke_test.go`:

```go
package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSmoke(t *testing.T) {
	bin := buildBinary(t)
	g := newFakeGoogle(t)
	env := scrubbedEnv(t,
		"GOOGLE_ADS_JSON_KEY_FILE_PATH="+writeKeyFile(t), "GOOGLE_ADS_TOKEN_URL="+g.URL+"/token",
		"GOOGLE_ADS_API_BASE="+g.URL, "GOOGLE_ADS_ALLOW_CUSTOM_BASE=1", "GOOGLE_ADS_CUSTOMER_ID=123-456-7890")
	enable := `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x"}}}]}`

	t.Run("version", func(t *testing.T) {
		out, _, code := run(t, bin, env, "", "version")
		if code != 0 || !strings.Contains(out, "Google Ads API v25") {
			t.Fatalf("exit %d out %q", code, out)
		}
	})
	t.Run("catalog", func(t *testing.T) {
		out, _, code := run(t, bin, env, "", "commands", "--json")
		var cat struct {
			Commands []struct {
				Command string `json:"command"`
			} `json:"commands"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &cat) != nil || len(cat.Commands) < 150 {
			t.Fatalf("exit %d, %d commands", code, len(cat.Commands))
		}
	})
	t.Run("search", func(t *testing.T) {
		out, errOut, code := run(t, bin, env, "", "search", "--query", "SELECT customer.id FROM customer")
		if code != 0 || errOut != "" || !strings.Contains(out, `"results"`) {
			t.Fatalf("exit %d out %q err %q", code, out, errOut)
		}
	})
	t.Run("spend needs force", func(t *testing.T) {
		_, errOut, code := run(t, bin, env, "", "mutate", "--data", enable)
		if code != 2 || errLine(t, errOut)["kind"] != "usage" || g.mutates.Load() != 0 {
			t.Fatalf("exit %d err %q mutates %d", code, errOut, g.mutates.Load())
		}
	})
	t.Run("validate-only", func(t *testing.T) {
		_, errOut, code := run(t, bin, env, "", "mutate", "--validate-only", "--data", enable)
		if code != 0 || !strings.Contains(g.body(), `"validateOnly":true`) {
			t.Fatalf("exit %d err %q body %q", code, errOut, g.body())
		}
	})
	t.Run("google error", func(t *testing.T) {
		body := `{"mutateOperations":[{"campaignOperation":{"create":{"name":"FAIL_ME","status":"PAUSED"}}}]}`
		_, errOut, code := run(t, bin, env, "", "mutate", "--data", body)
		e := errLine(t, errOut)
		if code != 1 || e["kind"] != "validation" || e["request_id"] != "rq-smoke" ||
			!strings.Contains(e["error"].(string), "(mutate_operations[0].campaign_operation.create.contains_eu_political_advertising)") {
			t.Fatalf("exit %d err %v", code, e)
		}
	})
	t.Run("auth status", func(t *testing.T) {
		out, _, code := run(t, bin, env, "", "auth", "status")
		if code != 0 || strings.Contains(out, "PRIVATE KEY") || !strings.Contains(out, `"mode":"service_account"`) {
			t.Fatalf("exit %d out %q", code, out)
		}
	})
	t.Run("budget cap", func(t *testing.T) {
		budget := func(amount string) string {
			return `{"mutateOperations":[{"campaignBudgetOperation":{"create":{"name":"b","amountMicros":"` + amount + `","explicitlyShared":false}}}]}`
		}
		if _, errOut, code := run(t, bin, env, "", "mutate", "--data", budget("1000000")); code != 2 || !strings.Contains(errOut, "no budget cap") {
			t.Fatalf("no cap: exit %d %q", code, errOut)
		}
		if _, errOut, code := run(t, bin, env, "", "config", "set", "budget-cap", "30"); code != 0 {
			t.Fatalf("set cap: exit %d %q", code, errOut)
		}
		if _, errOut, code := run(t, bin, env, "", "mutate", "--force", "--data", budget("30000001")); code != 2 || !strings.Contains(errOut, "exceeds") {
			t.Fatalf("above cap: exit %d %q", code, errOut)
		}
		if _, errOut, code := run(t, bin, env, "", "mutate", "--data", budget("30000000")); code != 0 {
			t.Fatalf("at cap: exit %d %q", code, errOut)
		}
	})
	t.Run("no key", func(t *testing.T) {
		_, errOut, code := run(t, bin, scrubbedEnv(t, "GOOGLE_ADS_CUSTOMER_ID=1234567890"), "", "search", "--query", "x")
		if code != 2 || !strings.Contains(errLine(t, errOut)["error"].(string), "no service-account key") {
			t.Fatalf("exit %d err %q", code, errOut)
		}
	})
}
```

- [ ] **Step 3: Write the live check**

`e2e/live_test.go`:

```go
package e2e

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

// liveCampaign is a complete minimal Search campaign built with temporary
// resource names. It carries no language criterion: Google announced that
// Search campaigns stop accepting them through the API from late September
// 2026.
const liveCampaign = `{"mutateOperations":[
{"campaignBudgetOperation":{"create":{"resourceName":"customers/CID/campaignBudgets/-1","name":"googleads live check NAME","amountMicros":"AMOUNT","deliveryMethod":"STANDARD","explicitlyShared":false}}},
{"campaignOperation":{"create":{"resourceName":"customers/CID/campaigns/-2","name":"googleads live check NAME",STATUS"advertisingChannelType":"SEARCH","campaignBudget":"customers/CID/campaignBudgets/-1","manualCpc":{"enhancedCpcEnabled":false},"networkSettings":{"targetGoogleSearch":true,"targetSearchNetwork":false,"targetContentNetwork":false,"targetPartnerSearchNetwork":false},"containsEuPoliticalAdvertising":"DOES_NOT_CONTAIN_EU_POLITICAL_ADVERTISING"}}},
{"campaignCriterionOperation":{"create":{"campaign":"customers/CID/campaigns/-2","location":{"geoTargetConstant":"geoTargetConstants/2756"}}}},
{"adGroupOperation":{"create":{"resourceName":"customers/CID/adGroups/-3","name":"googleads live check","campaign":"customers/CID/campaigns/-2","type":"SEARCH_STANDARD","status":"ENABLED","cpcBidMicros":"1000000"}}},
{"adGroupCriterionOperation":{"create":{"adGroup":"customers/CID/adGroups/-3","status":"ENABLED","keyword":{"text":"googleads live check","matchType":"EXACT"}}}},
{"adGroupCriterionOperation":{"create":{"adGroup":"customers/CID/adGroups/-3","status":"ENABLED","keyword":{"text":"googleads live check","matchType":"PHRASE"}}}},
{"adGroupAdOperation":{"create":{"adGroup":"customers/CID/adGroups/-3","status":"ENABLED","ad":{"finalUrls":["FINALURL"],"responsiveSearchAd":{"headlines":[{"text":"Live check headline one"},{"text":"Live check headline two"},{"text":"Live check headline three"}],"descriptions":[{"text":"First description for the googleads live check."},{"text":"Second description for the googleads live check."}]}}}}}
]}`

// TestLive runs against a real Google Ads account with the operator's real
// configuration and changes nothing: the campaign is sent with
// --validate-only, and the unpaused variant is refused before it leaves.
//
//	GOOGLE_ADS_LIVE=1 GOOGLE_ADS_LIVE_FINAL_URL=https://example.com go test ./e2e -run TestLive -v
func TestLive(t *testing.T) {
	if os.Getenv("GOOGLE_ADS_LIVE") != "1" {
		t.Skip("set GOOGLE_ADS_LIVE=1 with a configured key, customer and budget cap to run the live checks")
	}
	bin := buildBinary(t)
	env := os.Environ()

	out, errOut, code := run(t, bin, env, "", "auth", "status")
	var st struct {
		CustomerID      string `json:"customer_id"`
		LoginCustomerID string `json:"login_customer_id"`
		BudgetCap       string `json:"budget_cap"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || st.CustomerID == "" || st.BudgetCap == "" {
		t.Fatalf("configure a key, a customer and a budget cap first: %s %s", out, errOut)
	}

	out, errOut, code = run(t, bin, env, "", "list-accessible-customers")
	if code != 0 {
		t.Fatalf("list-accessible-customers: exit %d %s", code, errOut)
	}
	if !strings.Contains(out, "customers/"+st.CustomerID) {
		if st.LoginCustomerID == "" {
			t.Fatalf("the service account cannot reach customer %s directly: %s", st.CustomerID, out)
		}
		t.Logf("customer %s is reached through manager %s", st.CustomerID, st.LoginCustomerID)
	}

	out, errOut, code = run(t, bin, env, "", "search", "--query",
		"SELECT customer.descriptive_name, customer.currency_code, customer.time_zone FROM customer LIMIT 1")
	if code != 0 {
		t.Fatalf("search: exit %d %s", code, errOut)
	}
	t.Logf("account: %s", strings.TrimSpace(out))

	capMicros, err := config.ParseMoneyToMicros(st.BudgetCap)
	if err != nil {
		t.Fatal(err)
	}
	amount := min(int64(1_000_000), capMicros)
	payload := func(paused bool) string {
		status := ""
		if paused {
			status = `"status":"PAUSED",`
		}
		return strings.NewReplacer("CID", st.CustomerID, "NAME", time.Now().UTC().Format("20060102T150405"),
			"AMOUNT", strconv.FormatInt(amount, 10), "STATUS", status,
			"FINALURL", os.Getenv("GOOGLE_ADS_LIVE_FINAL_URL")).Replace(liveCampaign)
	}

	if os.Getenv("GOOGLE_ADS_LIVE_FINAL_URL") == "" {
		t.Log("GOOGLE_ADS_LIVE_FINAL_URL is not set: skipping the validate-only campaign check")
	} else {
		if _, errOut, code := run(t, bin, env, "", "mutate", "--validate-only", "--data", payload(true)); code != 0 {
			t.Fatalf("validate-only campaign: exit %d %s", code, errOut)
		}
	}

	_, errOut, code = run(t, bin, env, "", "mutate", "--data", payload(false))
	if code != 2 || !strings.Contains(errOut, "PAUSED") {
		t.Fatalf("an unpaused campaign must be refused locally: exit %d %s", code, errOut)
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./e2e/ -v -run TestSmoke && go test ./e2e/ -run TestLive -v && go vet ./...`
Expected: TestSmoke PASS; TestLive SKIP.

- [ ] **Step 5: Commit**

```bash
git add e2e
git commit -m "Add the end-to-end smoke test and the manual live check

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Documentation, CI and release

**Files:**
- Create: `README.md`, `SECURITY.md`, `docs/agents.md`, `.goreleaser.yaml`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`

**Interfaces:**
- Consumes: the finished CLI. Every command, flag, error kind and message quoted in the docs must match the code; check each against `googleads --help`, `googleads <cmd> --help` and the tests.
- Produces: the public documentation and the release pipeline.

- [ ] **Step 1: Write README.md**

Write `README.md` with these sections, in this order, filling each with the content listed. Follow the tone of `/Users/daniel/Development/bexio-cli/README.md` (plain, exact, no marketing) but no em dash characters.

1. **Title and summary.** `# googleads`, then: a command-line client for the Google Ads API, built for agents and scripts; JSON in, JSON out, one static binary; the command tree is generated from Google's Discovery document for API v25, so every method is reachable as a command; stdout carries the API response, every failure is one line of JSON on stderr; it keeps the two decisions that cost money (switching a campaign on, and budget amounts) with a person. Then the sentence: "googleads is not affiliated with or endorsed by Google."
2. **Install.** `go install github.com/wir-drei-digital/google-ads-cli/cmd/googleads@latest`, the release table (Linux amd64/arm64, macOS amd64/arm64, Windows amd64, `checksums.txt`).
3. **Setup (once).** The six operator steps from the spec's *Operator setup*, verbatim in substance: Cloud project not on the Free Trial, enable the Google Ads API; apply for access (Explorer for campaigns, Basic for Keyword Planner, brand verification; unused access may be revoked after 90 days); service account with no IAM roles and one JSON key per machine; add the service account in Google Ads under Admin, Access and security with Standard access; `googleads init` (or the `config set` commands); check older campaigns for the EU political declaration with `googleads search --query "SELECT campaign.id, campaign.name FROM campaign WHERE campaign.missing_eu_political_advertising_declaration = TRUE"`.
4. **Authentication.** `GOOGLE_ADS_JSON_KEY_FILE_PATH` versus `googleads config set service-account-key < key.json`; one-hour tokens cached in the user cache dir; no developer token (sunset 2026-09-09); `auth status` fields; `auth token`; revocation is deleting the key in the Cloud Console. Link SECURITY.md.
5. **Usage.** Examples, each a real command:
   - `googleads search --query "SELECT campaign.id, campaign.name, metrics.clicks, metrics.cost_micros FROM campaign WHERE segments.date DURING LAST_7_DAYS"` and the same with `--all`;
   - `googleads generate-keyword-ideas --data @ideas.json` with an example `ideas.json`: `{"language":"languageConstants/1001","geoTargetConstants":["geoTargetConstants/2756"],"keywordPlanNetwork":"GOOGLE_SEARCH","keywordSeed":{"keywords":["rapport automatisieren"]}}`;
   - building a paused Search campaign in one `googleads mutate --validate-only --data @campaign.json`, then without `--validate-only`; show a shortened `campaign.json` with temporary resource names, `"status":"PAUSED"`, `containsEuPoliticalAdvertising` and `explicitlyShared: false`;
   - switching it on: `googleads campaigns mutate --force --data '{"operations":[{"update":{"resourceName":"customers/1234567890/campaigns/123","status":"ENABLED"},"updateMask":"status"}]}'`;
   - `googleads schema Campaign`, `googleads campaigns mutate --help`, `googleads commands --json`, `googleads api GET v25/customers:listAccessibleCustomers`.
   State that int64 values are strings and money is in micros (1 unit = 1,000,000 micros).
6. **Guardrails.** The risk class table from the spec; the payload check rules 1 to 8; how the body is read (both spellings, duplicates refused, plain whole-number amounts); the budget cap (config file only, per budget not per account, Google may spend up to twice the daily budget on a day and bills at most 30.4 times it in a month); `--validate-only`; read-only mode; `--force` is a flag only; the `api` escape hatch rules; what is deliberately not gated; what the CLI cannot do.
7. **Blocked and skipped methods.** The three blocked methods with their reasons from `tools/genmanifest/overrides.json`, and the skiplisted `media.upload`.
8. **Errors and exit codes.** The error line keys, the kinds table (all twelve kinds, with `partial_failure` and `conflict` explained), the four setup hints, exit codes 0/1/2, the retry policy.
9. **API version policy.** Pinned to v25 (sunset about August 2027); a new version is `make update-spec VERSION=vNN`, `make generate`, reviewing new methods and changed classes (`go run ./tools/genmanifest -write-proposals`), `UPDATE_GOLDEN=1 go test ./tools/genmanifest` after reading the diff, and a release; an old release stops working when Google sunsets its version.
10. **Development.** `make build`, `make test`, `make generate`, `make update-spec`, `make check`; the smoke test and `GOOGLE_ADS_LIVE=1 GOOGLE_ADS_LIVE_FINAL_URL=https://your-site.example go test ./e2e -run TestLive -v`.
11. **License.** MIT, see LICENSE.

- [ ] **Step 2: Write SECURITY.md**

```markdown
# Security

## What a service-account key is

googleads authenticates with a Google Cloud service-account key: a JSON file holding a private key.
Whoever has the file can act as the service account, with the access it holds in Google Ads, from
any machine, until the key is deleted. The key does not expire on its own.

Limit what it can do:

- Add the service account in Google Ads with Standard access, not Admin. Standard access cannot
  manage users.
- Create one key per machine, so one machine can be cut off without touching the others.
- Delete a key in the Google Cloud Console (IAM & Admin, Service accounts, Keys) as soon as a machine
  no longer needs it. That is the only revocation: `googleads config unset service-account-key`
  removes the local copy and nothing else.

## Where the key lives

Either in the config file, stored with `googleads config set service-account-key < key.json` (read
from stdin, never from argv; the file is written `0600` inside a `0700` directory, and
`googleads config path` prints where), or in a file you point `GOOGLE_ADS_JSON_KEY_FILE_PATH` at,
which then wins. After storing the key, delete the downloaded copy. On Windows and on shared
machines the config file gets only the user-scoped protection of `%AppData%`.

Access tokens are cached for at most one hour in `<user cache dir>/googleads/token.json` (`0600`).

## What the CLI does not do

- It does not send credentials anywhere but `oauth2.googleapis.com` (the signed key assertion) and
  `googleads.googleapis.com` (the access token). Other hosts need `GOOGLE_ADS_ALLOW_CUSTOM_BASE=1`,
  which exists for tests and proxies, and plain HTTP is refused except on loopback. The `token_uri`
  inside a key file is ignored.
- It does not follow redirects, on either host.
- It does not take a key or token from argv, and `googleads api` cannot set `Authorization` or the
  credential query parameters `access_token`, `oauth_token` and `key`.
- It does not log credentials. `--verbose` logs method, path, status and response size.
- It does not phone home. The catalog is compiled into the binary.
- It prints a credential in exactly one place, on purpose: `googleads auth token`.

## Spending money

The guardrails in the README (payload check, budget cap, `--force`, read-only mode) are the CLI's
part. They cannot stop a caller with shell access from passing `--force` or editing the config file;
they make those decisions explicit and visible. Give agents rules of their own and keep the service
account's Google Ads access as narrow as the work allows.

## Reporting a vulnerability

Report security issues privately through GitHub Security Advisories on this repository (Security,
Report a vulnerability). Please do not open a public issue. Include `googleads version`, the steps
to reproduce and the impact you see.
```

- [ ] **Step 3: Write docs/agents.md**

````markdown
# googleads: agent reference

Paste the block below into your agent's instructions. It is the whole contract.

```markdown
# googleads (Google Ads API CLI): agent reference

Setup is a person's job. Never run `googleads init`, never change the budget cap
(`googleads config set budget-cap`), and never pass `--force` unless a person told you to,
in this conversation, for this exact call.

Credential and account
- `googleads auth status`: one line of JSON, offline, never a secret. `mode` is service_account or
  none; `missing` lists what blocks calls (service_account_key, valid_service_account_key,
  customer_id); `budget_cap` is the highest daily budget the CLI accepts, in the account currency.
- The customer comes from `--customer-id` (dashes allowed), else GOOGLE_ADS_CUSTOMER_ID or the config.

Discovery
- `googleads commands --json`: every command with id, method, path, risk, blocked state and flags.
- `googleads <command> --help`: fields, an example body and the risk class.
- `googleads schema <Name>`: fields of any type (Campaign, CampaignBudget, AdGroup, AdGroupAd,
  AdGroupCriterion, CampaignCriterion, Campaign_NetworkSettings, ResponsiveSearchAdInfo).
- Never guess a command path or a field name: read it from the catalog, the help or `schema`.

Reading
- `googleads search --query "<GAQL>"` or `--query @file.gaql`; `--all` merges every page into one
  array. GAQL names are snake_case: SELECT campaign.id, campaign.name, metrics.cost_micros
  FROM campaign WHERE segments.date DURING LAST_7_DAYS
- GAQL field metadata: googleads google-ads-fields search --data '{"query":"SELECT name, selectable, filterable WHERE name LIKE \"campaign.%\""}'
- Keyword ideas: `googleads generate-keyword-ideas --data @ideas.json` (needs Basic access).
- Responses are camelCase JSON. int64 values (ids, costMicros, amountMicros, clicks) are strings.
  Money is in micros: 1 unit of the account currency is 1,000,000 micros.

Writing
- Bodies are JSON: `--data '{...}'`, `--data @file.json` or `--data -`.
- Build a whole campaign in one `googleads mutate` with temporary resource names
  (customers/<id>/campaignBudgets/-1, customers/<id>/campaigns/-2) that later operations reference.
- Create campaigns with "status":"PAUSED" and
  "containsEuPoliticalAdvertising":"DOES_NOT_CONTAIN_EU_POLITICAL_ADVERTISING"; create budgets with
  "explicitlyShared":false.
- Validate first: `--validate-only` makes Google check the whole request and apply nothing. It
  runs without --force and in read-only mode.
- Classes: read and write run; delete, spend and admin need --force, which you do not pass on your
  own. spend means switching a campaign on, changing a budget amount, moving a campaign to another
  budget, promoting or scheduling experiments. Pausing always works without --force.
- A budget amount above the cap is refused even with --force. Tell the person; do not look for
  another way.
- Blocked commands (recommendations apply, recommendation auto-apply subscriptions, reservation
  booking) always refuse. Read recommendations with search and carry out a good one through mutate.

Errors
- stdout is the API response only. A failure is one line of JSON on stderr:
  {"kind","error","status","details","request_id"}. Exit 0 ok, 1 API or network error, 2 usage
  error, which includes every guardrail refusal.
- kinds: auth, forbidden, not_found, validation, rate_limited, server, transport, outcome_unknown,
  incomplete, usage, partial_failure, conflict.
- outcome_unknown: the change may or may not have landed; check with search before retrying.
- partial_failure (only with --partial-failure): stdout holds the full response; some operations failed.
- conflict: the same resource changed concurrently; retry once after a short wait.
- incomplete: --all hit --max-pages; the output is partial.
- The error text leads with Google's error code and field path, and may end with "hint: ..." naming
  the next step for the person (access level, account access, EU political declaration).
- Rate limits are retried automatically; do not add a retry loop of your own.
```

## Notes for the person wiring this up

- Give agents a read-only posture where writing is not their job: `GOOGLE_ADS_READ_ONLY=1` in their
  environment. `--validate-only` still works there.
- `--force` has no environment variable or config setting on purpose: every spend decision stays a
  visible flag in the transcript.
- Set a budget cap before any agent works on campaigns: `googleads config set budget-cap <amount>`.
- Regenerate the agent's command knowledge from `googleads commands --json` after an upgrade.
````

- [ ] **Step 4: Release and CI configuration**

`.goreleaser.yaml`:

```yaml
version: 2
project_name: googleads

builds:
  - main: ./cmd/googleads
    binary: googleads
    env: [CGO_ENABLED=0]
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ignore:
      - goos: windows
        goarch: arm64
    ldflags:
      - -s -w -X github.com/wir-drei-digital/google-ads-cli/internal/cli.Version={{.Version}}

archives:
  - formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]

checksum:
  name_template: checksums.txt
```

`.github/workflows/ci.yml`: copy `/Users/daniel/Development/bexio-cli/.github/workflows/ci.yml` and replace `./cmd/bexio` with `./cmd/googleads` in the three cross-compile lines. Keep the matrix, `go vet`, `go test ./...`, the manifest drift check and staticcheck.

`.github/workflows/release.yml`: copy `/Users/daniel/Development/bexio-cli/.github/workflows/release.yml` verbatim.

- [ ] **Step 5: Verify**

Run:

```bash
make check
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/googleads
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/googleads
grep -c "—" README.md SECURITY.md docs/agents.md
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```

Expected: `make check` passes; both builds succeed; the grep prints `0` for each file; staticcheck is silent (if it cannot be downloaded, say so in your report). Then read README.md once against the code: every command, flag, key and error kind it names must exist.

- [ ] **Step 6: Commit**

```bash
git add README.md SECURITY.md docs .goreleaser.yaml .github
git commit -m "Add README, SECURITY, agent reference, CI and release configuration

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## After Task 13 (controller, not a subagent task)

- Whole-branch review, then push `main` to `git@github.com:wir-drei-digital/google-ads-cli.git`.
- The live checks need the operator's Google setup (Cloud project, access level, service account, Google Ads access, `googleads init`). Run `GOOGLE_ADS_LIVE=1 GOOGLE_ADS_LIVE_FINAL_URL=<site> go test ./e2e -run TestLive -v` once that exists, and settle the spec's *Open questions* with what it shows.
- Tag `v0.1.0` only after the live checks pass.
