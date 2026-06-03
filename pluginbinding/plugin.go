package pluginbinding

import (
	stdcontext "context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	fpcontext "github.com/fluxplane/fluxplane-context"
	manifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/protocol"
	"github.com/invopop/jsonschema"
)

type Plugin struct {
	manifest         manifest.PluginManifest
	operations       map[string]operation
	datasources      map[string][]datasourceHandler
	contextProviders []contextProvider
	observers        []evidenceObserver
	commandHandlers  map[string]CommandHandler
	secretGetter     SecretGetter
}

type Context struct {
	stdcontext.Context
	Request protocol.Request
	Call    protocol.OperationCall
	Config  map[string]any
	Cache   *Cache
	Host    HostClient
	Events  EventSink
	plugin  *Plugin
}

type Cache struct {
	mu     sync.RWMutex
	values map[string]any
}

type CommandHandler func(Context) protocol.Response

type OperationHandler[I any, O any] func(Context, I) (O, error)
type DatasourceHandler[I any, O any] func(Context, I) (O, error)
type ContextProviderHandler func(Context, ContextBuildInput) (ContextBuildResult, error)
type EvidenceObserverHandler func(Context, EvidenceObserveInput) (EvidenceObserveResult, error)

type TextResult struct {
	Text    string `json:"text,omitempty"`
	Summary string `json:"summary,omitempty"`
	Data    any    `json:"data,omitempty"`
}

type Error struct {
	Code    string
	Message string
}

type operation interface {
	Spec() manifest.OperationSpec
	Run(Context) protocol.OperationResult
}

type datasourceHandler interface {
	Spec() manifest.DatasourceSpec
	Run(Context) protocol.Response
}

type contextProvider interface {
	Spec() manifest.ContextSpec
	Run(Context) protocol.Response
}

type evidenceObserver interface {
	Spec() manifest.ObserverSpec
	Observe(Context, EvidenceObserveInput) (EvidenceObserveResult, error)
}

type typedOperation[I any, O any] struct {
	spec    manifest.OperationSpec
	handler OperationHandler[I, O]
}

type typedDatasource[I any, O any] struct {
	spec    manifest.DatasourceSpec
	handler DatasourceHandler[I, O]
}

type typedContextProvider struct {
	spec    manifest.ContextSpec
	handler ContextProviderHandler
}

type typedEvidenceObserver struct {
	spec    manifest.ObserverSpec
	handler EvidenceObserverHandler
}

type ContextBuildInput struct {
	ThreadID      string                 `json:"thread_id,omitempty" jsonschema:"description=Conversation or thread id for this context render."`
	BranchID      string                 `json:"branch_id,omitempty" jsonschema:"description=Optional branch id for this context render."`
	TurnID        string                 `json:"turn_id,omitempty" jsonschema:"description=Turn id for this context render."`
	Reason        fpcontext.RenderReason `json:"reason,omitempty" jsonschema:"description=Reason this context is being rendered."`
	InputText     string                 `json:"input_text,omitempty" jsonschema:"description=Current user-visible input text."`
	RecentContext string                 `json:"recent_context,omitempty" jsonschema:"description=Recent conversation or rendered context summary."`
	Scope         map[string]string      `json:"scope,omitempty" jsonschema:"description=Runtime scope values for context selection."`
	BudgetTokens  int                    `json:"budget_tokens,omitempty" jsonschema:"description=Approximate token budget for produced context."`
	Query         string                 `json:"query,omitempty" jsonschema:"description=Compatibility context query, usually input_text or recent_context."`
	Kinds         []string               `json:"kinds,omitempty" jsonschema:"description=Optional context block kind filters."`
	Limit         int                    `json:"limit,omitempty" jsonschema:"description=Maximum context blocks to return."`
}

type ContextBuildResult struct {
	Blocks []manifest.ContextBlock `json:"blocks"`
}

type EvidenceObserveInput = protocol.EvidenceObserveRequest
type EvidenceObserveResult = protocol.EvidenceObserveResult

