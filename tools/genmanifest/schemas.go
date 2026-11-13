package main

import (
	"strings"

	"github.com/memohnsen/gads-cli/internal/manifest"
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
		doc := stripMarkers(desc)
		lead := strings.TrimSuffix(desc, doc) // all stacked markers, "Required. Immutable." say
		f.ReadOnly = p.ReadOnly || strings.Contains(lead, "Output only.")
		f.Required = strings.Contains(lead, "Required.")
		f.Immutable = strings.Contains(lead, "Immutable.")
		f.Doc = firstSentence(doc, 200)
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
