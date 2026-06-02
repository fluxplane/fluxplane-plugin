package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
)

type fakeBackend struct {
	installed management.InstallRequest
	updated   management.UpdateRequest
	enabled   management.SetEnabledRequest
	connected management.AuthConnectRequest
	invoked   management.OperationInvokeRequest
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
	return []management.Plugin{{Ref: management.Ref{Name: "gitlab"}, Installed: true}}, nil
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

func (f *fakeBackend) AuthTest(_ context.Context, req management.AuthTestRequest) (management.AuthResult, error) {
	return management.AuthResult{Plugin: req.Ref, Instance: req.Instance, Connected: true, Changed: true}, nil
}

func (f *fakeBackend) AuthDisconnect(_ context.Context, req management.AuthDisconnectRequest) (management.AuthResult, error) {
	return management.AuthResult{Plugin: req.Ref, Instance: req.Instance, Connected: false, Changed: true}, nil
}

func (f *fakeBackend) ListOperations(_ context.Context, req management.OperationListRequest) (management.OperationListResult, error) {
	return management.OperationListResult{Plugin: req.Ref, Instance: req.Instance}, nil
}

func (f *fakeBackend) InvokeOperation(_ context.Context, req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
	f.invoked = req
	return management.OperationInvokeResult{Plugin: req.Ref, Instance: req.Instance, Operation: req.Operation, Result: []byte(`{"ok":true}`)}, nil
}

func (f *fakeBackend) ListDatasources(_ context.Context, req management.DatasourceListRequest) (management.DatasourceListResult, error) {
	return management.DatasourceListResult{Plugin: req.Ref, Instance: req.Instance}, nil
}

func (f *fakeBackend) CallDatasource(_ context.Context, req management.DatasourceCallRequest) (management.DatasourceCallResult, error) {
	return management.DatasourceCallResult{Plugin: req.Ref, Instance: req.Instance, Capability: req.Capability, Result: []byte(`{"ok":true}`)}, nil
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

func TestCommandWithoutBackendFails(t *testing.T) {
	cmd := New(Options{})
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected missing backend error")
	}
}
