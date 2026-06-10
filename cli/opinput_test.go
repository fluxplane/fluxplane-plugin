package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

func TestBuildInvokeInputArgs(t *testing.T) {
	// dotted nesting + JSON value inference, merged onto a base object.
	got, err := buildInvokeInput(`{"key":"DEV-1"}`, "", []string{"limit=5", "all=true", "fields.priority=High", "labels=[\"a\",\"b\"]"}, nil, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("decode: %v\n%s", err, got)
	}
	if m["key"] != "DEV-1" || m["limit"].(float64) != 5 || m["all"] != true {
		t.Fatalf("scalar/merge wrong: %#v", m)
	}
	if nested, _ := m["fields"].(map[string]any); nested["priority"] != "High" {
		t.Fatalf("dotted nesting wrong: %#v", m["fields"])
	}
	if labels, _ := m["labels"].([]any); len(labels) != 2 || labels[0] != "a" {
		t.Fatalf("array value wrong: %#v", m["labels"])
	}
}

func TestBuildInvokeInputStringFallbackAndErrors(t *testing.T) {
	got, _ := buildInvokeInput("", "", []string{"summary=hello world"}, nil, nil)
	if !strings.Contains(string(got), `"summary":"hello world"`) {
		t.Fatalf("string fallback = %s", got)
	}
	if _, err := buildInvokeInput("", "", []string{"noequals"}, nil, nil); err == nil {
		t.Fatal("expected error for malformed --arg")
	}
	if _, err := buildInvokeInput(`[1,2]`, "", []string{"a=b"}, nil, nil); err == nil {
		t.Fatal("expected error: --arg onto non-object base")
	}
}

func TestBuildInvokeInputSchemaCoercion(t *testing.T) {
	schema := &operationInputSchema{Properties: map[string]operationInputField{
		"page_id": {Type: "string"},
		"limit":   {Type: "integer"},
		"note":    {Type: []any{"string", "null"}},
		"mixed":   {Type: []any{"string", "integer"}},
		"fields": {Type: "object", Properties: map[string]operationInputField{
			"priority": {Type: "string"},
			"count":    {Type: "number"},
		}},
	}}
	got, err := buildInvokeInput("", "", []string{
		"page_id=33729",      // declared string: numeric text survives as string
		"limit=5",            // declared integer: heuristic number
		"note=42",            // ["string","null"]: still a declared string
		"mixed=7",            // ambiguous union: heuristic number
		"fields.priority=33", // nested declared string
		"fields.count=2",     // nested declared number
		"unknown=9",          // undeclared: heuristic number
		`quoted="007"`,       // undeclared but explicitly quoted: unquoted string
		`page_id2="33729"`,   // undeclared, quoted escape hatch
	}, nil, schema)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("decode: %v\n%s", err, got)
	}
	if m["page_id"] != "33729" || m["note"] != "42" {
		t.Fatalf("declared strings should stay strings: %#v", m)
	}
	if m["limit"].(float64) != 5 || m["mixed"].(float64) != 7 || m["unknown"].(float64) != 9 {
		t.Fatalf("non-string declarations should keep heuristic: %#v", m)
	}
	nested, _ := m["fields"].(map[string]any)
	if nested["priority"] != "33" || nested["count"].(float64) != 2 {
		t.Fatalf("nested coercion wrong: %#v", nested)
	}
	if m["quoted"] != "007" || m["page_id2"] != "33729" {
		t.Fatalf("quoted escape hatch wrong: %#v", m)
	}
}

func TestCoerceArgValueQuotedStringField(t *testing.T) {
	schema := &operationInputSchema{Properties: map[string]operationInputField{
		"page_id": {Type: "string"},
	}}
	// Explicit JSON quoting on a declared-string field unquotes once.
	if got := coerceArgValue(`"33729"`, schema, []string{"page_id"}); got != "33729" {
		t.Fatalf("quoted string field = %#v", got)
	}
	// Malformed quoting falls back to the raw text rather than failing.
	if got := coerceArgValue(`"unterminated`, schema, []string{"page_id"}); got != `"unterminated` {
		t.Fatalf("malformed quote = %#v", got)
	}
	// nil schema keeps the old heuristic.
	if got := coerceArgValue("33729", nil, []string{"page_id"}); got.(float64) != 33729 {
		t.Fatalf("nil schema heuristic = %#v", got)
	}
}

