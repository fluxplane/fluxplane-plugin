package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

func TestBuildInvokeInputArgs(t *testing.T) {
	// dotted nesting + JSON value inference, merged onto a base object.
	got, err := buildInvokeInput(`{"key":"DEV-1"}`, "", []string{"limit=5", "all=true", "fields.priority=High", "labels=[\"a\",\"b\"]"}, nil)
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
	got, _ := buildInvokeInput("", "", []string{"summary=hello world"}, nil)
	if !strings.Contains(string(got), `"summary":"hello world"`) {
		t.Fatalf("string fallback = %s", got)
	}
	if _, err := buildInvokeInput("", "", []string{"noequals"}, nil); err == nil {
		t.Fatal("expected error for malformed --arg")
	}
	if _, err := buildInvokeInput(`[1,2]`, "", []string{"a=b"}, nil); err == nil {
		t.Fatal("expected error: --arg onto non-object base")
	}
}

func TestBuildInvokeInputStdin(t *testing.T) {
	got, err := buildInvokeInput("-", "", nil, strings.NewReader(`{"key":"X"}`))
	if err != nil || !strings.Contains(string(got), `"key":"X"`) {
		t.Fatalf("stdin = %s err=%v", got, err)
	}
	if _, err := buildInvokeInput("-", "", nil, strings.NewReader(`not json`)); err == nil {
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