func New(manifest manifest.PluginManifest) *Plugin {
	return &Plugin{
		manifest:         manifest,
		operations:       map[string]operation{},
		datasources:      map[string][]datasourceHandler{},
		contextProviders: nil,
		observers:        nil,
		commandHandlers:  map[string]CommandHandler{},
		secretGetter:     DefaultSecretGetter,
	}
}

func Serve(plugin *Plugin) {
	protocol.Serve(func(req protocol.Request, host protocol.HostCaller) protocol.Response {
		return plugin.HandleWithHostAndEvents(req, newHostClient(host), newEventSink(host))
	})
}

func Operation[I any, O any](plugin *Plugin, spec manifest.OperationSpec, handler OperationHandler[I, O]) {
	if plugin == nil {
		return
	}
	if strings.TrimSpace(spec.Name) == "" {
		panic("pluginbinding: operation name is required")
	}
	if len(spec.Input) == 0 {
		spec.Input = MustSchemaFor[I]()
	}
	if len(spec.Output) == 0 {
		spec.Output = MustSchemaFor[O]()
	}
	spec = NormalizeOperationSpec(spec)
	plugin.operations[spec.Name] = typedOperation[I, O]{spec: spec, handler: handler}
	plugin.upsertOperation(spec)
}

func DatasourceHandlerFor[I any, O any](plugin *Plugin, spec manifest.DatasourceSpec, capability string, handler DatasourceHandler[I, O]) {
	if plugin == nil {
		return
	}
	if strings.TrimSpace(spec.Name) == "" {
		panic("pluginbinding: datasource name is required")
	}
	if strings.TrimSpace(capability) == "" {
		panic("pluginbinding: datasource capability is required")
	}
	if len(spec.Input) == 0 {
		spec.Input = MustSchemaFor[I]()
	}
	if len(spec.Output) == 0 {
		spec.Output = MustSchemaFor[O]()
	}
	spec.Capabilities = ensureString(spec.Capabilities, capability)
	spec = NormalizeDatasourceSpec(spec)
	plugin.datasources[capability] = append(plugin.datasources[capability], typedDatasource[I, O]{spec: spec, handler: handler})
	plugin.upsertDatasource(spec)
}

func ContextProvider(plugin *Plugin, spec manifest.ContextSpec, handler ContextProviderHandler) {
	if plugin == nil {
		return
	}
	if strings.TrimSpace(string(spec.Name)) == "" {
		panic("pluginbinding: context provider name is required")
	}
	if handler == nil {
		panic("pluginbinding: context provider handler is required")
	}
	plugin.contextProviders = append(plugin.contextProviders, typedContextProvider{spec: spec, handler: handler})
	plugin.upsertContext(spec)
}

func EvidenceObserver(plugin *Plugin, spec manifest.ObserverSpec, handler EvidenceObserverHandler) {
	if plugin == nil {
		return
	}
	if strings.TrimSpace(spec.Name) == "" {
		panic("pluginbinding: evidence observer name is required")
	}
	if handler == nil {
		panic("pluginbinding: evidence observer handler is required")
	}
	plugin.observers = append(plugin.observers, typedEvidenceObserver{spec: spec, handler: handler})
	plugin.upsertObserver(spec)
}

func (p *Plugin) Command(command string, handler CommandHandler) {
	if p == nil || strings.TrimSpace(command) == "" || handler == nil {
		return
	}
	p.commandHandlers[command] = handler
}

func (p *Plugin) WithSecretGetter(getter SecretGetter) *Plugin {
	if p == nil {
		return p
	}
	if getter == nil {
		p.secretGetter = DefaultSecretGetter
	} else {
		p.secretGetter = getter
	}
	return p
}

func (p *Plugin) AuthConnectText(text string) {
	p.Command(protocol.CommandAuthConnect, func(Context) protocol.Response {
		return OKText(text, nil)
	})
}