func TestBuildInvokeInputStdin(t *testing.T) {
	got, err := buildInvokeInput("-", "", nil, strings.NewReader(`{"key":"X"}`), nil)
	if err != nil || !strings.Contains(string(got), `"key":"X"`) {
		t.Fatalf("stdin = %s err=%v", got, err)
	}
	if _, err := buildInvokeInput("-", "", nil, strings.NewReader(`not json`), nil); err == nil {
		t.Fatal("expected error for invalid stdin JSON")
	}
}

func TestInvokeArgFlagReachesBackend(t *testing.T) {
	backend := &fakeBackend{}
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--no-validate", "--arg", "key=DEV-1", "--arg", "limit=5"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var in map[string]any
	if err := json.Unmarshal(backend.invoked.Input, &in); err != nil {
		t.Fatalf("decode input: %v", err)
	}
	if in["key"] != "DEV-1" || in["limit"].(float64) != 5 {
		t.Fatalf("backend received %#v", in)
	}
}

func TestInvokeArgCoercesToDeclaredString(t *testing.T) {
	spec := sdkmanifest.OperationSpec{
		Name:  "demo.page.get",
		Input: json.RawMessage(`{"type":"object","required":["page_id"],"properties":{"page_id":{"type":"string"},"limit":{"type":"integer"}}}`),
	}
	for _, extraFlags := range [][]string{nil, {"--no-validate"}} {
		backend := &fakeBackend{listOpsFn: func(req management.OperationListRequest) (management.OperationListResult, error) {
			return management.OperationListResult{Plugin: req.Ref, Operations: []sdkmanifest.OperationSpec{spec}}, nil
		}}
		cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}})
		cmd.SetArgs(append([]string{"operation", "invoke", "demo", "demo.page.get", "--arg", "page_id=33729", "--arg", "limit=5"}, extraFlags...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute (%v): %v", extraFlags, err)
		}
		var in map[string]any
		if err := json.Unmarshal(backend.invoked.Input, &in); err != nil {
			t.Fatalf("decode input: %v", err)
		}
		if in["page_id"] != "33729" {
			t.Fatalf("page_id should reach the backend as a string (%v): %#v", extraFlags, in)
		}
		if in["limit"].(float64) != 5 {
			t.Fatalf("limit should stay numeric (%v): %#v", extraFlags, in)
		}
	}
}

func TestInvokeStructuredErrorRendered(t *testing.T) {
	backend := &fakeBackend{invokeFn: func(req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
		return management.OperationInvokeResult{}, &management.OperationFailure{
			Plugin: "demo", Operation: "demo.do",
			Err: protocol.Error{Code: "bad_request", Message: "nope", Fields: map[string]string{"parent": "missing"}},
		}
	}}
	var errBuf bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}, Err: &errBuf})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--no-validate", "--input", `{"x":1}`})
	err := cmd.Execute()
	if !errors.Is(err, ErrReported) {
		t.Fatalf("expected ErrReported, got %v", err)
	}
	// Agent can parse code + field-level detail from the structured stderr JSON.
	var payload struct {
		Error protocol.Error `json:"error"`
	}
	if jerr := json.Unmarshal(errBuf.Bytes(), &payload); jerr != nil {
		t.Fatalf("structured error not JSON: %v\n%s", jerr, errBuf.String())
	}
	if payload.Error.Code != "bad_request" || payload.Error.Fields["parent"] != "missing" {
		t.Fatalf("structured error = %#v", payload.Error)
	}
}

func TestRedactInputForDisplay(t *testing.T) {
	in := json.RawMessage(`{"api_token":"sek","issue_key":"DEV-1","nested":{"password":"p","parent_key":"DEV-2"}}`)
	out := string(redactInputForDisplay(in))
	if strings.Contains(out, "sek") || strings.Contains(out, `"password":"p"`) {
		t.Fatalf("secrets not redacted: %s", out)
	}
	if !strings.Contains(out, "DEV-1") || !strings.Contains(out, "DEV-2") {
		t.Fatalf("non-secret keys (issue_key/parent_key) should survive: %s", out)
	}
}

func TestInvokeStrictFieldMissing(t *testing.T) {
	backend := &fakeBackend{invokeFn: func(req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
		return management.OperationInvokeResult{Result: json.RawMessage(`{"ok":true}`)}, nil
	}}
	// Without --strict: missing field is reported but exit is clean.
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--no-validate", "--field", "nope"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("non-strict missing field should not error: %v", err)
	}
	// With --strict: missing field -> ErrReported (non-zero).
	cmd = New(Options{Backend: backend, Out: &bytes.Buffer{}})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--no-validate", "--field", "nope", "--strict"})
	if err := cmd.Execute(); !errors.Is(err, ErrReported) {
		t.Fatalf("--strict missing field should return ErrReported, got %v", err)
	}
}
