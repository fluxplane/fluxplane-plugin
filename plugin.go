package fluxplaneplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	coreactivation "github.com/fluxplane/fluxplane-core/core/activation"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	coredatasource "github.com/fluxplane/fluxplane-datasource"

	dex "github.com/fluxplane/fluxplane-dex"
	dexcore "github.com/fluxplane/fluxplane-dex/core"
)

// adapter exposes one dex plugin as a pluginhost.Plugin +
// OperationContributor + DatasourceProviderContributor +
// AssertionDeriverContributor. The intent deriver hangs off the adapter so
// every registered dex plugin contributes one to the pluginhost — which is
// fine because identical deriver Specs are deduplicated by name downstream.
type adapter struct {
	engine *dex.Engine
	name   string
	shared *adapterState

	manifestMu     sync.Mutex
	manifestCached bool
	manifest       dexcore.PluginManifest
}

type adapterState struct {
	intentOnce sync.Once
	intent     *intentIndex
}

var (
	_ pluginhost.Plugin                        = (*adapter)(nil)
	_ pluginhost.OperationContributor          = (*adapter)(nil)
	_ pluginhost.DatasourceProviderContributor = (*adapter)(nil)
	_ pluginhost.AssertionDeriverContributor   = (*adapter)(nil)
)

// Wrap returns a pluginhost.Plugin that proxies a single dex plugin into the
// fluxplane-core plugin host. The dex plugin must be resolvable in
// engine.Marketplace(); installation/activation state is not checked here so
// every marketplace entry is available for activation regardless of whether
// dex has stored credentials for it.
func Wrap(engine *dex.Engine, name string) (pluginhost.Plugin, error) {
	return wrapWithState(engine, name, &adapterState{})
}

func wrapWithState(engine *dex.Engine, name string, shared *adapterState) (pluginhost.Plugin, error) {
	if engine == nil {
		return nil, fmt.Errorf("fluxplaneplugin: engine is nil")
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, fmt.Errorf("fluxplaneplugin: plugin name is empty")
	}
	if _, ok := engine.Marketplace().Resolve(trimmed); !ok {
		return nil, fmt.Errorf("fluxplaneplugin: %w: %q", dex.ErrPluginNotFound, trimmed)
	}
	if shared == nil {
		shared = &adapterState{}
	}
	return &adapter{engine: engine, name: trimmed, shared: shared}, nil
}

// All wraps every plugin from the engine marketplace and returns one
// pluginhost.Plugin per dex plugin. Coverage is independent of auth/install
// state: every plugin can be surfaced for activation, the agent runs
// `dex auth connect` (or equivalent) on demand before invoking an op that
// needs credentials.
func All(engine *dex.Engine) ([]pluginhost.Plugin, error) {
	if engine == nil {
		return nil, fmt.Errorf("fluxplaneplugin: engine is nil")
	}
	entries := engine.Marketplace().Plugins()
	out := make([]pluginhost.Plugin, 0, len(entries))
	shared := &adapterState{}
	for _, entry := range entries {
		plug, err := wrapWithState(engine, entry.Name, shared)
		if err != nil {
			return nil, err
		}
		out = append(out, plug)
	}
	return out, nil
}

// Manifest reports the dex plugin's marketplace entry as a pluginhost manifest.
// Version is not surfaced here — the dex marketplace entry doesn't carry it;
// the live manifest does, and it's read during Contributions.
func (a *adapter) Manifest() pluginhost.Manifest {
	entry, _ := a.engine.Marketplace().Resolve(a.name)
	return pluginhost.Manifest{
		Name:        a.name,
		Description: entry.Description,
	}
}