func (p *Plugin) HostOwnedIndexStatus(product string) {
	p.Command(protocol.CommandIndexStatus, func(Context) protocol.Response {
		return OKText(product+" index is host-owned", map[string]any{"status": "host_owned"})
	})
}

func (p *Plugin) AuthTestOperation(name string) {
	p.Command(protocol.CommandAuthTest, func(ctx Context) protocol.Response {
		if ctx.Request.Grant == "" {
			return OKText(p.manifest.Name+" auth is host-managed; use fluxplane-plugin auth status "+p.manifest.Name+" or fluxplane-plugin operation invoke "+p.manifest.Name+" "+name, map[string]any{"status": "host_managed"})
		}
		return p.callOperation(ctx.Context, ctx.Request, protocol.OperationCall{Name: name}, NewCache(), ctx.Host, ctx.Events, true)
	})
}

func (p *Plugin) IndexBuildOperation(name string) {
	p.Command(protocol.CommandIndexBuild, func(ctx Context) protocol.Response {
		if ctx.Request.Grant == "" {
			return OKText("Use fluxplane-plugin operation invoke "+p.manifest.Name+" "+name+" to build live records", map[string]any{"status": "requires_operation_grant"})
		}
		return p.callOperation(ctx.Context, ctx.Request, protocol.OperationCall{Name: name, Input: ctx.Request.Payload}, NewCache(), ctx.Host, ctx.Events, false)
	})
}

func (p *Plugin) Handle(req protocol.Request) protocol.Response {
	return p.HandleWithHostAndEvents(req, newHostClient(nil), unavailableEventSink{})
}

func (p *Plugin) HandleWithHost(req protocol.Request, host HostClient) protocol.Response {
	return p.HandleWithHostAndEvents(req, host, unavailableEventSink{})
}

func (p *Plugin) HandleWithHostAndEvents(req protocol.Request, host HostClient, events EventSink) protocol.Response {
	return p.HandleWithContextHostAndEvents(stdcontext.Background(), req, host, events)
}

func (p *Plugin) HandleWithContextHostAndEvents(ctx stdcontext.Context, req protocol.Request, host HostClient, events EventSink) protocol.Response {
	if p == nil {
		return protocol.Fail("plugin_error", "plugin is nil")
	}
	if ctx == nil {
		ctx = stdcontext.Background()
	}
	if host == nil {
		host = newHostClient(nil)
	}
	if events == nil {
		events = unavailableEventSink{}
	}
	cache := NewCache()
	bindingCtx := Context{Context: ctx, Request: req, Config: cloneConfig(req.Config), Cache: cache, Host: host, Events: events, plugin: p}
	if handler := p.commandHandlers[req.Command]; handler != nil {
		return handler(bindingCtx)
	}
	switch req.Command {
	case protocol.CommandManifest:
		return protocol.OK(p.Manifest())
	case protocol.CommandAuthMethods:
		return protocol.OK(p.Manifest().Auth)
	case protocol.CommandOperationsList:
		return protocol.OK(p.Manifest().Operations)
	case protocol.CommandOperationsCall:
		call, err := protocol.DecodePayload[protocol.OperationCall](req.Payload)
		if err != nil {
			return protocol.Fail("bad_payload", err.Error())
		}
		return p.callOperation(ctx, req, call, cache, host, events, true)
	case protocol.CommandOperationsBatch:
		return p.callBatch(ctx, req, cache, host, events)
	case protocol.CommandDatasourcesList:
		return protocol.OK(p.Manifest().Datasources)
	case protocol.CommandDatasourcesRecords:
		return p.runDatasource(bindingCtx, CapabilityList)
	case protocol.CommandDatasourcesSearch:
		return p.runDatasource(bindingCtx, CapabilitySearch)
	case protocol.CommandDatasourcesGet:
		return p.runDatasource(bindingCtx, CapabilityGet)
	case protocol.CommandDatasourcesBatchGet:
		return p.runDatasource(bindingCtx, CapabilityBatchGet)
	case protocol.CommandDatasourcesLookup:
		return p.runDatasource(bindingCtx, CapabilityLookup)
	case protocol.CommandContextBuild:
		return p.runContext(bindingCtx)
	case protocol.CommandEvidenceObserve:
		return p.runEvidence(bindingCtx)
	case protocol.CommandEndpointsDiscover:
		return OKData(map[string]any{"candidates": []manifest.EndpointCandidate{}})
	default:
		return protocol.Fail("unknown_command", p.manifest.Name+" plugin does not implement "+req.Command)
	}
}

