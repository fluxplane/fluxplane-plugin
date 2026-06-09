package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

func transitionRunSpec() sdkmanifest.OperationSpec {
	return sdkmanifest.OperationSpec{
		Name:        "jira.issue.transition.run",
		Description: "Run a Jira issue transition.",
		Risk:        "medium",
		Idempotency: "non_idempotent",
		Effects:     []sdkmanifest.OperationEffect{"write", "network"},
		Input: json.RawMessage(`{
			"required":["key"],
			"properties":{
				"key":{"type":"string","description":"Issue key"},
				"transition_id":{"type":"string","description":"Transition ID"},
				"order":{"type":"string","enum":["created","-created"]}
			}
		}`),
		Output: json.RawMessage(`{"properties":{"ok":{"type":"boolean"},"issue_key":{"type":"string"}}}`),
	}
}

func TestDescribeOperationShape(t *testing.T) {
	d := describeOperation("jira", transitionRunSpec())
	if d.Plugin != "jira" || d.Name != "jira.issue.transition.run" {
		t.Fatalf("header = %#v", d)
	}
	if d.Risk != "medium" || d.Idempotency != "non_idempotent" || len(d.Effects) != 2 {
		t.Fatalf("metadata = %#v", d)
	}
	if len(d.Fields) != 3 || d.Fields[0].Name != "key" || !d.Fields[0].Required {
		t.Fatalf("fields = %#v", d.Fields)
	}
	// non-required sorted: order before transition_id
	if d.Fields[1].Name != "order" || d.Fields[2].Name != "transition_id" {
		t.Fatalf("field order = %v", []string{d.Fields[1].Name, d.Fields[2].Name})
	}
	if len(d.Fields[1].Enum) != 2 {
		t.Fatalf("enum not captured: %#v", d.Fields[1])
	}
	if !strings.Contains(d.Example, "operation invoke jira jira.issue.transition.run") {
		t.Fatalf("example = %q", d.Example)
	}
	if len(d.OutputKeys) != 2 {
		t.Fatalf("output keys = %v", d.OutputKeys)
	}
}

func describeBackend(ops ...sdkmanifest.OperationSpec) *fakeBackend {
	return &fakeBackend{listOpsFn: func(req management.OperationListRequest) (management.OperationListResult, error) {
		return management.OperationListResult{Plugin: req.Ref, Operations: ops}, nil
	}}
}

func TestOperationDescribeTextAndJSON(t *testing.T) {
	backend := describeBackend(transitionRunSpec())

	var text bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &text})
	cmd.SetArgs([]string{"operation", "describe", "jira", "jira.issue.transition.run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("describe text: %v", err)
	}
	for _, want := range []string{"key", "required", "transition_id", "enum: created, -created", "example:"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("text output missing %q:\n%s", want, text.String())
		}
	}

	var jsonOut bytes.Buffer
	cmd = New(Options{Backend: backend, Out: &jsonOut})
	cmd.SetArgs([]string{"operation", "describe", "jira", "jira.issue.transition.run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("describe json: %v", err)
	}
	var d operationDescription
	if err := json.Unmarshal(jsonOut.Bytes(), &d); err != nil {
		t.Fatalf("decode: %v\n%s", err, jsonOut.String())
	}
	if d.Name != "jira.issue.transition.run" || len(d.Fields) != 3 {
		t.Fatalf("json = %#v", d)
	}
}

func TestOperationDescribeNotFound(t *testing.T) {
	backend := describeBackend(transitionRunSpec())
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}})
	cmd.SetArgs([]string{"operation", "describe", "jira", "jira.nope"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}
