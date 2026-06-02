// Package local provides a filesystem-backed plugin management backend.
package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	stdRuntime "runtime"
	"sort"
	"strings"
	"time"

	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/pluginruntime"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

// Backend stores plugin metadata in a local JSON state file.
type Backend struct {
	path             string
	binDir           string
	marketplace      sdkmanifest.Marketplace
	marketplacePaths []string
}

// Option configures a local backend.
type Option func(*Backend)

// WithPath stores plugin metadata at path.
func WithPath(path string) Option {
	return func(b *Backend) {
		b.path = path
	}
}

// WithBinDir stores marketplace-built plugin binaries at path.
func WithBinDir(path string) Option {
	return func(b *Backend) {
		b.binDir = path
	}
}

// WithMarketplace configures registry entries used to resolve bare installs.
func WithMarketplace(marketplace sdkmanifest.Marketplace) Option {
	return func(b *Backend) {
		b.marketplace = marketplace
	}
}

// WithMarketplacePath loads registry entries from path.
func WithMarketplacePath(path string) Option {
	return func(b *Backend) {
		if strings.TrimSpace(path) != "" {
			b.marketplacePaths = append(b.marketplacePaths, path)
		}
	}
}

// New returns a filesystem-backed plugin management backend.
func New(opts ...Option) (*Backend, error) {
	backend := &Backend{}
	for _, opt := range opts {
		opt(backend)
	}
	if strings.TrimSpace(backend.path) == "" {
		path, err := defaultStatePath()
		if err != nil {
			return nil, err
		}
		backend.path = path
	}
	if strings.TrimSpace(backend.binDir) == "" {
		backend.binDir = filepath.Join(filepath.Dir(backend.path), "bin")
	}
	if len(backend.marketplace.Plugins) == 0 {
		if len(backend.marketplacePaths) == 0 {
			backend.marketplacePaths = defaultMarketplacePaths()
		}
		marketplace, err := loadMarketplaceFiles(backend.marketplacePaths)
		if err != nil {
			return nil, err
		}
		backend.marketplace = marketplace
	}
	return backend, nil
}

func defaultStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("fluxplane-plugin: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".fluxplane", "plugins", "state.json"), nil
}

func defaultMarketplacePaths() []string {
	var paths []string
	if env := strings.TrimSpace(os.Getenv("FLUXPLANE_PLUGIN_MARKETPLACE")); env != "" {
		paths = append(paths, filepath.SplitList(env)...)
	}
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths,
			filepath.Join(cwd, "marketplace.json"),
			filepath.Join(cwd, "..", "fluxplane-plugins", "marketplace.json"),
		)
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".fluxplane", "plugins", "marketplace.json"))
	}
	return paths
}

func loadMarketplaceFiles(paths []string) (sdkmanifest.Marketplace, error) {
	out := sdkmanifest.Marketplace{Version: "1"}
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, fmt.Errorf("fluxplane-plugin: read marketplace %q: %w", path, err)
		}
		var marketplace sdkmanifest.Marketplace
		if err := json.Unmarshal(data, &marketplace); err != nil {
			return out, fmt.Errorf("fluxplane-plugin: parse marketplace %q: %w", path, err)
		}
		baseDir := filepath.Dir(path)
		for _, entry := range marketplace.Plugins {
			entry = resolveMarketplaceLocalPath(entry, baseDir)
			name := strings.TrimSpace(entry.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out.Plugins = append(out.Plugins, entry)
		}
	}
	sort.Slice(out.Plugins, func(i, j int) bool { return out.Plugins[i].Name < out.Plugins[j].Name })
	return out, nil
}

func resolveMarketplaceLocalPath(entry sdkmanifest.PluginEntry, baseDir string) sdkmanifest.PluginEntry {
	localPath := strings.TrimSpace(entry.LocalPath)
	if localPath == "" || filepath.IsAbs(localPath) {
		return entry
	}
	entry.LocalPath = filepath.Clean(filepath.Join(baseDir, localPath))
	return entry
}

type state struct {
	Plugins   map[string]storedPlugin   `json:"plugins,omitempty"`
	Instances map[string]storedInstance `json:"instances,omitempty"`
}

type storedPlugin struct {
	management.Plugin
	Config   map[string]any  `json:"config,omitempty"`
	Manifest json.RawMessage `json:"manifest,omitempty"`
}

type storedInstance struct {
	management.Instance
}

// InstallPlugin installs or records a plugin in the local store.
func (b *Backend) InstallPlugin(ctx context.Context, req management.InstallRequest) (management.InstallResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.InstallResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	entry, resolved := b.resolveMarketplace(req.Ref)
	if !resolved && !explicitInstall(req) {
		return management.InstallResult{}, fmt.Errorf("fluxplane-plugin: plugin %q is not in the marketplace; pass --source, --command, or --manifest for a local/dev install", req.Ref.Key())
	}
	st, err := b.readState()
	if err != nil {
		return management.InstallResult{}, err
	}
	key := req.Ref.Key()
	existing, exists := st.Plugins[key]
	if exists && !req.Force && !req.DryRun {
		return management.InstallResult{}, fmt.Errorf("fluxplane-plugin: plugin %q is already installed", key)
	}
	now := time.Now().UTC()
	labels := mergeLabels(nil, req.Labels)
	if resolved {
		labels = mergeLabels(marketplaceLabels(entry), labels)
	}
	runtime := req.Runtime
	if isEmptyRuntime(runtime) && resolved {
		if req.DryRun {
			runtime = marketplaceRuntime(entry)
		} else {
			preparedRuntime, preparedLabels, err := b.prepareMarketplaceRuntime(ctx, req.Ref, entry)
			if err != nil {
				return management.InstallResult{}, err
			}
			runtime = preparedRuntime
			labels = mergeLabels(labels, preparedLabels)
		}
	}
	plugin := storedPlugin{
		Plugin: management.Plugin{
			Ref:         req.Ref,
			Source:      req.Source,
			Description: marketplaceDescription(entry),
			Installed:   true,
			Enabled:     true,
			Runtime:     runtime,
			ManifestRef: firstNonEmpty(req.ManifestRef, entry.GoInstall),
			Labels:      labels,
			InstalledAt: now,
			UpdatedAt:   now,
		},
		Config:   req.Config,
		Manifest: copyRaw(req.Manifest),
	}
	if plugin.Source == "" && resolved {
		plugin.Source = "marketplace"
	}
	if exists {
		plugin.InstalledAt = existing.InstalledAt
		if plugin.Source == "" {
			plugin.Source = existing.Source
		}
		if isEmptyRuntime(plugin.Runtime) {
			plugin.Runtime = existing.Runtime
		}
		if plugin.ManifestRef == "" {
			plugin.ManifestRef = existing.ManifestRef
		}
		if len(plugin.Manifest) == 0 {
			plugin.Manifest = existing.Manifest
		}
		if plugin.Config == nil {
			plugin.Config = existing.Config
		}
		if len(plugin.Labels) == 0 {
			plugin.Labels = existing.Labels
		}
	}
	if req.DryRun {
		return management.InstallResult{Plugin: plugin.Plugin, Installed: false, Updated: exists, Message: "dry run"}, nil
	}
	st.Plugins[key] = plugin
	if err := b.ensureDefaultInstance(&st, plugin.Plugin, now); err != nil {
		return management.InstallResult{}, err
	}
	if err := b.writeState(st); err != nil {
		return management.InstallResult{}, err
	}
	return management.InstallResult{Plugin: plugin.Plugin, Installed: !exists, Updated: exists}, nil
}

