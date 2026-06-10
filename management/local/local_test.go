package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/pluginbinding"
	"github.com/fluxplane/fluxplane-plugin/protocol"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
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

func TestCLIHostProcessRun(t *testing.T) {
	raw, err := (cliHost{}).CallHost(protocol.HostCapabilityProcessRun, sdkhost.ProcessRunRequest{
		Command:   os.Args[0],
		Args:      []string{"-test.run=TestHelperProcess", "--", "process-run"},
		Env:       []string{"GO_WANT_HELPER_PROCESS=1"},
		MaxStdout: 5,
		MaxStderr: 32,
	})
	if err != nil {
		t.Fatalf("CallHost: %v", err)
	}
	var resp sdkhost.ProcessRunResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ExitCode != 7 || resp.Stdout != "hello" || !resp.StdoutTruncated || resp.Stderr != "problem\n" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestCLIHostBlobStore(t *testing.T) {
	backend, err := New(WithPath(t.TempDir() + "/plugins.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	host := cliHost{backend: backend, plugin: "slack", instance: "default"}
	raw, err := host.CallHost(protocol.HostCapabilityBlobWrite, sdkhost.BlobWriteRequest{
		Ref:       "download/test",
		Content:   []byte("hello blob"),
		Filename:  "hello.txt",
		MediaType: "text/plain",
		Metadata:  map[string]string{"source": "test"},
	})
	if err != nil {
		t.Fatalf("BlobWrite: %v", err)
	}
	var written sdkhost.BlobRef
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatalf("BlobWrite JSON: %v", err)
	}
	if written.Ref != "download/test" || written.Filename != "hello.txt" || written.Size != int64(len("hello blob")) {
		t.Fatalf("written blob = %#v", written)
	}
	raw, err = host.CallHost(protocol.HostCapabilityBlobInfo, sdkhost.BlobInfoRequest{Ref: written.Ref})
	if err != nil {
		t.Fatalf("BlobInfo: %v", err)
	}
	var info sdkhost.BlobRef
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("BlobInfo JSON: %v", err)
	}
	if info.Ref != written.Ref || info.Size != written.Size {
		t.Fatalf("blob info = %#v", info)
	}
	raw, err = host.CallHost(protocol.HostCapabilityBlobRead, sdkhost.BlobReadRequest{Ref: written.Ref, MaxBytes: 5})
	if err != nil {
		t.Fatalf("BlobRead: %v", err)
	}
	var read sdkhost.BlobReadResponse
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatalf("BlobRead JSON: %v", err)
	}
	if string(read.Content) != "hello" || !read.Truncated || read.Blob.Ref != written.Ref {
		t.Fatalf("blob read = %#v", read)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, "hello world")
	fmt.Fprint(os.Stderr, "problem\n")
	os.Exit(7)
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
	t.Setenv("FLUXPLANE_TEST_ACCESS_TOKEN", "env-secret")
	t.Setenv("FLUXPLANE_TEST_URL", "https://test.example.com")
	autoConnected, err := backend.AuthAuto(context.Background(), management.AuthAutoRequest{Ref: ref, Instance: "env"})
	if err != nil {
		t.Fatalf("AuthAuto: %v", err)
	}
	if !autoConnected.Changed || len(autoConnected.Saved) != 1 || autoConnected.Saved[0] != "access_token" || len(autoConnected.Endpoints) != 1 || len(autoConnected.Missing) != 0 {
		t.Fatalf("auto connected = %#v", autoConnected)
	}
	autoStatus, err := backend.AuthStatus(context.Background(), management.AuthStatusRequest{Ref: ref, Instance: "env"})
	if err != nil {
		t.Fatalf("AuthStatus auto: %v", err)
	}
	if len(autoStatus.Auth) != 1 || autoStatus.Auth[0].Metadata["access_token_ref"] != "plugin/test/env/access_token" || autoStatus.Auth[0].Metadata["endpoint_ref"] == "" {
		t.Fatalf("auto status = %#v", autoStatus)
	}
	storedSecret, ok, err := backend.secretStore.LoadSecret(context.Background(), sharedsecret.Plugin("test", "env", "access_token"))
	if err != nil || !ok || storedSecret.Value != "env-secret" {
		t.Fatalf("stored secret ok=%v err=%v secret=%#v", ok, err, storedSecret)
	}
	status, err := backend.PluginStatus(context.Background(), management.StatusRequest{Ref: ref})
	if err != nil {
		t.Fatalf("PluginStatus endpoint config: %v", err)
	}
	var envInstance management.Instance
	for _, instance := range status.Instances {
		if instance.Name == "env" {
			envInstance = instance
			break
		}
	}
	if envInstance.Config["endpoint_ref"] == "" {
		t.Fatalf("env instance config = %#v", envInstance.Config)
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
	if !hasOperation(operations.Operations, "test.hello") {
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
	batch, err := backend.BatchOperations(context.Background(), management.OperationBatchRequest{
		Ref: ref,
		Calls: []protocol.OperationCall{{
			Name:  "test.hello",
			Input: []byte(`{"name":"Ada"}`),
		}},
	})
	if err != nil {
		t.Fatalf("BatchOperations: %v", err)
	}
	var batchResult protocol.OperationBatchResult
	if err := json.Unmarshal(batch.Result, &batchResult); err != nil {
		t.Fatalf("batch result JSON: %v", err)
	}
	if len(batchResult.Results) != 1 || !batchResult.Results[0].OK || batchResult.Results[0].ID != "1" {
		t.Fatalf("batch result = %#v", batchResult)
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
	listed, err := backend.CallDatasource(context.Background(), management.DatasourceCallRequest{Ref: ref, Capability: "list", Input: []byte(`{"entity":"test.item","limit":2}`)})
	if err != nil {
		t.Fatalf("CallDatasource list: %v", err)
	}
	var listResult map[string]any
	if err := json.Unmarshal(listed.Result, &listResult); err != nil {
		t.Fatalf("datasource list result JSON: %v", err)
	}
	if listResult["count"].(float64) != 2 {
		t.Fatalf("datasource list result = %#v", listResult)
	}
	batchGet, err := backend.CallDatasource(context.Background(), management.DatasourceCallRequest{Ref: ref, Capability: "batch_get", Input: []byte(`{"entity":"test.item","ids":["A","B"]}`)})
	if err != nil {
		t.Fatalf("CallDatasource batch_get: %v", err)
	}
	var batchGetResult map[string]any
	if err := json.Unmarshal(batchGet.Result, &batchGetResult); err != nil {
		t.Fatalf("datasource batch_get result JSON: %v", err)
	}
	if batchGetResult["count"].(float64) != 2 {
		t.Fatalf("datasource batch_get result = %#v", batchGetResult)
	}

	contexts, err := backend.ListContextProviders(context.Background(), management.ContextListRequest{Ref: ref})
	if err != nil {
		t.Fatalf("ListContextProviders: %v", err)
	}
	if len(contexts.Context) != 1 || contexts.Context[0].Name != "test.context" {
		t.Fatalf("contexts = %#v", contexts)
	}
	contextResult, err := backend.BuildContext(context.Background(), management.ContextBuildRequest{Ref: ref, Query: "Ada", Kinds: []string{"text"}, Limit: 1})
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	var built pluginbinding.ContextBuildResult
	if err := json.Unmarshal(contextResult.Result, &built); err != nil {
		t.Fatalf("context result JSON: %v", err)
	}
	if len(built.Blocks) != 1 || built.Blocks[0].Content != "context Ada" {
		t.Fatalf("context result = %#v", built)
	}
	// Before any index is built, a lookup falls through to the plugin and the
	// result carries an actionable hint (the manifest declares indexes).
	preLookup, err := backend.CallDatasource(context.Background(), management.DatasourceCallRequest{Ref: ref, Instance: "work", Capability: "lookup", Input: []byte(`{"text":"open item B","entity":"test.item"}`)})
	if err != nil {
		t.Fatalf("CallDatasource lookup before build: %v", err)
	}
	if !strings.Contains(preLookup.Hint, "index build test") {
		t.Fatalf("expected index-build hint before build, got %q", preLookup.Hint)
	}
	// The host index commands fail actionably on a never-built index instead of
	// silently returning zero matches.
	preHost := cliHost{backend: backend, plugin: ref.Name, instance: "work"}
	if _, err := preHost.CallHost(sdkhost.IndexLookupCommand, pluginbinding.DatasourceLookupInput{Text: "open item B", Entity: "test.item"}); err == nil || !strings.Contains(err.Error(), "index build test") {
		t.Fatalf("expected no-index host lookup error, got %v", err)
	}
	indexBuilt, err := backend.BuildIndex(context.Background(), management.IndexBuildRequest{Ref: ref, Instance: "work", Index: "test.items"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if !indexBuilt.Stored || indexBuilt.Index != "test.items" || indexBuilt.Records != 2 {
		t.Fatalf("index built = %#v", indexBuilt)
	}
	indexStatus, err := backend.IndexStatus(context.Background(), management.IndexStatusRequest{Ref: ref, Instance: "work"})
	if err != nil {
		t.Fatalf("IndexStatus: %v", err)
	}
	if len(indexStatus.Indexes) != 1 || indexStatus.Indexes[0].Records != 2 || len(indexStatus.Indexes[0].Details) != 1 {
		t.Fatalf("index status = %#v", indexStatus)
	}
	if indexStatus.Indexes[0].Details[0].Index != "test.items" {
		t.Fatalf("index status details = %#v", indexStatus.Indexes[0].Details)
	}
	indexedSearch, err := backend.CallDatasource(context.Background(), management.DatasourceCallRequest{Ref: ref, Instance: "work", Capability: "search", Input: []byte(`{"datasource":"test.items","query":"A","entity":"test.item"}`)})
	if err != nil {
		t.Fatalf("CallDatasource indexed search: %v", err)
	}
	var indexedSearchResult struct {
		Count   int `json:"count"`
		Records []struct {
			Entity string `json:"entity"`
			ID     string `json:"id"`
			Origin struct {
				Source string `json:"source"`
				Plugin string `json:"plugin"`
				Index  string `json:"index"`
			} `json:"origin"`
		} `json:"records"`
	}
	if err := json.Unmarshal(indexedSearch.Result, &indexedSearchResult); err != nil {
		t.Fatalf("indexed search JSON: %v", err)
	}
	if indexedSearchResult.Count != 1 || indexedSearchResult.Records[0].ID != "A" || indexedSearchResult.Records[0].Origin.Source != "host_index" {
		t.Fatalf("indexed search = %#v", indexedSearchResult)
	}
	indexedRecords, err := backend.CallDatasource(context.Background(), management.DatasourceCallRequest{Ref: ref, Instance: "work", Capability: "list", Input: []byte(`{"datasource":"test.items","entity":"test.item","limit":1}`)})
	if err != nil {
		t.Fatalf("CallDatasource indexed records: %v", err)
	}
	var indexedRecordsResult struct {
		Source  string `json:"source"`
		Count   int    `json:"count"`
		Records []struct {
			Entity string `json:"entity"`
			ID     string `json:"id"`
			Origin struct {
				Source string `json:"source"`
				Plugin string `json:"plugin"`
				Index  string `json:"index"`
			} `json:"origin"`
		} `json:"records"`
	}
	if err := json.Unmarshal(indexedRecords.Result, &indexedRecordsResult); err != nil {
		t.Fatalf("indexed records JSON: %v", err)
	}
	if indexedRecordsResult.Source != "host_index" || indexedRecordsResult.Count != 1 || indexedRecordsResult.Records[0].Origin.Index != "test.items" {
		t.Fatalf("indexed records = %#v", indexedRecordsResult)
	}
	indexedLookup, err := backend.CallDatasource(context.Background(), management.DatasourceCallRequest{Ref: ref, Instance: "work", Capability: "lookup", Input: []byte(`{"text":"open item B","entity":"test.item","limit":1}`)})
	if err != nil {
		t.Fatalf("CallDatasource indexed lookup: %v", err)
	}
	var indexedLookupResult struct {
		Count   int `json:"count"`
		Matches []struct {
			ID     string `json:"id"`
			Source struct {
				Source string `json:"source"`
				Plugin string `json:"plugin"`
				Index  string `json:"index"`
			} `json:"source"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(indexedLookup.Result, &indexedLookupResult); err != nil {
		t.Fatalf("indexed lookup JSON: %v", err)
	}
	if indexedLookupResult.Count != 1 || indexedLookupResult.Matches[0].ID != "B" || indexedLookupResult.Matches[0].Source.Source != "host_index" {
		t.Fatalf("indexed lookup = %#v", indexedLookupResult)
	}
	if indexedLookup.Hint != "" {
		t.Fatalf("indexed lookup should carry no hint, got %q", indexedLookup.Hint)
	}
	indexedGet, err := backend.CallDatasource(context.Background(), management.DatasourceCallRequest{Ref: ref, Instance: "work", Capability: "get", Input: []byte(`{"datasource":"test.items","entity":"test.item","id":"B"}`)})
	if err != nil {
		t.Fatalf("CallDatasource indexed get: %v", err)
	}
	var indexedGetResult struct {
		Record struct {
			Entity string `json:"entity"`
			ID     string `json:"id"`
			Origin struct {
				Source string `json:"source"`
				Plugin string `json:"plugin"`
				Index  string `json:"index"`
			} `json:"origin"`
		} `json:"record"`
	}
	if err := json.Unmarshal(indexedGet.Result, &indexedGetResult); err != nil {
		t.Fatalf("indexed get JSON: %v", err)
	}
	if indexedGetResult.Record.ID != "B" || indexedGetResult.Record.Origin.Index != "test.items" {
		t.Fatalf("indexed get = %#v", indexedGetResult)
	}
	indexedBatchGet, err := backend.CallDatasource(context.Background(), management.DatasourceCallRequest{Ref: ref, Instance: "work", Capability: "batch_get", Input: []byte(`{"datasource":"test.items","entity":"test.item","ids":["B","missing"]}`)})
	if err != nil {
		t.Fatalf("CallDatasource indexed batch_get: %v", err)
	}
	var indexedBatchGetResult struct {
		Source  string `json:"source"`
		Count   int    `json:"count"`
		Records []struct {
			Entity string `json:"entity"`
			ID     string `json:"id"`
			Origin struct {
				Source string `json:"source"`
				Plugin string `json:"plugin"`
				Index  string `json:"index"`
			} `json:"origin"`
		} `json:"records"`
		Errors []struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(indexedBatchGet.Result, &indexedBatchGetResult); err != nil {
		t.Fatalf("indexed batch_get JSON: %v", err)
	}
	if indexedBatchGetResult.Source != "host_index" || indexedBatchGetResult.Count != 1 || indexedBatchGetResult.Records[0].ID != "B" || indexedBatchGetResult.Records[0].Origin.Index != "test.items" || len(indexedBatchGetResult.Errors) != 1 || indexedBatchGetResult.Errors[0].ID != "missing" {
		t.Fatalf("indexed batch_get = %#v", indexedBatchGetResult)
	}
	host := cliHost{backend: backend, plugin: ref.Name, instance: "work"}
	hostLookup, err := host.CallHost(sdkhost.IndexLookupCommand, pluginbinding.DatasourceLookupInput{Text: "open item B", Entity: "test.item", Limit: 1})
	if err != nil {
		t.Fatalf("host index lookup: %v", err)
	}
	var hostLookupResult struct {
		Source  string `json:"source"`
		Count   int    `json:"count"`
		Matches []struct {
			ID     string `json:"id"`
			Source struct {
				Index string `json:"index"`
			} `json:"source"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(hostLookup, &hostLookupResult); err != nil {
		t.Fatalf("host index lookup JSON: %v", err)
	}
	if hostLookupResult.Source != "host_index" || hostLookupResult.Count != 1 || hostLookupResult.Matches[0].ID != "B" || hostLookupResult.Matches[0].Source.Index != "test.items" {
		t.Fatalf("host index lookup = %#v", hostLookupResult)
	}
	hostSearch, err := host.CallHost(sdkhost.IndexSearchCommand, pluginbinding.DatasourceSearchInput{Query: "A", Entity: "test.item", Limit: 1})
	if err != nil {
		t.Fatalf("host index search: %v", err)
	}
	var hostSearchResult struct {
		Source  string `json:"source"`
		Count   int    `json:"count"`
		Records []struct {
			ID string `json:"id"`
		} `json:"records"`
	}
	if err := json.Unmarshal(hostSearch, &hostSearchResult); err != nil {
		t.Fatalf("host index search JSON: %v", err)
	}
	if hostSearchResult.Source != "host_index" || hostSearchResult.Count != 1 || hostSearchResult.Records[0].ID != "A" {
		t.Fatalf("host index search = %#v", hostSearchResult)
	}
	hostGet, err := host.CallHost(sdkhost.IndexGetCommand, pluginbinding.DatasourceGetInput{Datasource: "test.items", Entity: "test.item", ID: "A"})
	if err != nil {
		t.Fatalf("host index get: %v", err)
	}
	var hostGetResult struct {
		Source string `json:"source"`
		Record struct {
			ID string `json:"id"`
		} `json:"record"`
	}
	if err := json.Unmarshal(hostGet, &hostGetResult); err != nil {
		t.Fatalf("host index get JSON: %v", err)
	}
	if hostGetResult.Source != "host_index" || hostGetResult.Record.ID != "A" {
		t.Fatalf("host index get = %#v", hostGetResult)
	}
	discovered, err := backend.DiscoverEndpoints(context.Background(), management.EndpointDiscoverRequest{Ref: ref, Product: "test", Namespace: "dev", Limit: 1})
	if err != nil {
		t.Fatalf("DiscoverEndpoints: %v", err)
	}
	var endpointResult map[string]any
	if err := json.Unmarshal(discovered.Result, &endpointResult); err != nil {
		t.Fatalf("endpoint result JSON: %v", err)
	}
	candidates, ok := endpointResult["candidates"].([]any)
	if !ok || len(candidates) != 1 {
		t.Fatalf("endpoint result = %#v", endpointResult)
	}

	saved, err := backend.SaveEndpoint(context.Background(), management.EndpointSaveRequest{Endpoint: fpendpoint.EndpointRef{
		ID:       "test-endpoint",
		Product:  "test",
		Protocol: "http",
		Source:   "manual",
		URL:      "http://example.test/",
		Labels:   map[string]string{"env": "test"},
	}})
	if err != nil {
		t.Fatalf("SaveEndpoint: %v", err)
	}
	if !saved.Saved || saved.Updated || saved.Endpoint.URL != "http://example.test" {
		t.Fatalf("saved = %#v", saved)
	}
	listedEndpoints, err := backend.ListEndpoints(context.Background(), management.EndpointListRequest{Product: "test"})
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if !hasEndpoint(listedEndpoints.Endpoints, "test-endpoint") {
		t.Fatalf("listed endpoints = %#v", listedEndpoints)
	}
	if !hasEndpointRecord(listedEndpoints.Records, "test-endpoint") {
		t.Fatalf("listed endpoint records = %#v", listedEndpoints.Records)
	}
	got, err := backend.GetEndpoint(context.Background(), management.EndpointGetRequest{ID: "@endpoint/test-endpoint"})
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if !got.Found || got.Endpoint.ID != "test-endpoint" || got.Endpoint.URL != "http://example.test" {
		t.Fatalf("got endpoint = %#v", got)
	}
	checkedAt := time.Date(2026, 6, 3, 1, 2, 3, 0, time.UTC)
	health, err := backend.SaveEndpointHealth(context.Background(), management.EndpointHealthRequest{
		ID: "@endpoint/test-endpoint",
		Health: fpendpoint.Health{
			OK:         true,
			CheckedAt:  checkedAt,
			Method:     "tcp_connect",
			DurationMS: 42,
			Details:    map[string]any{"address": "example.test:80"},
		},
	})
	if err != nil {
		t.Fatalf("SaveEndpointHealth: %v", err)
	}
	if !health.Saved || health.Record.LastHealth == nil || !health.Record.LastHealth.OK || health.Record.LastHealth.Method != "tcp_connect" {
		t.Fatalf("health = %#v", health)
	}
	updatedEndpoint, err := backend.SaveEndpoint(context.Background(), management.EndpointSaveRequest{Endpoint: fpendpoint.EndpointRef{
		ID:       "test-endpoint",
		Product:  "test",
		Protocol: "http",
		Source:   "manual",
		URL:      "http://example.test:8080",
	}})
	if err != nil {
		t.Fatalf("SaveEndpoint update: %v", err)
	}
	if !updatedEndpoint.Updated || updatedEndpoint.Record.LastHealth == nil || updatedEndpoint.Record.LastHealth.DurationMS != 42 {
		t.Fatalf("updated endpoint = %#v", updatedEndpoint)
	}
	removed, err := backend.RemoveEndpoint(context.Background(), management.EndpointRemoveRequest{ID: "test-endpoint"})
	if err != nil {
		t.Fatalf("RemoveEndpoint: %v", err)
	}
	if !removed.Removed || removed.ID != "test-endpoint" {
		t.Fatalf("removed = %#v", removed)
	}
	empty, err := backend.ListEndpoints(context.Background(), management.EndpointListRequest{Product: "test"})
	if err != nil {
		t.Fatalf("ListEndpoints after remove: %v", err)
	}
	if hasEndpoint(empty.Endpoints, "test-endpoint") {
		t.Fatalf("endpoints after remove = %#v", empty)
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

func hasOperation(operations []sdkmanifest.OperationSpec, name string) bool {
	for _, operation := range operations {
		if operation.Name == name {
			return true
		}
	}
	return false
}

func hasEndpoint(endpoints []fpendpoint.EndpointRef, id string) bool {
	for _, endpoint := range endpoints {
		if endpoint.ID == id {
			return true
		}
	}
	return false
}

func hasEndpointRecord(records []fpendpoint.Record, id string) bool {
	for _, record := range records {
		if record.ID == id && !record.CreatedAt.IsZero() {
			return true
		}
	}
	return false
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
	type listInput struct {
		Entity string `json:"entity,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}
	type listOutput struct {
		Records []pluginbinding.DatasourceRecord `json:"records"`
		Count   int                              `json:"count"`
	}
	type batchGetInput struct {
		Entity string   `json:"entity,omitempty"`
		IDs    []string `json:"ids,omitempty"`
	}
	type batchGetOutput struct {
		Records []pluginbinding.DatasourceRecord `json:"records"`
		Count   int                              `json:"count"`
	}
	type lookupInput struct {
		Text   string `json:"text,omitempty"`
		Entity string `json:"entity,omitempty"`
	}
	type lookupOutput struct {
		Matches []pluginbinding.DatasourceRecord `json:"matches"`
		Count   int                              `json:"count"`
	}
	datasourceSpec := pluginbinding.TypedDatasourceSpec[searchInput, searchOutput](
		"test.items",
		"test.item",
		"Test items.",
		[]string{pluginbinding.CapabilitySearch, pluginbinding.CapabilityList, pluginbinding.CapabilityBatchGet, pluginbinding.CapabilityGet, pluginbinding.CapabilityLookup, pluginbinding.CapabilityIndex},
	)
	contextSpec := pluginbinding.ContextSpec("test.context", "Test context.", pluginbinding.ContextKindText)
	plugin := pluginbinding.Define(pluginbinding.ManifestSpec{
		Name: "test",
		Auth: []sdkmanifest.AuthMethod{{
			Name: "token",
			Fields: []sdkmanifest.AuthField{{
				Name:      "access_token",
				Required:  true,
				Sensitive: true,
				Env:       []string{"FLUXPLANE_TEST_ACCESS_TOKEN"},
			}},
		}},
		Datasources: []sdkmanifest.DatasourceSpec{datasourceSpec},
		Indexes:     []sdkmanifest.IndexSpec{pluginbinding.Index("test.items", "Test items.", "test.item")},
		Context:     []sdkmanifest.ContextSpec{contextSpec},
		Endpoints: []sdkmanifest.EndpointSpec{{
			Name:     "test.endpoint",
			Products: []string{"test"},
			Env:      []string{"FLUXPLANE_TEST_URL"},
		}},
	},
		pluginbinding.WithAuthConnectText("connected"),
		pluginbinding.WithHostManagedAuthTest("test"),
		pluginbinding.WithIndexBuildOperation("test.index.build"),
		pluginbinding.RegisterOperation(pluginbinding.TypedOperationSpec[helloInput, helloOutput]("test.hello", "Say hello."),
			func(_ pluginbinding.Context, input helloInput) (helloOutput, error) {
				return helloOutput{Message: "hello " + input.Name}, nil
			}),
		pluginbinding.RegisterOperation(pluginbinding.TypedOperationSpec[pluginbinding.IndexBuildInput, pluginbinding.IndexBuildResult]("test.index.build", "Build test indexes."),
			func(ctx pluginbinding.Context, input pluginbinding.IndexBuildInput) (pluginbinding.IndexBuildResult, error) {
				selector, err := pluginbinding.NewIndexSelector(pluginbinding.InputMap(input), map[string]string{"test.items": "test.items", "test.item": "test.items"}, "test")
				if err != nil {
					return pluginbinding.IndexBuildResult{}, err
				}
				var records []pluginbinding.DatasourceRecord
				if selector.Includes("test.items") {
					records = []pluginbinding.DatasourceRecord{
						pluginbinding.NewDatasourceRecord(ctx.DatasourceSource(), "test.item", "A"),
						pluginbinding.NewDatasourceRecord(ctx.DatasourceSource(), "test.item", "B"),
					}
				}
				return pluginbinding.NewIndexBuildResult(pluginbinding.NewIndexResult("test.items", records, map[string]any{"entity": "test.item"})), nil
			}),
		pluginbinding.RegisterDatasourceSearch(datasourceSpec, func(ctx pluginbinding.Context, input searchInput) (searchOutput, error) {
			record := pluginbinding.NewDatasourceRecord(ctx.DatasourceSource(), "test.item", input.Query)
			return searchOutput{Records: []pluginbinding.DatasourceRecord{record}, Count: 1}, nil
		}),
		pluginbinding.RegisterDatasourceList(datasourceSpec, func(ctx pluginbinding.Context, input listInput) (listOutput, error) {
			limit := input.Limit
			if limit <= 0 {
				limit = 1
			}
			records := make([]pluginbinding.DatasourceRecord, 0, limit)
			for i := 0; i < limit; i++ {
				records = append(records, pluginbinding.NewDatasourceRecord(ctx.DatasourceSource(), "test.item", string(rune('A'+i))))
			}
			return listOutput{Records: records, Count: len(records)}, nil
		}),
		pluginbinding.RegisterDatasourceLookup(datasourceSpec, func(ctx pluginbinding.Context, input lookupInput) (lookupOutput, error) {
			// Like a real plugin probing its live API: empty matches, no error.
			return lookupOutput{}, nil
		}),
		pluginbinding.RegisterDatasourceBatchGet(datasourceSpec, func(ctx pluginbinding.Context, input batchGetInput) (batchGetOutput, error) {
			records := make([]pluginbinding.DatasourceRecord, 0, len(input.IDs))
			for _, id := range input.IDs {
				records = append(records, pluginbinding.NewDatasourceRecord(ctx.DatasourceSource(), "test.item", id))
			}
			return batchGetOutput{Records: records, Count: len(records)}, nil
		}),
		pluginbinding.RegisterContextProvider(contextSpec, func(_ pluginbinding.Context, input pluginbinding.ContextBuildInput) (pluginbinding.ContextBuildResult, error) {
			return pluginbinding.ContextBuildResult{Blocks: []sdkmanifest.ContextBlock{{
				ID:      "test",
				Content: "context " + input.Query,
			}}}, nil
		}),
	)
	plugin.Command(protocol.CommandEndpointsDiscover, func(ctx pluginbinding.Context) protocol.Response {
		var input struct {
			Product   string `json:"product,omitempty"`
			Namespace string `json:"namespace,omitempty"`
		}
		if len(ctx.Request.Payload) > 0 {
			_ = json.Unmarshal(ctx.Request.Payload, &input)
		}
		return protocol.OK(map[string]any{"candidates": []map[string]any{{
			"id":        "test-endpoint",
			"product":   input.Product,
			"namespace": input.Namespace,
			"url":       "https://example.test",
		}}})
	})
	return plugin
}