// Contributions fetches the dex plugin manifest and contributes:
//   - operation.Spec entries (one per dex op, names left in dex's dotted form
//     e.g. "gitlab.mr.create");
//   - operation.Set entries grouped by second dotted segment, e.g.
//     "gitlab_mr" aggregating every "gitlab.mr.*" op. Activation sets in
//     fluxplane-core / coder typically target operation sets by name, so this
//     keeps the existing wiring shape (`gitlab_mr`, `gitlab_pipeline`, …) usable
//     with dex-backed operations underneath;
//   - datasource specs surfaced via DatasourceProviderContributor below.
func (a *adapter) Contributions(ctx context.Context, _ pluginhost.Context) (resource.ContributionBundle, error) {
	manifest, err := a.cachedManifest(ctx)
	if err != nil {
		// Plugin not installed / manifest unreachable. Return a stub bundle
		// with a PluginRef and a warning diagnostic instead of failing —
		// pluginhost.Resolve callers should not have startup blocked by a
		// dex plugin that just hasn't been installed yet. The agent can
		// `dex plugin install <name>` later and re-resolve.
		return resource.ContributionBundle{
			Plugins: []resource.PluginRef{{Name: a.name}},
			Diagnostics: []resource.Diagnostic{{
				Severity: resource.SeverityWarning,
				Message:  fmt.Sprintf("dex plugin %q manifest unavailable: %v", a.name, err),
			}},
		}, nil
	}
	specs := make([]operation.Spec, 0, len(manifest.Operations))
	for _, op := range manifest.Operations {
		specs = append(specs, dexOpToSpec(a.name, op))
	}
	// Emit ONE coredatasource.Spec per plugin (named after the plugin)
	// whose Entities list aggregates every dex datasource entity. This
	// matches the fluxplane-core convention where each plugin contributes
	// one datasource keyed on the plugin name and exposes its surface
	// through entity filters on datasource_search/list/get. The accessor
	// (see datasource.go) dispatches each request to the right dex
	// datasource based on req.Entity.
	datasources := aggregatedDatasourceSpecs(a.name, manifest.Datasources)
	return resource.ContributionBundle{
		Operations:     specs,
		OperationSets:  buildOperationSets(a.name, manifest.Operations),
		Datasources:    datasources,
		ActivationSets: buildActivationSets(a.name, manifest.Operations, datasources),
		Plugins:        []resource.PluginRef{{Name: a.name}},
	}, nil
}

// Operations returns executable bindings that call into the dex engine.
//
// When the plugin manifest can't be fetched (e.g. binary not installed yet)
// the result is an empty slice rather than an error, matching the tolerant
// behavior of Contributions. The op name → binding mapping is rebuilt the
// next time the plugin host resolves the ref after the plugin is installed.
func (a *adapter) Operations(ctx context.Context, pluginCtx pluginhost.Context) ([]operation.Operation, error) {
	manifest, err := a.cachedManifest(ctx)
	if err != nil {
		return nil, nil
	}
	instance := pluginCtx.Ref.Instance
	ops := make([]operation.Operation, 0, len(manifest.Operations))
	for _, op := range manifest.Operations {
		spec := dexOpToSpec(a.name, op)
		ops = append(ops, operation.New(spec, a.runner(op.Name, instance)))
	}
	return ops, nil
}

// AssertionDerivers contributes the dex intent deriver. Each registered
// adapter returns the same logical deriver (built from a fresh index over
// the engine's marketplace); pluginhost-level dedup keeps the runtime
// from invoking it more than once per turn.
func (a *adapter) AssertionDerivers(ctx context.Context, _ pluginhost.Context) ([]runtimeevidence.AssertionDeriver, error) {
	idx := a.cachedIntentIndex(ctx)
	if idx == nil || len(idx.keywords) == 0 {
		return nil, nil
	}
	return []runtimeevidence.AssertionDeriver{intentDeriver{index: idx}}, nil
}

func (a *adapter) cachedManifest(ctx context.Context) (dexcore.PluginManifest, error) {
	a.manifestMu.Lock()
	if a.manifestCached {
		manifest := a.manifest
		a.manifestMu.Unlock()
		return manifest, nil
	}
	a.manifestMu.Unlock()

	manifest, err := a.engine.Manifest(ctx, a.name)
	if err != nil {
		return dexcore.PluginManifest{}, err
	}
	a.manifestMu.Lock()
	a.manifest = manifest
	a.manifestCached = true
	a.manifestMu.Unlock()
	return manifest, nil
}

func (a *adapter) cachedIntentIndex(ctx context.Context) *intentIndex {
	if a.shared == nil {
		return buildIntentIndex(ctx, a.engine)
	}
	a.shared.intentOnce.Do(func() {
		a.shared.intent = buildIntentIndex(ctx, a.engine)
	})
	return a.shared.intent
}

