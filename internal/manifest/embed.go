package manifest

import _ "embed"

//go:embed manifest.json.gz
var embedded []byte

// Load parses the manifest embedded in this binary.
func Load() (*Manifest, error) { return Parse(embedded) }
