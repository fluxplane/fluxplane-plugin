package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	fpcontext "github.com/fluxplane/fluxplane-context"
	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

type fakeBackend struct {
	installed        management.InstallRequest
	updated          management.UpdateRequest
	enabled          management.SetEnabledRequest
	connected        management.AuthConnectRequest
	authAuto         management.AuthAutoRequest
	invoked          management.OperationInvokeRequest
	batched          management.OperationBatchRequest
	datasource       management.DatasourceCallRequest
	built            management.ContextBuildRequest
	evidenceListed   management.EvidenceListRequest
	evidenceObserved management.EvidenceObserveRequest
	indexBuilt       management.IndexBuildRequest
	indexStatusReq   management.IndexStatusRequest
	discovered       management.EndpointDiscoverRequest
	endpointListed   management.EndpointListRequest
	endpointList     management.EndpointListResult
	endpointGot      management.EndpointGetRequest
	endpointGet      management.EndpointGetResult
	endpointSaved    management.EndpointSaveRequest
	endpointHealth   management.EndpointHealthRequest
	endpointRemoved  management.EndpointRemoveRequest

	// Optional overrides for operation discovery/invocation. When nil the default
	// fixed behavior is used. invokeCount records InvokeOperation calls so tests
	// (e.g. dry-run) can assert none were made; listOpsReqs records the refs
	// queried (so search tests can assert --plugin filtering).
	listOpsFn   func(management.OperationListRequest) (management.OperationListResult, error)
	invokeFn    func(management.OperationInvokeRequest) (management.OperationInvokeResult, error)
	listOpsReqs []management.OperationListRequest
	invokeCount int
	mu          sync.Mutex
}

func (f *fakeBackend) recordListOps(req management.OperationListRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listOpsReqs = append(f.listOpsReqs, req)
}

func (f *fakeBackend) recordInvoke(req management.OperationInvokeRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invoked = req
	f.invokeCount++
}

func (f *fakeBackend) InstallPlugin(_ context.Context, req management.InstallRequest) (management.InstallResult, error) {
	f.installed = req
	return management.InstallResult{Plugin: management.Plugin{Ref: req.Ref}, Installed: true}, nil
}

func (f *fakeBackend) UpdatePlugin(_ context.Context, req management.UpdateRequest) (management.UpdateResult, error) {
	f.updated = req
	return management.UpdateResult{Plugin: management.Plugin{Ref: req.Ref}, Updated: true}, nil
}

func (f *fakeBackend) ListPlugins(context.Context, management.ListRequest) ([]management.Plugin, error) {
	return []management.Plugin{{Ref: management.Ref{Name: "gitlab"}, Installed: true, Enabled: true}}, nil
}

func (f *fakeBackend) PluginStatus(context.Context, management.StatusRequest) (management.StatusResult, error) {
	return management.StatusResult{Plugins: []management.Plugin{{Ref: management.Ref{Name: "gitlab"}, Installed: true}}}, nil
}

func (f *fakeBackend) SetPluginEnabled(_ context.Context, req management.SetEnabledRequest) (management.SetEnabledResult, error) {
	f.enabled = req
	return management.SetEnabledResult{Plugin: management.Plugin{Ref: req.Ref, Enabled: req.Enabled}, Changed: true}, nil
}

func (f *fakeBackend) RemovePlugin(_ context.Context, req management.RemoveRequest) (management.RemoveResult, error) {
	return management.RemoveResult{Ref: req.Ref, Removed: true}, nil
}

func (f *fakeBackend) SearchPlugins(context.Context, management.SearchRequest) (management.SearchResult, error) {
	return management.SearchResult{Plugins: []management.Plugin{{Ref: management.Ref{Name: "gitlab"}}}}, nil
}

func (f *fakeBackend) PluginManifest(_ context.Context, req management.ManifestRequest) (management.ManifestResult, error) {
	return management.ManifestResult{Ref: req.Ref, Manifest: []byte(`{"name":"gitlab"}`), Format: "json"}, nil
}

func (f *fakeBackend) RunPlugin(_ context.Context, req management.RunRequest) (management.RunResult, error) {
	return management.RunResult{Ref: req.Ref}, nil
}

func (f *fakeBackend) AuthStatus(_ context.Context, req management.AuthStatusRequest) (management.AuthStatusResult, error) {
	return management.AuthStatusResult{Plugin: req.Ref, Instance: req.Instance}, nil
}

func (f *fakeBackend) AuthMethods(_ context.Context, req management.AuthMethodsRequest) (management.AuthMethodsResult, error) {
	return management.AuthMethodsResult{Plugin: req.Ref, Instance: req.Instance}, nil
}

func (f *fakeBackend) AuthConnect(_ context.Context, req management.AuthConnectRequest) (management.AuthResult, error) {
	f.connected = req
	return management.AuthResult{Plugin: req.Ref, Instance: req.Instance, Connected: true, Changed: true}, nil
}

