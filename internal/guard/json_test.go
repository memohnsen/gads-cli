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