func (p *Plugin) Manifest() manifest.PluginManifest {
	manifest := p.manifest
	manifest.Operations = normalizeOperationSpecs(manifest.Operations)
	manifest.Datasources = normalizeDatasourceSpecs(manifest.Datasources)
	return manifest
}

func cloneConfig(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (p *Plugin) callBatch(ctx stdcontext.Context, req protocol.Request, cache *Cache, host HostClient, events EventSink) protocol.Response {
	batch, err := protocol.DecodePayload[protocol.OperationBatch](req.Payload)
	if err != nil {
		return protocol.Fail("bad_payload", err.Error())
	}
	results := make([]protocol.OperationResult, 0, len(batch.Calls))
	for _, call := range batch.Calls {
		results = append(results, p.runOperation(ctx, req, call, cache, host, events))
	}
	return protocol.OK(protocol.OperationBatchResult{Results: results})
}

func (p *Plugin) callOperation(ctx stdcontext.Context, req protocol.Request, call protocol.OperationCall, cache *Cache, host HostClient, events EventSink, unwrap bool) protocol.Response {
	result := p.runOperation(ctx, req, call, cache, host, events)
	if !result.OK {
		return protocol.Response{Protocol: protocol.Version, OK: false, Error: result.Error}
	}
	if unwrap {
		return protocol.Response{Protocol: protocol.Version, OK: true, Result: result.Result}
	}
	var value any
	if len(result.Result) > 0 {
		if err := json.Unmarshal(result.Result, &value); err != nil {
			return protocol.Fail("marshal_result", err.Error())
		}
	}
	return OKData(value)
}

func (p *Plugin) runOperation(ctx stdcontext.Context, req protocol.Request, call protocol.OperationCall, cache *Cache, host HostClient, events EventSink) protocol.OperationResult {
	if call.ID == "" {
		call.ID = call.Name
	}
	op := p.operations[call.Name]
	if op == nil {
		return OperationError(call, "unknown_operation", "unknown operation "+call.Name)
	}
	if host == nil {
		host = newHostClient(nil)
	}
	if events == nil {
		events = unavailableEventSink{}
	}
	if ctx == nil {
		ctx = stdcontext.Background()
	}
	return op.Run(Context{Context: ctx, Request: req, Call: call, Config: cloneConfig(req.Config), Cache: cache, Host: host, Events: events, plugin: p})
}

func (p *Plugin) RunOperation(req protocol.Request, call protocol.OperationCall, cache *Cache) protocol.OperationResult {
	return p.RunOperationWithHost(req, call, cache, newHostClient(nil))
}

func (p *Plugin) RunOperationWithHost(req protocol.Request, call protocol.OperationCall, cache *Cache, host HostClient) protocol.OperationResult {
	if cache == nil {
		cache = NewCache()
	}
	return p.runOperation(stdcontext.Background(), req, call, cache, host, unavailableEventSink{})
}

func (p *Plugin) upsertOperation(spec manifest.OperationSpec) {
	for i := range p.manifest.Operations {
		if p.manifest.Operations[i].Name == spec.Name {
			p.manifest.Operations[i] = mergeOperationSpec(p.manifest.Operations[i], spec)
			return
		}
	}
	p.manifest.Operations = append(p.manifest.Operations, spec)
}

func (p *Plugin) upsertDatasource(spec manifest.DatasourceSpec) {
	for i := range p.manifest.Datasources {
		if p.manifest.Datasources[i].Name == spec.Name {
			p.manifest.Datasources[i] = mergeDatasourceSpec(p.manifest.Datasources[i], spec)
			return
		}
	}
	p.manifest.Datasources = append(p.manifest.Datasources, spec)
}

func (p *Plugin) upsertContext(spec manifest.ContextSpec) {
	for i := range p.manifest.Context {
		if p.manifest.Context[i].Name == spec.Name {
			p.manifest.Context[i] = mergeContextSpec(p.manifest.Context[i], spec)
			return
		}
	}
	p.manifest.Context = append(p.manifest.Context, spec)
}

func (p *Plugin) upsertObserver(spec manifest.ObserverSpec) {
	for i := range p.manifest.Observers {
		if p.manifest.Observers[i].Name == spec.Name {
			p.manifest.Observers[i] = spec
			return
		}
	}
	p.manifest.Observers = append(p.manifest.Observers, spec)
}

func (p *Plugin) runDatasource(ctx Context, capability string) protocol.Response {
	handlers := p.datasources[capability]
	if len(handlers) == 0 {
		return protocol.Fail("not_implemented", p.manifest.Name+" datasource "+capability+" requires host index integration")
	}
	handler, err := selectDatasourceHandler(ctx.Request.Payload, handlers)
	if err != nil {
		return protocol.Fail("bad_payload", err.Error())
	}
	return handler.Run(ctx)
}

func (p *Plugin) runContext(ctx Context) protocol.Response {
	input, err := DecodePayload[ContextBuildInput](ctx.Request.Payload)
	if err != nil {
		return protocol.Fail("bad_payload", err.Error())
	}
	if len(p.contextProviders) == 0 {
		return protocol.OK(ContextBuildResult{Blocks: []manifest.ContextBlock{}})
	}
	var out ContextBuildResult
	for _, provider := range p.contextProviders {
		resp := provider.Run(Context{Context: ctx.Context, Request: ctx.Request, Config: cloneConfig(ctx.Request.Config), Cache: ctx.Cache, Host: ctx.Host, Events: ctx.Events, plugin: p})
		if !resp.OK {
			return resp
		}
		var result ContextBuildResult
		if len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return protocol.Fail("bad_payload", err.Error())
			}
		}
		out.Blocks = append(out.Blocks, result.Blocks...)
	}
	out.Blocks = filterContextBlocks(out.Blocks, input)
	return protocol.OK(out)
}

