package plugintest

import (
	"encoding/json"
	"strings"
	"testing"

	manifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/pluginbinding"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

type runConfig struct {
	req  protocol.Request
	host pluginbinding.HostClient
}

type RunOption func(*runConfig)

func WithInstance(instance string) RunOption {
	return func(cfg *runConfig) {
		cfg.req.Instance = instance
	}
}

func WithGrant(grant string) RunOption {
	return func(cfg *runConfig) {
		cfg.req.Grant = grant
	}
}

func WithRequest(request protocol.Request) RunOption {
	return func(cfg *runConfig) {
		cfg.req = request
	}
}

func WithHost(host pluginbinding.HostClient) RunOption {
	return func(cfg *runConfig) {
		cfg.host = host
	}
}

func Call(t *testing.T, name string, input any) protocol.OperationCall {
	t.Helper()
	var raw json.RawMessage
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatalf("marshal operation input: %v", err)
		}
		raw = data
	}
	return protocol.OperationCall{Name: name, Input: raw}
}

func Run(t *testing.T, plugin *pluginbinding.Plugin, name string, input any, options ...RunOption) protocol.OperationResult {
	t.Helper()
	cfg := runConfig{req: protocol.Request{Instance: "default"}}
	if plugin != nil {
		cfg.req.Plugin = plugin.Manifest().Name
	}
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}
	return plugin.RunOperationWithHost(cfg.req, Call(t, name, input), pluginbinding.NewCache(), cfg.host)
}

func RunOK[T any](t *testing.T, plugin *pluginbinding.Plugin, name string, input any, options ...RunOption) T {
	t.Helper()
	result := Run(t, plugin, name, input, options...)
	if !result.OK {
		t.Fatalf("operation failed: %#v", result.Error)
	}
	var out T
	if err := json.Unmarshal(result.Result, &out); err != nil {
		t.Fatalf("decode operation result: %v\n%s", err, string(result.Result))
	}
	return out
}

func RunError(t *testing.T, plugin *pluginbinding.Plugin, name string, input any, options ...RunOption) *protocol.Error {
	t.Helper()
	result := Run(t, plugin, name, input, options...)
	if result.OK {
		t.Fatalf("expected operation failure")
	}
	if result.Error == nil {
		t.Fatalf("operation failed without error")
	}
	return result.Error
}

func Datasource(t *testing.T, plugin *pluginbinding.Plugin, command string, input any, options ...RunOption) protocol.Response {
	t.Helper()
	cfg := runConfig{req: protocol.Request{Command: command, Instance: "default"}}
	if plugin != nil {
		cfg.req.Plugin = plugin.Manifest().Name
	}
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatalf("marshal datasource input: %v", err)
		}
		cfg.req.Payload = data
	}
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}
	return plugin.HandleWithHost(cfg.req, cfg.host)
}

func DatasourceOK[T any](t *testing.T, plugin *pluginbinding.Plugin, command string, input any, options ...RunOption) T {
	t.Helper()
	resp := Datasource(t, plugin, command, input, options...)
	if !resp.OK {
		t.Fatalf("datasource command failed: %#v", resp.Error)
	}
	var out T
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("decode datasource result: %v\n%s", err, string(resp.Result))
	}
	return out
}

func DatasourceError(t *testing.T, plugin *pluginbinding.Plugin, command string, input any, options ...RunOption) *protocol.Error {
	t.Helper()
	resp := Datasource(t, plugin, command, input, options...)
	if resp.OK {
		t.Fatalf("expected datasource command failure")
	}
	if resp.Error == nil {
		t.Fatalf("datasource command failed without error")
	}
	return resp.Error
}

func DatasourceSearchOK[T any](t *testing.T, plugin *pluginbinding.Plugin, input any, options ...RunOption) T {
	t.Helper()
	return DatasourceOK[T](t, plugin, protocol.CommandDatasourcesSearch, input, options...)
}

func DatasourceSearchError(t *testing.T, plugin *pluginbinding.Plugin, input any, options ...RunOption) *protocol.Error {
	t.Helper()
	return DatasourceError(t, plugin, protocol.CommandDatasourcesSearch, input, options...)
}

func DatasourceLookupOK[T any](t *testing.T, plugin *pluginbinding.Plugin, input any, options ...RunOption) T {
	t.Helper()
	return DatasourceOK[T](t, plugin, protocol.CommandDatasourcesLookup, input, options...)
}