// UpdatePlugin updates an installed plugin record and refreshes marketplace artifacts.
func (b *Backend) UpdatePlugin(ctx context.Context, req management.UpdateRequest) (management.UpdateResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.UpdateResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	st, err := b.readState()
	if err != nil {
		return management.UpdateResult{}, err
	}
	key := req.Ref.Key()
	plugin, ok := st.Plugins[key]
	if !ok {
		return management.UpdateResult{}, fmt.Errorf("fluxplane-plugin: plugin %q is not installed", key)
	}
	entry, resolved := b.resolveMarketplace(req.Ref)
	now := time.Now().UTC()
	if strings.TrimSpace(req.Source) != "" {
		plugin.Source = req.Source
	}
	if !isEmptyRuntime(req.Runtime) {
		plugin.Runtime = req.Runtime
	} else if resolved && strings.TrimSpace(plugin.Source) == "marketplace" {
		if req.DryRun {
			plugin.Runtime = marketplaceRuntime(entry)
		} else {
			oldPlugin := plugin
			runtime, labels, err := b.prepareMarketplaceRuntime(ctx, req.Ref, entry)
			if err != nil {
				return management.UpdateResult{}, err
			}
			plugin.Runtime = runtime
			plugin.Labels = mergeLabels(mergeLabels(marketplaceLabels(entry), plugin.Labels), labels)
			if err := b.removeOwnedArtifact(oldPlugin); err != nil {
				return management.UpdateResult{}, err
			}
		}
	}
	if strings.TrimSpace(req.ManifestRef) != "" {
		plugin.ManifestRef = req.ManifestRef
	}
	if len(req.Manifest) > 0 {
		plugin.Manifest = copyRaw(req.Manifest)
	}
	plugin.UpdatedAt = now
	if req.DryRun {
		return management.UpdateResult{Plugin: plugin.Plugin, Updated: false, Message: "dry run"}, nil
	}
	st.Plugins[key] = plugin
	if err := b.writeState(st); err != nil {
		return management.UpdateResult{}, err
	}
	return management.UpdateResult{Plugin: plugin.Plugin, Updated: true}, nil
}

// ListPlugins lists locally installed plugins.
func (b *Backend) ListPlugins(_ context.Context, req management.ListRequest) ([]management.Plugin, error) {
	st, err := b.readState()
	if err != nil {
		return nil, err
	}
	plugins := make([]management.Plugin, 0, len(st.Plugins))
	for _, plugin := range st.Plugins {
		if !req.All && !plugin.Enabled {
			continue
		}
		plugins = append(plugins, plugin.Plugin)
	}
	sortPlugins(plugins)
	return plugins, nil
}

// PluginStatus returns installed plugin and instance state.
func (b *Backend) PluginStatus(_ context.Context, req management.StatusRequest) (management.StatusResult, error) {
	st, err := b.readState()
	if err != nil {
		return management.StatusResult{}, err
	}
	plugins := make([]management.Plugin, 0, len(st.Plugins))
	for _, plugin := range st.Plugins {
		if strings.TrimSpace(req.Ref.Name) != "" && plugin.Ref.Key() != req.Ref.Key() && plugin.Ref.Name != req.Ref.Name {
			continue
		}
		plugins = append(plugins, plugin.Plugin)
	}
	sortPlugins(plugins)
	instances := make([]management.Instance, 0, len(st.Instances))
	for _, instance := range st.Instances {
		if strings.TrimSpace(req.Ref.Name) != "" && instance.Plugin.Key() != req.Ref.Key() && instance.Plugin.Name != req.Ref.Name {
			continue
		}
		instances = append(instances, instance.Instance)
	}
	sort.Slice(instances, func(i, j int) bool {
		if instances[i].Plugin.Key() == instances[j].Plugin.Key() {
			return instances[i].Name < instances[j].Name
		}
		return instances[i].Plugin.Key() < instances[j].Plugin.Key()
	})
	return management.StatusResult{Plugins: plugins, Instances: instances}, nil
}

