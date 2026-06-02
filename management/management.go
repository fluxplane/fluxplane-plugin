// Package management defines backend-neutral plugin management contracts.
package management

import (
	"context"
	"encoding/json"
	"time"

	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

const DefaultInstance = "default"

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

// RuntimeSpec describes how a plugin should be executed by consumers.
type RuntimeSpec struct {
	Kind    string   `json:"kind,omitempty"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Path    string   `json:"path,omitempty"`
}

// InstallRequest describes a request to install or update a plugin.
type InstallRequest struct {
	Ref         Ref               `json:"ref"`
	Source      string            `json:"source,omitempty"`
	Force       bool              `json:"force,omitempty"`
	Config      map[string]any    `json:"config,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Runtime     RuntimeSpec       `json:"runtime,omitempty"`
	Manifest    json.RawMessage   `json:"manifest,omitempty"`
	ManifestRef string            `json:"manifest_ref,omitempty"`
	DryRun      bool              `json:"dry_run,omitempty"`
}

// InstallResult describes an installed plugin artifact.
type InstallResult struct {
	Plugin    Plugin `json:"plugin"`
	Installed bool   `json:"installed"`
	Updated   bool   `json:"updated,omitempty"`
	Message   string `json:"message,omitempty"`
}

// UpdateRequest describes a request to refresh or replace a known plugin.
type UpdateRequest struct {
	Ref         Ref             `json:"ref"`
	Source      string          `json:"source,omitempty"`
	Runtime     RuntimeSpec     `json:"runtime,omitempty"`
	Manifest    json.RawMessage `json:"manifest,omitempty"`
	ManifestRef string          `json:"manifest_ref,omitempty"`
	DryRun      bool            `json:"dry_run,omitempty"`
}

// UpdateResult describes an update outcome.
type UpdateResult struct {
	Plugin  Plugin `json:"plugin"`
	Updated bool   `json:"updated"`
	Message string `json:"message,omitempty"`
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
	Runtime     RuntimeSpec       `json:"runtime,omitempty"`
	ManifestRef string            `json:"manifest_ref,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	InstalledAt time.Time         `json:"installed_at,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at,omitempty"`
}

// StatusRequest filters plugin status.
type StatusRequest struct {
	Ref Ref `json:"ref,omitempty"`
}

// StatusResult describes installed plugin state and configured instances.
type StatusResult struct {
	Plugins   []Plugin   `json:"plugins"`
	Instances []Instance `json:"instances,omitempty"`
}

// SetEnabledRequest changes plugin activation state.
type SetEnabledRequest struct {
	Ref     Ref  `json:"ref"`
	Enabled bool `json:"enabled"`
	DryRun  bool `json:"dry_run,omitempty"`
}

// SetEnabledResult describes activation state changes.
type SetEnabledResult struct {
	Plugin  Plugin `json:"plugin"`
	Changed bool   `json:"changed"`
	Message string `json:"message,omitempty"`
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

// ManifestResult contains an opaque manifest payload.
type ManifestResult struct {
	Ref      Ref    `json:"ref"`
	Manifest []byte `json:"manifest,omitempty"`
	Format   string `json:"format,omitempty"`
}

// RuntimeRequest identifies a plugin instance for protocol-backed runtime calls.
type RuntimeRequest struct {
	Ref      Ref    `json:"ref"`
	Instance string `json:"instance,omitempty"`
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
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

// Instance identifies product/user-specific plugin state.
type Instance struct {
	Plugin    Ref               `json:"plugin"`
	Name      string            `json:"name"`
	Enabled   bool              `json:"enabled,omitempty"`
	Config    map[string]any    `json:"config,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Auth      []AuthState       `json:"auth,omitempty"`
	CreatedAt time.Time         `json:"created_at,omitempty"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
}

// AuthState records stored authentication state for a plugin instance.
type AuthState struct {
	Method      string            `json:"method,omitempty"`
	Connected   bool              `json:"connected"`
	ConnectedAt time.Time         `json:"connected_at,omitempty"`
	TestedAt    time.Time         `json:"tested_at,omitempty"`
	Error       string            `json:"error,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// AuthStatusRequest requests auth state for a plugin instance.
type AuthStatusRequest struct {
	Ref      Ref    `json:"ref"`
	Instance string `json:"instance,omitempty"`
}

// AuthStatusResult contains auth state for a plugin instance.
type AuthStatusResult struct {
	Plugin   Ref         `json:"plugin"`
	Instance string      `json:"instance"`
	Auth     []AuthState `json:"auth,omitempty"`
}

// AuthMethodsRequest requests auth methods from the plugin runtime.
type AuthMethodsRequest = RuntimeRequest

// AuthMethodsResult contains auth methods advertised by the plugin runtime.
type AuthMethodsResult struct {
	Plugin   Ref                      `json:"plugin"`
	Instance string                   `json:"instance"`
	Methods  []sdkmanifest.AuthMethod `json:"methods,omitempty"`
}

// AuthConnectRequest records a connected auth method for a plugin instance.
type AuthConnectRequest struct {
	Ref      Ref               `json:"ref"`
	Instance string            `json:"instance,omitempty"`
	Method   string            `json:"method,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	DryRun   bool              `json:"dry_run,omitempty"`
}

// AuthTestRequest records the result of testing auth for a plugin instance.
type AuthTestRequest struct {
	Ref      Ref    `json:"ref"`
	Instance string `json:"instance,omitempty"`
	Method   string `json:"method,omitempty"`
	DryRun   bool   `json:"dry_run,omitempty"`
}

// AuthDisconnectRequest clears auth state for a plugin instance.
type AuthDisconnectRequest struct {
	Ref      Ref    `json:"ref"`
	Instance string `json:"instance,omitempty"`
	Method   string `json:"method,omitempty"`
	DryRun   bool   `json:"dry_run,omitempty"`
}

// AuthResult describes an auth state transition.
type AuthResult struct {
	Plugin    Ref       `json:"plugin"`
	Instance  string    `json:"instance"`
	Auth      AuthState `json:"auth,omitempty"`
	Connected bool      `json:"connected,omitempty"`
	Changed   bool      `json:"changed"`
	Message   string    `json:"message,omitempty"`
}

// OperationListRequest requests operations from the plugin runtime.
type OperationListRequest = RuntimeRequest

// OperationListResult contains operations advertised by the plugin runtime.
type OperationListResult struct {
	Plugin     Ref                         `json:"plugin"`
	Instance   string                      `json:"instance"`
	Operations []sdkmanifest.OperationSpec `json:"operations,omitempty"`
}

// OperationInvokeRequest calls a named plugin operation.
type OperationInvokeRequest struct {
	Ref       Ref             `json:"ref"`
	Instance  string          `json:"instance,omitempty"`
	Operation string          `json:"operation"`
	Input     json.RawMessage `json:"input,omitempty"`
}

// OperationInvokeResult contains an operation result payload.
type OperationInvokeResult struct {
	Plugin    Ref             `json:"plugin"`
	Instance  string          `json:"instance"`
	Operation string          `json:"operation"`
	Result    json.RawMessage `json:"result,omitempty"`
}

// DatasourceListRequest requests datasources from the plugin runtime.
type DatasourceListRequest = RuntimeRequest

// DatasourceListResult contains datasources advertised by the plugin runtime.
type DatasourceListResult struct {
	Plugin      Ref                          `json:"plugin"`
	Instance    string                       `json:"instance"`
	Datasources []sdkmanifest.DatasourceSpec `json:"datasources,omitempty"`
}

// DatasourceCallRequest calls one datasource capability on the plugin runtime.
type DatasourceCallRequest struct {
	Ref        Ref             `json:"ref"`
	Instance   string          `json:"instance,omitempty"`
	Capability string          `json:"capability"`
	Input      json.RawMessage `json:"input,omitempty"`
}

// DatasourceCallResult contains a datasource capability result payload.
type DatasourceCallResult struct {
	Plugin     Ref             `json:"plugin"`
	Instance   string          `json:"instance"`
	Capability string          `json:"capability"`
	Result     json.RawMessage `json:"result,omitempty"`
}

// Installer installs plugins.
type Installer interface {
	InstallPlugin(context.Context, InstallRequest) (InstallResult, error)
	UpdatePlugin(context.Context, UpdateRequest) (UpdateResult, error)
}

// Store lists, inspects, activates, and removes installed plugins.
type Store interface {
	ListPlugins(context.Context, ListRequest) ([]Plugin, error)
	PluginStatus(context.Context, StatusRequest) (StatusResult, error)
	SetPluginEnabled(context.Context, SetEnabledRequest) (SetEnabledResult, error)
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

// AuthManager manages persisted plugin instance auth state.
type AuthManager interface {
	AuthStatus(context.Context, AuthStatusRequest) (AuthStatusResult, error)
	AuthMethods(context.Context, AuthMethodsRequest) (AuthMethodsResult, error)
	AuthConnect(context.Context, AuthConnectRequest) (AuthResult, error)
	AuthTest(context.Context, AuthTestRequest) (AuthResult, error)
	AuthDisconnect(context.Context, AuthDisconnectRequest) (AuthResult, error)
}

// OperationRunner lists and invokes plugin operations.
type OperationRunner interface {
	ListOperations(context.Context, OperationListRequest) (OperationListResult, error)
	InvokeOperation(context.Context, OperationInvokeRequest) (OperationInvokeResult, error)
}

// DatasourceRunner lists and invokes plugin datasource capabilities.
type DatasourceRunner interface {
	ListDatasources(context.Context, DatasourceListRequest) (DatasourceListResult, error)
	CallDatasource(context.Context, DatasourceCallRequest) (DatasourceCallResult, error)
}

// Backend is the full backend surface used by the reusable CLI.
type Backend interface {
	Installer
	Store
	Registry
	ManifestProvider
	Runner
	AuthManager
	OperationRunner
	DatasourceRunner
}
