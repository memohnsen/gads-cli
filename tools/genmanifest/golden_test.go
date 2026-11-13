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
	m, _, err := Build(mustVendoredSpec(t), "overrides.json", "classifications.json")
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
