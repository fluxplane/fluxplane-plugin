// Package management defines backend-neutral plugin management contracts.
package management

import (
	"context"
	"encoding/json"
	"time"

	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/protocol"
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

// AuthAutoRequest connects auth by importing manifest-declared environment variables.
type AuthAutoRequest struct {
	Ref      Ref    `json:"ref"`
	Instance string `json:"instance,omitempty"`
	DryRun   bool   `json:"dry_run,omitempty"`
}

// AuthAutoResult describes an environment import outcome.
type AuthAutoResult struct {
	Plugin   Ref      `json:"plugin"`
	Instance string   `json:"instance"`
	Saved    []string `json:"saved,omitempty"`
	Missing  []string `json:"missing,omitempty"`
	Skipped  []string `json:"skipped,omitempty"`
	Changed  bool     `json:"changed"`
	Message  string   `json:"message,omitempty"`
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

// OperationBatchRequest calls multiple operations on one plugin instance.
type OperationBatchRequest struct {
	Ref      Ref                      `json:"ref"`
	Instance string                   `json:"instance,omitempty"`
	Calls    []protocol.OperationCall `json:"calls"`
}

// OperationBatchResult contains a protocol batch result payload.
type OperationBatchResult struct {
	Plugin   Ref             `json:"plugin"`
	Instance string          `json:"instance"`
	Result   json.RawMessage `json:"result,omitempty"`
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

// ContextListRequest describes a context provider listing request.
type ContextListRequest struct {
	Ref      Ref    `json:"ref"`
	Instance string `json:"instance,omitempty"`
}

// ContextListResult contains context provider declarations advertised by a plugin.
type ContextListResult struct {
	Plugin   Ref                       `json:"plugin"`
	Instance string                    `json:"instance,omitempty"`
	Context  []sdkmanifest.ContextSpec `json:"context,omitempty"`
}

// ContextBuildRequest describes a context provider build request.
type ContextBuildRequest struct {
	Ref      Ref             `json:"ref"`
	Instance string          `json:"instance,omitempty"`
	Query    string          `json:"query,omitempty"`
	Kinds    []string        `json:"kinds,omitempty"`
	Limit    int             `json:"limit,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

// ContextBuildResult contains a context build result payload.
type ContextBuildResult struct {
	Plugin   Ref             `json:"plugin"`
	Instance string          `json:"instance,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
}

// IndexBuildRequest asks a plugin to build one or more local index snapshots.
type IndexBuildRequest struct {
	Ref      Ref    `json:"ref"`
	Instance string `json:"instance,omitempty"`
	Index    string `json:"index,omitempty"`
	Entity   string `json:"entity,omitempty"`
	DryRun   bool   `json:"dry_run,omitempty"`
}

// IndexBuildResult describes stored index snapshots.
type IndexBuildResult struct {
	Plugin    Ref       `json:"plugin"`
	Instance  string    `json:"instance"`
	Index     string    `json:"index,omitempty"`
	Indexes   []string  `json:"indexes,omitempty"`
	Records   int       `json:"records"`
	Stored    bool      `json:"stored"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Message   string    `json:"message,omitempty"`
}

// IndexStatusRequest asks for local index status.
type IndexStatusRequest struct {
	Ref      Ref    `json:"ref,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// IndexStatusResult contains local index status for one or more plugins.
type IndexStatusResult struct {
	Plugin   Ref                    `json:"plugin,omitempty"`
	Instance string                 `json:"instance,omitempty"`
	Indexes  []IndexStatus          `json:"indexes,omitempty"`
	Status   map[string]IndexStatus `json:"status,omitempty"`
}

// IndexStatus describes local index snapshots for one plugin instance.
type IndexStatus struct {
	Plugin    Ref                `json:"plugin"`
	Instance  string             `json:"instance"`
	Indexes   []string           `json:"indexes,omitempty"`
	Records   int                `json:"records"`
	UpdatedAt time.Time          `json:"updated_at,omitempty"`
	Details   []IndexStatusEntry `json:"details,omitempty"`
}

// IndexStatusEntry describes one local index snapshot.
type IndexStatusEntry struct {
	Index     string          `json:"index"`
	Records   int             `json:"records"`
	UpdatedAt time.Time       `json:"updated_at,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

// EndpointDiscoverRequest asks a plugin to discover endpoint candidates.
type EndpointDiscoverRequest struct {
	Ref       Ref             `json:"ref"`
	Instance  string          `json:"instance,omitempty"`
	Product   string          `json:"product,omitempty"`
	Context   string          `json:"context,omitempty"`
	Namespace string          `json:"namespace,omitempty"`
	Limit     int             `json:"limit,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

// EndpointDiscoverResult contains endpoint discovery candidate payload.
type EndpointDiscoverResult struct {
	Plugin   Ref             `json:"plugin"`
	Instance string          `json:"instance,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
}

// EndpointListRequest filters stored endpoints.
type EndpointListRequest struct {
	Product string `json:"product,omitempty"`
}

// EndpointListResult contains stored endpoints.
type EndpointListResult struct {
	Endpoints []fpendpoint.EndpointRef `json:"endpoints,omitempty"`
	Records   []fpendpoint.Record      `json:"records,omitempty"`
}

// EndpointGetRequest gets one stored endpoint.
type EndpointGetRequest struct {
	ID string `json:"id"`
}

// EndpointGetResult contains one stored endpoint.
type EndpointGetResult struct {
	Endpoint fpendpoint.EndpointRef `json:"endpoint"`
	Record   fpendpoint.Record      `json:"record,omitempty"`
	Found    bool                   `json:"found"`
}

// EndpointSaveRequest stores or updates an endpoint.
type EndpointSaveRequest struct {
	Endpoint fpendpoint.EndpointRef `json:"endpoint"`
	DryRun   bool                   `json:"dry_run,omitempty"`
}

// EndpointSaveResult describes a stored endpoint transition.
type EndpointSaveResult struct {
	Endpoint fpendpoint.EndpointRef `json:"endpoint"`
	Record   fpendpoint.Record      `json:"record,omitempty"`
	Saved    bool                   `json:"saved"`
	Updated  bool                   `json:"updated,omitempty"`
	Message  string                 `json:"message,omitempty"`
}

// EndpointHealthRequest stores the latest non-secret endpoint health probe.
type EndpointHealthRequest struct {
	ID     string            `json:"id"`
	Health fpendpoint.Health `json:"health"`
	DryRun bool              `json:"dry_run,omitempty"`
}

// EndpointHealthResult describes an endpoint health state transition.
type EndpointHealthResult struct {
	ID      string            `json:"id"`
	Record  fpendpoint.Record `json:"record,omitempty"`
	Saved   bool              `json:"saved"`
	Message string            `json:"message,omitempty"`
}

// EndpointRemoveRequest removes one stored endpoint.
type EndpointRemoveRequest struct {
	ID     string `json:"id"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// EndpointRemoveResult describes endpoint removal.
type EndpointRemoveResult struct {
	ID      string `json:"id"`
	Removed bool   `json:"removed"`
	Message string `json:"message,omitempty"`
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
	AuthAuto(context.Context, AuthAutoRequest) (AuthAutoResult, error)
	AuthTest(context.Context, AuthTestRequest) (AuthResult, error)
	AuthDisconnect(context.Context, AuthDisconnectRequest) (AuthResult, error)
}

// OperationRunner lists and invokes plugin operations.
type OperationRunner interface {
	ListOperations(context.Context, OperationListRequest) (OperationListResult, error)
	InvokeOperation(context.Context, OperationInvokeRequest) (OperationInvokeResult, error)
	BatchOperations(context.Context, OperationBatchRequest) (OperationBatchResult, error)
}

// DatasourceRunner lists and invokes plugin datasource capabilities.
type DatasourceRunner interface {
	ListDatasources(context.Context, DatasourceListRequest) (DatasourceListResult, error)
	CallDatasource(context.Context, DatasourceCallRequest) (DatasourceCallResult, error)
}

// ContextRunner lists and builds plugin context provider output.
type ContextRunner interface {
	ListContextProviders(context.Context, ContextListRequest) (ContextListResult, error)
	BuildContext(context.Context, ContextBuildRequest) (ContextBuildResult, error)
}

// EndpointRunner invokes plugin endpoint discovery.
type EndpointRunner interface {
	DiscoverEndpoints(context.Context, EndpointDiscoverRequest) (EndpointDiscoverResult, error)
}

// IndexManager builds and reports local plugin indexes.
type IndexManager interface {
	BuildIndex(context.Context, IndexBuildRequest) (IndexBuildResult, error)
	IndexStatus(context.Context, IndexStatusRequest) (IndexStatusResult, error)
}

// EndpointStore manages stored host endpoint refs.
type EndpointStore interface {
	ListEndpoints(context.Context, EndpointListRequest) (EndpointListResult, error)
	GetEndpoint(context.Context, EndpointGetRequest) (EndpointGetResult, error)
	SaveEndpoint(context.Context, EndpointSaveRequest) (EndpointSaveResult, error)
	SaveEndpointHealth(context.Context, EndpointHealthRequest) (EndpointHealthResult, error)
	RemoveEndpoint(context.Context, EndpointRemoveRequest) (EndpointRemoveResult, error)
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
	ContextRunner
	EndpointRunner
	IndexManager
	EndpointStore
}