// SetPluginEnabled changes local activation state.
func (b *Backend) SetPluginEnabled(_ context.Context, req management.SetEnabledRequest) (management.SetEnabledResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.SetEnabledResult{}, err
	}
	st, err := b.readState()
	if err != nil {
		return management.SetEnabledResult{}, err
	}
	key := req.Ref.Key()
	plugin, ok := st.Plugins[key]
	if !ok {
		return management.SetEnabledResult{}, fmt.Errorf("fluxplane-plugin: plugin %q is not installed", key)
	}
	changed := plugin.Enabled != req.Enabled
	plugin.Enabled = req.Enabled
	plugin.UpdatedAt = time.Now().UTC()
	if req.DryRun {
		return management.SetEnabledResult{Plugin: plugin.Plugin, Changed: false, Message: "dry run"}, nil
	}
	st.Plugins[key] = plugin
	if err := b.writeState(st); err != nil {
		return management.SetEnabledResult{}, err
	}
	return management.SetEnabledResult{Plugin: plugin.Plugin, Changed: changed}, nil
}

// RemovePlugin removes a plugin from the local store.
func (b *Backend) RemovePlugin(_ context.Context, req management.RemoveRequest) (management.RemoveResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.RemoveResult{}, err
	}
	st, err := b.readState()
	if err != nil {
		return management.RemoveResult{}, err
	}
	key := req.Ref.Key()
	plugin, ok := st.Plugins[key]
	if !ok {
		return management.RemoveResult{}, fmt.Errorf("fluxplane-plugin: plugin %q is not installed", key)
	}
	if req.DryRun {
		return management.RemoveResult{Ref: req.Ref, Removed: false, Message: "dry run"}, nil
	}
	if err := b.removeOwnedArtifact(plugin); err != nil {
		return management.RemoveResult{}, err
	}
	delete(st.Plugins, key)
	for instanceKey, instance := range st.Instances {
		if instance.Plugin.Key() == key {
			delete(st.Instances, instanceKey)
		}
	}
	if err := b.writeState(st); err != nil {
		return management.RemoveResult{}, err
	}
	return management.RemoveResult{Ref: req.Ref, Removed: true}, nil
}

// SearchPlugins searches marketplace entries plus the local store.
func (b *Backend) SearchPlugins(ctx context.Context, req management.SearchRequest) (management.SearchResult, error) {
	plugins, err := b.ListPlugins(ctx, management.ListRequest{All: true})
	if err != nil {
		return management.SearchResult{}, err
	}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	limit := req.Limit
	if limit <= 0 {
		limit = len(plugins)
	}
	byKey := map[string]management.Plugin{}
	for _, plugin := range plugins {
		if query != "" && !strings.Contains(strings.ToLower(plugin.Ref.Name), query) && !strings.Contains(strings.ToLower(plugin.Description), query) {
			continue
		}
		byKey[plugin.Ref.Key()] = plugin
	}
	for _, entry := range b.marketplace.Plugins {
		plugin := marketplacePlugin(entry)
		if plugin.Ref.Name == "" {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(plugin.Ref.Name), query) && !strings.Contains(strings.ToLower(plugin.Description), query) {
			continue
		}
		if existing, ok := byKey[plugin.Ref.Key()]; ok {
			existing.Description = firstNonEmpty(existing.Description, plugin.Description)
			byKey[plugin.Ref.Key()] = existing
			continue
		}
		byKey[plugin.Ref.Key()] = plugin
	}
	out := make([]management.Plugin, 0, len(byKey))
	for _, plugin := range byKey {
		out = append(out, plugin)
	}
	sortPlugins(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return management.SearchResult{Plugins: out}, nil
}

// PluginManifest returns the stored plugin manifest or asks the plugin runtime.
func (b *Backend) PluginManifest(ctx context.Context, req management.ManifestRequest) (management.ManifestResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.ManifestResult{}, err
	}
	st, err := b.readState()
	if err != nil {
		return management.ManifestResult{}, err
	}
	plugin, ok := st.Plugins[req.Ref.Key()]
	if !ok {
		return management.ManifestResult{}, fmt.Errorf("fluxplane-plugin: plugin %q is not installed", req.Ref.Key())
	}
	if len(plugin.Manifest) > 0 {
		return management.ManifestResult{Ref: req.Ref, Manifest: copyRaw(plugin.Manifest), Format: "json"}, nil
	}
	resp, err := b.invokePlugin(ctx, plugin, management.DefaultInstance, protocol.CommandManifest, nil)
	if err != nil {
		return management.ManifestResult{}, err
	}
	data, err := jsonPretty(resp.Result)
	if err != nil {
		return management.ManifestResult{}, err
	}
	return management.ManifestResult{Ref: req.Ref, Manifest: data, Format: "json"}, nil
}

// RunPlugin executes the configured plugin runtime command.
func (b *Backend) RunPlugin(ctx context.Context, req management.RunRequest) (management.RunResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.RunResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	st, err := b.readState()
	if err != nil {
		return management.RunResult{}, err
	}
	plugin, ok := st.Plugins[req.Ref.Key()]
	if !ok {
		return management.RunResult{}, fmt.Errorf("fluxplane-plugin: plugin %q is not installed", req.Ref.Key())
	}
	if !plugin.Enabled {
		return management.RunResult{}, fmt.Errorf("fluxplane-plugin: plugin %q is disabled", req.Ref.Key())
	}
	if len(req.Args) == 0 && strings.TrimSpace(plugin.Runtime.Kind) == "stdio" {
		resp, err := b.invokePlugin(ctx, plugin, management.DefaultInstance, protocol.CommandManifest, nil)
		if err != nil {
			return management.RunResult{}, err
		}
		data, err := jsonPretty(resp.Result)
		if err != nil {
			return management.RunResult{}, err
		}
		return management.RunResult{Ref: req.Ref, Message: "plugin runtime responded", Stdout: string(data)}, nil
	}
	command := strings.TrimSpace(plugin.Runtime.Command)
	if command == "" {
		command = strings.TrimSpace(plugin.Runtime.Path)
	}
	if command == "" {
		return management.RunResult{}, fmt.Errorf("fluxplane-plugin: plugin %q has no runnable runtime configured", req.Ref.Key())
	}
	args := append([]string(nil), plugin.Runtime.Args...)
	args = append(args, req.Args...)
	cmd := exec.CommandContext(ctx, command, args...)
	if dir := runtimeWorkingDir(plugin.Runtime); dir != "" {
		cmd.Dir = dir
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = strings.NewReader("")
	err = cmd.Run()
	result := management.RunResult{Ref: req.Ref, Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		result.Message = err.Error()
		return result, nil
	}
	return management.RunResult{}, fmt.Errorf("fluxplane-plugin: run plugin %q: %w", req.Ref.Key(), err)
}

// AuthMethods returns auth methods advertised by the plugin runtime.
func (b *Backend) AuthMethods(ctx context.Context, req management.AuthMethodsRequest) (management.AuthMethodsResult, error) {
	plugin, err := b.installedPlugin(req.Ref)
	if err != nil {
		return management.AuthMethodsResult{}, err
	}
	instance := normalizeInstance(req.Instance)
	resp, err := b.invokePlugin(ctx, plugin, instance, protocol.CommandAuthMethods, nil)
	if err != nil {
		return management.AuthMethodsResult{}, err
	}
	methods, err := protocol.DecodePayload[[]sdkmanifest.AuthMethod](resp.Result)
	if err != nil {
		return management.AuthMethodsResult{}, err
	}
	return management.AuthMethodsResult{Plugin: req.Ref, Instance: instance, Methods: methods}, nil
}

// AuthStatus returns stored auth state for a plugin instance.
func (b *Backend) AuthStatus(_ context.Context, req management.AuthStatusRequest) (management.AuthStatusResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.AuthStatusResult{}, err
	}
	st, err := b.readState()
	if err != nil {
		return management.AuthStatusResult{}, err
	}
	instance := b.instance(st, req.Ref, normalizeInstance(req.Instance))
	return management.AuthStatusResult{Plugin: req.Ref, Instance: instance.Name, Auth: append([]management.AuthState(nil), instance.Auth...)}, nil
}

