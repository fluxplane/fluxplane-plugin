// Package management defines backend-neutral plugin management contracts.
package management

import "context"

// Ref identifies a plugin by name and optional version/channel.
type Ref struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Channel string `json:"channel,omitempty"`
}

// Key returns a stable local identity for this plugin ref.
func (r Ref) Key() string {
	if r.Version == "" {
		return r.Name
	}
	return r.Name + "@" + r.Version
}

// InstallRequest describes a request to install or update a plugin.
type InstallRequest struct {
	Ref    Ref               `json:"ref"`
	Source string            `json:"source,omitempty"`
	Force  bool              `json:"force,omitempty"`
	Config map[string]any    `json:"config,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	DryRun bool              `json:"dry_run,omitempty"`
}

// InstallResult describes an installed plugin artifact.
type InstallResult struct {
	Plugin    Plugin `json:"plugin"`
	Installed bool   `json:"installed"`
	Updated   bool   `json:"updated,omitempty"`
	Message   string `json:"message,omitempty"`
}

// ListRequest filters installed plugins.
type ListRequest struct {
	All bool `json:"all,omitempty"`
}

// Plugin describes a locally known plugin.
type Plugin struct {
	Ref         Ref               `json:"ref"`
	Description string            `json:"description,omitempty"`
	Source      string            `json:"source,omitempty"`
	Installed   bool              `json:"installed,omitempty"`
	Enabled     bool              `json:"enabled,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// RemoveRequest describes a plugin removal request.
type RemoveRequest struct {
	Ref    Ref  `json:"ref"`
	Force  bool `json:"force,omitempty"`
	DryRun bool `json:"dry_run,omitempty"`
}

// RemoveResult describes removal outcome.
type RemoveResult struct {
	Ref     Ref    `json:"ref"`
	Removed bool   `json:"removed"`
	Message string `json:"message,omitempty"`
}

// SearchRequest describes a plugin registry search.
type SearchRequest struct {
	Query string `json:"query,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// SearchResult contains registry search matches.
type SearchResult struct {
	Plugins []Plugin `json:"plugins,omitempty"`
}

// ManifestRequest requests a plugin manifest.
type ManifestRequest struct {
	Ref Ref `json:"ref"`
}

// ManifestResult contains an opaque manifest payload until manifest contracts are extracted.
type ManifestResult struct {
	Ref      Ref    `json:"ref"`
	Manifest []byte `json:"manifest,omitempty"`
	Format   string `json:"format,omitempty"`
}

// RunRequest describes a plugin run request.
type RunRequest struct {
	Ref  Ref      `json:"ref"`
	Args []string `json:"args,omitempty"`
}

// RunResult describes a plugin run outcome.
type RunResult struct {
	Ref      Ref    `json:"ref"`
	ExitCode int    `json:"exit_code,omitempty"`
	Message  string `json:"message,omitempty"`
}

// Installer installs plugins.
type Installer interface {
	InstallPlugin(context.Context, InstallRequest) (InstallResult, error)
}

// Store lists and removes installed plugins.
type Store interface {
	ListPlugins(context.Context, ListRequest) ([]Plugin, error)
	RemovePlugin(context.Context, RemoveRequest) (RemoveResult, error)
}

// Registry searches and resolves registry plugins.
type Registry interface {
	SearchPlugins(context.Context, SearchRequest) (SearchResult, error)
}

// ManifestProvider returns plugin manifests.
type ManifestProvider interface {
	PluginManifest(context.Context, ManifestRequest) (ManifestResult, error)
}

// Runner runs plugins.
type Runner interface {
	RunPlugin(context.Context, RunRequest) (RunResult, error)
}

// Backend is the full backend surface used by the reusable CLI.
type Backend interface {
	Installer
	Store
	Registry
	ManifestProvider
	Runner
}
