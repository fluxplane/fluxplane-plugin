package fluxplaneplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	coredatasource "github.com/fluxplane/fluxplane-datasource"

	dex "github.com/fluxplane/fluxplane-dex"
)

// DatasourceProviders bridges dex-declared datasources to the core
// datasource.Provider surface. One Provider is returned per dex plugin; it
// exposes every entity declared in the dex manifest. Open() yields an
// Accessor that proxies Search and Get through the dex Datasources service.
//
// Lifecycle: the Provider is constructed from a manifest snapshot taken at
// resolution time. Plugins whose manifest can't be fetched yield a Provider
// that has no entities — callers see an empty set rather than a hard failure
// so listing is robust to transient dex errors.
func (a *adapter) DatasourceProviders(ctx context.Context, pluginCtx pluginhost.Context) ([]coredatasource.Provider, error) {
	manifest, err := a.cachedManifest(ctx)
	if err != nil {
		return []coredatasource.Provider{&dexDatasourceProvider{plugin: a.name, instance: pluginCtx.Ref.Instance, engine: a.engine}}, nil
	}
	specs := append([]dex.DatasourceSpec(nil), manifest.Datasources...)
	return []coredatasource.Provider{&dexDatasourceProvider{
		plugin:   a.name,
		instance: pluginCtx.Ref.Instance,
		engine:   a.engine,
		sources:  specs,
	}}, nil
}

type dexDatasourceProvider struct {
	plugin   string
	instance string
	engine   *dex.Engine
	sources  []dex.DatasourceSpec
}

// Entities returns the union of entity specs declared across this plugin's
// datasources. EntitySpec.Fields/Capabilities are best-effort mappings from
// the dex DatasourceEntitySchema.
func (p *dexDatasourceProvider) Entities() []coredatasource.EntitySpec {
	seen := map[string]bool{}
	out := make([]coredatasource.EntitySpec, 0, len(p.sources))
	for _, src := range p.sources {
		entity := strings.TrimSpace(src.Entity)
		if entity == "" || seen[entity] {
			continue
		}
		seen[entity] = true
		out = append(out, dexEntitySpec(src))
	}
	return out
}

// Open returns an Accessor for the configured spec. Two shapes are supported:
//
//   - Aggregated: spec.Name equals the plugin name (e.g. "gitlab"). The
//     accessor's byEntity map covers every dex datasource the plugin
//     declares, so Search/List/Get on any of the spec.Entities routes to
//     the right per-entity dex source.
//   - Legacy single-source: spec.Name matches a specific dex datasource
//     name (e.g. "gitlab.projects"). Older callers that still address one
//     dex source directly keep working; byEntity contains only that
//     source's entity.
//
// Any other spec name with no matching entity is rejected so callers
// learn at Open time, not via an empty Search result.
func (p *dexDatasourceProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	name := strings.TrimSpace(string(spec.Name))
	if name == p.plugin {
		byEntity := map[string]dex.DatasourceSpec{}
		for _, src := range p.sources {
			entity := strings.TrimSpace(src.Entity)
			if entity == "" {
				continue
			}
			if _, exists := byEntity[entity]; !exists {
				byEntity[entity] = src
			}
		}
		var fallback dex.DatasourceSpec
		for _, entity := range spec.Entities {
			if src, ok := byEntity[string(entity)]; ok {
				fallback = src
				break
			}
		}
		if fallback.Name == "" && len(p.sources) > 0 {
			fallback = p.sources[0]
		}
		if fallback.Name == "" {
			return nil, fmt.Errorf("fluxplaneplugin: plugin %q has no dex datasources", p.plugin)
		}
		return &dexAccessor{
			plugin:   p.plugin,
			instance: p.instance,
			engine:   p.engine,
			spec:     spec,
			byEntity: byEntity,
			fallback: fallback,
		}, nil
	}
	for _, src := range p.sources {
		if src.Name == name {
			byEntity := map[string]dex.DatasourceSpec{}
			if entity := strings.TrimSpace(src.Entity); entity != "" {
				byEntity[entity] = src
			}
			return &dexAccessor{
				plugin:   p.plugin,
				instance: p.instance,
				engine:   p.engine,
				spec:     spec,
				byEntity: byEntity,
				fallback: src,
			}, nil
		}
	}
	return nil, fmt.Errorf("fluxplaneplugin: no dex datasource matches %q (entities=%v)", spec.Name, spec.Entities)
}

func dexEntitySpec(src dex.DatasourceSpec) coredatasource.EntitySpec {
	entity := coredatasource.EntitySpec{
		Type:        coredatasource.EntityType(src.Entity),
		Description: src.Description,
	}
	for _, cap := range src.Capabilities {
		cap = strings.TrimSpace(cap)
		if cap != "" {
			entity.Capabilities = append(entity.Capabilities, coredatasource.EntityCapability(cap))
		}
	}
	if src.EntitySchema != nil {
		for _, f := range src.EntitySchema.Fields {
			entity.Fields = append(entity.Fields, coredatasource.FieldSpec{
				Name:        f.Name,
				Type:        coredatasource.FieldType(f.Type),
				Description: f.Description,
			})
		}
	}
	return entity
}

