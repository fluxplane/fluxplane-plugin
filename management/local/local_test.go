package local

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
)

func TestBackendInstallListManifestRemove(t *testing.T) {
	backend, err := New(WithPath(t.TempDir() + "/plugins.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	install, err := backend.InstallPlugin(ctx, management.InstallRequest{Ref: management.Ref{Name: "gitlab", Version: "v1"}, Source: "test"})
	if err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}
	if !install.Installed || install.Plugin.Ref.Name != "gitlab" {
		t.Fatalf("install = %#v", install)
	}
	plugins, err := backend.ListPlugins(ctx, management.ListRequest{})
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(plugins) != 1 || plugins[0].Ref.Key() != "gitlab@v1" {
		t.Fatalf("plugins = %#v", plugins)
	}
	manifest, err := backend.PluginManifest(ctx, management.ManifestRequest{Ref: management.Ref{Name: "gitlab", Version: "v1"}})
	if err != nil {
		t.Fatalf("PluginManifest: %v", err)
	}
	if manifest.Format != "json" || len(manifest.Manifest) == 0 {
		t.Fatalf("manifest = %#v", manifest)
	}
	removed, err := backend.RemovePlugin(ctx, management.RemoveRequest{Ref: management.Ref{Name: "gitlab", Version: "v1"}})
	if err != nil {
		t.Fatalf("RemovePlugin: %v", err)
	}
	if !removed.Removed {
		t.Fatalf("removed = %#v", removed)
	}
}