func (p *Plugin) runEvidence(ctx Context) protocol.Response {
	input, err := DecodePayload[EvidenceObserveInput](ctx.Request.Payload)
	if err != nil {
		return protocol.Fail("bad_payload", err.Error())
	}
	if len(p.observers) == 0 {
		return protocol.OK(EvidenceObserveResult{Observations: []manifest.Observation{}})
	}
	var out EvidenceObserveResult
	for _, observer := range p.observers {
		spec := observer.Spec()
		if spec.Phase != "" && input.Phase != "" && spec.Phase != input.Phase {
			continue
		}
		result, err := observer.Observe(Context{Context: ctx.Context, Request: ctx.Request, Config: cloneConfig(ctx.Request.Config), Cache: ctx.Cache, Host: ctx.Host, Events: ctx.Events, plugin: p}, input)
		if err != nil {
			var pluginErr Error
			if errors.As(err, &pluginErr) {
				return protocol.Fail(pluginErr.Code, pluginErr.Message)
			}
			return protocol.Fail("plugin_error", err.Error())
		}
		for _, observation := range result.Observations {
			if !observationKindAllowed(spec.ObservableKinds, observation.Kind) {
				continue
			}
			if observation.Source == "" {
				observation.Source = spec.Name
			}
			if observation.Environment.Name == "" {
				observation.Environment = spec.Environment
			}
			out.Observations = append(out.Observations, observation)
		}
		out.Assertions = append(out.Assertions, result.Assertions...)
	}
	if out.Observations == nil {
		out.Observations = []manifest.Observation{}
	}
	return protocol.OK(out)
}

func (op typedOperation[I, O]) Spec() manifest.OperationSpec {
	return op.spec
}

