package pluginbinding

import (
	"encoding/json"
	"testing"

	manifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

type helloInput struct {
	Name  string `json:"name" jsonschema:"required,description=Name to greet"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Optional limit"`
}

type helloOutput struct {
	Message string `json:"message"`
	Count   int    `json:"count"`
}

func TestPluginOperationCallDecodesTypedInput(t *testing.T) {
	plugin := newTestPlugin()
	call := operationCall(t, "test.hello", map[string]any{"name": "dex"})
	resp := plugin.Handle(request(t, protocol.CommandOperationsCall, call))
	if !resp.OK {
		t.Fatalf("operation failed: %#v", resp.Error)
	}
	var out helloOutput
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.Message != "hello dex" || out.Count != 1 {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestPluginOperationBatchReusesCache(t *testing.T) {
	plugin := newTestPlugin()
	resp := plugin.Handle(request(t, protocol.CommandOperationsBatch, protocol.OperationBatch{Calls: []protocol.OperationCall{
		operationCall(t, "test.hello", map[string]any{"name": "one"}),
		operationCall(t, "test.hello", map[string]any{"name": "two"}),
	}}))
	if !resp.OK {
		t.Fatalf("batch failed: %#v", resp.Error)
	}
	var out protocol.OperationBatchResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("results = %#v", out.Results)
	}
	var first, second helloOutput
	if err := json.Unmarshal(out.Results[0].Result, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Results[1].Result, &second); err != nil {
		t.Fatal(err)
	}
	if first.Count != 1 || second.Count != 2 {
		t.Fatalf("cache counts = %d, %d", first.Count, second.Count)
	}
}

func TestPluginOperationBadInputReturnsProtocolError(t *testing.T) {
	plugin := newTestPlugin()
	resp := plugin.Handle(request(t, protocol.CommandOperationsCall, operationCall(t, "test.hello", map[string]any{"name": 123})))
	if resp.OK || resp.Error == nil || resp.Error.Code != "bad_input" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestPluginContextBadPayloadReturnsProtocolError(t *testing.T) {
	plugin := Define(ManifestSpec{Name: "test"},
		RegisterContextProvider(ContextSpec("test.context", "Test.", ContextKindText), func(Context, ContextBuildInput) (ContextBuildResult, error) {
			return ContextBuildResult{}, nil
		}),
	)
	resp := plugin.Handle(protocol.Request{Command: protocol.CommandContextBuild, Plugin: "test", Payload: []byte(`{"limit":"bad"}`)})
	if resp.OK || resp.Error == nil || resp.Error.Code != "bad_payload" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestPluginManifestIncludesGeneratedInputSchema(t *testing.T) {
	plugin := newTestPlugin()
	resp := plugin.Handle(request(t, protocol.CommandManifest, nil))
	if !resp.OK {
		t.Fatalf("manifest failed: %#v", resp.Error)
	}
	var manifest manifest.PluginManifest
	if err := json.Unmarshal(resp.Result, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("operations = %#v", manifest.Operations)
	}
	if manifest.Metadata[ManifestProtocolKey] != "" {
		t.Fatalf("protocol metadata should be explicit opt-in: %#v", manifest.Metadata)
	}
	var schema struct {
		Type       string   `json:"type"`
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(manifest.Operations[0].Input, &schema); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(manifest.Operations[0].Input, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["$schema"]; ok {
		t.Fatalf("schema should be normalized without draft marker: %#v", raw)
	}
	if schema.Type != "object" || len(schema.Required) != 1 || schema.Required[0] != "name" {
		t.Fatalf("schema required = %#v", schema)
	}
	if schema.Properties["name"].Type != "string" || schema.Properties["name"].Description != "Name to greet" {
		t.Fatalf("schema properties = %#v", schema.Properties)
	}
	if schema.Properties["endpoint_ref"].Type != "string" {
		t.Fatalf("schema missing universal endpoint_ref: %#v", schema.Properties)
	}
}

func newTestPlugin() *Plugin {
	plugin := New(manifest.PluginManifest{Name: "test"})
	Operation(plugin, manifest.OperationSpec{Name: "test.hello"}, func(ctx Context, input helloInput) (helloOutput, error) {
		count := 1
		if raw, ok := ctx.Cache.Get("count"); ok {
			count = raw.(int) + 1
		}
		ctx.Cache.Set("count", count)
		return helloOutput{Message: "hello " + input.Name, Count: count}, nil
	})
	return plugin
}

func request(t *testing.T, command string, payload any) protocol.Request {
	t.Helper()
	req, err := protocol.NewRequest(command, "test", payload)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func operationCall(t *testing.T, name string, input map[string]any) protocol.OperationCall {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.OperationCall{Name: name, Input: raw}
}