func (a *adapter) runner(opName, instance string) operation.Handler {
	plugin := a.name
	engine := a.engine
	return func(ctx operation.Context, input operation.Value) operation.Result {
		resp, err := engine.Operations().RunInstance(ctx, plugin, instance, opName, input)
		if err != nil {
			return operation.Failed("dex_invoke_error", err.Error(), nil)
		}
		if resp.Error != nil {
			var details map[string]any
			if resp.Error.Code != "" {
				details = map[string]any{"dex_code": resp.Error.Code}
			}
			msg := resp.Error.Message
			if msg == "" {
				msg = "dex plugin returned error"
			}
			return operation.Failed("dex_plugin_error", msg, details)
		}
		var output operation.Value
		if len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, &output); err != nil {
				return operation.Failed("dex_decode_error", err.Error(), nil)
			}
		}
		return operation.OK(output)
	}
}

// qualifiedOpName returns the dex op name fully prefixed with the plugin
// scope. Dex names are already prefixed in modern manifests
// (e.g. "gitlab.mr.create"); older or unscoped names get the prefix added.
func qualifiedOpName(plugin, raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, plugin+".") {
		return raw
	}
	return plugin + "." + raw
}

func dexOpToSpec(plugin string, op dex.OperationSpec) operation.Spec {
	if op.Name == "" {
		return operation.Spec{}
	}
	full := qualifiedOpName(plugin, op.Name)
	spec := operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(full)},
		Description: op.Description,
	}
	if data := trimSchema(op.Input); len(data) > 0 {
		spec.Input = operation.Type{Name: full + "_input", Schema: operation.Schema{Format: "json-schema", Data: data}}
	}
	if data := trimSchema(op.Output); len(data) > 0 {
		spec.Output = operation.Type{Name: full + "_output", Schema: operation.Schema{Format: "json-schema", Data: data}}
	}
	return spec
}

// buildOperationSets groups dex ops by their second dotted segment.
//
// For dex op "gitlab.mr.create" the set name is "gitlab_mr" containing every
// "gitlab.mr.*" op. Ops without a second segment (e.g. a hypothetical
// "gitlab.ping") are skipped — those don't form a useful entity grouping for
// activation. In addition to the per-entity sets, an aggregate set named
// after the plugin (e.g. "gitlab") lists every op so a single activation can
// pull the whole surface.
func buildOperationSets(plugin string, ops []dex.OperationSpec) []operation.Set {
	grouped := map[string][]operation.Ref{}
	var order []string
	var all []operation.Ref
	for _, op := range ops {
		if op.Name == "" {
			continue
		}
		full := qualifiedOpName(plugin, op.Name)
		all = append(all, operation.Ref{Name: operation.Name(full)})

		rest := strings.TrimPrefix(full, plugin+".")
		segments := strings.SplitN(rest, ".", 2)
		if len(segments) < 2 || strings.TrimSpace(segments[0]) == "" {
			continue
		}
		setName := plugin + "_" + segments[0]
		if _, seen := grouped[setName]; !seen {
			order = append(order, setName)
		}
		grouped[setName] = append(grouped[setName], operation.Ref{Name: operation.Name(full)})
	}

	sets := make([]operation.Set, 0, len(order)+1)
	for _, name := range order {
		entity := strings.TrimPrefix(name, plugin+"_")
		sets = append(sets, operation.Set{
			Name:        name,
			Description: fmt.Sprintf("dex %s plugin %s operations.", plugin, entity),
			Operations:  grouped[name],
		})
	}
	if len(all) > 0 {
		sets = append(sets, operation.Set{
			Name:        plugin,
			Description: fmt.Sprintf("All dex %s plugin operations.", plugin),
			Operations:  all,
		})
	}
	return sets
}

