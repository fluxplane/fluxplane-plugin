// Package management defines backend-neutral plugin management contracts.
package management

import (
	"context"
	"encoding/json"
	"time"

	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
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
	// PreferRemote forces resolution from the marketplace go_install source
	// even when a local_path is available, so an upgrade installs the published
	// artifact rather than a local dev build.
	PreferRemote bool `json:"prefer_remote,omitempty"`
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
	// InstalledVersion is the resolved module version of the current binary
	// (go_install artifacts only; empty for local/dev builds).
	InstalledVersion string `json:"installed_version,omitempty"`
	// PreviousVersion is the version replaced by the most recent
	// install/upgrade, kept for rollback.
	PreviousVersion string `json:"previous_version,omitempty"`
	// Pinned holds the version the plugin is held at; upgrade and
	// install --all skip pinned plugins. Empty means unpinned.
	Pinned string `json:"pinned,omitempty"`
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

// SyncRequest asks the backend to rebuild installed plugins from their recorded
// workspace local_path, bypassing the marketplace catalog entirely. This is the
// dev-loop counterpart to install: it is immune to a stale cached marketplace.json
// (whose entries carry no usable local_path) because it reads each plugin's
// stored local_path/binary labels directly.
type SyncRequest struct {
	Refs   []Ref `json:"refs,omitempty"`
	All    bool  `json:"all,omitempty"`
	DryRun bool  `json:"dry_run,omitempty"`
}

// SyncPluginResult describes the outcome of rebuilding one plugin from source.
type SyncPluginResult struct {
	Plugin     Ref    `json:"plugin"`
	Rebuilt    bool   `json:"rebuilt,omitempty"`
	Skipped    bool   `json:"skipped,omitempty"`
	Reason     string `json:"reason,omitempty"`
	LocalPath  string `json:"local_path,omitempty"`
	BinaryPath string `json:"binary_path,omitempty"`
	Error      string `json:"error,omitempty"`
}

// SyncResult aggregates per-plugin rebuild outcomes.
type SyncResult struct {
	Plugins []SyncPluginResult `json:"plugins"`
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

// AuthEndpoint records an endpoint configured during plugin auth setup.
type AuthEndpoint struct {
	Name    string `json:"name,omitempty"`
	ID      string `json:"id,omitempty"`
	URL     string `json:"url,omitempty"`
	Product string `json:"product,omitempty"`
}

// AuthStatusRequest requests auth state for a plugin instance.
type AuthStatusRequest struct {
	Ref      Ref    `json:"ref"`
	Instance string `json:"instance,omitempty"`
}

// AuthFieldStatus describes one auth field's configuration state.
type AuthFieldStatus struct {
	Name       string   `json:"name"`
	Required   bool     `json:"required"`
	Secret     bool     `json:"secret,omitempty"`
	Configured bool     `json:"configured"`
	Env        []string `json:"env,omitempty"`
}

// AuthMethodStatus summarizes one manifest auth method's readiness: which
// fields exist, which are configured (persisted secret or recorded
// metadata), and which required ones are missing. Ready means every required
// field is configured — vacuously true for methods whose fields are all
// optional.
type AuthMethodStatus struct {
	Method  string            `json:"method"`
	Kind    string            `json:"kind,omitempty"`
	Ready   bool              `json:"ready"`
	Fields  []AuthFieldStatus `json:"fields,omitempty"`
	Missing []string          `json:"missing,omitempty"`
}

// AuthStatusResult contains recorded auth states plus per-method readiness
// derived from the manifest and the persisted secret store. Connected reports
// whether any auth state was successfully recorded (connect or test); Ready
// reports whether some method has all required fields configured (true for
// all-optional methods even before any connect).
type AuthStatusResult struct {
	Plugin    Ref                `json:"plugin"`
	Instance  string             `json:"instance"`
	Connected bool               `json:"connected"`
	Ready     bool               `json:"ready"`
	Methods   []AuthMethodStatus `json:"methods,omitempty"`
	Auth      []AuthState        `json:"auth,omitempty"`
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
	Ref       Ref               `json:"ref"`
	Instance  string            `json:"instance,omitempty"`
	Method    string            `json:"method,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Endpoints []AuthEndpoint    `json:"endpoints,omitempty"`
	DryRun    bool              `json:"dry_run,omitempty"`
}

// AuthAutoRequest connects auth by importing manifest-declared environment variables.
type AuthAutoRequest struct {
	Ref      Ref    `json:"ref"`
	Instance string `json:"instance,omitempty"`
	DryRun   bool   `json:"dry_run,omitempty"`
}

// AuthAutoResult describes an environment import outcome.
type AuthAutoResult struct {
	Plugin    Ref            `json:"plugin"`
	Instance  string         `json:"instance"`
	Saved     []string       `json:"saved,omitempty"`
	Endpoints []AuthEndpoint `json:"endpoints,omitempty"`
	Missing   []string       `json:"missing,omitempty"`
	Skipped   []string       `json:"skipped,omitempty"`
	Changed   bool           `json:"changed"`
	Message   string         `json:"message,omitempty"`
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
	Connected bool      `json:"connected"`
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

// OperationFailure is a structured operation error: it preserves the plugin's
// field-level error detail (code/message/fields/details) so callers can act on
// it programmatically instead of parsing a flattened message string.
type OperationFailure struct {
	Plugin    string         `json:"plugin"`
	Operation string         `json:"operation"`
	Err       protocol.Error `json:"error"`
}

func (e *OperationFailure) Error() string {
	if e == nil {
		return ""
	}
	if code := e.Err.Code; code != "" {
		return code + ": " + e.Err.Message
	}
	return e.Err.Message
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

// DatasourceCallResult contains a datasource capability result payload. Hint,
// when set, carries an actionable remedy alongside an otherwise valid result
// (e.g. a lookup served without a built index).
type DatasourceCallResult struct {
	Plugin     Ref             `json:"plugin"`
	Instance   string          `json:"instance"`
	Capability string          `json:"capability"`
	Result     json.RawMessage `json:"result,omitempty"`
	Hint       string          `json:"hint,omitempty"`
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

// EvidenceListRequest describes an evidence declaration listing request.
type EvidenceListRequest struct {
	Ref      Ref    `json:"ref"`
	Instance string `json:"instance,omitempty"`
}

// EvidenceListResult contains evidence declarations advertised by a plugin.
type EvidenceListResult struct {
	Plugin            Ref                                `json:"plugin"`
	Instance          string                             `json:"instance,omitempty"`
	Observers         []sdkmanifest.ObserverSpec         `json:"observers,omitempty"`
	AssertionDerivers []sdkmanifest.AssertionDeriverSpec `json:"assertion_derivers,omitempty"`
}

// EvidenceObserveRequest asks a plugin runtime to produce observations.
type EvidenceObserveRequest struct {
	Ref          Ref                          `json:"ref"`
	Instance     string                       `json:"instance,omitempty"`
	Phase        sdkmanifest.ObservationPhase `json:"phase,omitempty"`
	Input        json.RawMessage              `json:"input,omitempty"`
	Observations []sdkmanifest.Observation    `json:"observations,omitempty"`
}

// EvidenceObserveResult contains an evidence observe result payload.
type EvidenceObserveResult struct {
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

// EndpointListResult contains stored endpoints. One array: each entry is the
// full stored record (ref plus created_at/updated_at/last_health) — no bare-ref
// duplicate alongside it.
type EndpointListResult struct {
	Endpoints []fpendpoint.Record `json:"endpoints,omitempty"`
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

// LocalSyncer is an optional backend capability that rebuilds installed plugins
// from their recorded workspace local_path. Backends that support a dev loop
// implement it; the CLI detects it via a type assertion.
type LocalSyncer interface {
	SyncLocalPlugins(context.Context, SyncRequest) (SyncResult, error)
}

// PinRequest pins or unpins an installed plugin. The version to pin comes from
// Ref.Version; when empty the currently installed version is used.
type PinRequest struct {
	Ref    Ref  `json:"ref"`
	Unpin  bool `json:"unpin,omitempty"`
	DryRun bool `json:"dry_run,omitempty"`
}

// PinResult reports the pin state after the change.
type PinResult struct {
	Plugin  Plugin `json:"plugin"`
	Pinned  string `json:"pinned,omitempty"`
	Changed bool   `json:"changed"`
}

// RollbackRequest reverts a plugin to its previously installed version.
type RollbackRequest struct {
	Ref    Ref  `json:"ref"`
	DryRun bool `json:"dry_run,omitempty"`
}

// RollbackResult reports the version swap performed by a rollback.
type RollbackResult struct {
	Plugin     Plugin `json:"plugin"`
	RolledBack bool   `json:"rolled_back"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
}

// VersionManager is an optional backend capability for pinning installed
// plugins at a version and rolling back to the previously installed one. The
// CLI detects it via a type assertion.
type VersionManager interface {
	PinPlugin(context.Context, PinRequest) (PinResult, error)
	RollbackPlugin(context.Context, RollbackRequest) (RollbackResult, error)
}

// ProcessManager is an optional backend capability exposing host-managed
// background processes (started by plugins through the host process
// capability, e.g. kubernetes port-forwards). The CLI detects it via a type
// assertion.
type ProcessManager interface {
	ListProcesses(context.Context, sdkhost.ProcessListRequest) (sdkhost.ProcessListResponse, error)
	StopProcess(context.Context, sdkhost.ProcessStopRequest) (sdkhost.ProcessStopResponse, error)
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

// EvidenceRunner lists and invokes plugin evidence observers.
type EvidenceRunner interface {
	ListEvidence(context.Context, EvidenceListRequest) (EvidenceListResult, error)
	ObserveEvidence(context.Context, EvidenceObserveRequest) (EvidenceObserveResult, error)
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
	EvidenceRunner
	EndpointRunner
	IndexManager
	EndpointStore
}
