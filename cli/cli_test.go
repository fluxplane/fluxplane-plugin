package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
)

type fakeBackend struct {
	installed management.InstallRequest
}

func (f *fakeBackend) InstallPlugin(_ context.Context, req management.InstallRequest) (management.InstallResult, error) {
	f.installed = req
	return management.InstallResult{Plugin: management.Plugin{Ref: req.Ref}, Installed: true}, nil
}

func (f *fakeBackend) ListPlugins(context.Context, management.ListRequest) ([]management.Plugin, error) {
	return []management.Plugin{{Ref: management.Ref{Name: "gitlab"}, Installed: true}}, nil
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

func TestInstallCommandUsesBackend(t *testing.T) {
	backend := &fakeBackend{}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"install", "gitlab@v1.2.3", "--source", "local", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.installed.Ref.Name != "gitlab" || backend.installed.Ref.Version != "v1.2.3" {
		t.Fatalf("installed ref = %#v", backend.installed.Ref)
	}
	if backend.installed.Source != "local" || !backend.installed.DryRun {
		t.Fatalf("install request = %#v", backend.installed)
	}
	if out.Len() == 0 {
		t.Fatalf("expected JSON output")
	}
}

func TestCommandWithoutBackendFails(t *testing.T) {
	cmd := New(Options{})
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected missing backend error")
	}
}