// aggregatedDatasourceSpecs returns a single coredatasource.Spec named after
// the plugin whose Entities list aggregates every entity declared across the
// dex per-entity datasources. The per-entity richness (descriptions, fields,
// capabilities, schemas) flows separately through Provider.Entities() at
// runtime — keeping the contribution Spec flat in entity names while the
// EntitySpec graph carries the structure agents reason about.
//
// Plugins with no datasource entries produce a nil result so an empty
// Datasources slice doesn't appear in the bundle.
func aggregatedDatasourceSpecs(plugin string, sources []dex.DatasourceSpec) []coredatasource.Spec {
	plugin = strings.TrimSpace(plugin)
	if plugin == "" {
		return nil
	}
	seen := map[string]bool{}
	entities := make([]coredatasource.EntityType, 0, len(sources))
	descriptions := make([]string, 0, len(sources))
	for _, src := range sources {
		entity := strings.TrimSpace(src.Entity)
		if entity == "" || seen[entity] {
			continue
		}
		seen[entity] = true
		entities = append(entities, coredatasource.EntityType(entity))
		if d := strings.TrimSpace(src.Description); d != "" {
			descriptions = append(descriptions, d)
		}
	}
	if len(entities) == 0 {
		return nil
	}
	desc := strings.Join(descriptions, " | ")
	if desc == "" {
		desc = fmt.Sprintf("Dex %s plugin datasource.", plugin)
	}
	return []coredatasource.Spec{{
		Name:        coredatasource.Name(plugin),
		Description: desc,
		Kind:        plugin,
		Entities:    entities,
	}}
}

// buildActivationSets emits one aggregate activation set named after the
// plugin (e.g. "slack") and one per dotted entity group (e.g. "slack_users").
// Each set targets both the matching operation set AND any datasource(s) the
// plugin provides for that scope. This pairing is what makes the intent
// reaction's "enable activation set slack" actually expose slack's datasource
// to the agent — operation-set activation alone leaves datasource_list/search
// denied.
//
// Datasource→entity matching for per-entity sets uses the dex declared
// `Entities[0]` of each datasource spec (lowercased). If a datasource carries
// no entity it only attaches to the aggregate set.
func buildActivationSets(plugin string, ops []dex.OperationSpec, datasources []coredatasource.Spec) []coreactivation.Set {
	if plugin == "" {
		return nil
	}

	entities := map[string]struct{}{}
	for _, op := range ops {
		if op.Name == "" {
			continue
		}
		full := qualifiedOpName(plugin, op.Name)
		rest := strings.TrimPrefix(full, plugin+".")
		segments := strings.SplitN(rest, ".", 2)
		if len(segments) < 2 {
			continue
		}
		entity := strings.TrimSpace(segments[0])
		if entity == "" {
			continue
		}
		entities[strings.ToLower(entity)] = struct{}{}
	}

	aggregate := coreactivation.Set{
		Name:        plugin,
		Description: fmt.Sprintf("Activate the %s dex surface (operations + datasources).", plugin),
		Targets:     []coreactivation.Target{{Kind: coreactivation.TargetOperationSet, OperationSet: plugin}},
	}
	for _, ds := range datasources {
		if strings.TrimSpace(string(ds.Name)) == "" {
			continue
		}
		aggregate.Targets = append(aggregate.Targets, coreactivation.Target{
			Kind:       coreactivation.TargetDatasource,
			Datasource: coredatasource.Ref{Name: ds.Name},
		})
	}

	sets := []coreactivation.Set{aggregate}
	for entity := range entities {
		setName := plugin + "_" + entity
		set := coreactivation.Set{
			Name:        setName,
			Description: fmt.Sprintf("Activate the %s dex %s surface.", plugin, entity),
			Targets:     []coreactivation.Target{{Kind: coreactivation.TargetOperationSet, OperationSet: setName}},
		}
		for _, ds := range datasources {
			if matchesEntity(ds, entity) {
				set.Targets = append(set.Targets, coreactivation.Target{
					Kind:       coreactivation.TargetDatasource,
					Datasource: coredatasource.Ref{Name: ds.Name},
				})
			}
		}
		sets = append(sets, set)
	}
	return sets
}

func matchesEntity(ds coredatasource.Spec, entity string) bool {
	for _, e := range ds.Entities {
		if strings.EqualFold(string(e), entity) {
			return true
		}
	}
	return false
}

func trimSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}
