package cli

import (
	"strings"
	"testing"
)

func TestSampleInputJSONPrefersDeclaredExample(t *testing.T) {
	// A one-of operation: no single field is required, but a declared example
	// gives a runnable invocation that a flat required list cannot express.
	schema := operationInputSchema{
		Properties: map[string]operationInputField{
			"key":           {Type: "string"},
			"transition_id": {Type: "string"},
		},
		Examples: []map[string]any{{"key": "DEV-1", "transition_id": "171"}},
	}
	got := sampleInputJSON(schema)
	if !strings.Contains(got, `"key":"DEV-1"`) || !strings.Contains(got, `"transition_id":"171"`) {
		t.Fatalf("sample should use declared example, got %s", got)
	}
}

func TestSampleInputJSONFallsBackToRequired(t *testing.T) {
	schema := operationInputSchema{
		Required: []string{"project_key", "summary"},
		Properties: map[string]operationInputField{
			"project_key":  {Type: "string"},
			"summary":      {Type: "string"},
			"endpoint_ref": {Type: "string"},
		},
	}
	got := sampleInputJSON(schema)
	for _, want := range []string{`"project_key"`, `"summary"`, `"endpoint_ref"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("sample = %s, missing %s", got, want)
		}
	}
}
