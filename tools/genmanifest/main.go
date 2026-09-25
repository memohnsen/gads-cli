package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// encode returns the manifest's canonical JSON and its gzipped form.
func encode(m *manifest.Manifest) (raw, gz []byte, err error) {
	raw, err = json.Marshal(m)
	if err != nil {
		return nil, nil, err
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, nil, err
	}
	if _, err := zw.Write(raw); err != nil {
		return nil, nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, nil, err
	}
	return raw, buf.Bytes(), nil
}

func main() {
	spec := flag.String("spec", "spec/googleads-v25.discovery.json", "path to the vendored Discovery document")
	overrides := flag.String("overrides", "tools/genmanifest/overrides.json", "path to the naming override table")
	classifications := flag.String("classifications", "tools/genmanifest/classifications.json", "path to the reviewed risk table")
	out := flag.String("out", "internal/manifest/manifest.json.gz", "path of the generated manifest")
	check := flag.Bool("check", false, "verify the committed manifest matches the spec instead of writing it")
	writeProposals := flag.Bool("write-proposals", false, "write classifications.proposed.json for review and exit")
	flag.Parse()

	m, proposals, err := Build(*spec, *overrides, *classifications)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(proposals) > 0 {
		if *writeProposals {
			merged, err := LoadClassifications(*classifications)
			if err != nil || merged == nil {
				merged = map[string]string{}
			}
			for _, p := range proposals {
				merged[p.ID] = p.Proposed
			}
			raw, err := json.MarshalIndent(merged, "", "  ")
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			// Sits beside the table it proposes for, so a run with a custom
			// -classifications path cannot quietly write somewhere else.
			dest := strings.TrimSuffix(*classifications, ".json") + ".proposed.json"
			if err := os.WriteFile(dest, append(raw, '\n'), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "wrote %d proposals to %s; review, then rename to classifications.json\n", len(proposals), dest)
		} else {
			for _, p := range proposals {
				fmt.Fprintf(os.Stderr, "unclassified: %-40s %s %s (proposed: %s)\n", p.ID, p.HTTPMethod, p.Path, p.Proposed)
			}
			fmt.Fprintln(os.Stderr, "run with -write-proposals, review, rename to classifications.json")
		}
		os.Exit(1)
	}

	rawJSON, gz, err := encode(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *check {
		existing, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "check:", err)
			os.Exit(1)
		}
		zr, err := gzip.NewReader(bytes.NewReader(existing))
		if err != nil {
			fmt.Fprintln(os.Stderr, "check:", err)
			os.Exit(1)
		}
		existingJSON, err := io.ReadAll(zr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "check:", err)
			os.Exit(1)
		}
		if !bytes.Equal(existingJSON, rawJSON) {
			fmt.Fprintln(os.Stderr, "manifest drift: run `make generate` and commit")
			os.Exit(1)
		}
		fmt.Println("manifest up to date")
		return
	}
	if err := os.WriteFile(*out, gz, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s: %d methods, %d bytes gz\n", *out, len(m.Methods), len(gz))
}
