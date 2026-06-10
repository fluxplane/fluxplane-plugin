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

func TestDescribeOperationOutputSummary(t *testing.T) {
	spec := sdkmanifest.OperationSpec{
		Name:  "demo.page.list",
		Input: json.RawMessage(`{"required":["space"],"properties":{"space":{"type":"string"}}}`),
		Output: json.RawMessage(`{"properties":{
			"items":{"type":"array","description":"Pages","items":{"type":"object","properties":{
				"id":{"type":"string"},
				"labels":{"type":"array","items":{"type":"string"}},
				"nested":{"type":"object","properties":{"deep":{"type":"string"}}}
			}}},
			"count":{"type":"integer"},
			"total":{"type":"integer"},
			"has_more":{"type":"boolean"},
			"next_page_token":{"type":"string"}
		}}`),
	}
	d := describeOperation("demo", spec)
	if len(d.OutputFields) != 5 {
		t.Fatalf("output fields = %#v", d.OutputFields)
	}
	byName := map[string]operationOutputFieldSummary{}
	for _, f := range d.OutputFields {
		byName[f.Name] = f
	}
	items := byName["items"]
	if items.Type != "array" || items.Items != "object" || len(items.Fields) != 3 {
		t.Fatalf("items summary = %#v", items)
	}
	var nested operationOutputFieldSummary
	for _, child := range items.Fields {
		if child.Name == "nested" {
			nested = child
		}
		if child.Name == "labels" && (child.Type != "array" || child.Items != "string") {
			t.Fatalf("labels child = %#v", child)
		}
	}
	// exactly one nesting level: nested object shown without its own children
	if nested.Type != "object" || len(nested.Fields) != 0 {
		t.Fatalf("nested child should not recurse: %#v", nested)
	}
	if strings.Join(d.Pagination, ",") != "has_more,next_page_token,total" {
		t.Fatalf("pagination = %v", d.Pagination)
	}

	// text rendering shows the output block and the pagination line
	backend := describeBackend(spec)
	var text bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &text})
	cmd.SetArgs([]string{"operation", "describe", "demo", "demo.page.list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("describe: %v", err)
	}
	for _, want := range []string{"output:", "items  array[object]", "paginates: has_more, next_page_token, total"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("text output missing %q:\n%s", want, text.String())
		}
	}
}

func TestSummarizeOperationOutputEdgeCases(t *testing.T) {
	// total alone is just a count, not a pagination signal
	_, pagination := summarizeOperationOutput(json.RawMessage(`{"properties":{"total":{"type":"integer"}}}`))
	if pagination != nil {
		t.Fatalf("bare total should not signal pagination: %v", pagination)
	}
	// truncated alone is a signal
	_, pagination = summarizeOperationOutput(json.RawMessage(`{"properties":{"rows":{"type":"array"},"truncated":{"type":"boolean"}}}`))
	if strings.Join(pagination, ",") != "truncated" {
		t.Fatalf("truncated signal = %v", pagination)
	}
	// unparseable / empty schemas degrade to nil
	if fields, _ := summarizeOperationOutput(nil); fields != nil {
		t.Fatalf("nil schema = %#v", fields)
	}
	if fields, _ := summarizeOperationOutput(json.RawMessage(`{"$ref":"#/defs/x"}`)); fields != nil {
		t.Fatalf("ref schema = %#v", fields)
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
