package local

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/pluginbinding"
)

func TestBackendInstallListManifestRemove(t *testing.T) {
	backend, err := New(WithPath(t.TempDir() + "/plugins.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	ref := management.Ref{Name: "gitlab", Version: "v1"}
	install, err := backend.InstallPlugin(ctx, management.InstallRequest{
		Ref:      ref,
		Source:   "test",
		Runtime:  management.RuntimeSpec{Kind: "stdio", Command: "gitlab"},
		Manifest: []byte(`{"name":"gitlab"}`),
	})
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
	manifest, err := backend.PluginManifest(ctx, management.ManifestRequest{Ref: ref})
	if err != nil {
		t.Fatalf("PluginManifest: %v", err)
	}
	var manifestData map[string]string
	if err := json.Unmarshal(manifest.Manifest, &manifestData); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if manifest.Format != "json" || manifestData["name"] != "gitlab" {
		t.Fatalf("manifest = %#v", manifest)
	}
	status, err := backend.PluginStatus(ctx, management.StatusRequest{Ref: ref})
	if err != nil {
		t.Fatalf("PluginStatus: %v", err)
	}
	if len(status.Plugins) != 1 || len(status.Instances) != 1 || status.Instances[0].Name != management.DefaultInstance {
		t.Fatalf("status = %#v", status)
	}
	updated, err := backend.UpdatePlugin(ctx, management.UpdateRequest{Ref: ref, Source: "updated"})
	if err != nil {
		t.Fatalf("UpdatePlugin: %v", err)
	}
	if !updated.Updated || updated.Plugin.Source != "updated" {
		t.Fatalf("updated = %#v", updated)
	}
	disabled, err := backend.SetPluginEnabled(ctx, management.SetEnabledRequest{Ref: ref, Enabled: false})
	if err != nil {
		t.Fatalf("SetPluginEnabled: %v", err)
	}
	if !disabled.Changed || disabled.Plugin.Enabled {
		t.Fatalf("disabled = %#v", disabled)
	}
	removed, err := backend.RemovePlugin(ctx, management.RemoveRequest{Ref: ref})
	if err != nil {
		t.Fatalf("RemovePlugin: %v", err)
	}
	if !removed.Removed {
		t.Fatalf("removed = %#v", removed)
	}
}

func TestBackendAuthState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test plugin launcher uses /usr/bin/env")
	}
	backend, err := New(WithPath(t.TempDir() + "/plugins.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	ref := management.Ref{Name: "test"}
	if _, err := backend.InstallPlugin(ctx, management.InstallRequest{Ref: ref, Source: "test", Runtime: testRuntimeSpec()}); err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}
	connected, err := backend.AuthConnect(ctx, management.AuthConnectRequest{Ref: ref, Instance: "work", Method: "token"})
	if err != nil {
		t.Fatalf("AuthConnect: %v", err)
	}
	if !connected.Connected || !connected.Changed {
		t.Fatalf("connected = %#v", connected)
	}
	status, err := backend.AuthStatus(ctx, management.AuthStatusRequest{Ref: ref, Instance: "work"})
	if err != nil {
		t.Fatalf("AuthStatus: %v", err)
	}
	if len(status.Auth) != 1 || !status.Auth[0].Connected {
		t.Fatalf("status = %#v", status)
	}
	tested, err := backend.AuthTest(ctx, management.AuthTestRequest{Ref: ref, Instance: "work", Method: "token"})
	if err != nil {
		t.Fatalf("AuthTest: %v", err)
	}
	if !tested.Connected || tested.Auth.TestedAt.IsZero() {
		t.Fatalf("tested = %#v", tested)
	}
	disconnected, err := backend.AuthDisconnect(ctx, management.AuthDisconnectRequest{Ref: ref, Instance: "work", Method: "token"})
	if err != nil {
		t.Fatalf("AuthDisconnect: %v", err)
	}
	if disconnected.Connected || !disconnected.Changed {
		t.Fatalf("disconnected = %#v", disconnected)
	}
}

func TestBackendRejectsUnknownBareInstall(t *testing.T) {
	backend, err := New(WithPath(t.TempDir()+"/plugins.json"), WithMarketplace(sdkmanifest.Marketplace{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = backend.InstallPlugin(context.Background(), management.InstallRequest{Ref: management.Ref{Name: "bananawix"}})
	if err == nil {
		t.Fatal("InstallPlugin error is nil, want unknown marketplace plugin error")
	}
}

func TestBackendInstallsMarketplacePlugin(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "gitlab")
	cmdDir := filepath.Join(sourceDir, "cmd", "fluxplane-plugin-gitlab")
	if err := os.MkdirAll(cmdDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "go.mod"), []byte("module example.com/gitlab-plugin\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("WriteFile go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile main.go: %v", err)
	}
	backend, err := New(WithPath(filepath.Join(dir, "plugins.json")), WithMarketplace(sdkmanifest.Marketplace{
		Version: "1",
		Plugins: []sdkmanifest.PluginEntry{{
			Name:        "gitlab",
			Description: "GitLab plugin.",
			Binary:      "fluxplane-plugin-gitlab",
			GoInstall:   "github.com/fluxplane/fluxplane-plugins/gitlab/cmd/fluxplane-plugin-gitlab@latest",
			LocalPath:   sourceDir,
		}},
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	install, err := backend.InstallPlugin(context.Background(), management.InstallRequest{Ref: management.Ref{Name: "gitlab"}})
	if err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}
	if !install.Installed || install.Plugin.Source != "marketplace" || !strings.Contains(install.Plugin.Runtime.Command, filepath.Join("bin", "fluxplane-plugin-gitlab")) {
		t.Fatalf("install = %#v", install)
	}
	if _, err := os.Stat(install.Plugin.Runtime.Command); err != nil {
		t.Fatalf("cached binary: %v", err)
	}
	search, err := backend.SearchPlugins(context.Background(), management.SearchRequest{Query: "git"})
	if err != nil {
		t.Fatalf("SearchPlugins: %v", err)
	}
	if len(search.Plugins) != 1 || search.Plugins[0].Ref.Name != "gitlab" {
		t.Fatalf("search = %#v", search)
	}
	if _, err := backend.RemovePlugin(context.Background(), management.RemoveRequest{Ref: management.Ref{Name: "gitlab"}}); err != nil {
		t.Fatalf("RemovePlugin: %v", err)
	}
	if _, err := os.Stat(install.Plugin.Runtime.Command); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cached binary after remove err = %v, want not exist", err)
	}
}

func TestBackendRunRequiresRuntime(t *testing.T) {
	backend, err := New(WithPath(t.TempDir() + "/plugins.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ref := management.Ref{Name: "local"}
	if _, err := backend.InstallPlugin(context.Background(), management.InstallRequest{Ref: ref, Source: "dev"}); err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}
	_, err = backend.RunPlugin(context.Background(), management.RunRequest{Ref: ref})
	if err == nil {
		t.Fatal("RunPlugin error is nil, want missing runtime error")
	}
}

func TestBackendRunExecutesConfiguredRuntime(t *testing.T) {
	backend, err := New(WithPath(t.TempDir() + "/plugins.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "plugin")
	body := "#!/bin/sh\necho plugin:$1\n"
	if runtime.GOOS == "windows" {
		script = filepath.Join(dir, "plugin.bat")
		body = "@echo off\necho plugin:%1\n"
	}
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ref := management.Ref{Name: "local"}
	if _, err := backend.InstallPlugin(context.Background(), management.InstallRequest{
		Ref:     ref,
		Source:  "dev",
		Runtime: management.RuntimeSpec{Kind: "stdio", Command: script},
	}); err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}
	result, err := backend.RunPlugin(context.Background(), management.RunRequest{Ref: ref, Args: []string{"ok"}})
	if err != nil {
		t.Fatalf("RunPlugin: %v", err)
	}
	if result.ExitCode != 0 || result.Stdout == "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestBackendInvokesConfiguredPluginRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test plugin launcher uses /usr/bin/env")
	}
	backend, err := New(WithPath(t.TempDir() + "/plugins.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ref := management.Ref{Name: "test"}
	if _, err := backend.InstallPlugin(context.Background(), management.InstallRequest{
		Ref:     ref,
		Source:  "dev",
		Runtime: testRuntimeSpec(),
	}); err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}

	manifest, err := backend.PluginManifest(context.Background(), management.ManifestRequest{Ref: ref})
	if err != nil {
		t.Fatalf("PluginManifest: %v", err)
	}
	var manifestData map[string]any
	if err := json.Unmarshal(manifest.Manifest, &manifestData); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if manifestData["name"] != "test" {
		t.Fatalf("manifest = %#v", manifestData)
	}

	methods, err := backend.AuthMethods(context.Background(), management.AuthMethodsRequest{Ref: ref})
	if err != nil {
		t.Fatalf("AuthMethods: %v", err)
	}
	if len(methods.Methods) != 1 || methods.Methods[0].Name != "token" {
		t.Fatalf("methods = %#v", methods)
	}
	connected, err := backend.AuthConnect(context.Background(), management.AuthConnectRequest{Ref: ref, Method: "token", Metadata: map[string]string{"access_token": "secret"}})
	if err != nil {
		t.Fatalf("AuthConnect: %v", err)
	}
	if !connected.Connected {
		t.Fatalf("connected = %#v", connected)
	}
	tested, err := backend.AuthTest(context.Background(), management.AuthTestRequest{Ref: ref, Method: "token"})
	if err != nil {
		t.Fatalf("AuthTest: %v", err)
	}
	if !tested.Connected || tested.Auth.TestedAt.IsZero() {
		t.Fatalf("tested = %#v", tested)
	}

	operations, err := backend.ListOperations(context.Background(), management.OperationListRequest{Ref: ref})
	if err != nil {
		t.Fatalf("ListOperations: %v", err)
	}
	if len(operations.Operations) != 1 || operations.Operations[0].Name != "test.hello" {
		t.Fatalf("operations = %#v", operations)
	}
	op, err := backend.InvokeOperation(context.Background(), management.OperationInvokeRequest{Ref: ref, Operation: "test.hello", Input: []byte(`{"name":"Ada"}`)})
	if err != nil {
		t.Fatalf("InvokeOperation: %v", err)
	}
	var opResult map[string]any
	if err := json.Unmarshal(op.Result, &opResult); err != nil {
		t.Fatalf("operation result JSON: %v", err)
	}
	if opResult["message"] != "hello Ada" {
		t.Fatalf("operation result = %#v", opResult)
	}

	datasources, err := backend.ListDatasources(context.Background(), management.DatasourceListRequest{Ref: ref})
	if err != nil {
		t.Fatalf("ListDatasources: %v", err)
	}
	if len(datasources.Datasources) != 1 || datasources.Datasources[0].Name != "test.items" {
		t.Fatalf("datasources = %#v", datasources)
	}
	ds, err := backend.CallDatasource(context.Background(), management.DatasourceCallRequest{Ref: ref, Capability: "search", Input: []byte(`{"query":"A","entity":"test.item"}`)})
	if err != nil {
		t.Fatalf("CallDatasource: %v", err)
	}
	var dsResult map[string]any
	if err := json.Unmarshal(ds.Result, &dsResult); err != nil {
		t.Fatalf("datasource result JSON: %v", err)
	}
	if dsResult["count"].(float64) != 1 {
		t.Fatalf("datasource result = %#v", dsResult)
	}
}

func TestRuntimePluginHelper(t *testing.T) {
	if os.Getenv("FLUXPLANE_LOCAL_TEST_PLUGIN") != "1" {
		return
	}
	pluginbinding.Serve(testRuntimePlugin())
	os.Exit(0)
}

func testRuntimeSpec() management.RuntimeSpec {
	return management.RuntimeSpec{
		Kind:    "stdio",
		Command: "/usr/bin/env",
		Args:    []string{"FLUXPLANE_LOCAL_TEST_PLUGIN=1", os.Args[0], "-test.run=TestRuntimePluginHelper", "--"},
	}
}

func testRuntimePlugin() *pluginbinding.Plugin {
	type helloInput struct {
		Name string `json:"name,omitempty"`
	}
	type helloOutput struct {
		Message string `json:"message"`
	}
	type searchInput struct {
		Query  string `json:"query,omitempty"`
		Entity string `json:"entity,omitempty"`
	}
	type searchOutput struct {
		Records []pluginbinding.DatasourceRecord `json:"records"`
		Count   int                              `json:"count"`
	}
	datasourceSpec := pluginbinding.TypedDatasourceSpec[searchInput, searchOutput]("test.items", "test.item", "Test items.", []string{pluginbinding.CapabilitySearch})
	return pluginbinding.Define(pluginbinding.ManifestSpec{
		Name: "test",
		Auth: []sdkmanifest.AuthMethod{{
			Name: "token",
			Fields: []sdkmanifest.AuthField{{
				Name:      "access_token",
				Required:  true,
				Sensitive: true,
			}},
		}},
		Datasources: []sdkmanifest.DatasourceSpec{datasourceSpec},
	},
		pluginbinding.WithAuthConnectText("connected"),
		pluginbinding.WithHostManagedAuthTest("test"),
		pluginbinding.RegisterOperation(pluginbinding.TypedOperationSpec[helloInput, helloOutput]("test.hello", "Say hello."),
			func(_ pluginbinding.Context, input helloInput) (helloOutput, error) {
				return helloOutput{Message: "hello " + input.Name}, nil
			}),
		pluginbinding.RegisterDatasourceSearch(datasourceSpec, func(ctx pluginbinding.Context, input searchInput) (searchOutput, error) {
			record := pluginbinding.NewDatasourceRecord(ctx.DatasourceSource(), "test.item", input.Query)
			return searchOutput{Records: []pluginbinding.DatasourceRecord{record}, Count: 1}, nil
		}),
	)
}