func (f *fakeBackend) AuthAuto(_ context.Context, req management.AuthAutoRequest) (management.AuthAutoResult, error) {
	f.authAuto = req
	return management.AuthAutoResult{Plugin: req.Ref, Instance: req.Instance, Saved: []string{"access_token"}, Changed: !req.DryRun}, nil
}

func (f *fakeBackend) AuthTest(_ context.Context, req management.AuthTestRequest) (management.AuthResult, error) {
	return management.AuthResult{Plugin: req.Ref, Instance: req.Instance, Connected: true, Changed: true}, nil
}

func (f *fakeBackend) AuthDisconnect(_ context.Context, req management.AuthDisconnectRequest) (management.AuthResult, error) {
	return management.AuthResult{Plugin: req.Ref, Instance: req.Instance, Connected: false, Changed: true}, nil
}

func (f *fakeBackend) ListOperations(_ context.Context, req management.OperationListRequest) (management.OperationListResult, error) {
	f.recordListOps(req)
	if f.listOpsFn != nil {
		return f.listOpsFn(req)
	}
	return management.OperationListResult{Plugin: req.Ref, Instance: req.Instance}, nil
}

func (f *fakeBackend) InvokeOperation(_ context.Context, req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
	f.recordInvoke(req)
	if f.invokeFn != nil {
		return f.invokeFn(req)
	}
	return management.OperationInvokeResult{Plugin: req.Ref, Instance: req.Instance, Operation: req.Operation, Result: []byte(`{"ok":true,"endpoint_url":"https://user:secret@example.test","rows":[{"ok":true}]}`)}, nil
}

func (f *fakeBackend) BatchOperations(_ context.Context, req management.OperationBatchRequest) (management.OperationBatchResult, error) {
	f.batched = req
	return management.OperationBatchResult{Plugin: req.Ref, Instance: req.Instance, Result: []byte(`{"results":[]}`)}, nil
}

func (f *fakeBackend) ListDatasources(_ context.Context, req management.DatasourceListRequest) (management.DatasourceListResult, error) {
	return management.DatasourceListResult{Plugin: req.Ref, Instance: req.Instance, Datasources: []sdkmanifest.DatasourceSpec{{
		Name:         req.Ref.Name + ".items",
		Entity:       req.Ref.Name + ".item",
		Capabilities: []string{"search", "lookup", "get", "list", "batch_get"},
	}}}, nil
}

func (f *fakeBackend) CallDatasource(_ context.Context, req management.DatasourceCallRequest) (management.DatasourceCallResult, error) {
	f.datasource = req
	return management.DatasourceCallResult{Plugin: req.Ref, Instance: req.Instance, Capability: req.Capability, Result: []byte(`{"ok":true}`)}, nil
}

func (f *fakeBackend) ListContextProviders(_ context.Context, req management.ContextListRequest) (management.ContextListResult, error) {
	return management.ContextListResult{Plugin: req.Ref, Instance: req.Instance, Context: []sdkmanifest.ContextSpec{{Name: fpcontext.ProviderName(req.Ref.Name + ".context")}}}, nil
}

func (f *fakeBackend) BuildContext(_ context.Context, req management.ContextBuildRequest) (management.ContextBuildResult, error) {
	f.built = req
	return management.ContextBuildResult{Plugin: req.Ref, Instance: req.Instance, Result: []byte(`{"blocks":[]}`)}, nil
}

func (f *fakeBackend) ListEvidence(_ context.Context, req management.EvidenceListRequest) (management.EvidenceListResult, error) {
	f.evidenceListed = req
	return management.EvidenceListResult{Plugin: req.Ref, Instance: req.Instance, Observers: []sdkmanifest.ObserverSpec{{Name: req.Ref.Name + ".environment"}}}, nil
}

func (f *fakeBackend) ObserveEvidence(_ context.Context, req management.EvidenceObserveRequest) (management.EvidenceObserveResult, error) {
	f.evidenceObserved = req
	return management.EvidenceObserveResult{Plugin: req.Ref, Instance: req.Instance, Result: []byte(`{"observations":[]}`)}, nil
}

func (f *fakeBackend) BuildIndex(_ context.Context, req management.IndexBuildRequest) (management.IndexBuildResult, error) {
	f.indexBuilt = req
	return management.IndexBuildResult{Plugin: req.Ref, Instance: req.Instance, Index: "test.items", Indexes: []string{"test.items"}, Records: 1, Stored: !req.DryRun}, nil
}

func (f *fakeBackend) IndexStatus(_ context.Context, req management.IndexStatusRequest) (management.IndexStatusResult, error) {
	f.indexStatusReq = req
	return management.IndexStatusResult{Plugin: req.Ref, Instance: req.Instance, Indexes: []management.IndexStatus{{Plugin: req.Ref, Instance: req.Instance, Indexes: []string{"test.items"}, Records: 1}}}, nil
}

