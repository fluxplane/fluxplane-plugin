package local

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

func TestPreferPathBinary(t *testing.T) {
	withGoInstall := sdkmanifest.PluginEntry{Binary: "fluxplane-plugin-jira", GoInstall: "example.com/jira@latest"}
	pathOnly := sdkmanifest.PluginEntry{Binary: "fluxplane-plugin-jira"}
	noBinary := sdkmanifest.PluginEntry{GoInstall: "example.com/jira@latest"}

	// Normal install reuses a PATH binary for convenience.
	if !preferPathBinary(false, withGoInstall) {
		t.Fatal("normal install should reuse PATH binary")
	}
	// Upgrade (preferRemote) with a go_install source must NOT reuse PATH —
	// otherwise it silently keeps a stale binary instead of fetching latest.
	if preferPathBinary(true, withGoInstall) {
		t.Fatal("upgrade with go_install must not reuse PATH binary")
	}
	// Upgrade of a PATH-only plugin (nothing to fetch) still uses PATH.
	if !preferPathBinary(true, pathOnly) {
		t.Fatal("upgrade of PATH-only plugin should use PATH binary")
	}
	// No binary name -> never PATH resolution.
	if preferPathBinary(false, noBinary) {
		t.Fatal("entry without binary should not resolve via PATH")
	}
}