func (op typedOperation[I, O]) Run(ctx Context) protocol.OperationResult {
	input, err := DecodeCallInput[I](ctx.Call)
	if err != nil {
		return OperationError(ctx.Call, "bad_input", err.Error())
	}
	out, err := op.handler(ctx, input)
	if err != nil {
		var pluginErr Error
		if errors.As(err, &pluginErr) {
			return OperationError(ctx.Call, pluginErr.Code, pluginErr.Message)
		}
		return OperationError(ctx.Call, "plugin_error", err.Error())
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return OperationError(ctx.Call, "marshal_result", err.Error())
	}
	return protocol.OperationResult{ID: ctx.Call.ID, Name: ctx.Call.Name, OK: true, Result: raw}
}

func (ds typedDatasource[I, O]) Spec() manifest.DatasourceSpec {
	return ds.spec
}

func (ds typedDatasource[I, O]) Run(ctx Context) protocol.Response {
	input, err := DecodePayload[I](ctx.Request.Payload)
	if err != nil {
		return protocol.Fail("bad_payload", err.Error())
	}
	out, err := ds.handler(ctx, input)
	if err != nil {
		var pluginErr Error
		if errors.As(err, &pluginErr) {
			return protocol.Fail(pluginErr.Code, pluginErr.Message)
		}
		return protocol.Fail("plugin_error", err.Error())
	}
	return protocol.OK(out)
}

func (provider typedContextProvider) Spec() manifest.ContextSpec {
	return provider.spec
}

func (provider typedContextProvider) Run(ctx Context) protocol.Response {
	input, err := DecodePayload[ContextBuildInput](ctx.Request.Payload)
	if err != nil {
		return protocol.Fail("bad_payload", err.Error())
	}
	out, err := provider.handler(ctx, input)
	if err != nil {
		var pluginErr Error
		if errors.As(err, &pluginErr) {
			return protocol.Fail(pluginErr.Code, pluginErr.Message)
		}
		return protocol.Fail("plugin_error", err.Error())
	}
	for i := range out.Blocks {
		out.Blocks[i] = ctx.NormalizeContextBlock(out.Blocks[i])
	}
	if out.Blocks == nil {
		out.Blocks = []manifest.ContextBlock{}
	}
	return protocol.OK(out)
}

func (observer typedEvidenceObserver) Spec() manifest.ObserverSpec {
	return observer.spec
}

func (observer typedEvidenceObserver) Observe(ctx Context, input EvidenceObserveInput) (EvidenceObserveResult, error) {
	return observer.handler(ctx, input)
}

func (ctx Context) NormalizeContextBlock(block manifest.ContextBlock) manifest.ContextBlock {
	if strings.TrimSpace(string(block.Kind)) == "" {
		block.Kind = ContextKindText
	}
	if block.Source == nil {
		plugin := strings.TrimSpace(ctx.Request.Plugin)
		if plugin == "" && ctx.plugin != nil {
			plugin = ctx.plugin.manifest.Name
		}
		block.Source = &manifest.ContextSource{Plugin: plugin, Instance: strings.TrimSpace(ctx.Request.Instance)}
	}
	return block
}

func DecodeCallInput[T any](call protocol.OperationCall) (T, error) {
	var input T
	if len(call.Input) == 0 {
		return input, nil
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return input, fmt.Errorf("decode operation input: %w", err)
	}
	return input, nil
}

func observationKindAllowed(kinds []string, kind string) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

func DecodePayload[T any](raw json.RawMessage) (T, error) {
	var input T
	if len(raw) == 0 {
		return input, nil
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, fmt.Errorf("decode payload: %w", err)
	}
	return input, nil
}

func NewCache() *Cache {
	return &Cache{values: map[string]any{}}
}

func (c *Cache) Get(key string) (any, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	value, ok := c.values[key]
	return value, ok
}

func (c *Cache) Set(key string, value any) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = value
}

func Fail(code, message string) error {
	return Error{Code: code, Message: message}
}