// dexAccessor implements the base Accessor plus Searcher, Lister, and Getter.
// List is mapped to an empty-query datasource search, which lets host-backed
// indexes expose their first page without adding another dex protocol command.
//
// byEntity routes per-request entity strings to the matching dex
// DatasourceSpec; fallback is used when req.Entity is empty (single-entity
// callers or legacy specs).
type dexAccessor struct {
	plugin   string
	instance string
	engine   *dex.Engine
	spec     coredatasource.Spec
	byEntity map[string]dex.DatasourceSpec
	fallback dex.DatasourceSpec
}

// sourceFor resolves the dex datasource for a given core EntityType. Returns
// the fallback only when entity is empty, so typoed or unsupported entity names
// fail closed instead of querying and relabeling an unrelated datasource.
func (a *dexAccessor) sourceFor(entity string) (dex.DatasourceSpec, error) {
	entity = strings.TrimSpace(entity)
	if entity == "" {
		return a.fallback, nil
	}
	if src, ok := a.byEntity[entity]; ok {
		return src, nil
	}
	return dex.DatasourceSpec{}, fmt.Errorf("fluxplaneplugin: datasource %q does not support entity %q", a.spec.Name, entity)
}

var (
	_ coredatasource.Accessor = (*dexAccessor)(nil)
	_ coredatasource.Searcher = (*dexAccessor)(nil)
	_ coredatasource.Lister   = (*dexAccessor)(nil)
	_ coredatasource.Getter   = (*dexAccessor)(nil)
)

func (a *dexAccessor) Spec() coredatasource.Spec { return a.spec }

func (a *dexAccessor) applySpecConfig(payload map[string]any) {
	if payload == nil || len(a.spec.Config) == 0 {
		return
	}
	if endpointRef := strings.TrimSpace(a.spec.Config["endpoint_ref"]); endpointRef != "" {
		payload["endpoint_ref"] = endpointRef
	}
}

// Entities exposes the rich per-entity metadata for every dex datasource the
// accessor can route to, so the host's datasource_get_schema / introspection
// surface sees all entities the plugin actually serves — not just the
// fallback's. The contribution Spec only carries entity names; this is where
// per-entity descriptions, capabilities, and field schemas surface.
func (a *dexAccessor) Entities() []coredatasource.EntitySpec {
	out := make([]coredatasource.EntitySpec, 0, len(a.byEntity))
	for _, src := range a.byEntity {
		out = append(out, dexEntitySpec(src))
	}
	return out
}

// Search proxies a search through dex. The request shape sent to dex follows
// the dex datasource search convention: {datasource, entity, query, limit,
// filters}. The datasource name in the payload is the entity-specific dex
// source (e.g. gitlab.projects) so the dex plugin's handler still dispatches
// on its own per-source registry.
func (a *dexAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	src, err := a.sourceFor(string(req.Entity))
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	entity := strings.TrimSpace(string(req.Entity))
	if entity == "" {
		entity = src.Entity
	}
	payload := map[string]any{
		"datasource": src.Name,
		"entity":     entity,
	}
	a.applySpecConfig(payload)
	if q := strings.TrimSpace(req.Query); q != "" {
		payload["query"] = q
	}
	if req.Limit > 0 {
		payload["limit"] = req.Limit
	}
	if len(req.Filters) > 0 {
		payload["filters"] = req.Filters
	}
	resp, err := a.engine.Datasources().SearchInstance(ctx, a.plugin, a.instance, payload)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	if resp.Error != nil {
		return coredatasource.SearchResult{}, fmt.Errorf("dex %s: %s", a.plugin, resp.Error.Message)
	}
	result := coredatasource.SearchResult{
		Datasource: a.spec.Name,
		Entity:     coredatasource.EntityType(entity),
	}
	result.Records = decodeRecords(resp.Result, result.Datasource, result.Entity)
	if total, ok := decodeTotal(resp.Result); ok {
		result.Total = total
	}
	return result, nil
}

func (a *dexAccessor) List(ctx context.Context, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	src, err := a.sourceFor(string(req.Entity))
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	entity := strings.TrimSpace(string(req.Entity))
	if entity == "" {
		entity = src.Entity
	}
	payload := map[string]any{
		"datasource": src.Name,
		"entity":     entity,
	}
	a.applySpecConfig(payload)
	if req.Limit > 0 {
		payload["limit"] = req.Limit
	}
	if req.Cursor != "" {
		payload["cursor"] = strings.TrimSpace(req.Cursor)
	}
	if len(req.Filters) > 0 {
		payload["filters"] = req.Filters
	}
	resp, err := a.engine.Datasources().SearchInstance(ctx, a.plugin, a.instance, payload)
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	if resp.Error != nil {
		return coredatasource.ListResult{}, fmt.Errorf("dex %s: %s", a.plugin, resp.Error.Message)
	}
	result := coredatasource.ListResult{
		Datasource: a.spec.Name,
		Entity:     coredatasource.EntityType(entity),
		Complete:   true,
	}
	result.Records = decodeRecords(resp.Result, result.Datasource, result.Entity)
	if total, ok := decodeTotal(resp.Result); ok {
		result.Total = total
	} else {
		result.Total = len(result.Records)
	}
	return result, nil
}

