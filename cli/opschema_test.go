package cli

import (
	"encoding/json"
	"strings"
	"testing"

	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

func opSpec(inputSchema string) sdkmanifest.OperationSpec {
	return sdkmanifest.OperationSpec{Input: json.RawMessage(inputSchema)}
}

func TestParseOperationInputSchemaAdditionalProperties(t *testing.T) {
	closed := parseOperationInputSchema(opSpec(`{"properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`))
	if !closed.closedTopLevel() {
		t.Fatal("additionalProperties:false should be closed")
	}
	open := parseOperationInputSchema(opSpec(`{"properties":{"key":{"type":"string"}}}`))
	if open.closedTopLevel() {
		t.Fatal("unspecified additionalProperties should not be closed")
	}
	// Regression: additionalProperties as an OBJECT must not blank the schema.
	obj := parseOperationInputSchema(opSpec(`{"properties":{"key":{"type":"string"},"extra":{"type":"number"}},"additionalProperties":{"type":"string"}}`))
	if obj.closedTopLevel() {
		t.Fatal("object additionalProperties should not be treated as closed")
	}
	if len(obj.Properties) != 2 {
		t.Fatalf("object additionalProperties blanked the schema: %#v", obj.Properties)
	}
}

func TestSummarizeOperationInputOrderingAndEnum(t *testing.T) {
	schema := parseOperationInputSchema(opSpec(`{
		"required":["key"],
		"properties":{
			"key":{"type":"string","description":"Issue key"},
			"order":{"type":"string","enum":["created","-created"],"description":"Sort order"},
			"limit":{"type":"integer"}
		}
	}`))
	fields := summarizeOperationInput(schema)
	if len(fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(fields))
	}
	// required first
	if fields[0].Name != "key" || !fields[0].Required {
		t.Fatalf("first field should be required key, got %#v", fields[0])
	}
	// rest sorted: limit before order
	if fields[1].Name != "limit" || fields[2].Name != "order" {
		t.Fatalf("non-required fields should be sorted: %v", []string{fields[1].Name, fields[2].Name})
	}
	if fields[2].Type != "string" || len(fields[2].Enum) != 2 {
		t.Fatalf("enum field not summarized: %#v", fields[2])
	}
}

func TestSampleInputJSONStillWorksAfterExtraction(t *testing.T) {
	schema := parseOperationInputSchema(opSpec(`{"required":["project_key","summary"],"properties":{"project_key":{"type":"string"},"summary":{"type":"string"},"endpoint_ref":{"type":"string"}}}`))
	got := sampleInputJSON(schema)
	for _, want := range []string{`"project_key"`, `"summary"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("sample = %s, missing %s", got, want)
		}
	}
	if strings.Contains(got, "endpoint_ref") {
		t.Fatalf("sample = %s, endpoint_ref must not be auto-injected", got)
	}
}
