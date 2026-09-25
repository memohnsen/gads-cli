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