// AuthConnect asks the plugin runtime to connect auth and records successful state.
func (b *Backend) AuthConnect(ctx context.Context, req management.AuthConnectRequest) (management.AuthResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.AuthResult{}, err
	}
	st, err := b.readState()
	if err != nil {
		return management.AuthResult{}, err
	}
	plugin, ok := st.Plugins[req.Ref.Key()]
	if !ok {
		return management.AuthResult{}, fmt.Errorf("fluxplane-plugin: plugin %q is not installed", req.Ref.Key())
	}
	now := time.Now().UTC()
	instance := b.instance(st, req.Ref, normalizeInstance(req.Instance))
	auth := management.AuthState{Method: normalizeMethod(req.Method), Connected: true, ConnectedAt: now, TestedAt: now, Metadata: req.Metadata}
	if req.DryRun {
		return management.AuthResult{Plugin: req.Ref, Instance: instance.Name, Auth: auth, Connected: true, Changed: false, Message: "dry run"}, nil
	}
	if _, err := b.invokePlugin(ctx, plugin, instance.Name, protocol.CommandAuthConnect, sdkmanifest.AuthMaterial{Method: auth.Method, Values: auth.Metadata}); err != nil {
		auth.Connected = false
		auth.Error = err.Error()
		return management.AuthResult{}, err
	}
	changed := upsertAuth(&instance, auth)
	instance.UpdatedAt = now
	st.Instances[instanceKey(req.Ref, instance.Name)] = storedInstance{Instance: instance}
	if err := b.writeState(st); err != nil {
		return management.AuthResult{}, err
	}
	return management.AuthResult{Plugin: req.Ref, Instance: instance.Name, Auth: auth, Connected: true, Changed: changed}, nil
}

// AuthTest asks the plugin runtime to test auth and records the latest test state.
func (b *Backend) AuthTest(ctx context.Context, req management.AuthTestRequest) (management.AuthResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.AuthResult{}, err
	}
	st, err := b.readState()
	if err != nil {
		return management.AuthResult{}, err
	}
	plugin, ok := st.Plugins[req.Ref.Key()]
	if !ok {
		return management.AuthResult{}, fmt.Errorf("fluxplane-plugin: plugin %q is not installed", req.Ref.Key())
	}
	instance := b.instance(st, req.Ref, normalizeInstance(req.Instance))
	method := normalizeMethod(req.Method)
	auth, connected := findAuth(instance, method)
	if !connected {
		auth = management.AuthState{Method: method, Connected: true}
	}
	auth.TestedAt = time.Now().UTC()
	if req.DryRun {
		return management.AuthResult{Plugin: req.Ref, Instance: instance.Name, Auth: auth, Connected: auth.Connected, Changed: false, Message: "dry run"}, nil
	}
	if _, err := b.invokePlugin(ctx, plugin, instance.Name, protocol.CommandAuthTest, sdkmanifest.AuthMaterial{Method: method, Values: auth.Metadata}); err != nil {
		auth.Connected = false
		auth.Error = err.Error()
		upsertAuth(&instance, auth)
		instance.UpdatedAt = auth.TestedAt
		st.Instances[instanceKey(req.Ref, instance.Name)] = storedInstance{Instance: instance}
		_ = b.writeState(st)
		return management.AuthResult{}, err
	}
	auth.Connected = true
	auth.Error = ""
	upsertAuth(&instance, auth)
	instance.UpdatedAt = auth.TestedAt
	st.Instances[instanceKey(req.Ref, instance.Name)] = storedInstance{Instance: instance}
	if err := b.writeState(st); err != nil {
		return management.AuthResult{}, err
	}
	return management.AuthResult{Plugin: req.Ref, Instance: instance.Name, Auth: auth, Connected: auth.Connected, Changed: true}, nil
}

// ListOperations returns operations advertised by the plugin runtime.
func (b *Backend) ListOperations(ctx context.Context, req management.OperationListRequest) (management.OperationListResult, error) {
	plugin, err := b.installedPlugin(req.Ref)
	if err != nil {
		return management.OperationListResult{}, err
	}
	instance := normalizeInstance(req.Instance)
	resp, err := b.invokePlugin(ctx, plugin, instance, protocol.CommandOperationsList, nil)
	if err != nil {
		return management.OperationListResult{}, err
	}
	operations, err := protocol.DecodePayload[[]sdkmanifest.OperationSpec](resp.Result)
	if err != nil {
		return management.OperationListResult{}, err
	}
	return management.OperationListResult{Plugin: req.Ref, Instance: instance, Operations: operations}, nil
}

