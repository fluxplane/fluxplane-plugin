// Package local provides a filesystem-backed plugin management backend.
package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	stdRuntime "runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/pluginruntime"
	"github.com/fluxplane/fluxplane-plugin/protocol"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
)

// Backend stores plugin metadata in a local JSON state file.
type Backend struct {
	path             string
	binDir           string
	marketplace      sdkmanifest.Marketplace
	marketplacePaths []string
	secretStore      sharedsecret.FileStore
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

// WithSecretStore configures where plugin auth secrets are persisted.
func WithSecretStore(store sharedsecret.FileStore) Option {
	return func(b *Backend) {
		b.secretStore = store
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
	if strings.TrimSpace(backend.secretStore.Dir) == "" {
		backend.secretStore = sharedsecret.NewFileStore("")
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
	Endpoints map[string]storedEndpoint `json:"endpoints,omitempty"`
	Processes map[string]storedProcess  `json:"processes,omitempty"`
}

type storedPlugin struct {
	management.Plugin
	Config   map[string]any  `json:"config,omitempty"`
	Manifest json.RawMessage `json:"manifest,omitempty"`
}

type storedInstance struct {
	management.Instance
}

type storedEndpoint struct {
	Endpoint  fpendpoint.Record `json:"endpoint"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
}

type storedProcess struct {
	ID           string            `json:"id"`
	Command      string            `json:"command"`
	Args         []string          `json:"args,omitempty"`
	Workdir      string            `json:"workdir,omitempty"`
	PID          int               `json:"pid,omitempty"`
	ProcessGroup int               `json:"process_group,omitempty"`
	LogPath      string            `json:"log_path,omitempty"`
	Plugin       string            `json:"plugin,omitempty"`
	Instance     string            `json:"instance,omitempty"`
	Group        string            `json:"group,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	StartedAt    time.Time         `json:"started_at,omitempty"`
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
	manifest, err := b.manifestForPlugin(ctx, plugin, normalizeInstance(req.Instance))
	if err != nil {
		return management.AuthResult{}, err
	}
	now := time.Now().UTC()
	instance := b.instance(st, req.Ref, normalizeInstance(req.Instance))
	method := authMethodName(req.Method, manifest.Auth)
	auth := management.AuthState{Method: method, Connected: true, ConnectedAt: now, TestedAt: now}
	if req.DryRun {
		auth.Metadata = authStateMetadata(manifest.Name, instance.Name, req.Metadata, manifestAuthFields(manifest.Auth, method), nil)
		auth.Metadata = mergeAuthEndpointMetadata(auth.Metadata, normalizedAuthEndpoints(req.Endpoints, manifest, req.Ref.Name, instance.Name))
		return management.AuthResult{Plugin: req.Ref, Instance: instance.Name, Auth: auth, Connected: true, Changed: false, Message: "dry run"}, nil
	}
	if _, err := b.invokePlugin(ctx, plugin, instance.Name, protocol.CommandAuthConnect, sdkmanifest.AuthMaterial{Method: auth.Method, Values: req.Metadata}); err != nil {
		auth.Connected = false
		auth.Error = err.Error()
		return management.AuthResult{}, err
	}
	savedRefs, err := b.saveAuthSecrets(ctx, firstNonEmpty(manifest.Name, req.Ref.Name), instance.Name, req.Metadata, manifestAuthFields(manifest.Auth, method))
	if err != nil {
		return management.AuthResult{}, err
	}
	endpoints := normalizedAuthEndpoints(req.Endpoints, manifest, req.Ref.Name, instance.Name)
	if err := applyAuthEndpoints(&st, &instance, endpoints, now); err != nil {
		return management.AuthResult{}, err
	}
	auth.Metadata = authStateMetadata(firstNonEmpty(manifest.Name, req.Ref.Name), instance.Name, req.Metadata, manifestAuthFields(manifest.Auth, method), savedRefs)
	auth.Metadata = mergeAuthEndpointMetadata(auth.Metadata, endpoints)
	changed := upsertAuth(&instance, auth)
	instance.UpdatedAt = now
	st.Instances[instanceKey(req.Ref, instance.Name)] = storedInstance{Instance: instance}
	if err := b.writeState(st); err != nil {
		return management.AuthResult{}, err
	}
	return management.AuthResult{Plugin: req.Ref, Instance: instance.Name, Auth: auth, Connected: true, Changed: changed}, nil
}

// AuthAuto imports manifest-declared auth fields from environment variables.
func (b *Backend) AuthAuto(ctx context.Context, req management.AuthAutoRequest) (management.AuthAutoResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.AuthAutoResult{}, err
	}
	plugin, err := b.installedPlugin(req.Ref)
	if err != nil {
		return management.AuthAutoResult{}, err
	}
	manifest, err := b.manifestForPlugin(ctx, plugin, normalizeInstance(req.Instance))
	if err != nil {
		return management.AuthAutoResult{}, err
	}
	methods, err := b.AuthMethods(ctx, management.AuthMethodsRequest{Ref: req.Ref, Instance: req.Instance})
	if err != nil {
		return management.AuthAutoResult{}, err
	}
	instance := normalizeInstance(req.Instance)
	result := management.AuthAutoResult{Plugin: req.Ref, Instance: instance}
	metadataByMethod := map[string]map[string]string{}
	for _, entry := range authFieldEntries(methods.Methods) {
		name := strings.TrimSpace(entry.field.Name)
		if name == "" {
			continue
		}
		if value, ok := firstEnvValue(entry.field.Env); ok {
			method := normalizeMethod(entry.method)
			if metadataByMethod[method] == nil {
				metadataByMethod[method] = map[string]string{}
			}
			metadataByMethod[method][name] = value
			result.Saved = append(result.Saved, name)
			continue
		}
		if entry.field.Required {
			result.Missing = append(result.Missing, name)
		} else {
			result.Skipped = append(result.Skipped, name)
		}
	}
	endpoints := authEndpointsFromEnv(manifest, req.Ref.Name, instance)
	result.Endpoints = endpoints
	if len(metadataByMethod) == 0 && len(endpoints) == 0 {
		if req.DryRun {
			result.Message = "dry run"
		}
		return result, nil
	}
	if req.DryRun {
		result.Changed = false
		result.Message = "dry run"
		return result, nil
	}
	for method, metadata := range metadataByMethod {
		auth, err := b.AuthConnect(ctx, management.AuthConnectRequest{
			Ref:       req.Ref,
			Instance:  instance,
			Method:    method,
			Metadata:  metadata,
			Endpoints: endpoints,
		})
		if err != nil {
			return result, err
		}
		result.Changed = result.Changed || auth.Changed
	}
	if len(metadataByMethod) == 0 && len(endpoints) > 0 {
		auth, err := b.AuthConnect(ctx, management.AuthConnectRequest{
			Ref:       req.Ref,
			Instance:  instance,
			Method:    authMethodName("", manifest.Auth),
			Endpoints: endpoints,
		})
		if err != nil {
			return result, err
		}
		result.Changed = result.Changed || auth.Changed
	}
	return result, nil
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

// BatchOperations calls multiple operations on one plugin runtime invocation.
func (b *Backend) BatchOperations(ctx context.Context, req management.OperationBatchRequest) (management.OperationBatchResult, error) {
	if len(req.Calls) == 0 {
		return management.OperationBatchResult{}, errors.New("fluxplane-plugin: at least one operation call is required")
	}
	calls := make([]protocol.OperationCall, 0, len(req.Calls))
	for i, call := range req.Calls {
		call.Name = strings.TrimSpace(call.Name)
		if call.Name == "" {
			return management.OperationBatchResult{}, fmt.Errorf("fluxplane-plugin: batch call %d operation name is required", i+1)
		}
		if call.ID == "" {
			call.ID = fmt.Sprintf("%d", i+1)
		}
		call.Input = copyRaw(call.Input)
		calls = append(calls, call)
	}
	plugin, err := b.installedPlugin(req.Ref)
	if err != nil {
		return management.OperationBatchResult{}, err
	}
	instance := normalizeInstance(req.Instance)
	resp, err := b.invokePlugin(ctx, plugin, instance, protocol.CommandOperationsBatch, protocol.OperationBatch{Calls: calls})
	if err != nil {
		return management.OperationBatchResult{}, err
	}
	return management.OperationBatchResult{Plugin: req.Ref, Instance: instance, Result: copyRaw(resp.Result)}, nil
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
	if result, handled, err := b.callIndexedDatasource(plugin, instance, command, req.Input); err != nil || handled {
		return management.DatasourceCallResult{Plugin: req.Ref, Instance: instance, Capability: capability, Result: result}, err
	}
	resp, err := b.invokePlugin(ctx, plugin, instance, command, copyRaw(req.Input))
	if err != nil {
		return management.DatasourceCallResult{}, err
	}
	return management.DatasourceCallResult{Plugin: req.Ref, Instance: instance, Capability: capability, Result: copyRaw(resp.Result)}, nil
}

// ListContextProviders returns context providers advertised by the plugin manifest.
func (b *Backend) ListContextProviders(ctx context.Context, req management.ContextListRequest) (management.ContextListResult, error) {
	manifestResult, err := b.PluginManifest(ctx, management.ManifestRequest{Ref: req.Ref})
	if err != nil {
		return management.ContextListResult{}, err
	}
	var manifest sdkmanifest.PluginManifest
	if err := json.Unmarshal(manifestResult.Manifest, &manifest); err != nil {
		return management.ContextListResult{}, fmt.Errorf("fluxplane-plugin: decode plugin manifest: %w", err)
	}
	return management.ContextListResult{Plugin: req.Ref, Instance: normalizeInstance(req.Instance), Context: append([]sdkmanifest.ContextSpec(nil), manifest.Context...)}, nil
}

// BuildContext asks a plugin runtime to build context blocks.
func (b *Backend) BuildContext(ctx context.Context, req management.ContextBuildRequest) (management.ContextBuildResult, error) {
	plugin, err := b.installedPlugin(req.Ref)
	if err != nil {
		return management.ContextBuildResult{}, err
	}
	instance := normalizeInstance(req.Instance)
	payload := copyRaw(req.Input)
	if len(payload) == 0 {
		payload, err = json.Marshal(struct {
			Query string   `json:"query,omitempty"`
			Kinds []string `json:"kinds,omitempty"`
			Limit int      `json:"limit,omitempty"`
		}{
			Query: strings.TrimSpace(req.Query),
			Kinds: append([]string(nil), req.Kinds...),
			Limit: req.Limit,
		})
		if err != nil {
			return management.ContextBuildResult{}, err
		}
	}
	resp, err := b.invokePlugin(ctx, plugin, instance, protocol.CommandContextBuild, payload)
	if err != nil {
		return management.ContextBuildResult{}, err
	}
	return management.ContextBuildResult{Plugin: req.Ref, Instance: instance, Result: copyRaw(resp.Result)}, nil
}

// ListEvidence returns evidence declarations advertised by the plugin manifest.
func (b *Backend) ListEvidence(ctx context.Context, req management.EvidenceListRequest) (management.EvidenceListResult, error) {
	manifestResult, err := b.PluginManifest(ctx, management.ManifestRequest{Ref: req.Ref})
	if err != nil {
		return management.EvidenceListResult{}, err
	}
	var manifest sdkmanifest.PluginManifest
	if err := json.Unmarshal(manifestResult.Manifest, &manifest); err != nil {
		return management.EvidenceListResult{}, fmt.Errorf("fluxplane-plugin: decode plugin manifest: %w", err)
	}
	return management.EvidenceListResult{
		Plugin:            req.Ref,
		Instance:          normalizeInstance(req.Instance),
		Observers:         append([]sdkmanifest.ObserverSpec(nil), manifest.Observers...),
		AssertionDerivers: append([]sdkmanifest.AssertionDeriverSpec(nil), manifest.AssertionDerivers...),
	}, nil
}

// ObserveEvidence asks a plugin runtime to produce evidence observations.
func (b *Backend) ObserveEvidence(ctx context.Context, req management.EvidenceObserveRequest) (management.EvidenceObserveResult, error) {
	plugin, err := b.installedPlugin(req.Ref)
	if err != nil {
		return management.EvidenceObserveResult{}, err
	}
	instance := normalizeInstance(req.Instance)
	payload := copyRaw(req.Input)
	if len(payload) == 0 {
		payload, err = json.Marshal(protocol.EvidenceObserveRequest{
			Phase:        req.Phase,
			Observations: append([]sdkmanifest.Observation(nil), req.Observations...),
		})
		if err != nil {
			return management.EvidenceObserveResult{}, err
		}
	}
	resp, err := b.invokePlugin(ctx, plugin, instance, protocol.CommandEvidenceObserve, payload)
	if err != nil {
		return management.EvidenceObserveResult{}, err
	}
	return management.EvidenceObserveResult{Plugin: req.Ref, Instance: instance, Result: copyRaw(resp.Result)}, nil
}

// DiscoverEndpoints asks a plugin runtime to discover endpoint candidates.
func (b *Backend) DiscoverEndpoints(ctx context.Context, req management.EndpointDiscoverRequest) (management.EndpointDiscoverResult, error) {
	plugin, err := b.installedPlugin(req.Ref)
	if err != nil {
		return management.EndpointDiscoverResult{}, err
	}
	instance := normalizeInstance(req.Instance)
	payload := copyRaw(req.Input)
	if len(payload) == 0 {
		payload, err = json.Marshal(struct {
			Product   string `json:"product,omitempty"`
			Context   string `json:"context,omitempty"`
			Namespace string `json:"namespace,omitempty"`
			Limit     int    `json:"limit,omitempty"`
		}{
			Product:   strings.TrimSpace(req.Product),
			Context:   strings.TrimSpace(req.Context),
			Namespace: strings.TrimSpace(req.Namespace),
			Limit:     req.Limit,
		})
		if err != nil {
			return management.EndpointDiscoverResult{}, err
		}
	}
	resp, err := b.invokePlugin(ctx, plugin, instance, protocol.CommandEndpointsDiscover, payload)
	if err != nil {
		return management.EndpointDiscoverResult{}, err
	}
	return management.EndpointDiscoverResult{Plugin: req.Ref, Instance: instance, Result: copyRaw(resp.Result)}, nil
}

// ListEndpoints returns locally stored endpoint refs.
func (b *Backend) ListEndpoints(_ context.Context, req management.EndpointListRequest) (management.EndpointListResult, error) {
	st, err := b.readState()
	if err != nil {
		return management.EndpointListResult{}, err
	}
	product := strings.TrimSpace(req.Product)
	endpoints := make([]fpendpoint.EndpointRef, 0, len(st.Endpoints))
	records := make([]fpendpoint.Record, 0, len(st.Endpoints))
	for _, stored := range st.Endpoints {
		record := stored.Endpoint
		record.EndpointRef = record.EndpointRef.Normalize()
		endpoint := record.EndpointRef
		if product != "" && endpoint.Product != product {
			continue
		}
		endpoints = append(endpoints, endpoint)
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Product == records[j].Product {
			return records[i].ID < records[j].ID
		}
		return records[i].Product < records[j].Product
	})
	sort.SliceStable(endpoints, func(i, j int) bool {
		if endpoints[i].Product == endpoints[j].Product {
			return endpoints[i].ID < endpoints[j].ID
		}
		return endpoints[i].Product < endpoints[j].Product
	})
	return management.EndpointListResult{Endpoints: endpoints, Records: records}, nil
}

// GetEndpoint returns one locally stored endpoint ref by id or @endpoint ref.
func (b *Backend) GetEndpoint(_ context.Context, req management.EndpointGetRequest) (management.EndpointGetResult, error) {
	id := fpendpoint.ParseRef(req.ID).ID()
	if id == "" {
		return management.EndpointGetResult{}, errors.New("fluxplane-plugin: endpoint id is required")
	}
	st, err := b.readState()
	if err != nil {
		return management.EndpointGetResult{}, err
	}
	stored, ok := st.Endpoints[id]
	if !ok {
		return management.EndpointGetResult{Found: false}, nil
	}
	record := stored.Endpoint
	record.EndpointRef = record.EndpointRef.Normalize()
	return management.EndpointGetResult{Endpoint: record.EndpointRef, Record: record, Found: true}, nil
}

// SaveEndpoint stores or updates one endpoint ref.
func (b *Backend) SaveEndpoint(_ context.Context, req management.EndpointSaveRequest) (management.EndpointSaveResult, error) {
	endpoint := req.Endpoint.Normalize()
	if err := endpoint.Validate(); err != nil {
		return management.EndpointSaveResult{}, err
	}
	if req.DryRun {
		return management.EndpointSaveResult{Endpoint: endpoint, Saved: false, Message: "dry run"}, nil
	}
	st, err := b.readState()
	if err != nil {
		return management.EndpointSaveResult{}, err
	}
	existing, existed := st.Endpoints[endpoint.ID]
	now := time.Now().UTC()
	record := fpendpoint.Record{EndpointRef: endpoint, CreatedAt: now, UpdatedAt: now}
	if existed {
		record.CreatedAt = existing.Endpoint.CreatedAt
		if record.CreatedAt.IsZero() {
			record.CreatedAt = now
		}
		record.LastHealth = existing.Endpoint.LastHealth
	}
	st.Endpoints[endpoint.ID] = storedEndpoint{Endpoint: record, UpdatedAt: now}
	if err := b.writeState(st); err != nil {
		return management.EndpointSaveResult{}, err
	}
	return management.EndpointSaveResult{Endpoint: endpoint, Record: record, Saved: true, Updated: existed}, nil
}

// SaveEndpointHealth stores the latest non-secret endpoint health probe.
func (b *Backend) SaveEndpointHealth(_ context.Context, req management.EndpointHealthRequest) (management.EndpointHealthResult, error) {
	id := fpendpoint.ParseRef(req.ID).ID()
	if id == "" {
		return management.EndpointHealthResult{}, errors.New("fluxplane-plugin: endpoint id is required")
	}
	health := req.Health
	if health.CheckedAt.IsZero() {
		health.CheckedAt = time.Now().UTC()
	} else {
		health.CheckedAt = health.CheckedAt.UTC()
	}
	st, err := b.readState()
	if err != nil {
		return management.EndpointHealthResult{}, err
	}
	stored, ok := st.Endpoints[id]
	if !ok {
		return management.EndpointHealthResult{}, fmt.Errorf("fluxplane-plugin: endpoint %q is not stored", id)
	}
	record := stored.Endpoint
	record.EndpointRef = record.EndpointRef.Normalize()
	record.LastHealth = &health
	record.UpdatedAt = time.Now().UTC()
	if req.DryRun {
		return management.EndpointHealthResult{ID: id, Record: record, Saved: false, Message: "dry run"}, nil
	}
	stored.Endpoint = record
	stored.UpdatedAt = record.UpdatedAt
	st.Endpoints[id] = stored
	if err := b.writeState(st); err != nil {
		return management.EndpointHealthResult{}, err
	}
	return management.EndpointHealthResult{ID: id, Record: record, Saved: true}, nil
}

// RemoveEndpoint deletes one locally stored endpoint ref.
func (b *Backend) RemoveEndpoint(_ context.Context, req management.EndpointRemoveRequest) (management.EndpointRemoveResult, error) {
	id := fpendpoint.ParseRef(req.ID).ID()
	if id == "" {
		return management.EndpointRemoveResult{}, errors.New("fluxplane-plugin: endpoint id is required")
	}
	st, err := b.readState()
	if err != nil {
		return management.EndpointRemoveResult{}, err
	}
	if _, ok := st.Endpoints[id]; !ok {
		return management.EndpointRemoveResult{ID: id, Removed: false, Message: "endpoint was not stored"}, nil
	}
	if req.DryRun {
		return management.EndpointRemoveResult{ID: id, Removed: false, Message: "dry run"}, nil
	}
	delete(st.Endpoints, id)
	if err := b.writeState(st); err != nil {
		return management.EndpointRemoveResult{}, err
	}
	return management.EndpointRemoveResult{ID: id, Removed: true}, nil
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
	resp, err := host.Invoke(ctx, plugin.Ref.Name, command, payload,
		pluginruntime.WithInstance(normalizeInstance(instance)),
		pluginruntime.WithConfig(b.instanceConfig(plugin.Ref, instance)),
		pluginruntime.WithHostCaller(cliHost{backend: b, plugin: plugin.Ref.Name, instance: normalizeInstance(instance)}),
	)
	if err != nil {
		return resp, fmt.Errorf("fluxplane-plugin: invoke %s on plugin %q: %w", command, plugin.Ref.Key(), err)
	}
	return resp, nil
}

func (b *Backend) instanceConfig(ref management.Ref, name string) map[string]any {
	st, err := b.readState()
	if err != nil {
		return nil
	}
	instance := b.instance(st, ref, normalizeInstance(name))
	return cloneAnyMap(instance.Config)
}

type cliHost struct {
	backend  *Backend
	plugin   string
	instance string
}

func (h cliHost) CallHost(command string, payload any) (json.RawMessage, error) {
	switch strings.TrimSpace(command) {
	case sdkhost.SecretGetCommand:
		var req struct {
			Purpose string `json:"purpose"`
		}
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		value, _, err := h.resolveSecret(req.Purpose)
		if err != nil {
			return nil, err
		}
		return json.Marshal(sdkhost.SecretMaterial{Purpose: strings.TrimSpace(req.Purpose), Value: value})
	case sdkhost.IndexLookupCommand:
		return h.indexLookup(payload)
	case sdkhost.IndexSearchCommand:
		return h.indexSearch(payload)
	case sdkhost.IndexGetCommand:
		return h.indexGet(payload)
	case protocol.HostCapabilityHTTPDo:
		return h.httpDo(payload)
	case sdkhost.EndpointResolve:
		endpoint, err := h.endpointRefFromPayload(payload)
		if err != nil {
			return nil, err
		}
		return json.Marshal(endpoint)
	case protocol.HostCapabilityEnvLookup:
		var req sdkhost.EnvLookupRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		value, found := os.LookupEnv(strings.TrimSpace(req.Key))
		return json.Marshal(sdkhost.EnvLookupResponse{Key: strings.TrimSpace(req.Key), Value: value, Found: found})
	case protocol.HostCapabilityBlobRead:
		return h.blobRead(payload)
	case protocol.HostCapabilityBlobWrite:
		return h.blobWrite(payload)
	case protocol.HostCapabilityBlobInfo:
		return h.blobInfo(payload)
	case protocol.HostCapabilityProcessRun:
		return h.processRun(payload)
	case protocol.HostCapabilityProcessStart:
		return h.processStart(payload)
	case protocol.HostCapabilityProcessStop:
		return h.processStop(payload)
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

func (h cliHost) blobRead(payload any) (json.RawMessage, error) {
	var req sdkhost.BlobReadRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	ref := h.blobRef(req.Ref, req.Path)
	blob, err := h.blobInfoForRef(ref)
	if err != nil {
		return nil, err
	}
	content, truncated, err := readBlobContent(blob.Path, req.MaxBytes)
	if err != nil {
		return nil, err
	}
	return json.Marshal(sdkhost.BlobReadResponse{Blob: blob, Content: content, Truncated: truncated})
}

func (h cliHost) blobWrite(payload any) (json.RawMessage, error) {
	var req sdkhost.BlobWriteRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	ref := h.blobRef(req.Ref, req.Path)
	if ref == "" {
		ref = "blob-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	path := h.blobContentPath(ref)
	if !req.Overwrite {
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("blob %q already exists", ref)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, append([]byte(nil), req.Content...), 0o600); err != nil {
		return nil, err
	}
	blob := sdkhost.BlobRef{
		Ref:       ref,
		Path:      path,
		MediaType: strings.TrimSpace(req.MediaType),
		Filename:  strings.TrimSpace(req.Filename),
		Size:      int64(len(req.Content)),
		Metadata:  cloneStringMap(req.Metadata),
	}
	if err := h.saveBlobInfo(blob); err != nil {
		return nil, err
	}
	return json.Marshal(blob)
}

func (h cliHost) blobInfo(payload any) (json.RawMessage, error) {
	var req sdkhost.BlobInfoRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	blob, err := h.blobInfoForRef(h.blobRef(req.Ref, req.Path))
	if err != nil {
		return nil, err
	}
	return json.Marshal(blob)
}

func (h cliHost) blobRef(ref, path string) string {
	ref = strings.TrimSpace(ref)
	if ref != "" {
		return ref
	}
	path = strings.TrimSpace(path)
	if path != "" {
		return path
	}
	return ""
}

func (h cliHost) blobDir() string {
	return filepath.Join(filepath.Dir(h.backend.path), "blobs", pathSegment(h.plugin), pathSegment(normalizeInstance(h.instance)))
}

func (h cliHost) blobContentPath(ref string) string {
	return filepath.Join(h.blobDir(), pathSegment(ref)+".bin")
}

func (h cliHost) blobInfoPath(ref string) string {
	return filepath.Join(h.blobDir(), pathSegment(ref)+".json")
}

func (h cliHost) blobInfoForRef(ref string) (sdkhost.BlobRef, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return sdkhost.BlobRef{}, fmt.Errorf("blob ref or path is required")
	}
	path := h.blobInfoPath(ref)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sdkhost.BlobRef{}, fmt.Errorf("blob %q not found", ref)
		}
		return sdkhost.BlobRef{}, err
	}
	var blob sdkhost.BlobRef
	if err := json.Unmarshal(data, &blob); err != nil {
		return sdkhost.BlobRef{}, err
	}
	if strings.TrimSpace(blob.Ref) == "" {
		blob.Ref = ref
	}
	if strings.TrimSpace(blob.Path) == "" {
		blob.Path = h.blobContentPath(ref)
	}
	info, err := os.Stat(blob.Path)
	if err == nil {
		blob.Size = info.Size()
	}
	return blob, nil
}

func (h cliHost) saveBlobInfo(blob sdkhost.BlobRef) error {
	path := h.blobInfoPath(blob.Ref)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(blob, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func readBlobContent(path string, maxBytes int64) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	if maxBytes <= 0 {
		content, err := io.ReadAll(file)
		return content, false, err
	}
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(content)) <= maxBytes {
		return content, false, nil
	}
	return content[:maxBytes], true, nil
}

func (h cliHost) processRun(payload any) (json.RawMessage, error) {
	var req sdkhost.ProcessRunRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	command, err := validatedProcessCommand(req.Command)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if req.TimeoutMS > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	cmd := exec.CommandContext(ctx, command, req.Args...)
	if strings.TrimSpace(req.Workdir) != "" {
		cmd.Dir = strings.TrimSpace(req.Workdir)
	}
	if len(req.Env) > 0 {
		cmd.Env = append(os.Environ(), req.Env...)
	}
	stdout := &limitedBuffer{limit: req.MaxStdout}
	stderr := &limitedBuffer{limit: req.MaxStderr}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()
	err = cmd.Run()
	duration := time.Since(start)
	timedOut := ctx.Err() == context.DeadlineExceeded
	exitCode := 0
	if err != nil {
		switch typed := err.(type) {
		case *exec.ExitError:
			exitCode = typed.ExitCode()
		default:
			if timedOut {
				exitCode = -1
			} else {
				return nil, err
			}
		}
	}
	resp := sdkhost.ProcessRunResponse{
		Command:         command,
		Args:            append([]string(nil), req.Args...),
		Workdir:         strings.TrimSpace(req.Workdir),
		ExitCode:        exitCode,
		TimedOut:        timedOut,
		DurationMS:      duration.Milliseconds(),
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
	}
	return json.Marshal(resp)
}

func (h cliHost) processStart(payload any) (json.RawMessage, error) {
	var req sdkhost.ProcessStartRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	if h.backend == nil {
		return nil, fmt.Errorf("process store is unavailable")
	}
	command, err := validatedProcessCommand(req.Command)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = processID(command)
	}
	logPath := strings.TrimSpace(req.LogPath)
	if logPath == "" {
		logPath = filepath.Join(filepath.Dir(h.backend.path), "processes", id+".log")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(command, req.Args...)
	if strings.TrimSpace(req.Workdir) != "" {
		cmd.Dir = strings.TrimSpace(req.Workdir)
	}
	if len(req.Env) > 0 {
		cmd.Env = append(os.Environ(), req.Env...)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	startedAt := time.Now().UTC()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	_ = logFile.Close()
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()

	record := storedProcess{
		ID: id, Command: command, Args: append([]string(nil), req.Args...), Workdir: strings.TrimSpace(req.Workdir),
		PID: pid, ProcessGroup: pid, LogPath: logPath, Plugin: h.plugin, Instance: h.instance, Group: strings.TrimSpace(req.Group),
		Tags: append([]string(nil), req.Tags...), Metadata: cloneStringMap(req.Metadata), StartedAt: startedAt,
	}
	if err := h.storeProcess(record); err != nil {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		return nil, err
	}
	if marker := strings.TrimSpace(req.StartedOK); marker != "" {
		timeout := time.Duration(req.TimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		if err := waitForLogMarker(logPath, marker, timeout); err != nil {
			_, _ = h.stopStoredProcess(sdkhost.ProcessStopRequest{ID: id, Signal: "SIGTERM"})
			return nil, err
		}
	}
	return json.Marshal(sdkhost.ProcessStartResponse{
		ID: id, Command: command, Args: append([]string(nil), req.Args...), Workdir: strings.TrimSpace(req.Workdir),
		PID: pid, ProcessGroup: pid, LogPath: logPath, StartedAt: startedAt,
	})
}

func (h cliHost) processStop(payload any) (json.RawMessage, error) {
	var req sdkhost.ProcessStopRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	resp, err := h.stopStoredProcess(req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(resp)
}

func (h cliHost) httpDo(payload any) (json.RawMessage, error) {
	var req sdkhost.HTTPRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	urlString, err := h.httpURL(req)
	if err != nil {
		return nil, err
	}
	headers, err := h.resolveHTTPAuth(req.Headers, req.Auth)
	if err != nil {
		return nil, err
	}
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = http.MethodGet
	}
	httpReq, err := http.NewRequest(method, urlString, bytes.NewReader(req.Body))
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			httpReq.Header.Set(key, value)
		}
	}
	if strings.TrimSpace(req.UserAgent) != "" {
		httpReq.Header.Set("User-Agent", strings.TrimSpace(req.UserAgent))
	}
	client := &http.Client{}
	if req.TimeoutMS > 0 {
		client.Timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	limit := int64(req.MaxBytes)
	if limit <= 0 {
		limit = 4 * 1024 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	truncated := int64(len(body)) > limit
	if truncated {
		body = body[:limit]
	}
	return json.Marshal(sdkhost.HTTPResponse{
		URL:         urlString,
		FinalURL:    resp.Request.URL.String(),
		Method:      method,
		Status:      resp.Status,
		StatusCode:  resp.StatusCode,
		Headers:     map[string][]string(resp.Header),
		ContentType: resp.Header.Get("Content-Type"),
		Body:        body,
		Truncated:   truncated,
		DurationMS:  time.Since(start).Milliseconds(),
	})
}

func (h cliHost) httpURL(req sdkhost.HTTPRequest) (string, error) {
	raw := strings.TrimSpace(req.URL)
	if raw == "" {
		endpoint, err := h.endpointRef(strings.TrimSpace(req.EndpointRef))
		if err != nil {
			return "", err
		}
		raw = endpoint.URL
	}
	if raw == "" {
		return "", fmt.Errorf("host HTTP URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(req.Path) != "" {
		joined, err := url.JoinPath(parsed.String(), req.Path)
		if err != nil {
			return "", err
		}
		parsed, err = url.Parse(joined)
		if err != nil {
			return "", err
		}
	}
	query := parsed.Query()
	for key, values := range req.Query {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (h cliHost) endpointRefFromPayload(payload any) (fpendpoint.EndpointRef, error) {
	var req struct {
		EndpointRef string `json:"endpoint_ref"`
	}
	if err := decodeHostPayload(payload, &req); err != nil {
		return fpendpoint.EndpointRef{}, err
	}
	return h.endpointRef(req.EndpointRef)
}

func (h cliHost) endpointRef(ref string) (fpendpoint.EndpointRef, error) {
	if h.backend == nil {
		return fpendpoint.EndpointRef{}, fmt.Errorf("endpoint store is unavailable")
	}
	id := fpendpoint.ParseRef(ref).ID()
	if id == "" {
		return fpendpoint.EndpointRef{}, fmt.Errorf("endpoint ref is required")
	}
	got, err := h.backend.GetEndpoint(context.Background(), management.EndpointGetRequest{ID: id})
	if err != nil {
		return fpendpoint.EndpointRef{}, err
	}
	if !got.Found {
		return fpendpoint.EndpointRef{}, fmt.Errorf("endpoint %q is not stored", id)
	}
	return got.Endpoint.Normalize(), nil
}

func (h cliHost) resolveHTTPAuth(headers map[string]string, auth *sdkhost.HTTPAuthRequest) (map[string]string, error) {
	out := map[string]string{}
	for key, value := range headers {
		out[key] = value
	}
	if auth == nil {
		return out, nil
	}
	if strings.TrimSpace(out["Authorization"]) == "" {
		if purpose := strings.TrimSpace(auth.BearerTokenPurpose); purpose != "" {
			if value, ok, err := h.resolveSecret(purpose); err != nil {
				return nil, err
			} else if ok {
				out["Authorization"] = "Bearer " + value
			}
		}
	}
	for header, purpose := range auth.HeaderPurposes {
		header = strings.TrimSpace(header)
		if header == "" || strings.TrimSpace(out[header]) != "" {
			continue
		}
		if value, ok, err := h.resolveSecret(purpose); err != nil {
			return nil, err
		} else if ok {
			out[header] = value
		}
	}
	return out, nil
}

func (h cliHost) resolveSecret(purpose string) (string, bool, error) {
	if h.backend == nil {
		return "", false, nil
	}
	ref := sharedsecret.Plugin(h.plugin, h.instance, sharedsecret.Slot(strings.TrimSpace(purpose)))
	material, ok, err := h.backend.secretStore.ResolveSecret(context.Background(), ref)
	if err != nil || !ok {
		return "", ok, err
	}
	value := strings.TrimSpace(material.String())
	if value == "" {
		return "", false, nil
	}
	return value, true, nil
}

func (h cliHost) storeProcess(record storedProcess) error {
	st, err := h.backend.readState()
	if err != nil {
		return err
	}
	if st.Processes == nil {
		st.Processes = map[string]storedProcess{}
	}
	st.Processes[record.ID] = record
	return h.backend.writeState(st)
}

func (h cliHost) stopStoredProcess(req sdkhost.ProcessStopRequest) (sdkhost.ProcessStopResponse, error) {
	if h.backend == nil {
		return sdkhost.ProcessStopResponse{}, fmt.Errorf("process store is unavailable")
	}
	id := strings.TrimSpace(req.ID)
	st, err := h.backend.readState()
	if err != nil {
		return sdkhost.ProcessStopResponse{}, err
	}
	record, found := st.Processes[id]
	pid := req.PID
	pgid := req.ProcessGroup
	if found {
		if pid == 0 {
			pid = record.PID
		}
		if pgid == 0 {
			pgid = record.ProcessGroup
		}
	}
	signalName := firstNonEmpty(req.Signal, "SIGTERM")
	sig := processSignal(signalName)
	resp := sdkhost.ProcessStopResponse{ID: id, Signal: signalName}
	if pgid != 0 {
		if err := syscall.Kill(-pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			resp.Error = err.Error()
			return resp, nil
		}
		resp.Stopped = true
	} else if pid != 0 {
		proc, err := os.FindProcess(pid)
		if err != nil {
			resp.Error = err.Error()
			return resp, nil
		}
		if err := proc.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
			resp.Error = err.Error()
			return resp, nil
		}
		resp.Stopped = true
	} else {
		resp.Error = "process id or process group is required"
		return resp, nil
	}
	if found {
		delete(st.Processes, id)
		_ = h.backend.writeState(st)
	}
	return resp, nil
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
	st := state{Plugins: map[string]storedPlugin{}, Instances: map[string]storedInstance{}, Endpoints: map[string]storedEndpoint{}, Processes: map[string]storedProcess{}}
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
	if st.Endpoints == nil {
		st.Endpoints = map[string]storedEndpoint{}
	}
	if st.Processes == nil {
		st.Processes = map[string]storedProcess{}
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

func (b *Backend) manifestForPlugin(ctx context.Context, plugin storedPlugin, instance string) (sdkmanifest.PluginManifest, error) {
	if len(plugin.Manifest) > 0 {
		var manifest sdkmanifest.PluginManifest
		if err := json.Unmarshal(plugin.Manifest, &manifest); err != nil {
			return sdkmanifest.PluginManifest{}, fmt.Errorf("fluxplane-plugin: decode stored plugin manifest: %w", err)
		}
		if strings.TrimSpace(manifest.Name) == "" {
			manifest.Name = plugin.Ref.Name
		}
		return manifest, nil
	}
	resp, err := b.invokePlugin(ctx, plugin, normalizeInstance(instance), protocol.CommandManifest, nil)
	if err != nil {
		return sdkmanifest.PluginManifest{}, err
	}
	manifest, err := protocol.DecodePayload[sdkmanifest.PluginManifest](resp.Result)
	if err != nil {
		return sdkmanifest.PluginManifest{}, err
	}
	if strings.TrimSpace(manifest.Name) == "" {
		manifest.Name = plugin.Ref.Name
	}
	return manifest, nil
}

func instanceKey(ref management.Ref, name string) string {
	return ref.Key() + "#" + normalizeInstance(name)
}

type authFieldEntry struct {
	method string
	field  sdkmanifest.AuthField
}

func authFieldEntries(methods []sdkmanifest.AuthMethod) []authFieldEntry {
	seen := map[string]bool{}
	var out []authFieldEntry
	for _, method := range methods {
		methodName := normalizeMethod(method.Name)
		for _, field := range method.Fields {
			name := strings.TrimSpace(field.Name)
			if name == "" || seen[name] {
				continue
			}
			field.Name = name
			if len(field.Env) == 0 {
				field.Env = append(field.Env, method.Env...)
			}
			seen[name] = true
			out = append(out, authFieldEntry{method: methodName, field: field})
		}
	}
	return out
}

func manifestAuthFields(methods []sdkmanifest.AuthMethod, method string) map[string]sdkmanifest.AuthField {
	method = authMethodName(method, methods)
	out := map[string]sdkmanifest.AuthField{}
	for _, candidate := range methods {
		if normalizeMethod(candidate.Name) != method {
			continue
		}
		for _, field := range candidate.Fields {
			name := strings.TrimSpace(field.Name)
			if name != "" {
				field.Name = name
				out[name] = field
			}
		}
		return out
	}
	return out
}

func authMethodName(requested string, methods []sdkmanifest.AuthMethod) string {
	requested = normalizeMethod(requested)
	if requested != "default" {
		return requested
	}
	for _, method := range methods {
		if normalizeMethod(method.Name) == requested {
			return requested
		}
	}
	for _, method := range methods {
		if name := normalizeMethod(method.Name); name != "" && name != "default" {
			return name
		}
	}
	return requested
}

func (b *Backend) saveAuthSecrets(ctx context.Context, plugin, instance string, values map[string]string, fields map[string]sdkmanifest.AuthField) (map[string]sharedsecret.Ref, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := map[string]sharedsecret.Ref{}
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		ref := sharedsecret.Plugin(plugin, instance, sharedsecret.Slot(key))
		kind := sharedsecret.KindBearerToken
		if field, ok := fields[key]; ok && !field.Secret && !field.Sensitive {
			kind = sharedsecret.KindAPIKey
		}
		if err := b.secretStore.SaveSecret(ctx, sharedsecret.StoredSecret{Ref: ref, Kind: kind, Value: value}); err != nil {
			return nil, err
		}
		out[key] = ref
	}
	return out, nil
}

func authStateMetadata(plugin, instance string, values map[string]string, fields map[string]sdkmanifest.AuthField, saved map[string]sharedsecret.Ref) map[string]string {
	if len(values) == 0 && len(saved) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		ref, ok := saved[key]
		if !ok {
			ref = sharedsecret.Plugin(plugin, instance, sharedsecret.Slot(key))
		}
		if field, known := fields[key]; known && !field.Secret && !field.Sensitive {
			out[key] = strings.TrimSpace(value)
			continue
		}
		out[key+"_ref"] = ref.ResourceName()
	}
	return out
}

func authEndpointsFromEnv(manifest sdkmanifest.PluginManifest, plugin, instance string) []management.AuthEndpoint {
	var out []management.AuthEndpoint
	for _, spec := range manifest.Endpoints {
		if value, ok := firstEnvValue(spec.Env); ok {
			out = append(out, management.AuthEndpoint{
				Name:    strings.TrimSpace(spec.Name),
				URL:     value,
				Product: firstString(spec.Products, plugin),
			})
		}
	}
	return normalizedAuthEndpoints(out, manifest, plugin, instance)
}

func normalizedAuthEndpoints(endpoints []management.AuthEndpoint, manifest sdkmanifest.PluginManifest, plugin, instance string) []management.AuthEndpoint {
	if len(endpoints) == 0 {
		return nil
	}
	byName := endpointSpecByName(manifest.Endpoints)
	defaultSpec := firstEndpointSpec(manifest.Endpoints)
	out := make([]management.AuthEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		name := strings.TrimSpace(endpoint.Name)
		spec, ok := byName[name]
		if name == "" || !ok {
			spec = defaultSpec
			if name == "" {
				name = strings.TrimSpace(spec.Name)
			}
		}
		product := strings.TrimSpace(endpoint.Product)
		if product == "" {
			product = firstString(spec.Products, plugin)
		}
		ref := fpendpoint.EndpointRef{
			ID:      strings.TrimSpace(endpoint.ID),
			URL:     strings.TrimSpace(endpoint.URL),
			Product: product,
			Source:  "auth",
		}.Normalize()
		if ref.URL == "" {
			continue
		}
		out = append(out, management.AuthEndpoint{Name: name, ID: ref.ID, URL: ref.URL, Product: ref.Product})
	}
	return out
}

func applyAuthEndpoints(st *state, instance *management.Instance, endpoints []management.AuthEndpoint, now time.Time) error {
	if len(endpoints) == 0 {
		return nil
	}
	if st.Endpoints == nil {
		st.Endpoints = map[string]storedEndpoint{}
	}
	if instance.Config == nil {
		instance.Config = map[string]any{}
	}
	endpointRefs := configMap(instance.Config["endpoint_refs"])
	var firstID string
	for _, endpoint := range endpoints {
		ref := fpendpoint.EndpointRef{ID: endpoint.ID, URL: endpoint.URL, Product: endpoint.Product, Source: "auth"}.Normalize()
		if err := ref.Validate(); err != nil {
			return err
		}
		existing, existed := st.Endpoints[ref.ID]
		record := fpendpoint.Record{EndpointRef: ref, CreatedAt: now, UpdatedAt: now}
		if existed {
			record.CreatedAt = existing.Endpoint.CreatedAt
			if record.CreatedAt.IsZero() {
				record.CreatedAt = now
			}
			record.LastHealth = existing.Endpoint.LastHealth
		}
		st.Endpoints[ref.ID] = storedEndpoint{Endpoint: record, UpdatedAt: now}
		if firstID == "" {
			firstID = ref.ID
		}
		if endpoint.Name != "" {
			endpointRefs[endpoint.Name] = ref.ID
		}
	}
	if firstID != "" {
		instance.Config["endpoint_ref"] = firstID
	}
	if len(endpointRefs) > 0 {
		instance.Config["endpoint_refs"] = endpointRefs
	}
	return nil
}

func mergeAuthEndpointMetadata(metadata map[string]string, endpoints []management.AuthEndpoint) map[string]string {
	if len(endpoints) == 0 {
		return metadata
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	for i, endpoint := range endpoints {
		if endpoint.ID == "" {
			continue
		}
		if i == 0 {
			metadata["endpoint_ref"] = endpoint.ID
		}
		if endpoint.Name != "" {
			metadata["endpoint_ref."+endpoint.Name] = endpoint.ID
		}
	}
	return metadata
}

func endpointSpecByName(specs []sdkmanifest.EndpointSpec) map[string]sdkmanifest.EndpointSpec {
	out := map[string]sdkmanifest.EndpointSpec{}
	for _, spec := range specs {
		if name := strings.TrimSpace(spec.Name); name != "" {
			out[name] = spec
		}
	}
	return out
}

func firstEndpointSpec(specs []sdkmanifest.EndpointSpec) sdkmanifest.EndpointSpec {
	for _, spec := range specs {
		if strings.TrimSpace(spec.Name) != "" || len(spec.Products) > 0 {
			return spec
		}
	}
	return sdkmanifest.EndpointSpec{}
}

func firstString(values []string, fallback string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return strings.TrimSpace(fallback)
}

func configMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case map[string]string:
		out := map[string]any{}
		for key, value := range typed {
			out[key] = value
		}
		return out
	default:
		return map[string]any{}
	}
}

func firstEnvValue(keys []string) (string, bool) {
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value, true
		}
	}
	return "", false
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
		changed := instance.Auth[i].Connected != auth.Connected || !instance.Auth[i].ConnectedAt.Equal(auth.ConnectedAt) || instance.Auth[i].Error != auth.Error || !stringMapEqual(instance.Auth[i].Metadata, auth.Metadata)
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

func stringMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func validatedProcessCommand(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("host process command is required")
	}
	if strings.ContainsAny(command, "\n\r;&|<>$`") {
		return "", fmt.Errorf("host process command must be an executable name or path, not shell syntax")
	}
	return command, nil
}

func processID(command string) string {
	base := strings.TrimSpace(filepath.Base(command))
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "process"
	}
	return base + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func processSignal(name string) syscall.Signal {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "SIGKILL", "KILL":
		return syscall.SIGKILL
	case "SIGINT", "INT":
		return syscall.SIGINT
	default:
		return syscall.SIGTERM
	}
}

func waitForLogMarker(path, marker string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), marker) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process did not report readiness marker %q", marker)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.limit <= 0 {
		_, _ = b.buf.Write(p)
		return len(p), nil
	}
	remaining := b.limit - int64(b.buf.Len())
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		_, _ = b.buf.Write(p[:int(remaining)])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}

func (b *limitedBuffer) Truncated() bool {
	return b.truncated
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

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
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
	case "list", "records":
		return protocol.CommandDatasourcesRecords
	case "get":
		return protocol.CommandDatasourcesGet
	case "batch_get", "batch-get":
		return protocol.CommandDatasourcesBatchGet
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
