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