// InvokeOperation calls one operation on the plugin runtime.
func (b *Backend) InvokeOperation(ctx context.Context, req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
	if strings.TrimSpace(req.Operation) == "" {
		return management.OperationInvokeResult{}, errors.New("fluxplane-plugin: operation name is required")
	}
	plugin, err := b.installedPlugin(req.Ref)
	if err != nil {
		return management.OperationInvokeResult{}, err
	}
	instance := normalizeInstance(req.Instance)
	resp, err := b.invokePlugin(ctx, plugin, instance, protocol.CommandOperationsCall, protocol.OperationCall{Name: strings.TrimSpace(req.Operation), Input: copyRaw(req.Input)})
	if err != nil {
		return management.OperationInvokeResult{}, err
	}
	return management.OperationInvokeResult{Plugin: req.Ref, Instance: instance, Operation: strings.TrimSpace(req.Operation), Result: copyRaw(resp.Result)}, nil
}

// ListDatasources returns datasources advertised by the plugin runtime.
func (b *Backend) ListDatasources(ctx context.Context, req management.DatasourceListRequest) (management.DatasourceListResult, error) {
	plugin, err := b.installedPlugin(req.Ref)
	if err != nil {
		return management.DatasourceListResult{}, err
	}
	instance := normalizeInstance(req.Instance)
	resp, err := b.invokePlugin(ctx, plugin, instance, protocol.CommandDatasourcesList, nil)
	if err != nil {
		return management.DatasourceListResult{}, err
	}
	datasources, err := protocol.DecodePayload[[]sdkmanifest.DatasourceSpec](resp.Result)
	if err != nil {
		return management.DatasourceListResult{}, err
	}
	return management.DatasourceListResult{Plugin: req.Ref, Instance: instance, Datasources: datasources}, nil
}

// CallDatasource calls one datasource capability on the plugin runtime.
func (b *Backend) CallDatasource(ctx context.Context, req management.DatasourceCallRequest) (management.DatasourceCallResult, error) {
	capability := strings.TrimSpace(req.Capability)
	if capability == "" {
		return management.DatasourceCallResult{}, errors.New("fluxplane-plugin: datasource capability is required")
	}
	command := datasourceCommand(capability)
	if command == "" {
		return management.DatasourceCallResult{}, fmt.Errorf("fluxplane-plugin: unsupported datasource capability %q", capability)
	}
	plugin, err := b.installedPlugin(req.Ref)
	if err != nil {
		return management.DatasourceCallResult{}, err
	}
	instance := normalizeInstance(req.Instance)
	resp, err := b.invokePlugin(ctx, plugin, instance, command, copyRaw(req.Input))
	if err != nil {
		return management.DatasourceCallResult{}, err
	}
	return management.DatasourceCallResult{Plugin: req.Ref, Instance: instance, Capability: capability, Result: copyRaw(resp.Result)}, nil
}

// AuthDisconnect clears stored auth state for a plugin instance.
func (b *Backend) AuthDisconnect(_ context.Context, req management.AuthDisconnectRequest) (management.AuthResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.AuthResult{}, err
	}
	st, err := b.readState()
	if err != nil {
		return management.AuthResult{}, err
	}
	instance := b.instance(st, req.Ref, normalizeInstance(req.Instance))
	method := normalizeMethod(req.Method)
	auth, ok := findAuth(instance, method)
	if !ok {
		return management.AuthResult{Plugin: req.Ref, Instance: instance.Name, Changed: false, Message: "auth state was not connected"}, nil
	}
	if req.DryRun {
		return management.AuthResult{Plugin: req.Ref, Instance: instance.Name, Auth: auth, Connected: false, Changed: false, Message: "dry run"}, nil
	}
	removeAuth(&instance, method)
	instance.UpdatedAt = time.Now().UTC()
	st.Instances[instanceKey(req.Ref, instance.Name)] = storedInstance{Instance: instance}
	if err := b.writeState(st); err != nil {
		return management.AuthResult{}, err
	}
	auth.Connected = false
	return management.AuthResult{Plugin: req.Ref, Instance: instance.Name, Auth: auth, Connected: false, Changed: true}, nil
}

func validateRef(ref management.Ref) error {
	if strings.TrimSpace(ref.Name) == "" {
		return errors.New("fluxplane-plugin: plugin name is required")
	}
	return nil
}

func (b *Backend) installedPlugin(ref management.Ref) (storedPlugin, error) {
	if err := validateRef(ref); err != nil {
		return storedPlugin{}, err
	}
	st, err := b.readState()
	if err != nil {
		return storedPlugin{}, err
	}
	plugin, ok := st.Plugins[ref.Key()]
	if !ok {
		return storedPlugin{}, fmt.Errorf("fluxplane-plugin: plugin %q is not installed", ref.Key())
	}
	if !plugin.Enabled {
		return storedPlugin{}, fmt.Errorf("fluxplane-plugin: plugin %q is disabled", ref.Key())
	}
	return plugin, nil
}

func (b *Backend) invokePlugin(ctx context.Context, plugin storedPlugin, instance, command string, payload any) (protocol.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	commandName := strings.TrimSpace(plugin.Runtime.Command)
	if commandName == "" {
		commandName = strings.TrimSpace(plugin.Runtime.Path)
	}
	if commandName == "" {
		return protocol.Response{}, fmt.Errorf("fluxplane-plugin: plugin %q has no runnable runtime configured", plugin.Ref.Key())
	}
	runtime := pluginruntime.Stdio(plugin.Ref.Name, commandName, plugin.Runtime.Args...)
	runtime.Dir = runtimeWorkingDir(plugin.Runtime)
	host, err := pluginruntime.NewHost(runtime)
	if err != nil {
		return protocol.Response{}, err
	}
	resp, err := host.Invoke(ctx, plugin.Ref.Name, command, payload, pluginruntime.WithInstance(normalizeInstance(instance)), pluginruntime.WithHostCaller(cliHost{}))
	if err != nil {
		return resp, fmt.Errorf("fluxplane-plugin: invoke %s on plugin %q: %w", command, plugin.Ref.Key(), err)
	}
	return resp, nil
}