func (f *fakeBackend) DiscoverEndpoints(_ context.Context, req management.EndpointDiscoverRequest) (management.EndpointDiscoverResult, error) {
	f.discovered = req
	return management.EndpointDiscoverResult{Plugin: req.Ref, Instance: req.Instance, Result: []byte(`{"candidates":[]}`)}, nil
}

func (f *fakeBackend) ListEndpoints(_ context.Context, req management.EndpointListRequest) (management.EndpointListResult, error) {
	f.endpointListed = req
	if len(f.endpointList.Records) > 0 || len(f.endpointList.Endpoints) > 0 {
		return f.endpointList, nil
	}
	return management.EndpointListResult{Endpoints: []fpendpoint.EndpointRef{{ID: "gitlab", URL: "https://gitlab.example.com", Product: "gitlab"}}}, nil
}

func (f *fakeBackend) GetEndpoint(_ context.Context, req management.EndpointGetRequest) (management.EndpointGetResult, error) {
	f.endpointGot = req
	if f.endpointGet.Found {
		return f.endpointGet, nil
	}
	return management.EndpointGetResult{Endpoint: fpendpoint.EndpointRef{ID: req.ID, URL: "https://gitlab.example.com", Product: "gitlab"}, Found: true}, nil
}

func (f *fakeBackend) SaveEndpoint(_ context.Context, req management.EndpointSaveRequest) (management.EndpointSaveResult, error) {
	f.endpointSaved = req
	return management.EndpointSaveResult{Endpoint: req.Endpoint, Saved: true}, nil
}

func (f *fakeBackend) SaveEndpointHealth(_ context.Context, req management.EndpointHealthRequest) (management.EndpointHealthResult, error) {
	f.endpointHealth = req
	return management.EndpointHealthResult{ID: req.ID, Saved: true}, nil
}

func (f *fakeBackend) RemoveEndpoint(_ context.Context, req management.EndpointRemoveRequest) (management.EndpointRemoveResult, error) {
	f.endpointRemoved = req
	return management.EndpointRemoveResult{ID: req.ID, Removed: true}, nil
}

func TestInstallCommandUsesBackend(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"install", "gitlab@v1.2.3", "--source", "local", "--runtime-kind", "stdio", "--command", "gitlab", "--arg", "serve", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.installed.Ref.Name != "gitlab" || backend.installed.Ref.Version != "v1.2.3" {
		t.Fatalf("installed ref = %#v", backend.installed.Ref)
	}
	if backend.installed.Source != "local" || !backend.installed.DryRun {
		t.Fatalf("install request = %#v", backend.installed)
	}
	if backend.installed.Runtime.Kind != "stdio" || backend.installed.Runtime.Command != "gitlab" || len(backend.installed.Runtime.Args) != 1 {
		t.Fatalf("runtime = %#v", backend.installed.Runtime)
	}
	if out.Len() == 0 {
		t.Fatalf("expected JSON output")
	}
}

func TestUpdateCommandUsesBackend(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"update", "gitlab", "--source", "marketplace", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.updated.Ref.Name != "gitlab" || backend.updated.Source != "marketplace" || !backend.updated.DryRun {
		t.Fatalf("update request = %#v", backend.updated)
	}
}

func TestEnableCommandUsesBackend(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"disable", "gitlab"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.enabled.Ref.Name != "gitlab" || backend.enabled.Enabled {
		t.Fatalf("enabled request = %#v", backend.enabled)
	}
}

func TestAuthConnectCommandUsesBackend(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"auth", "connect", "gitlab", "--instance", "work", "--method", "token", "--field", "access_token=secret"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.connected.Ref.Name != "gitlab" || backend.connected.Instance != "work" || backend.connected.Method != "token" {
		t.Fatalf("auth connect request = %#v", backend.connected)
	}
	if backend.connected.Metadata["access_token"] != "secret" {
		t.Fatalf("metadata = %#v", backend.connected.Metadata)
	}
}

func TestAuthAutoCommandUsesBackend(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"auth", "connect", "auto", "gitlab", "--instance", "work", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.authAuto.Ref.Name != "gitlab" || backend.authAuto.Instance != "work" || !backend.authAuto.DryRun {
		t.Fatalf("auth auto request = %#v", backend.authAuto)
	}
	if bytes.Contains(out.Bytes(), []byte("secret")) {
		t.Fatalf("auth auto output leaked secret material:\n%s", out.String())
	}
}

func TestOperationInvokeCommandUsesBackend(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"operation", "invoke", "gitlab", "gitlab.project.list", "--instance", "work", "--input", `{"limit":1}`})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.invoked.Ref.Name != "gitlab" || backend.invoked.Instance != "work" || backend.invoked.Operation != "gitlab.project.list" {
		t.Fatalf("operation invoke request = %#v", backend.invoked)
	}
	if string(backend.invoked.Input) != `{"limit":1}` {
		t.Fatalf("operation input = %s", string(backend.invoked.Input))
	}
}

