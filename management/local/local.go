// Package local provides a filesystem-backed plugin management backend.
package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-plugin/management"
)

// Backend stores plugin metadata in a local JSON state file.
type Backend struct {
	path string
}

// Option configures a local backend.
type Option func(*Backend)

// WithPath stores plugin metadata at path.
func WithPath(path string) Option {
	return func(b *Backend) {
		b.path = path
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
	return backend, nil
}

func defaultStatePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("fluxplane-plugin: resolve config dir: %w", err)
	}
	return filepath.Join(base, "fluxplane", "plugins.json"), nil
}

type state struct {
	Plugins map[string]storedPlugin `json:"plugins,omitempty"`
}

type storedPlugin struct {
	management.Plugin
	Config      map[string]any `json:"config,omitempty"`
	InstalledAt time.Time      `json:"installed_at,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at,omitempty"`
}

// InstallPlugin records an installed plugin in the local store.
func (b *Backend) InstallPlugin(_ context.Context, req management.InstallRequest) (management.InstallResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.InstallResult{}, err
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
	plugin := storedPlugin{
		Plugin: management.Plugin{
			Ref:       req.Ref,
			Source:    req.Source,
			Installed: true,
			Enabled:   true,
			Labels:    req.Labels,
		},
		Config:      req.Config,
		InstalledAt: now,
		UpdatedAt:   now,
	}
	if exists {
		plugin.InstalledAt = existing.InstalledAt
	}
	if req.DryRun {
		return management.InstallResult{Plugin: plugin.Plugin, Installed: false, Updated: exists, Message: "dry run"}, nil
	}
	st.Plugins[key] = plugin
	if err := b.writeState(st); err != nil {
		return management.InstallResult{}, err
	}
	return management.InstallResult{Plugin: plugin.Plugin, Installed: !exists, Updated: exists}, nil
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
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Ref.Key() < plugins[j].Ref.Key() })
	return plugins, nil
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
	if _, ok := st.Plugins[key]; !ok {
		return management.RemoveResult{}, fmt.Errorf("fluxplane-plugin: plugin %q is not installed", key)
	}
	if req.DryRun {
		return management.RemoveResult{Ref: req.Ref, Removed: false, Message: "dry run"}, nil
	}
	delete(st.Plugins, key)
	if err := b.writeState(st); err != nil {
		return management.RemoveResult{}, err
	}
	return management.RemoveResult{Ref: req.Ref, Removed: true}, nil
}

// SearchPlugins searches the local store until registry support is added.
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
	out := make([]management.Plugin, 0, len(plugins))
	for _, plugin := range plugins {
		if query != "" && !strings.Contains(strings.ToLower(plugin.Ref.Name), query) && !strings.Contains(strings.ToLower(plugin.Description), query) {
			continue
		}
		out = append(out, plugin)
		if len(out) >= limit {
			break
		}
	}
	return management.SearchResult{Plugins: out}, nil
}

// PluginManifest returns locally stored plugin metadata as a JSON manifest until registry manifests are added.
func (b *Backend) PluginManifest(_ context.Context, req management.ManifestRequest) (management.ManifestResult, error) {
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
	data, err := json.MarshalIndent(plugin, "", "  ")
	if err != nil {
		return management.ManifestResult{}, err
	}
	return management.ManifestResult{Ref: req.Ref, Manifest: data, Format: "json"}, nil
}

// RunPlugin currently validates that the plugin is installed. Execution backends will replace this with protocol runtime support.
func (b *Backend) RunPlugin(_ context.Context, req management.RunRequest) (management.RunResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return management.RunResult{}, err
	}
	st, err := b.readState()
	if err != nil {
		return management.RunResult{}, err
	}
	if _, ok := st.Plugins[req.Ref.Key()]; !ok {
		return management.RunResult{}, fmt.Errorf("fluxplane-plugin: plugin %q is not installed", req.Ref.Key())
	}
	return management.RunResult{Ref: req.Ref, Message: "local backend does not run plugin processes yet"}, nil
}

func validateRef(ref management.Ref) error {
	if strings.TrimSpace(ref.Name) == "" {
		return errors.New("fluxplane-plugin: plugin name is required")
	}
	return nil
}

func (b *Backend) readState() (state, error) {
	st := state{Plugins: map[string]storedPlugin{}}
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