type cliHost struct{}

func (cliHost) CallHost(command string, payload any) (json.RawMessage, error) {
	switch strings.TrimSpace(command) {
	case protocol.HostCapabilityEnvLookup:
		var req sdkhost.EnvLookupRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		value, found := os.LookupEnv(strings.TrimSpace(req.Key))
		return json.Marshal(sdkhost.EnvLookupResponse{Key: strings.TrimSpace(req.Key), Value: value, Found: found})
	case protocol.HostCapabilityProviderCall:
		var req sdkhost.ProviderCallRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.Provider) == "system" && strings.TrimSpace(req.Action) == "info" {
			result, err := localSystemInfo(req.Payload)
			if err != nil {
				return nil, err
			}
			return json.Marshal(sdkhost.ProviderCallResponse{Result: result})
		}
		return nil, fmt.Errorf("unsupported provider call %s.%s", req.Provider, req.Action)
	default:
		return nil, fmt.Errorf("unsupported host capability %q", command)
	}
}

func (cliHost) EmitHostEvent(string, any) error {
	return nil
}

func decodeHostPayload(payload any, out any) error {
	if out == nil {
		return nil
	}
	if raw, ok := payload.(json.RawMessage); ok {
		return json.Unmarshal(raw, out)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func localSystemInfo(raw json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Categories []string `json:"categories"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
	}
	if len(req.Categories) == 0 {
		req.Categories = []string{"os", "runtime", "user", "paths", "cpu", "time", "env", "network"}
	}
	system := map[string]any{}
	for _, category := range req.Categories {
		switch strings.TrimSpace(category) {
		case "os":
			system["os"] = localOSInfo()
		case "runtime":
			system["runtime"] = localRuntimeInfo()
		case "user":
			system["user"] = localUserInfo()
		case "paths":
			system["paths"] = localPathsInfo()
		case "cpu":
			system["cpu"] = map[string]any{"logical_cpus": stdRuntime.NumCPU(), "gomaxprocs": stdRuntime.GOMAXPROCS(0)}
		case "time":
			system["time"] = localTimeInfo()
		case "env":
			system["env"] = localEnvInfo()
		case "network":
			system["network"] = localNetworkInfo()
		}
	}
	return json.Marshal(map[string]any{
		"categories":   req.Categories,
		"generated_at": time.Now().UTC().Format(time.RFC3339Nano),
		"system":       system,
	})
}

func localOSInfo() map[string]any {
	info := map[string]any{"goos": stdRuntime.GOOS, "goarch": stdRuntime.GOARCH}
	if hostname, err := os.Hostname(); err == nil {
		info["hostname"] = hostname
	}
	return info
}

func localRuntimeInfo() map[string]any {
	info := map[string]any{"go_version": stdRuntime.Version(), "compiler": stdRuntime.Compiler, "process_id": os.Getpid(), "parent_id": os.Getppid()}
	if exe, err := os.Executable(); err == nil {
		info["executable"] = exe
	}
	return info
}

func localUserInfo() map[string]any {
	info := map[string]any{}
	if user, err := osuser.Current(); err == nil {
		info["username"] = user.Username
		info["name"] = user.Name
		info["uid"] = user.Uid
		info["gid"] = user.Gid
		info["home_dir"] = user.HomeDir
	}
	return info
}

func localPathsInfo() map[string]any {
	info := map[string]any{"temp_dir": os.TempDir()}
	if cwd, err := os.Getwd(); err == nil {
		info["working_dir"] = cwd
	}
	if home, err := os.UserHomeDir(); err == nil {
		info["home_dir"] = home
	}
	if exe, err := os.Executable(); err == nil {
		info["executable"] = exe
	}
	return info
}

func localTimeInfo() map[string]any {
	now := time.Now()
	name, offset := now.Zone()
	return map[string]any{
		"utc":            now.UTC().Format(time.RFC3339Nano),
		"local":          now.Format(time.RFC3339Nano),
		"unix":           now.Unix(),
		"timezone":       now.Location().String(),
		"zone_name":      name,
		"offset_seconds": offset,
	}
}

func localEnvInfo() map[string]any {
	values := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return map[string]any{"values": values}
}

func localNetworkInfo() map[string]any {
	info := map[string]any{}
	if hostname, err := os.Hostname(); err == nil {
		info["hostname"] = hostname
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		info["warnings"] = []string{err.Error()}
		return info
	}
	interfaces := make([]map[string]any, 0, len(ifaces))
	for _, iface := range ifaces {
		item := map[string]any{"name": iface.Name, "index": iface.Index, "mtu": iface.MTU}
		if iface.HardwareAddr.String() != "" {
			item["hardware_addr"] = iface.HardwareAddr.String()
		}
		if flags := interfaceFlags(iface.Flags); len(flags) > 0 {
			item["flags"] = flags
		}
		if addrs, err := iface.Addrs(); err == nil {
			values := make([]string, 0, len(addrs))
			for _, addr := range addrs {
				values = append(values, addr.String())
			}
			item["addresses"] = values
		}
		interfaces = append(interfaces, item)
	}
	info["interfaces"] = interfaces
	proxies := map[string]string{}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
		if value, ok := os.LookupEnv(key); ok {
			proxies[key] = value
		}
	}
	if len(proxies) > 0 {
		info["proxies"] = proxies
	}
	return info
}

func interfaceFlags(flags net.Flags) []string {
	var out []string
	if flags&net.FlagUp != 0 {
		out = append(out, "up")
	}
	if flags&net.FlagBroadcast != 0 {
		out = append(out, "broadcast")
	}
	if flags&net.FlagLoopback != 0 {
		out = append(out, "loopback")
	}
	if flags&net.FlagPointToPoint != 0 {
		out = append(out, "point_to_point")
	}
	if flags&net.FlagMulticast != 0 {
		out = append(out, "multicast")
	}
	return out
}

func (b *Backend) readState() (state, error) {
	st := state{Plugins: map[string]storedPlugin{}, Instances: map[string]storedInstance{}}
	data, err := os.ReadFile(b.path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("fluxplane-plugin: read local plugin state: %w", err)
	}
	if len(data) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("fluxplane-plugin: parse local plugin state: %w", err)
	}
	if st.Plugins == nil {
		st.Plugins = map[string]storedPlugin{}
	}
	if st.Instances == nil {
		st.Instances = map[string]storedInstance{}
	}
	return st, nil
}

func (b *Backend) writeState(st state) error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
		return fmt.Errorf("fluxplane-plugin: create local plugin state dir: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(b.path, data, 0o600); err != nil {
		return fmt.Errorf("fluxplane-plugin: write local plugin state: %w", err)
	}
	return nil
}

func (b *Backend) ensureDefaultInstance(st *state, plugin management.Plugin, now time.Time) error {
	key := instanceKey(plugin.Ref, management.DefaultInstance)
	if _, ok := st.Instances[key]; ok {
		return nil
	}
	st.Instances[key] = storedInstance{Instance: management.Instance{Plugin: plugin.Ref, Name: management.DefaultInstance, Enabled: true, CreatedAt: now, UpdatedAt: now}}
	return nil
}

func (b *Backend) instance(st state, ref management.Ref, name string) management.Instance {
	key := instanceKey(ref, name)
	if instance, ok := st.Instances[key]; ok {
		return instance.Instance
	}
	now := time.Now().UTC()
	return management.Instance{Plugin: ref, Name: name, Enabled: true, CreatedAt: now, UpdatedAt: now}
}

func instanceKey(ref management.Ref, name string) string {
	return ref.Key() + "#" + normalizeInstance(name)
}

func normalizeInstance(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return management.DefaultInstance
	}
	return name
}

func normalizeMethod(method string) string {
	method = strings.TrimSpace(method)
	if method == "" {
		return "default"
	}
	return method
}

func upsertAuth(instance *management.Instance, auth management.AuthState) bool {
	for i := range instance.Auth {
		if normalizeMethod(instance.Auth[i].Method) != normalizeMethod(auth.Method) {
			continue
		}
		changed := instance.Auth[i].Connected != auth.Connected || !instance.Auth[i].ConnectedAt.Equal(auth.ConnectedAt) || instance.Auth[i].Error != auth.Error
		instance.Auth[i] = auth
		return changed
	}
	instance.Auth = append(instance.Auth, auth)
	sortAuth(instance.Auth)
	return true
}

func findAuth(instance management.Instance, method string) (management.AuthState, bool) {
	method = normalizeMethod(method)
	for _, auth := range instance.Auth {
		if normalizeMethod(auth.Method) == method {
			return auth, true
		}
	}
	return management.AuthState{}, false
}

func removeAuth(instance *management.Instance, method string) {
	method = normalizeMethod(method)
	auth := instance.Auth[:0]
	for _, candidate := range instance.Auth {
		if normalizeMethod(candidate.Method) == method {
			continue
		}
		auth = append(auth, candidate)
	}
	instance.Auth = auth
}

func sortPlugins(plugins []management.Plugin) {
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Ref.Key() < plugins[j].Ref.Key() })
}

func sortAuth(auth []management.AuthState) {
	sort.Slice(auth, func(i, j int) bool { return normalizeMethod(auth[i].Method) < normalizeMethod(auth[j].Method) })
}

func isEmptyRuntime(runtime management.RuntimeSpec) bool {
	return strings.TrimSpace(runtime.Kind) == "" && strings.TrimSpace(runtime.Command) == "" && len(runtime.Args) == 0 && strings.TrimSpace(runtime.Path) == ""
}

func copyRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func jsonPretty(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.MarshalIndent(value, "", "  ")
}

func datasourceCommand(capability string) string {
	switch strings.TrimSpace(capability) {
	case "search":
		return protocol.CommandDatasourcesSearch
	case "get":
		return protocol.CommandDatasourcesGet
	case "lookup":
		return protocol.CommandDatasourcesLookup
	default:
		return ""
	}
}

func (b *Backend) resolveMarketplace(ref management.Ref) (sdkmanifest.PluginEntry, bool) {
	name := strings.TrimSpace(ref.Name)
	for _, entry := range b.marketplace.Plugins {
		if strings.TrimSpace(entry.Name) == name {
			return entry, true
		}
	}
	return sdkmanifest.PluginEntry{}, false
}

func explicitInstall(req management.InstallRequest) bool {
	return strings.TrimSpace(req.Source) != "" ||
		!isEmptyRuntime(req.Runtime) ||
		len(req.Manifest) > 0 ||
		strings.TrimSpace(req.ManifestRef) != ""
}

func marketplacePlugin(entry sdkmanifest.PluginEntry) management.Plugin {
	return management.Plugin{
		Ref:         management.Ref{Name: strings.TrimSpace(entry.Name)},
		Description: strings.TrimSpace(entry.Description),
		Source:      "marketplace",
		Runtime:     marketplaceRuntime(entry),
		ManifestRef: strings.TrimSpace(entry.GoInstall),
		Labels:      marketplaceLabels(entry),
	}
}

func marketplaceDescription(entry sdkmanifest.PluginEntry) string {
	return strings.TrimSpace(entry.Description)
}

func marketplaceRuntime(entry sdkmanifest.PluginEntry) management.RuntimeSpec {
	localPath := strings.TrimSpace(entry.LocalPath)
	binary := strings.TrimSpace(entry.Binary)
	if localPath != "" && binary != "" {
		if info, err := os.Stat(filepath.Join(localPath, "cmd", binary)); err == nil && info.IsDir() {
			return management.RuntimeSpec{Kind: "stdio", Command: "go", Args: []string{"run", "./cmd/" + binary}, Path: localPath}
		}
	}
	command := binary
	if command == "" {
		command = strings.TrimSpace(entry.GoInstall)
	}
	return management.RuntimeSpec{Kind: "stdio", Command: command, Path: localPath}
}

func (b *Backend) prepareMarketplaceRuntime(ctx context.Context, ref management.Ref, entry sdkmanifest.PluginEntry) (management.RuntimeSpec, map[string]string, error) {
	binary := marketplaceBinaryName(ref, entry)
	binPath := filepath.Join(b.binDir, executableName(binary))
	localPath := strings.TrimSpace(entry.LocalPath)
	if localPath != "" && strings.TrimSpace(entry.Binary) != "" {
		cmdDir := filepath.Join(localPath, "cmd", strings.TrimSpace(entry.Binary))
		if info, err := os.Stat(cmdDir); err == nil && info.IsDir() {
			if err := b.buildLocalMarketplaceBinary(ctx, localPath, strings.TrimSpace(entry.Binary), binPath); err != nil {
				return management.RuntimeSpec{}, nil, err
			}
			return cachedRuntime(binPath), artifactLabels(binPath, "local_build"), nil
		}
	}
	if strings.TrimSpace(entry.Binary) != "" {
		if path, err := exec.LookPath(strings.TrimSpace(entry.Binary)); err == nil {
			return management.RuntimeSpec{Kind: "stdio", Command: path}, artifactLabels(path, "path"), nil
		}
	}
	if strings.TrimSpace(entry.GoInstall) != "" {
		if err := b.goInstallMarketplaceBinary(ctx, entry.GoInstall); err != nil {
			return management.RuntimeSpec{}, nil, err
		}
		installed := filepath.Join(b.binDir, executableName(binary))
		if _, err := os.Stat(installed); err != nil {
			return management.RuntimeSpec{}, nil, fmt.Errorf("fluxplane-plugin: go install %q did not produce %q", entry.GoInstall, installed)
		}
		return cachedRuntime(installed), artifactLabels(installed, "go_install"), nil
	}
	return management.RuntimeSpec{}, nil, fmt.Errorf("fluxplane-plugin: marketplace plugin %q has no local command, PATH binary, or go_install source", ref.Key())
}

func (b *Backend) buildLocalMarketplaceBinary(ctx context.Context, localPath, binary, binPath string) error {
	if err := os.MkdirAll(filepath.Dir(binPath), 0o700); err != nil {
		return fmt.Errorf("fluxplane-plugin: create plugin bin dir: %w", err)
	}
	args := []string{"build", "-buildvcs=false", "-o", binPath, "./cmd/" + binary}
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = localPath
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("fluxplane-plugin: build marketplace plugin %q in %q: %s", binary, localPath, msg)
	}
	return nil
}

func (b *Backend) goInstallMarketplaceBinary(ctx context.Context, source string) error {
	if err := os.MkdirAll(b.binDir, 0o700); err != nil {
		return fmt.Errorf("fluxplane-plugin: create plugin bin dir: %w", err)
	}
	cmd := exec.CommandContext(ctx, "go", "install", strings.TrimSpace(source))
	cmd.Env = append(os.Environ(), "GOBIN="+b.binDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("fluxplane-plugin: go install marketplace plugin %q: %s", source, msg)
	}
	return nil
}

func (b *Backend) removeOwnedArtifact(plugin storedPlugin) error {
	if plugin.Labels["installed_binary_kind"] == "path" {
		return nil
	}
	path := strings.TrimSpace(plugin.Labels["installed_binary_path"])
	if path == "" {
		return nil
	}
	cleanPath := filepath.Clean(path)
	cleanBinDir := filepath.Clean(b.binDir)
	if cleanPath != cleanBinDir && !strings.HasPrefix(cleanPath, cleanBinDir+string(os.PathSeparator)) {
		return fmt.Errorf("fluxplane-plugin: refuse to remove plugin artifact outside bin dir: %s", path)
	}
	if err := os.Remove(cleanPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("fluxplane-plugin: remove plugin artifact %q: %w", path, err)
	}
	return nil
}

func marketplaceBinaryName(ref management.Ref, entry sdkmanifest.PluginEntry) string {
	if binary := strings.TrimSpace(entry.Binary); binary != "" {
		return binary
	}
	if source := strings.TrimSpace(entry.GoInstall); source != "" {
		source, _, _ = strings.Cut(source, "@")
		if base := strings.TrimSpace(filepath.Base(source)); base != "" && base != "." {
			return base
		}
	}
	return strings.TrimSpace(ref.Name)
}

func executableName(name string) string {
	name = strings.TrimSpace(name)
	if stdRuntime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

func cachedRuntime(path string) management.RuntimeSpec {
	return management.RuntimeSpec{Kind: "stdio", Command: path}
}

func artifactLabels(path, kind string) map[string]string {
	return map[string]string{
		"installed_binary_path": path,
		"installed_binary_kind": kind,
	}
}

func mergeLabels(base map[string]string, overlays ...map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range base {
		key = strings.TrimSpace(key)
		if key != "" {
			out[key] = value
		}
	}
	for _, overlay := range overlays {
		for key, value := range overlay {
			key = strings.TrimSpace(key)
			if key != "" {
				out[key] = value
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func marketplaceLabels(entry sdkmanifest.PluginEntry) map[string]string {
	labels := map[string]string{}
	if strings.TrimSpace(entry.Binary) != "" {
		labels["binary"] = strings.TrimSpace(entry.Binary)
	}
	if strings.TrimSpace(entry.GoInstall) != "" {
		labels["go_install"] = strings.TrimSpace(entry.GoInstall)
	}
	if strings.TrimSpace(entry.LocalPath) != "" {
		labels["local_path"] = strings.TrimSpace(entry.LocalPath)
	}
	for key, value := range entry.Metadata {
		key = strings.TrimSpace(key)
		if key != "" {
			labels["metadata."+key] = value
		}
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

func runtimeWorkingDir(runtime management.RuntimeSpec) string {
	path := strings.TrimSpace(runtime.Path)
	command := strings.TrimSpace(runtime.Command)
	if path == "" || path == command {
		return ""
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