func TestOperationBatchCommandUsesBackend(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"operation", "batch", "gitlab", "--instance", "work", "--input", `{"calls":[{"name":"gitlab.project.list","input":{"limit":1}}]}`})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.batched.Ref.Name != "gitlab" || backend.batched.Instance != "work" {
		t.Fatalf("operation batch request = %#v", backend.batched)
	}
	if len(backend.batched.Calls) != 1 || backend.batched.Calls[0].Name != "gitlab.project.list" || backend.batched.Calls[0].ID != "1" {
		t.Fatalf("operation batch calls = %#v", backend.batched.Calls)
	}
	if out.Len() == 0 {
		t.Fatalf("expected JSON output")
	}
}

func TestDatasourceRecordsCommandUsesBackend(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"datasource", "records", "gitlab", "--instance", "work", "--input", `{"entity":"gitlab.issue","limit":2}`})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.datasource.Ref.Name != "gitlab" || backend.datasource.Instance != "work" || backend.datasource.Capability != "list" {
		t.Fatalf("datasource records request = %#v", backend.datasource)
	}
	if string(backend.datasource.Input) != `{"entity":"gitlab.issue","limit":2}` {
		t.Fatalf("datasource records input = %s", string(backend.datasource.Input))
	}
	if out.Len() == 0 {
		t.Fatalf("expected JSON output")
	}
}

func TestDatasourceBatchGetCommandUsesBackend(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"datasource", "batch-get", "gitlab", "--instance", "work", "--input", `{"entity":"gitlab.issue","ids":["1","2"]}`})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.datasource.Ref.Name != "gitlab" || backend.datasource.Instance != "work" || backend.datasource.Capability != "batch_get" {
		t.Fatalf("datasource batch-get request = %#v", backend.datasource)
	}
	if string(backend.datasource.Input) != `{"entity":"gitlab.issue","ids":["1","2"]}` {
		t.Fatalf("datasource batch-get input = %s", string(backend.datasource.Input))
	}
	if out.Len() == 0 {
		t.Fatalf("expected JSON output")
	}
}