// writeWorkspacePlugin creates a minimal standalone Go module with a
// cmd/<binary>/main.go so SyncLocalPlugins has something real to build.
func writeWorkspacePlugin(t *testing.T, binary string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/"+binary+"\n\ngo 1.23\n")
	mustWrite(t, filepath.Join(dir, "cmd", binary, "main.go"), "package main\n\nfunc main() {}\n")
	return dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func installWorkspacePlugin(t *testing.T, backend *Backend, name, localPath, binary, binPath string) {
	t.Helper()
	labels := map[string]string{"binary": binary}
	if localPath != "" {
		labels["local_path"] = localPath
	}
	if binPath != "" {
		labels["installed_binary_path"] = binPath
	}
	_, err := backend.InstallPlugin(context.Background(), management.InstallRequest{
		Ref:     management.Ref{Name: name},
		Runtime: management.RuntimeSpec{Kind: "stdio", Command: binary},
		Labels:  labels,
	})
	if err != nil {
		t.Fatalf("install %s: %v", name, err)
	}
}

func findSync(results []management.SyncPluginResult, name string) (management.SyncPluginResult, bool) {
	for _, r := range results {
		if r.Plugin.Name == name {
			return r, true
		}
	}
	return management.SyncPluginResult{}, false
}

func TestSyncLocalPluginsSkipsWithoutLocalPath(t *testing.T) {
	backend, err := New(WithPath(t.TempDir() + "/plugins.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	installWorkspacePlugin(t, backend, "remoteonly", "", "remoteonly", "")

	res, err := backend.SyncLocalPlugins(context.Background(), management.SyncRequest{All: true})
	if err != nil {
		t.Fatalf("SyncLocalPlugins: %v", err)
	}
	got, ok := findSync(res.Plugins, "remoteonly")
	if !ok || !got.Skipped || got.Rebuilt {
		t.Fatalf("expected skipped, got %#v", got)
	}
}

func TestSyncLocalPluginsDryRunDoesNotBuild(t *testing.T) {
	backend, err := New(WithPath(t.TempDir() + "/plugins.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ws := writeWorkspacePlugin(t, "probe")
	binPath := filepath.Join(t.TempDir(), "probe-bin")
	installWorkspacePlugin(t, backend, "probe", ws, "probe", binPath)

	res, err := backend.SyncLocalPlugins(context.Background(), management.SyncRequest{All: true, DryRun: true})
	if err != nil {
		t.Fatalf("SyncLocalPlugins: %v", err)
	}
	got, ok := findSync(res.Plugins, "probe")
	if !ok || got.Skipped || got.Rebuilt {
		t.Fatalf("dry run should be a planned rebuild, got %#v", got)
	}
	if got.BinaryPath != binPath {
		t.Fatalf("binary path = %q, want %q", got.BinaryPath, binPath)
	}
	if _, statErr := os.Stat(binPath); statErr == nil {
		t.Fatalf("dry run must not produce a binary at %s", binPath)
	}
}

func TestSyncLocalPluginsRebuildsFromWorkspace(t *testing.T) {
	// Build the temp module standalone so the repo's go.work does not reject a
	// module that is not one of its workspace members.
	t.Setenv("GOWORK", "off")
	backend, err := New(WithPath(t.TempDir() + "/plugins.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ws := writeWorkspacePlugin(t, "probe")
	binPath := filepath.Join(t.TempDir(), "probe-bin")
	installWorkspacePlugin(t, backend, "probe", ws, "probe", binPath)

	res, err := backend.SyncLocalPlugins(context.Background(), management.SyncRequest{Refs: []management.Ref{{Name: "probe"}}})
	if err != nil {
		t.Fatalf("SyncLocalPlugins: %v", err)
	}
	got, ok := findSync(res.Plugins, "probe")
	if !ok || !got.Rebuilt || got.Error != "" {
		t.Fatalf("expected rebuilt, got %#v", got)
	}
	if _, statErr := os.Stat(binPath); statErr != nil {
		t.Fatalf("expected binary at %s: %v", binPath, statErr)
	}
}

func TestSyncLocalPluginsReportsUnknownNamed(t *testing.T) {
	backend, err := New(WithPath(t.TempDir() + "/plugins.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := backend.SyncLocalPlugins(context.Background(), management.SyncRequest{Refs: []management.Ref{{Name: "ghost"}}})
	if err != nil {
		t.Fatalf("SyncLocalPlugins: %v", err)
	}
	got, ok := findSync(res.Plugins, "ghost")
	if !ok || !got.Skipped || got.Reason != "not installed" {
		t.Fatalf("expected not-installed skip, got %#v", got)
	}
}

func TestPluginInvokeTimeoutEnv(t *testing.T) {
	if got := pluginInvokeTimeout(); got.Seconds() != 120 {
		t.Fatalf("default timeout = %v, want 120s", got)
	}
	t.Setenv("FLUXPLANE_PLUGIN_TIMEOUT_SECONDS", "5")
	if got := pluginInvokeTimeout(); got.Seconds() != 5 {
		t.Fatalf("override = %v, want 5s", got)
	}
	t.Setenv("FLUXPLANE_PLUGIN_TIMEOUT_SECONDS", "garbage")
	if got := pluginInvokeTimeout(); got.Seconds() != 120 {
		t.Fatalf("invalid override should fall back to 120s, got %v", got)
	}
}

func TestListOperationsServesFromCache(t *testing.T) {
	backend, err := New(WithPath(t.TempDir() + "/plugins.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// A real file to key the cache on; the runtime command is bogus so any
	// attempt to actually spawn the plugin fails (proving cache hits don't spawn).
	binPath := filepath.Join(t.TempDir(), "demo-bin")
	if werr := os.WriteFile(binPath, []byte("x"), 0o755); werr != nil {
		t.Fatal(werr)
	}
	if _, ierr := backend.InstallPlugin(context.Background(), management.InstallRequest{
		Ref:     management.Ref{Name: "demo"},
		Runtime: management.RuntimeSpec{Kind: "stdio", Command: "/nonexistent/demo-binary"},
		Labels:  map[string]string{"installed_binary_path": binPath},
	}); ierr != nil {
		t.Fatalf("install: %v", ierr)
	}

	info, _ := os.Stat(binPath)
	mtime := strconv.FormatInt(info.ModTime().UnixNano(), 10)
	backend.cacheOperations(management.Ref{Name: "demo"}, json.RawMessage(`[{"name":"demo.read","read_only":true}]`), mtime)

	// Cache hit: returns cached ops without spawning the (bogus) binary.
	res, err := backend.ListOperations(context.Background(), management.OperationListRequest{Ref: management.Ref{Name: "demo"}})
	if err != nil {
		t.Fatalf("cache hit should not spawn/err: %v", err)
	}
	if len(res.Operations) != 1 || res.Operations[0].Name != "demo.read" {
		t.Fatalf("cached operations = %#v", res.Operations)
	}

	// Invalidate by bumping the binary mtime -> cache miss -> tries to spawn the
	// bogus command and fails.
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(binPath, future, future)
	if _, err := backend.ListOperations(context.Background(), management.OperationListRequest{Ref: management.Ref{Name: "demo"}}); err == nil {
		t.Fatal("stale cache (changed mtime) should miss and attempt a spawn (error)")
	}
}