func Errorf(code, format string, args ...any) error {
	return Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func (e Error) Error() string {
	return e.Message
}

func OperationError(call protocol.OperationCall, code, message string) protocol.OperationResult {
	return protocol.OperationResult{ID: call.ID, Name: call.Name, OK: false, Error: &protocol.Error{Code: code, Message: message}}
}

func OKText(text string, data any) protocol.Response {
	return protocol.OK(TextResult{Text: text, Summary: firstLine(text), Data: data})
}

func OKData(data any) protocol.Response {
	return protocol.OK(TextResult{Data: data})
}

func MustSchemaFor[T any]() json.RawMessage {
	raw, err := SchemaFor[T]()
	if err != nil {
		panic(err)
	}
	return raw
}

func SchemaFor[T any]() (json.RawMessage, error) {
	var zero T
	schema := schemaReflector().Reflect(zero)
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	return normalizeSchema(raw)
}

func schemaReflector() *jsonschema.Reflector {
	return &jsonschema.Reflector{
		Anonymous:                  true,
		DoNotReference:             true,
		ExpandedStruct:             true,
		RequiredFromJSONSchemaTags: false,
	}
}

func normalizeSchema(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	value = normalizeSchemaValue(value)
	return json.Marshal(value)
}

func normalizeSchemaValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		delete(typed, "$schema")
		for key, child := range typed {
			typed[key] = normalizeSchemaValue(child)
		}
		if required, ok := typed["required"].([]any); ok {
			sort.Slice(required, func(i, j int) bool {
				left, _ := required[i].(string)
				right, _ := required[j].(string)
				return left < right
			})
		}
		return typed
	case []any:
		for i, child := range typed {
			typed[i] = normalizeSchemaValue(child)
		}
		return typed
	default:
		return value
	}
}

func mergeOperationSpec(base, generated manifest.OperationSpec) manifest.OperationSpec {
	base.Name = firstNonEmpty(base.Name, generated.Name)
	base.Description = firstNonEmpty(base.Description, generated.Description)
	if len(base.Input) == 0 {
		base.Input = generated.Input
	}
	if len(base.Output) == 0 {
		base.Output = generated.Output
	}
	if !base.ReadOnly {
		base.ReadOnly = generated.ReadOnly
	}
	if !base.Compact {
		base.Compact = generated.Compact
	}
	if len(base.SecretPurposes) == 0 {
		base.SecretPurposes = generated.SecretPurposes
	}
	if len(base.Effects) == 0 {
		base.Effects = generated.Effects
	}
	if base.Risk == "" {
		base.Risk = generated.Risk
	}
	if base.Idempotency == "" {
		base.Idempotency = generated.Idempotency
	}
	if len(base.Access) == 0 {
		base.Access = generated.Access
	}
	if len(base.AuthScopes) == 0 {
		base.AuthScopes = generated.AuthScopes
	}
	if base.Render == nil {
		base.Render = generated.Render
	}
	return NormalizeOperationSpec(base)
}

func mergeDatasourceSpec(base, generated manifest.DatasourceSpec) manifest.DatasourceSpec {
	base.Name = firstNonEmpty(base.Name, generated.Name)
	base.Entity = firstNonEmpty(base.Entity, generated.Entity)
	base.Description = firstNonEmpty(base.Description, generated.Description)
	base.Capabilities = mergeStrings(base.Capabilities, generated.Capabilities)
	if len(base.SecretPurposes) == 0 {
		base.SecretPurposes = generated.SecretPurposes
	}
	if len(base.Input) == 0 {
		base.Input = generated.Input
	}
	if len(base.Output) == 0 {
		base.Output = generated.Output
	}
	if base.EntitySchema == nil {
		base.EntitySchema = generated.EntitySchema
	} else if generated.EntitySchema != nil {
		schema := mergeEntitySchema(*base.EntitySchema, *generated.EntitySchema)
		base.EntitySchema = &schema
	}
	base.Views = normalizeDatasourceViews(append(base.Views, generated.Views...))
	base.Relations = normalizeDatasourceRelations(append(base.Relations, generated.Relations...))
	if base.Fallback == "" {
		base.Fallback = generated.Fallback
	}
	if base.Completion == nil {
		base.Completion = generated.Completion
	} else if generated.Completion != nil {
		base.Completion.Fields = uniqueStringValues(append(base.Completion.Fields, generated.Completion.Fields...))
		if base.Completion.Description == "" {
			base.Completion.Description = generated.Completion.Description
		}
	}
	return NormalizeDatasourceSpec(base)
}