func TestDatasourceFanoutCommandsUseCapablePlugins(t *testing.T) {
	t.Run("search all", func(t *testing.T) {
		backend := &fakeBackend{}
		var out bytes.Buffer
		cmd := New(Options{Backend: backend, Out: &out})
		cmd.SetArgs([]string{"datasource", "search-all", "incident", "--instance", "work", "--entity", "gitlab.issue", "--limit", "3"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if backend.datasource.Ref.Name != "gitlab" || backend.datasource.Instance != "work" || backend.datasource.Capability != "search" {
			t.Fatalf("datasource search-all request = %#v", backend.datasource)
		}
		var input map[string]any
		if err := json.Unmarshal(backend.datasource.Input, &input); err != nil {
			t.Fatalf("search-all input JSON: %v", err)
		}
		if input["query"] != "incident" || input["entity"] != "gitlab.issue" || input["limit"].(float64) != 3 {
			t.Fatalf("search-all input = %#v", input)
		}
		if out.Len() == 0 {
			t.Fatalf("expected JSON output")
		}
	})
	t.Run("lookup", func(t *testing.T) {
		backend := &fakeBackend{}
		var out bytes.Buffer
		cmd := New(Options{Backend: backend, Out: &out})
		cmd.SetArgs([]string{"lookup", "group/project", "--instance", "work", "--entity", "gitlab.project", "--limit", "2"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if backend.datasource.Ref.Name != "gitlab" || backend.datasource.Instance != "work" || backend.datasource.Capability != "lookup" {
			t.Fatalf("lookup request = %#v", backend.datasource)
		}
		var input map[string]any
		if err := json.Unmarshal(backend.datasource.Input, &input); err != nil {
			t.Fatalf("lookup input JSON: %v", err)
		}
		if input["text"] != "group/project" || input["entity"] != "gitlab.project" || input["limit"].(float64) != 2 {
			t.Fatalf("lookup input = %#v", input)
		}
		if out.Len() == 0 {
			t.Fatalf("expected JSON output")
		}
	})
}

func TestContextBuildCommandUsesBackend(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"context", "build", "clock", "--instance", "work", "--query", "now", "--kind", "data", "--limit", "2"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.built.Ref.Name != "clock" || backend.built.Instance != "work" || backend.built.Query != "now" || backend.built.Limit != 2 {
		t.Fatalf("context build request = %#v", backend.built)
	}
	if len(backend.built.Kinds) != 1 || backend.built.Kinds[0] != "data" {
		t.Fatalf("context build kinds = %#v", backend.built.Kinds)
	}
	if out.Len() == 0 {
		t.Fatalf("expected JSON output")
	}
}

func TestContextBuildAllCommandUsesContextPlugins(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"context", "build-all", "release notes", "--instance", "work", "--kind", "text", "--limit", "4"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.built.Ref.Name != "gitlab" || backend.built.Instance != "work" || backend.built.Query != "release notes" || backend.built.Limit != 4 {
		t.Fatalf("context build-all request = %#v", backend.built)
	}
	if len(backend.built.Kinds) != 1 || backend.built.Kinds[0] != "text" {
		t.Fatalf("context build-all kinds = %#v", backend.built.Kinds)
	}
	if out.Len() == 0 {
		t.Fatalf("expected JSON output")
	}
}

func TestEvidenceCommandsUseBackend(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		backend := &fakeBackend{}
		var out bytes.Buffer
		cmd := New(Options{Backend: backend, Out: &out})
		cmd.SetArgs([]string{"evidence", "list", "aws", "--instance", "work"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if backend.evidenceListed.Ref.Name != "aws" || backend.evidenceListed.Instance != "work" {
			t.Fatalf("evidence list request = %#v", backend.evidenceListed)
		}
		if out.Len() == 0 {
			t.Fatalf("expected JSON output")
		}
	})
	t.Run("observe", func(t *testing.T) {
		backend := &fakeBackend{}
		var out bytes.Buffer
		cmd := New(Options{Backend: backend, Out: &out})
		cmd.SetArgs([]string{"evidence", "observe", "aws", "--instance", "work", "--phase", "turn", "--input", `{"phase":"turn"}`})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if backend.evidenceObserved.Ref.Name != "aws" || backend.evidenceObserved.Instance != "work" || backend.evidenceObserved.Phase != "turn" {
			t.Fatalf("evidence observe request = %#v", backend.evidenceObserved)
		}
		if string(backend.evidenceObserved.Input) != `{"phase":"turn"}` {
			t.Fatalf("evidence observe input = %s", string(backend.evidenceObserved.Input))
		}
		if out.Len() == 0 {
			t.Fatalf("expected JSON output")
		}
	})
}

func TestIndexCommandsUseBackend(t *testing.T) {
	t.Run("build", func(t *testing.T) {
		backend := &fakeBackend{}
		var out bytes.Buffer
		cmd := New(Options{Backend: backend, Out: &out})
		cmd.SetArgs([]string{"index", "build", "gitlab", "--instance", "work", "--index", "gitlab.issues", "--entity", "gitlab.issue", "--dry-run"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if backend.indexBuilt.Ref.Name != "gitlab" || backend.indexBuilt.Instance != "work" || backend.indexBuilt.Index != "gitlab.issues" || backend.indexBuilt.Entity != "gitlab.issue" || !backend.indexBuilt.DryRun {
			t.Fatalf("index build request = %#v", backend.indexBuilt)
		}
		if out.Len() == 0 {
			t.Fatalf("expected JSON output")
		}
	})
	t.Run("status", func(t *testing.T) {
		backend := &fakeBackend{}
		var out bytes.Buffer
		cmd := New(Options{Backend: backend, Out: &out})
		cmd.SetArgs([]string{"index", "status", "gitlab", "--instance", "work"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if backend.indexStatusReq.Ref.Name != "gitlab" || backend.indexStatusReq.Instance != "work" {
			t.Fatalf("index status request = %#v", backend.indexStatusReq)
		}
		if out.Len() == 0 {
			t.Fatalf("expected JSON output")
		}
	})
}

func TestEndpointDiscoverCommandUsesBackend(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"endpoint", "discover", "kubernetes", "loki", "--instance", "work", "--context", "dev", "--namespace", "monitoring", "--limit", "3"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.discovered.Ref.Name != "kubernetes" || backend.discovered.Instance != "work" || backend.discovered.Product != "loki" || backend.discovered.Context != "dev" || backend.discovered.Namespace != "monitoring" || backend.discovered.Limit != 3 {
		t.Fatalf("endpoint discover request = %#v", backend.discovered)
	}
	if out.Len() == 0 {
		t.Fatalf("expected JSON output")
	}
}

func TestEndpointStoreCommandsUseBackend(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		backend := &fakeBackend{}
		var out bytes.Buffer
		cmd := New(Options{Backend: backend, Out: &out})
		cmd.SetArgs([]string{"endpoint", "list", "--product", "gitlab"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if backend.endpointListed.Product != "gitlab" {
			t.Fatalf("endpoint list request = %#v", backend.endpointListed)
		}
		if out.Len() == 0 {
			t.Fatalf("expected JSON output")
		}
	})
	t.Run("get", func(t *testing.T) {
		backend := &fakeBackend{}
		var out bytes.Buffer
		cmd := New(Options{Backend: backend, Out: &out})
		cmd.SetArgs([]string{"endpoint", "get", "@endpoint/gitlab-dev"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if backend.endpointGot.ID != "@endpoint/gitlab-dev" {
			t.Fatalf("endpoint get request = %#v", backend.endpointGot)
		}
		if out.Len() == 0 {
			t.Fatalf("expected JSON output")
		}
	})
	t.Run("save", func(t *testing.T) {
		backend := &fakeBackend{}
		var out bytes.Buffer
		cmd := New(Options{Backend: backend, Out: &out})
		cmd.SetArgs([]string{"endpoint", "save", "gitlab-dev", "https://gitlab.example.com", "--product", "gitlab", "--protocol", "https", "--source", "manual", "--credential-ref", "@secret/gitlab", "--label", "env=dev", "--annotation", "owner=platform"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if backend.endpointSaved.Endpoint.ID != "gitlab-dev" || backend.endpointSaved.Endpoint.URL != "https://gitlab.example.com" || backend.endpointSaved.Endpoint.Product != "gitlab" {
			t.Fatalf("endpoint save request = %#v", backend.endpointSaved)
		}
		if backend.endpointSaved.Endpoint.Labels["env"] != "dev" || backend.endpointSaved.Endpoint.Annotations["owner"] != "platform" {
			t.Fatalf("endpoint metadata = %#v %#v", backend.endpointSaved.Endpoint.Labels, backend.endpointSaved.Endpoint.Annotations)
		}
		if out.Len() == 0 {
			t.Fatalf("expected JSON output")
		}
	})
	t.Run("health", func(t *testing.T) {
		backend := &fakeBackend{}
		var out bytes.Buffer
		cmd := New(Options{Backend: backend, Out: &out})
		cmd.SetArgs([]string{"endpoint", "health", "@endpoint/gitlab-dev", "--ok", "--method", "tcp_connect", "--duration-ms", "42", "--detail", "address=gitlab.example.com:443", "--metadata", "source=test"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if backend.endpointHealth.ID != "@endpoint/gitlab-dev" || !backend.endpointHealth.Health.OK || backend.endpointHealth.Health.Method != "tcp_connect" || backend.endpointHealth.Health.DurationMS != 42 {
			t.Fatalf("endpoint health request = %#v", backend.endpointHealth)
		}
		if backend.endpointHealth.Health.Details["address"] != "gitlab.example.com:443" || backend.endpointHealth.Health.Metadata["source"] != "test" {
			t.Fatalf("endpoint health metadata = %#v %#v", backend.endpointHealth.Health.Details, backend.endpointHealth.Health.Metadata)
		}
		if out.Len() == 0 {
			t.Fatalf("expected JSON output")
		}
	})
	t.Run("remove", func(t *testing.T) {
		backend := &fakeBackend{}
		cmd := New(Options{Backend: backend})
		cmd.SetArgs([]string{"endpoint", "remove", "@endpoint/gitlab-dev", "--dry-run"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if backend.endpointRemoved.ID != "@endpoint/gitlab-dev" || !backend.endpointRemoved.DryRun {
			t.Fatalf("endpoint remove request = %#v", backend.endpointRemoved)
		}
	})
}

func TestEndpointImportCommandSavesSelectedCandidate(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetIn(bytes.NewBufferString(`{"candidates":[{"index":2,"id":"mysql-abc","url":"mysql://db.example.com:3306/app","product":"mysql","protocol":"mysql","source":"kubernetes_secret","credential_ref":"kubernetes://latest/secrets/mysql","labels":{"namespace":"latest"}}]}`))
	cmd.SetArgs([]string{"endpoint", "import", "-", "--candidate", "2", "--id", "latest-mysql", "--label", "role=read", "--annotation", "owner=platform"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.endpointSaved.Endpoint.ID != "latest-mysql" || backend.endpointSaved.Endpoint.URL != "mysql://db.example.com:3306/app" || backend.endpointSaved.Endpoint.Product != "mysql" {
		t.Fatalf("endpoint save request = %#v", backend.endpointSaved)
	}
	if backend.endpointSaved.Endpoint.CredentialRef != "kubernetes://latest/secrets/mysql" || backend.endpointSaved.Endpoint.Source != "kubernetes_secret" {
		t.Fatalf("endpoint source/credential = %#v", backend.endpointSaved.Endpoint)
	}
	if backend.endpointSaved.Endpoint.Labels["namespace"] != "latest" || backend.endpointSaved.Endpoint.Labels["role"] != "read" || backend.endpointSaved.Endpoint.Annotations["owner"] != "platform" {
		t.Fatalf("endpoint metadata = %#v %#v", backend.endpointSaved.Endpoint.Labels, backend.endpointSaved.Endpoint.Annotations)
	}
	if out.Len() == 0 {
		t.Fatalf("expected JSON output")
	}
}

func TestEndpointImportCommandAcceptsDiscoverResultWrapper(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetIn(bytes.NewBufferString(`{"plugin":{"name":"kubernetes"},"instance":"work","result":{"candidates":[{"id":"loki-dev","url":"http://loki.monitoring:3100","product":"loki","protocol":"http","source":"kubernetes_service"}]}}`))
	cmd.SetArgs([]string{"endpoint", "import", "-"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.endpointSaved.Endpoint.ID != "loki-dev" || backend.endpointSaved.Endpoint.URL != "http://loki.monitoring:3100" || backend.endpointSaved.Endpoint.Product != "loki" {
		t.Fatalf("endpoint save request = %#v", backend.endpointSaved)
	}
	if backend.endpointSaved.Endpoint.Source != "kubernetes_service" {
		t.Fatalf("endpoint source = %q", backend.endpointSaved.Endpoint.Source)
	}
}

func TestEndpointDoctorCommandTestsTCPAndStoresHealth(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	backend := &fakeBackend{
		endpointList: management.EndpointListResult{
			Records: []fpendpoint.Record{{
				EndpointRef: fpendpoint.EndpointRef{
					ID:       "local-tcp",
					URL:      "tcp://" + listener.Addr().String(),
					Product:  "custom",
					Protocol: "tcp",
				},
			}},
		},
	}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"endpoint", "doctor", "custom"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.endpointListed.Product != "custom" {
		t.Fatalf("endpoint list request = %#v", backend.endpointListed)
	}
	if backend.endpointHealth.ID != "local-tcp" || !backend.endpointHealth.Health.OK || backend.endpointHealth.Health.Method != "tcp_connect" {
		t.Fatalf("endpoint health request = %#v", backend.endpointHealth)
	}
	if backend.endpointHealth.Health.Details["address"] != listener.Addr().String() {
		t.Fatalf("endpoint health details = %#v", backend.endpointHealth.Health.Details)
	}
	var result endpointDoctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if result.Count != 1 || result.OK != 1 || result.Failed != 0 || len(result.Endpoints) != 1 {
		t.Fatalf("doctor result = %#v", result)
	}
}

func TestEndpointTestCommandUsesPluginProbeForKubernetesEndpoint(t *testing.T) {
	backend := &fakeBackend{
		endpointGet: management.EndpointGetResult{
			Found: true,
			Record: fpendpoint.Record{
				EndpointRef: fpendpoint.EndpointRef{
					ID:       "dev-cluster",
					URL:      "kubernetes://context/dev",
					Product:  "kubernetes",
					Protocol: "kubernetes",
				},
			},
		},
	}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"endpoint", "test", "dev-cluster", "--instance", "work"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.endpointGot.ID != "dev-cluster" {
		t.Fatalf("endpoint get request = %#v", backend.endpointGot)
	}
	if backend.invoked.Ref.Name != "kubernetes" || backend.invoked.Instance != "work" || backend.invoked.Operation != "kubernetes.cluster.test" {
		t.Fatalf("operation invoke request = %#v", backend.invoked)
	}
	if backend.endpointHealth.ID != "dev-cluster" || !backend.endpointHealth.Health.OK || backend.endpointHealth.Health.Method != "kubernetes.cluster.test" {
		t.Fatalf("endpoint health request = %#v", backend.endpointHealth)
	}
	var result endpointTestResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got, _ := result.Details["endpoint_url"].(string); got != "https://user:xxxxx@example.test" {
		t.Fatalf("redacted endpoint url = %q details=%#v", got, result.Details)
	}
	if _, ok := result.Details["rows"]; ok {
		t.Fatalf("rows should not be retained in endpoint health details: %#v", result.Details)
	}
}

func TestCommandWithoutBackendFails(t *testing.T) {
	cmd := New(Options{})
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected missing backend error")
	}
}

func TestRedactEndpointURLMasksPasswords(t *testing.T) {
	cases := map[string]string{
		"mysql://user:s3cr3t@db.example.com:3306/app": "mysql://user:xxxxx@db.example.com:3306/app",
		"https://plain.example.com/path":              "https://plain.example.com/path",
		"postgres://user@db.example.com/app":          "postgres://user@db.example.com/app",
		"mysql://user:p@ss with space@host:3306/db":   "mysql://user:xxxxx@ss with space@host:3306/db",
	}
	for input, want := range cases {
		if got := redactEndpointURL(input); got != want {
			t.Fatalf("redactEndpointURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEndpointListRedactsCredentials(t *testing.T) {
	backend := &endpointListFakeBackend{fakeBackend: &fakeBackend{}}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"endpoint", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out.String(), "s3cr3t") {
		t.Fatalf("credentials leaked: %s", out.String())
	}
	if !strings.Contains(out.String(), "user:xxxxx@") {
		t.Fatalf("redaction marker missing: %s", out.String())
	}
}

type endpointListFakeBackend struct {
	*fakeBackend
}

func (b *endpointListFakeBackend) ListEndpoints(context.Context, management.EndpointListRequest) (management.EndpointListResult, error) {
	ref := fpendpoint.EndpointRef{ID: "aurora", URL: "mysql://user:s3cr3t@db.example.com:3306/app", Product: "mysql"}
	return management.EndpointListResult{
		Endpoints: []fpendpoint.EndpointRef{ref},
		Records:   []fpendpoint.Record{{EndpointRef: ref}},
	}, nil
}

func TestOperationListNamesIsCompact(t *testing.T) {
	backend := &fakeBackend{listOpsFn: func(management.OperationListRequest) (management.OperationListResult, error) {
		return management.OperationListResult{Operations: []sdkmanifest.OperationSpec{
			{Name: "x.alpha", Description: "Does alpha. With more detail here.", ReadOnly: true, Input: json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`)},
			{Name: "x.beta", Description: "Does beta."},
		}}, nil
	}}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"operation", "list", "x", "--names"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out.String(), "properties") {
		t.Fatalf("--names must not dump schemas: %s", out.String())
	}
	var result struct {
		Operations []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			ReadOnly    bool   `json:"read_only"`
		} `json:"operations"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if result.Count != 2 || result.Operations[0].Description != "Does alpha." || !result.Operations[0].ReadOnly {
		t.Fatalf("result = %#v", result)
	}
}

func TestSampleInputJSONPrefersRepresentativeFields(t *testing.T) {
	// No required fields, no example: representative optional fields beat an
	// endpoint_ref-only stub.
	schema := operationInputSchema{Properties: map[string]operationInputField{
		"endpoint_ref": {Type: "string"},
		"ref":          {Type: "string"},
		"channel":      {Type: "string"},
		"ts":           {Type: "string"},
	}}
	sample := sampleInputJSON(schema)
	if strings.Contains(sample, "endpoint_ref") {
		t.Fatalf("sample = %s, must not auto-inject endpoint_ref", sample)
	}
	if !strings.Contains(sample, `"ref"`) {
		t.Fatalf("sample = %s, want representative ref field", sample)
	}
	// Required fields still win.
	schema.Required = []string{"ts"}
	if sample := sampleInputJSON(schema); !strings.Contains(sample, `"ts"`) {
		t.Fatalf("sample = %s, want required ts", sample)
	}
}

func TestVersionCommandReportsBuildInfo(t *testing.T) {
	var out bytes.Buffer
	cmd := New(Options{Backend: &fakeBackend{}, Out: &out})
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var info struct {
		Version string `json:"version"`
		OS      string `json:"os"`
	}
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if info.Version == "" || info.OS == "" {
		t.Fatalf("info = %#v", info)
	}
}

func TestLookupFanoutSkipsUnconfiguredPlugins(t *testing.T) {
	backend := &lookupFanoutFakeBackend{fakeBackend: &fakeBackend{}}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"lookup", "https://example.test/thing"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result struct {
		Results []struct {
			Plugin  management.Ref `json:"plugin"`
			Skipped bool           `json:"skipped"`
			Reason  string         `json:"reason"`
			Error   string         `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	byName := map[string]int{}
	for i, r := range result.Results {
		byName[r.Plugin.Name] = i
	}
	unconfigured := result.Results[byName["ollama"]]
	if !unconfigured.Skipped || unconfigured.Error != "" || !strings.Contains(unconfigured.Reason, "endpoint_ref is required") {
		t.Fatalf("unconfigured plugin = %#v, want skipped with reason", unconfigured)
	}
	broken := result.Results[byName["slack"]]
	if broken.Skipped || !strings.Contains(broken.Error, "boom") {
		t.Fatalf("real failure must stay an error: %#v", broken)
	}
}

type lookupFanoutFakeBackend struct {
	*fakeBackend
}

func (b *lookupFanoutFakeBackend) ListPlugins(context.Context, management.ListRequest) ([]management.Plugin, error) {
	return []management.Plugin{
		{Ref: management.Ref{Name: "ollama"}, Installed: true, Enabled: true},
		{Ref: management.Ref{Name: "slack"}, Installed: true, Enabled: true},
	}, nil
}

func (b *lookupFanoutFakeBackend) ListDatasources(_ context.Context, req management.DatasourceListRequest) (management.DatasourceListResult, error) {
	return management.DatasourceListResult{Datasources: []sdkmanifest.DatasourceSpec{{
		Name:         req.Ref.Name + ".items",
		Entity:       req.Ref.Name + ".item",
		Capabilities: []string{"lookup"},
	}}}, nil
}

func (b *lookupFanoutFakeBackend) CallDatasource(_ context.Context, req management.DatasourceCallRequest) (management.DatasourceCallResult, error) {
	switch req.Ref.Name {
	case "ollama":
		return management.DatasourceCallResult{}, fmt.Errorf("invoke datasources.lookup on plugin %q: endpoint_ref is required", req.Ref.Name)
	default:
		return management.DatasourceCallResult{}, fmt.Errorf("boom")
	}
}
