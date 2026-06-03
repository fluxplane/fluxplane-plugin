package pluginbinding

import (
	"encoding/json"
	"strings"
	"testing"

	evidence "github.com/fluxplane/fluxplane-evidence"
	manifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

type definitionOKOutput struct {
	OK bool `json:"ok"`
}

func TestDefineRegistersOperationAndSchema(t *testing.T) {
	spec := TypedOperationSpec[helloInput, helloOutput]("test.hello", "Say hello.", ReadOnly(), Compact(), SecretPurposes("token"))
	plugin := Define(ManifestSpec{Name: "test"},
		RegisterOperation(spec, func(_ Context, input helloInput) (helloOutput, error) {
			return helloOutput{Message: "hello " + input.Name, Count: 1}, nil
		}),
	)
	gotManifest := plugin.Manifest()
	if len(gotManifest.Operations) != 1 {
		t.Fatalf("operations = %#v", gotManifest.Operations)
	}
	operation := gotManifest.Operations[0]
	if operation.Name != "test.hello" || !operation.ReadOnly || !operation.Compact || len(operation.SecretPurposes) != 1 || operation.SecretPurposes[0] != "token" {
		t.Fatalf("operation spec = %#v", operation)
	}
	if len(operation.Input) == 0 || len(operation.Output) == 0 {
		t.Fatalf("schemas were not generated: %#v", operation)
	}
	if operation.Risk != manifest.OperationRiskLow || operation.Idempotency != manifest.OperationIdempotent {
		t.Fatalf("operation metadata defaults = %#v", operation)
	}
	if !containsOperationAccess(operation.Access, manifest.OperationAccessSecret) || !containsOperationAccess(operation.Access, manifest.OperationAccessAuth) {
		t.Fatalf("operation access = %#v", operation.Access)
	}
}

func TestOperationMetadataHelpers(t *testing.T) {
	spec := TypedOperationSpec[struct{}, definitionOKOutput](
		"test.write",
		"Write.",
		Effects(manifest.OperationEffectWrite, manifest.OperationEffectNetwork),
		Access(manifest.OperationAccessNetwork),
		Risk(manifest.OperationRiskHigh),
		Idempotency(manifest.OperationNonIdempotent),
		AuthScopes("write:things"),
		Render("json", "json", "yaml"),
	)
	gotManifest := Manifest(ManifestSpec{Name: "test", Operations: []manifest.OperationSpec{spec}})
	operation := gotManifest.Operations[0]
	if operation.Risk != manifest.OperationRiskHigh || operation.Idempotency != manifest.OperationNonIdempotent {
		t.Fatalf("metadata = %#v", operation)
	}
	if len(operation.Effects) != 2 || operation.Effects[0] != manifest.OperationEffectWrite || operation.Effects[1] != manifest.OperationEffectNetwork {
		t.Fatalf("effects = %#v", operation.Effects)
	}
	if len(operation.AuthScopes) != 1 || operation.AuthScopes[0] != "write:things" {
		t.Fatalf("auth scopes = %#v", operation.AuthScopes)
	}
	if operation.Render == nil || operation.Render.Preferred != "json" || len(operation.Render.Formats) != 2 {
		t.Fatalf("render = %#v", operation.Render)
	}
}

func TestDefineRegistersDatasourceAndSchema(t *testing.T) {
	type searchInput struct {
		Query  string `json:"query,omitempty"`
		Entity string `json:"entity,omitempty"`
	}
	type searchOutput struct {
		Records []DatasourceRecord `json:"records"`
		Count   int                `json:"count"`
	}
	spec := TypedDatasourceSpec[searchInput, searchOutput]("test.items", "test.item", "Test items.", []string{CapabilitySearch})
	plugin := Define(ManifestSpec{Name: "test", Datasources: []manifest.DatasourceSpec{spec}},
		RegisterDatasourceSearch(spec, func(ctx Context, input searchInput) (searchOutput, error) {
			record := NewDatasourceRecord(ctx.DatasourceSource(), "test.item", input.Query)
			return searchOutput{Records: []DatasourceRecord{record}, Count: 1}, nil
		}),
	)
	resp := plugin.Handle(request(t, protocol.CommandDatasourcesSearch, map[string]any{"query": "a", "entity": "test.item"}))
	if !resp.OK {
		t.Fatalf("datasource search failed: %#v", resp.Error)
	}
	manifest := plugin.Manifest()
	if len(manifest.Datasources) != 1 || len(manifest.Datasources[0].Input) == 0 || len(manifest.Datasources[0].Output) == 0 {
		t.Fatalf("datasource schema missing: %#v", manifest.Datasources)
	}
}

func TestDatasourceMetadataHelpersAndTags(t *testing.T) {
	type taggedRecord struct {
		ID        string `json:"id" datasource:"id,completion,view=compact|lookup"`
		Title     string `json:"title,omitempty" datasource:"title,completion,view=compact|table"`
		ProjectID int64  `json:"project_id,omitempty" datasource:"relation=test.project:project"`
	}
	spec := TypedDatasourceSpec[DatasourceSearchInput, DatasourceSearchResult[taggedRecord]](
		"test.items",
		"test.item",
		"Test items.",
		[]string{CapabilitySearch},
		EntitySchemaFor[taggedRecord](),
		View("detail", "Detail view.", "id", "title", "project_id"),
		Fallback(manifest.DatasourceFallbackProviderFirst),
	)
	if spec.EntitySchema == nil || spec.EntitySchema.Entity != "test.item" {
		t.Fatalf("entity schema = %#v", spec.EntitySchema)
	}
	if spec.EntitySchema.IDField != "id" || spec.EntitySchema.TitleField != "title" {
		t.Fatalf("entity id/title = %#v", spec.EntitySchema)
	}
	if spec.Fallback != manifest.DatasourceFallbackProviderFirst {
		t.Fatalf("fallback = %q", spec.Fallback)
	}
	if spec.Completion == nil || len(spec.Completion.Fields) != 2 {
		t.Fatalf("completion = %#v", spec.Completion)
	}
	if !hasDatasourceView(spec.Views, "compact", "id") || !hasDatasourceView(spec.Views, "detail", "project_id") {
		t.Fatalf("views = %#v", spec.Views)
	}
	if len(spec.Relations) != 1 || spec.Relations[0].Entity != "test.project" || spec.Relations[0].Field != "project_id" {
		t.Fatalf("relations = %#v", spec.Relations)
	}
}

func TestEntitySchemaForPreservesExplicitSchemaOverrides(t *testing.T) {
	type taggedRecord struct {
		ID    string `json:"id" datasource:"id"`
		Title string `json:"title" datasource:"title" jsonschema:"description=Generated title"`
	}
	spec := TypedDatasourceSpec[DatasourceSearchInput, DatasourceSearchResult[taggedRecord]](
		"test.items",
		"test.item",
		"Test items.",
		[]string{CapabilitySearch},
		EntitySchema(manifest.DatasourceEntitySchema{
			IDField: "external_id",
			Fields:  []manifest.DatasourceFieldSpec{{Name: "title", Description: "Explicit title"}},
		}),
		EntitySchemaFor[taggedRecord](),
	)
	if spec.EntitySchema == nil {
		t.Fatal("entity schema is nil")
	}
	if spec.EntitySchema.IDField != "external_id" {
		t.Fatalf("IDField = %q, want explicit override", spec.EntitySchema.IDField)
	}
	if spec.EntitySchema.TitleField != "title" {
		t.Fatalf("TitleField = %q, want generated title field", spec.EntitySchema.TitleField)
	}
	for _, field := range spec.EntitySchema.Fields {
		if field.Name == "title" {
			if field.Description != "Explicit title" {
				t.Fatalf("title description = %q, want explicit override", field.Description)
			}
			return
		}
	}
	t.Fatalf("title field missing: %#v", spec.EntitySchema.Fields)
}

func TestDefineRegistersContextProvider(t *testing.T) {
	spec := ContextSpec("test.context", "Test context.", ContextKindText)
	plugin := Define(ManifestSpec{Name: "test"},
		RegisterContextProvider(spec, func(_ Context, input ContextBuildInput) (ContextBuildResult, error) {
			return ContextBuildResult{Blocks: []manifest.ContextBlock{{
				ID:      "one",
				Title:   "One",
				Content: input.Query,
			}}}, nil
		}),
	)
	resp := plugin.Handle(request(t, protocol.CommandContextBuild, map[string]any{"query": "hello"}))
	if !resp.OK {
		t.Fatalf("context build failed: %#v", resp.Error)
	}
	var result ContextBuildResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Blocks) != 1 || result.Blocks[0].Content != "hello" || result.Blocks[0].Kind != ContextKindText {
		t.Fatalf("blocks = %#v", result.Blocks)
	}
	if result.Blocks[0].Source == nil || result.Blocks[0].Source.Plugin != "test" {
		t.Fatalf("source = %#v", result.Blocks[0].Source)
	}
}