func DatasourceGetOK[T any](t *testing.T, plugin *pluginbinding.Plugin, input any, options ...RunOption) T {
	t.Helper()
	return DatasourceOK[T](t, plugin, protocol.CommandDatasourcesGet, input, options...)
}

func AssertManifestQuality(t *testing.T, manifest manifest.PluginManifest) {
	t.Helper()
	authFields := declaredAuthFields(manifest)
	indexes := declaredIndexes(manifest)
	operations := map[string]bool{}
	for _, operation := range manifest.Operations {
		if operation.Name == "" {
			t.Fatalf("operation without name: %#v", operation)
		}
		if operations[operation.Name] {
			t.Fatalf("duplicate operation %q", operation.Name)
		}
		operations[operation.Name] = true
		assertJSONSchema(t, "operation "+operation.Name+" input", operation.Input)
		assertJSONSchema(t, "operation "+operation.Name+" output", operation.Output)
		for _, purpose := range operation.SecretPurposes {
			if !authFields[purpose] {
				t.Fatalf("operation %q references undeclared secret purpose %q", operation.Name, purpose)
			}
		}
	}
	datasources := map[string]bool{}
	for _, datasource := range manifest.Datasources {
		if datasource.Name == "" {
			t.Fatalf("datasource without name: %#v", datasource)
		}
		if datasources[datasource.Name] {
			t.Fatalf("duplicate datasource %q", datasource.Name)
		}
		datasources[datasource.Name] = true
		for _, purpose := range datasource.SecretPurposes {
			if !authFields[purpose] {
				t.Fatalf("datasource %q references undeclared secret purpose %q", datasource.Name, purpose)
			}
		}
		liveCapability := false
		for _, capability := range datasource.Capabilities {
			switch capability {
			case pluginbinding.CapabilitySearch, pluginbinding.CapabilityList, pluginbinding.CapabilityLookup, pluginbinding.CapabilityGet, pluginbinding.CapabilityBatchGet:
				liveCapability = true
			}
		}
		if liveCapability && !hasCapability(datasource.Capabilities, pluginbinding.CapabilityIndex) {
			assertJSONSchema(t, "datasource "+datasource.Name+" input", datasource.Input)
			assertJSONSchema(t, "datasource "+datasource.Name+" output", datasource.Output)
		}
		if hasCapability(datasource.Capabilities, pluginbinding.CapabilityIndex) && !indexes[datasource.Name] {
			t.Fatalf("indexed datasource %q has no matching index", datasource.Name)
		}
	}
}

func declaredAuthFields(manifest manifest.PluginManifest) map[string]bool {
	fields := map[string]bool{}
	for _, method := range manifest.Auth {
		for _, field := range method.Fields {
			if field.Name != "" {
				fields[field.Name] = true
			}
		}
	}
	return fields
}

func declaredIndexes(manifest manifest.PluginManifest) map[string]bool {
	indexes := map[string]bool{}
	for _, index := range manifest.Indexes {
		if index.Name != "" {
			indexes[index.Name] = true
		}
	}
	return indexes
}

func assertJSONSchema(t *testing.T, label string, raw json.RawMessage) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("%s schema is empty", label)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("%s schema is invalid: %v", label, err)
	}
	if len(schema) == 0 {
		t.Fatalf("%s schema is empty", label)
	}
	assertNormalizedSchemaValue(t, label, schema)
}

func hasCapability(capabilities []string, candidate string) bool {
	for _, capability := range capabilities {
		if capability == candidate {
			return true
		}
	}
	return false
}

func assertNormalizedSchemaValue(t *testing.T, label string, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed["$schema"]; ok {
			t.Fatalf("%s schema contains embedded $schema: %#v", label, typed)
		}
		if enum, ok := typed["enum"].([]any); ok {
			seen := map[string]bool{}
			for _, raw := range enum {
				text, ok := raw.(string)
				if !ok {
					continue
				}
				if strings.Contains(text, "|") {
					t.Fatalf("%s schema contains unsplit enum value %q", label, text)
				}
				if seen[text] {
					t.Fatalf("%s schema contains duplicate enum value %q", label, text)
				}
				seen[text] = true
			}
		}
		for key, child := range typed {
			assertNormalizedSchemaValue(t, label+"."+key, child)
		}
	case []any:
		for _, child := range typed {
			assertNormalizedSchemaValue(t, label, child)
		}
	}
}