func (a *dexAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	src, err := a.sourceFor(string(req.Entity))
	if err != nil {
		return coredatasource.Record{}, err
	}
	entity := strings.TrimSpace(string(req.Entity))
	if entity == "" {
		entity = src.Entity
	}
	payload := map[string]any{
		"datasource": src.Name,
		"entity":     entity,
		"id":         req.ID,
	}
	a.applySpecConfig(payload)
	resp, err := a.engine.Datasources().GetInstance(ctx, a.plugin, a.instance, payload)
	if err != nil {
		return coredatasource.Record{}, err
	}
	if resp.Error != nil {
		return coredatasource.Record{}, fmt.Errorf("dex %s: %s", a.plugin, resp.Error.Message)
	}
	records := decodeRecords(resp.Result, a.spec.Name, coredatasource.EntityType(entity))
	if len(records) > 0 {
		return records[0], nil
	}
	return coredatasource.Record{
		ID:         req.ID,
		Datasource: a.spec.Name,
		Entity:     coredatasource.EntityType(entity),
		Raw:        json.RawMessage(resp.Result),
	}, nil
}

// decodeRecords best-effort flattens a dex JSON payload into core Records.
// Supported shapes:
//   - {"records":[…]} or {"results":[…]} — each element is a record-shaped object;
//   - a top-level array of record-shaped objects;
//   - a single record-shaped object — returned as a one-element list.
func decodeRecords(raw json.RawMessage, ds coredatasource.Name, entity coredatasource.EntityType) []coredatasource.Record {
	if len(raw) == 0 {
		return nil
	}
	var asObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asObject); err == nil {
		if record, ok := asObject["record"]; ok {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(record, &obj); err == nil {
				return []coredatasource.Record{recordFromObject(obj, ds, entity)}
			}
		}
		for _, key := range []string{"records", "results", "items"} {
			if arr, ok := asObject[key]; ok {
				return recordsFromArray(arr, ds, entity)
			}
		}
		return []coredatasource.Record{recordFromObject(asObject, ds, entity)}
	}
	var asArray []json.RawMessage
	if err := json.Unmarshal(raw, &asArray); err == nil {
		return recordsFromArrayElements(asArray, ds, entity)
	}
	return nil
}

func recordsFromArray(arr json.RawMessage, ds coredatasource.Name, entity coredatasource.EntityType) []coredatasource.Record {
	var elems []json.RawMessage
	if err := json.Unmarshal(arr, &elems); err != nil {
		return nil
	}
	return recordsFromArrayElements(elems, ds, entity)
}

func recordsFromArrayElements(elems []json.RawMessage, ds coredatasource.Name, entity coredatasource.EntityType) []coredatasource.Record {
	out := make([]coredatasource.Record, 0, len(elems))
	for _, elem := range elems {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(elem, &obj); err != nil {
			continue
		}
		out = append(out, recordFromObject(obj, ds, entity))
	}
	return out
}

func recordFromObject(obj map[string]json.RawMessage, ds coredatasource.Name, entity coredatasource.EntityType) coredatasource.Record {
	rec := coredatasource.Record{
		Datasource: ds,
		Entity:     entity,
	}
	if raw, ok := obj["id"]; ok {
		_ = json.Unmarshal(raw, &rec.ID)
	}
	if raw, ok := obj["title"]; ok {
		_ = json.Unmarshal(raw, &rec.Title)
	}
	if raw, ok := obj["url"]; ok {
		_ = json.Unmarshal(raw, &rec.URL)
	}
	if raw, ok := obj["content"]; ok {
		_ = json.Unmarshal(raw, &rec.Content)
	}
	if raw, ok := obj["score"]; ok {
		_ = json.Unmarshal(raw, &rec.Score)
	}
	if raw, ok := obj["metadata"]; ok {
		_ = json.Unmarshal(raw, &rec.Metadata)
	}
	// Preserve full payload for downstream consumers that need fields the
	// adapter doesn't map.
	flat := map[string]json.RawMessage{}
	for k, v := range obj {
		flat[k] = v
	}
	rec.Raw = flat
	return rec
}

func decodeTotal(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var obj struct {
		Total int `json:"total"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return 0, false
	}
	if obj.Total > 0 {
		return obj.Total, true
	}
	if obj.Count > 0 {
		return obj.Count, true
	}
	return 0, false
}