func mergeContextSpec(base, generated manifest.ContextSpec) manifest.ContextSpec {
	base.Name = fpcontext.ProviderName(firstNonEmpty(string(base.Name), string(generated.Name)))
	base.Description = firstNonEmpty(base.Description, generated.Description)
	base.Kinds = mergeBlockKinds(base.Kinds, generated.Kinds)
	return base
}

func filterContextBlocks(blocks []manifest.ContextBlock, input ContextBuildInput) []manifest.ContextBlock {
	if len(blocks) == 0 {
		return []manifest.ContextBlock{}
	}
	allowedKinds := map[string]bool{}
	for _, kind := range input.Kinds {
		kind = strings.TrimSpace(kind)
		if kind != "" {
			allowedKinds[kind] = true
		}
	}
	out := make([]manifest.ContextBlock, 0, len(blocks))
	for _, block := range blocks {
		if len(allowedKinds) > 0 && !allowedKinds[string(block.Kind)] {
			continue
		}
		out = append(out, block)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	if input.Limit > 0 && len(out) > input.Limit {
		out = out[:input.Limit]
	}
	return out
}

func selectDatasourceHandler(payload json.RawMessage, handlers []datasourceHandler) (datasourceHandler, error) {
	if len(handlers) == 0 {
		return nil, fmt.Errorf("no datasource handler")
	}
	datasource, err := payloadStringField(payload, "datasource")
	if err != nil {
		return nil, err
	}
	entity, err := payloadStringField(payload, "entity")
	if err != nil {
		return nil, err
	}
	if datasource == "" && entity == "" {
		return handlers[0], nil
	}
	var entityMatches []datasourceHandler
	for _, handler := range handlers {
		spec := handler.Spec()
		if datasource != "" && spec.Name != datasource {
			continue
		}
		if entity != "" && spec.Entity != entity {
			continue
		}
		if datasource != "" {
			return handler, nil
		}
		entityMatches = append(entityMatches, handler)
	}
	if datasource != "" {
		if entity != "" {
			return nil, fmt.Errorf("datasource %q does not expose entity %q", datasource, entity)
		}
		return nil, fmt.Errorf("unknown datasource %q", datasource)
	}
	if len(entityMatches) == 1 {
		return entityMatches[0], nil
	}
	if len(entityMatches) > 1 {
		return nil, fmt.Errorf("entity %q matches multiple datasources; pass datasource", entity)
	}
	return nil, fmt.Errorf("datasource does not expose entity %q", entity)
}

func payloadStringField(payload json.RawMessage, field string) (string, error) {
	if len(payload) == 0 {
		return "", nil
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	if value, ok := object[field].(string); ok {
		return strings.TrimSpace(value), nil
	}
	return "", nil
}

func ensureString(values []string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return append([]string(nil), values...)
	}
	for _, value := range values {
		if value == candidate {
			return append([]string(nil), values...)
		}
	}
	out := append([]string(nil), values...)
	return append(out, candidate)
}

func mergeStrings(base, generated []string) []string {
	out := append([]string(nil), base...)
	for _, value := range generated {
		out = ensureString(out, value)
	}
	return out
}

func mergeBlockKinds(base, generated []fpcontext.BlockKind) []fpcontext.BlockKind {
	out := append([]fpcontext.BlockKind(nil), base...)
	seen := map[fpcontext.BlockKind]bool{}
	for _, value := range out {
		seen[value] = true
	}
	for _, value := range generated {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx]
	}
	return text
}
