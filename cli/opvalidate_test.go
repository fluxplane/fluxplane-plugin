package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

func schemaFromJSON(s string) operationInputSchema {
	return parseOperationInputSchema(sdkmanifest.OperationSpec{Input: json.RawMessage(s)})
}

func TestValidateOperationInputRules(t *testing.T) {
	closed := schemaFromJSON(`{"required":["key"],"additionalProperties":false,"properties":{"key":{"type":"string"},"order":{"type":"string","enum":["created","-created"]}}}`)

	// missing required
	if p := validateOperationInput(closed, json.RawMessage(`{"order":"created"}`)); len(p) != 1 || p[0].Field != "key" {
		t.Fatalf("missing-required = %#v", p)
	}
	// enum violation
	if p := validateOperationInput(closed, json.RawMessage(`{"key":"X","order":"sideways"}`)); len(p) != 1 || p[0].Field != "order" {
		t.Fatalf("enum = %#v", p)
	}
	// unknown key (closed schema)
	if p := validateOperationInput(closed, json.RawMessage(`{"key":"X","bogus":1}`)); len(p) != 1 || p[0].Field != "bogus" {
		t.Fatalf("unknown-key = %#v", p)
	}
	// all good
	if p := validateOperationInput(closed, json.RawMessage(`{"key":"X","order":"created"}`)); len(p) != 0 {
		t.Fatalf("valid input flagged: %#v", p)
	}
	// open schema: unknown key NOT flagged
	open := schemaFromJSON(`{"required":["key"],"properties":{"key":{"type":"string"}}}`)
	if p := validateOperationInput(open, json.RawMessage(`{"key":"X","extra":1}`)); len(p) != 0 {
		t.Fatalf("open schema should not flag unknown key: %#v", p)
	}
	// empty / non-object payloads: skipped
	if p := validateOperationInput(closed, json.RawMessage(``)); p != nil {
		t.Fatalf("empty payload should skip: %#v", p)
	}
	if p := validateOperationInput(closed, json.RawMessage(`["x"]`)); p != nil {
		t.Fatalf("array payload should skip: %#v", p)
	}
}

func TestValidateEnumNumberAsFloat(t *testing.T) {
	schema := schemaFromJSON(`{"properties":{"level":{"type":"integer","enum":[1,2,3]}}}`)
	if p := validateOperationInput(schema, json.RawMessage(`{"level":2}`)); len(p) != 0 {
		t.Fatalf("valid numeric enum flagged: %#v", p)
	}
	if p := validateOperationInput(schema, json.RawMessage(`{"level":9}`)); len(p) != 1 {
		t.Fatalf("invalid numeric enum not flagged: %#v", p)
	}
}

func TestValidateExamplesSuppressMissingButNotUnknown(t *testing.T) {
	// An op with a one-of shape declares an example: missing-required must be
	// suppressed (the flat required list cannot express one-of), but unknown
	// keys are never valid under additionalProperties:false — a typo'd field
	// name must be flagged uniformly whether or not examples exist.
	schema := schemaFromJSON(`{
		"required":["key","transition_id"],
		"additionalProperties":false,
		"properties":{"key":{"type":"string"},"transition_id":{"type":"string"},"mode":{"type":"string","enum":["a","b"]}},
		"examples":[{"key":"DEV-1","transition_id":"171"}]
	}`)
	// missing transition_id is suppressed; the unknown key still flags.
	p := validateOperationInput(schema, json.RawMessage(`{"key":"DEV-1","other":1}`))
	if len(p) != 1 || p[0].Field != "other" || !strings.Contains(p[0].Reason, "unknown field") {
		t.Fatalf("want exactly the unknown-field problem: %#v", p)
	}
	// missing-required alone stays suppressed under examples.
	if p := validateOperationInput(schema, json.RawMessage(`{"key":"DEV-1"}`)); len(p) != 0 {
		t.Fatalf("examples should suppress missing-required: %#v", p)
	}
	// enum still enforced
	if p := validateOperationInput(schema, json.RawMessage(`{"key":"DEV-1","mode":"z"}`)); len(p) != 1 || p[0].Field != "mode" {
		t.Fatalf("enum should still be enforced with examples: %#v", p)
	}
}

// invokeValidationBackend serves a schema for one op and counts invocations.
func invokeValidationBackend(inputSchema string) *fakeBackend {
	return &fakeBackend{
		listOpsFn: func(req management.OperationListRequest) (management.OperationListResult, error) {
			return management.OperationListResult{Operations: []sdkmanifest.OperationSpec{{Name: "demo.do", Input: json.RawMessage(inputSchema)}}}, nil
		},
	}
}

func TestInvokeDryRunDoesNotInvoke(t *testing.T) {
	backend := invokeValidationBackend(`{"required":["key"],"properties":{"key":{"type":"string"}}}`)
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--input", `{}`, "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dry-run returned error: %v", err)
	}
	if backend.invokeCount != 0 {
		t.Fatalf("dry-run must not invoke; count=%d", backend.invokeCount)
	}
	var r operationDryRunResult
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if r.Valid || len(r.Problems) != 1 {
		t.Fatalf("dry-run result = %#v", r)
	}
}

func TestInvokeFailsFastOnInvalidInput(t *testing.T) {
	backend := invokeValidationBackend(`{"required":["key"],"properties":{"key":{"type":"string"}}}`)
	var errBuf bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}, Err: &errBuf})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--input", `{}`})
	err := cmd.Execute()
	if !errors.Is(err, ErrReported) {
		t.Fatalf("expected ErrReported, got %v", err)
	}
	if backend.invokeCount != 0 {
		t.Fatalf("invalid input must not reach the backend; count=%d", backend.invokeCount)
	}
	// The structured validation error is written for the agent to parse.
	if !strings.Contains(errBuf.String(), "invalid_input") || !strings.Contains(errBuf.String(), `"key"`) {
		t.Fatalf("expected structured validation error, got %s", errBuf.String())
	}
}

func TestInvokeNoValidateBypasses(t *testing.T) {
	backend := invokeValidationBackend(`{"required":["key"],"properties":{"key":{"type":"string"}}}`)
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--input", `{}`, "--no-validate"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("no-validate should invoke: %v", err)
	}
	if backend.invokeCount != 1 {
		t.Fatalf("--no-validate should reach the backend; count=%d", backend.invokeCount)
	}
}

func TestInvokeProceedsWhenSchemaUnavailable(t *testing.T) {
	// Default fakeBackend.ListOperations returns no ops -> schema not found ->
	// validation skipped, invoke proceeds.
	backend := &fakeBackend{}
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--input", `{"anything":true}`})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("invoke should proceed when schema unavailable: %v", err)
	}
	if backend.invokeCount != 1 {
		t.Fatalf("expected invoke; count=%d", backend.invokeCount)
	}
}