func TestDefaultContextProviderReturnsEmptyBlocks(t *testing.T) {
	plugin := Define(ManifestSpec{Name: "test"})
	resp := plugin.Handle(request(t, protocol.CommandContextBuild, map[string]any{"query": "hello"}))
	if !resp.OK {
		t.Fatalf("context build failed: %#v", resp.Error)
	}
	var result ContextBuildResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Blocks) != 0 {
		t.Fatalf("blocks = %#v", result.Blocks)
	}
}

func TestDefineRegistersEvidenceObserver(t *testing.T) {
	spec := manifest.ObserverSpec{
		Name:            "test.environment",
		Description:     "Test environment.",
		Environment:     evidence.Ref{Name: "test"},
		Phase:           evidence.PhaseTurn,
		ObservableKinds: []string{"test.ready"},
		Dynamic:         true,
	}
	plugin := Define(ManifestSpec{Name: "test"},
		RegisterEvidenceObserver(spec, func(_ Context, _ EvidenceObserveInput) (EvidenceObserveResult, error) {
			return EvidenceObserveResult{Observations: []evidence.Observation{{
				ID:      "test:ready",
				Kind:    "test.ready",
				Scope:   "test",
				Content: map[string]any{"ready": true},
			}, {
				ID:   "test:ignored",
				Kind: "test.ignored",
			}}}, nil
		}),
	)
	gotManifest := plugin.Manifest()
	if len(gotManifest.Observers) != 1 || gotManifest.Observers[0].Name != "test.environment" {
		t.Fatalf("observers = %#v", gotManifest.Observers)
	}
	resp := plugin.Handle(request(t, protocol.CommandEvidenceObserve, protocol.EvidenceObserveRequest{Phase: evidence.PhaseTurn}))
	if !resp.OK {
		t.Fatalf("evidence observe failed: %#v", resp.Error)
	}
	var result protocol.EvidenceObserveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 1 {
		t.Fatalf("observations = %#v", result.Observations)
	}
	observation := result.Observations[0]
	if observation.Kind != "test.ready" || observation.Source != "test.environment" || observation.Environment.Name != "test" {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestDefineGeneratesAuthConnectText(t *testing.T) {
	plugin := Define(ManifestSpec{
		Name: "test",
		Auth: []manifest.AuthMethod{BearerAuth(
			"token_set",
			"Tokens.",
			AuthField("access_token", "Access token", true, true, "TEST_TOKEN"),
			AuthField("optional_token", "Optional token", false, true, "TEST_OPTIONAL_TOKEN"),
			AuthField("base_url", "Base URL", true, false, "TEST_URL"),
		)},
	})
	resp := plugin.Handle(protocol.Request{Command: protocol.CommandAuthConnect, Plugin: "test", Instance: "default"})
	if !resp.OK {
		t.Fatalf("auth connect failed: %#v", resp.Error)
	}
	var out TextResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "--field access_token=<value>") || !strings.Contains(out.Text, "--field base_url=<value>") {
		t.Fatalf("auth connect text = %q", out.Text)
	}
	if strings.Contains(out.Text, "optional_token") || strings.Contains(out.Text, "TEST_TOKEN") {
		t.Fatalf("auth connect text leaked unexpected values: %q", out.Text)
	}
}

func TestIndexedDatasourceAddsDatasourceAndIndex(t *testing.T) {
	manifest := Manifest(ManifestSpec{
		Name: "test",
		IndexedDatasources: []IndexedDatasourceSpec{
			IndexedDatasource("test.users", "test.user", "Users.", "User index.", CapabilitySearch, CapabilityIndex),
		},
	})
	if len(manifest.Datasources) != 1 || manifest.Datasources[0].Name != "test.users" || manifest.Datasources[0].Entity != "test.user" {
		t.Fatalf("datasources = %#v", manifest.Datasources)
	}
	if len(manifest.Indexes) != 1 || manifest.Indexes[0].Name != "test.users" || manifest.Indexes[0].Entities[0] != "test.user" {
		t.Fatalf("indexes = %#v", manifest.Indexes)
	}
}

func TestDefinitionCommandHelpers(t *testing.T) {
	spec := TypedOperationSpec[struct{}, definitionOKOutput]("test.index.build", "Build index.", ReadOnly())
	plugin := Define(ManifestSpec{Name: "test"},
		WithHostManagedAuthTest("Test"),
		WithIndexBuildOperation("test.index.build"),
		WithHostOwnedIndexStatus("Test"),
		RegisterOperation(spec, func(_ Context, _ struct{}) (definitionOKOutput, error) {
			return definitionOKOutput{OK: true}, nil
		}),
	)
	auth := plugin.Handle(protocol.Request{Command: protocol.CommandAuthTest, Plugin: "test", Instance: "default"})
	if !auth.OK {
		t.Fatalf("auth test failed: %#v", auth.Error)
	}
	status := plugin.Handle(protocol.Request{Command: protocol.CommandIndexStatus, Plugin: "test", Instance: "default"})
	if !status.OK {
		t.Fatalf("index status failed: %#v", status.Error)
	}
	build := plugin.Handle(protocol.Request{Command: protocol.CommandIndexBuild, Plugin: "test", Instance: "default", Grant: "grant"})
	if !build.OK {
		t.Fatalf("index build failed: %#v", build.Error)
	}
}

func TestNotImplementedOperationReturnsCallSpecificError(t *testing.T) {
	spec := TypedOperationSpec[struct{}, definitionOKOutput]("test.todo", "Todo.", ReadOnly())
	plugin := Define(ManifestSpec{Name: "test"},
		RegisterOperation(spec, NotImplementedOperation[struct{}, definitionOKOutput]("requires migration")),
	)
	resp := plugin.Handle(request(t, protocol.CommandOperationsCall, protocol.OperationCall{Name: "test.todo"}))
	if resp.OK || resp.Error == nil || resp.Error.Code != "not_implemented" {
		t.Fatalf("response = %#v", resp)
	}
	if !strings.Contains(resp.Error.Message, "test.todo") || !strings.Contains(resp.Error.Message, "requires migration") {
		t.Fatalf("message = %q", resp.Error.Message)
	}
}

func containsOperationAccess(values []manifest.OperationAccess, candidate manifest.OperationAccess) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func hasDatasourceView(views []manifest.DatasourceViewSpec, name, field string) bool {
	for _, view := range views {
		if view.Name != name {
			continue
		}
		for _, candidate := range view.Fields {
			if candidate == field {
				return true
			}
		}
	}
	return false
}

func TestGenericDatasourceSearchInputSchemaDoesNotAdvertiseKubernetesFields(t *testing.T) {
	schema := MustSchemaFor[DatasourceSearchInput]()
	if strings.Contains(string(schema), `"context"`) || strings.Contains(string(schema), `"namespace"`) {
		t.Fatalf("generic datasource search schema includes provider-specific fields: %s", string(schema))
	}
}
