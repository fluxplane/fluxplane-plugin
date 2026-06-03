package pluginbinding

import (
	"encoding/json"

	fpcontext "github.com/fluxplane/fluxplane-context"
	datasource "github.com/fluxplane/fluxplane-datasource"
	manifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

const (
	CapabilitySearch   = "search"
	CapabilityList     = "list"
	CapabilityLookup   = "lookup"
	CapabilityGet      = "get"
	CapabilityBatchGet = "batch_get"
	CapabilityIndex    = "index"

	ContextKindText      = "text"
	ContextKindReference = "reference"
	ContextKindData      = "data"
)

type ManifestSpec struct {
	Name               string
	Version            string
	Description        string
	Aliases            []string
	Operations         []manifest.OperationSpec
	Auth               []manifest.AuthMethod
	Datasources        []manifest.DatasourceSpec
	IndexedDatasources []IndexedDatasourceSpec
	Context            []manifest.ContextSpec
	Endpoints          []manifest.EndpointSpec
	Indexes            []manifest.IndexSpec
	Metadata           map[string]string
}

type OperationSpecOption func(*manifest.OperationSpec)
type DatasourceSpecOption func(*manifest.DatasourceSpec)

type IndexedDatasourceSpec struct {
	Name             string
	Entity           string
	Description      string
	IndexDescription string
	Capabilities     []string
	Options          []DatasourceSpecOption
}

func Manifest(spec ManifestSpec) manifest.PluginManifest {
	datasources := normalizeDatasourceSpecs(spec.Datasources)
	indexes := append([]manifest.IndexSpec(nil), spec.Indexes...)
	for _, indexed := range spec.IndexedDatasources {
		datasource := Datasource(indexed.Name, indexed.Entity, indexed.Description, indexed.Capabilities...)
		for _, option := range indexed.Options {
			if option != nil {
				option(&datasource)
			}
		}
		datasources = append(datasources, NormalizeDatasourceSpec(datasource))
		indexDescription := indexed.IndexDescription
		if indexDescription == "" {
			indexDescription = indexed.Description
		}
		indexes = append(indexes, Index(indexed.Name, indexDescription, indexed.Entity))
	}
	return manifest.PluginManifest{
		Name:        spec.Name,
		Version:     spec.Version,
		Description: spec.Description,
		Aliases:     append([]string(nil), spec.Aliases...),
		Operations:  normalizeOperationSpecs(spec.Operations),
		Auth:        append([]manifest.AuthMethod(nil), spec.Auth...),
		Datasources: datasources,
		Context:     append([]manifest.ContextSpec(nil), spec.Context...),
		Endpoints:   append([]manifest.EndpointSpec(nil), spec.Endpoints...),
		Indexes:     indexes,
		Metadata:    cloneStringMap(spec.Metadata),
	}
}

func OperationSpec(name, description string, options ...OperationSpecOption) manifest.OperationSpec {
	spec := manifest.OperationSpec{Name: name, Description: description}
	for _, option := range options {
		option(&spec)
	}
	return spec
}

func ReadOnly() OperationSpecOption {
	return func(spec *manifest.OperationSpec) {
		spec.ReadOnly = true
	}
}

func Compact() OperationSpecOption {
	return func(spec *manifest.OperationSpec) {
		spec.Compact = true
		if spec.Render == nil {
			spec.Render = &manifest.OperationRenderSpec{Preferred: "compact", Formats: []string{"text", "compact", "json", "yaml"}}
		}
	}
}

func SecretPurposes(purposes ...string) OperationSpecOption {
	return func(spec *manifest.OperationSpec) {
		spec.SecretPurposes = append([]string(nil), purposes...)
	}
}

func DatasourceAccess(access ...manifest.OperationAccess) DatasourceSpecOption {
	return func(spec *manifest.DatasourceSpec) {
		spec.Access = make([]datasource.Access, 0, len(access))
		for _, value := range access {
			spec.Access = append(spec.Access, datasource.Access(value))
		}
	}
}

func Effects(effects ...manifest.OperationEffect) OperationSpecOption {
	return func(spec *manifest.OperationSpec) {
		spec.Effects = append([]manifest.OperationEffect(nil), effects...)
	}
}

func Risk(risk manifest.OperationRisk) OperationSpecOption {
	return func(spec *manifest.OperationSpec) {
		spec.Risk = risk
	}
}

func Idempotency(idempotency manifest.OperationIdempotency) OperationSpecOption {
	return func(spec *manifest.OperationSpec) {
		spec.Idempotency = idempotency
	}
}

func Access(access ...manifest.OperationAccess) OperationSpecOption {
	return func(spec *manifest.OperationSpec) {
		spec.Access = append([]manifest.OperationAccess(nil), access...)
	}
}

func AuthScopes(scopes ...string) OperationSpecOption {
	return func(spec *manifest.OperationSpec) {
		spec.AuthScopes = append([]string(nil), scopes...)
	}
}

func Render(preferred string, formats ...string) OperationSpecOption {
	return func(spec *manifest.OperationSpec) {
		spec.Render = &manifest.OperationRenderSpec{Preferred: preferred, Formats: append([]string(nil), formats...)}
	}
}

func DatasourceSecretPurposes(purposes ...string) DatasourceSpecOption {
	return func(spec *manifest.DatasourceSpec) {
		spec.SecretPurposes = append([]string(nil), purposes...)
	}
}

func BearerAuth(name, description string, fields ...manifest.AuthField) manifest.AuthMethod {
	return manifest.AuthMethod{
		Name:        name,
		Kind:        "bearer_token",
		Description: description,
		Env:         authEnv(fields),
		Fields:      append([]manifest.AuthField(nil), fields...),
	}
}

func AuthField(name, description string, required, secret bool, env ...string) manifest.AuthField {
	return manifest.AuthField{
		Name:        name,
		Description: description,
		Required:    required,
		Sensitive:   secret,
		Secret:      secret,
		Env:         append([]string(nil), env...),
	}
}

func Datasource(name, entity, description string, capabilities ...string) manifest.DatasourceSpec {
	return manifest.DatasourceSpec{
		Name:         name,
		Entity:       entity,
		Description:  description,
		Capabilities: append([]string(nil), capabilities...),
	}
}

func ContextSpec(name, description string, kinds ...string) manifest.ContextSpec {
	return manifest.ContextSpec{
		Name:        fpcontext.ProviderName(name),
		Description: description,
		Kinds:       blockKinds(kinds),
	}
}

func blockKinds(values []string) []fpcontext.BlockKind {
	out := make([]fpcontext.BlockKind, 0, len(values))
	for _, value := range values {
		out = append(out, fpcontext.BlockKind(value))
	}
	return out
}

func Endpoint(name, description string, products ...string) manifest.EndpointSpec {
	return manifest.EndpointSpec{
		Name:        name,
		Description: description,
		Products:    append([]string(nil), products...),
	}
}

func Index(name, description string, entities ...string) manifest.IndexSpec {
	return manifest.IndexSpec{
		Name:        name,
		Description: description,
		Entities:    append([]string(nil), entities...),
	}
}

func IndexedDatasource(name, entity, description, indexDescription string, capabilities ...string) IndexedDatasourceSpec {
	return IndexedDatasourceSpec{
		Name:             name,
		Entity:           entity,
		Description:      description,
		IndexDescription: indexDescription,
		Capabilities:     append([]string(nil), capabilities...),
	}
}

func IndexedDatasourceWithOptions(name, entity, description, indexDescription string, capabilities []string, options ...DatasourceSpecOption) IndexedDatasourceSpec {
	return IndexedDatasourceSpec{
		Name:             name,
		Entity:           entity,
		Description:      description,
		IndexDescription: indexDescription,
		Capabilities:     append([]string(nil), capabilities...),
		Options:          append([]DatasourceSpecOption(nil), options...),
	}
}

func SearchableIndexCapabilities() []string {
	return []string{CapabilitySearch, CapabilityList, CapabilityLookup, CapabilityGet, CapabilityBatchGet, CapabilityIndex}
}

func authEnv(fields []manifest.AuthField) []string {
	seen := map[string]bool{}
	var out []string
	for _, field := range fields {
		for _, env := range field.Env {
			if env == "" || seen[env] {
				continue
			}
			seen[env] = true
			out = append(out, env)
		}
	}
	return out
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func normalizeOperationSpecs(specs []manifest.OperationSpec) []manifest.OperationSpec {
	out := make([]manifest.OperationSpec, 0, len(specs))
	for _, spec := range specs {
		out = append(out, NormalizeOperationSpec(spec))
	}
	return out
}

func NormalizeOperationSpec(spec manifest.OperationSpec) manifest.OperationSpec {
	spec.Input = normalizeOperationInputSchema(spec.Input)
	spec.Effects = uniqueOperationEffects(spec.Effects)
	spec.Access = uniqueOperationAccess(spec.Access)
	spec.AuthScopes = uniqueStringValues(spec.AuthScopes)
	spec.SecretPurposes = uniqueStringValues(spec.SecretPurposes)
	if len(spec.Effects) == 0 {
		if spec.ReadOnly {
			spec.Effects = []manifest.OperationEffect{manifest.OperationEffectRead}
		} else {
			spec.Effects = []manifest.OperationEffect{manifest.OperationEffectWrite}
		}
	}
	if spec.Risk == "" {
		if spec.ReadOnly {
			spec.Risk = manifest.OperationRiskLow
		} else {
			spec.Risk = manifest.OperationRiskMedium
		}
	}
	if spec.Idempotency == "" {
		if spec.ReadOnly {
			spec.Idempotency = manifest.OperationIdempotent
		} else {
			spec.Idempotency = manifest.OperationUnknown
		}
	}
	for _, effect := range spec.Effects {
		switch effect {
		case manifest.OperationEffectNetwork:
			spec.Access = ensureOperationAccess(spec.Access, manifest.OperationAccessNetwork)
		case manifest.OperationEffectProcess:
			spec.Access = ensureOperationAccess(spec.Access, manifest.OperationAccessProcess)
		case manifest.OperationEffectBrowser:
			spec.Access = ensureOperationAccess(spec.Access, manifest.OperationAccessBrowser)
		case manifest.OperationEffectFilesystem:
			spec.Access = ensureOperationAccess(spec.Access, manifest.OperationAccessFilesystem)
		case manifest.OperationEffectLocalSystem:
			spec.Access = ensureOperationAccess(spec.Access, manifest.OperationAccessLocalSystem)
		}
	}
	if len(spec.SecretPurposes) > 0 {
		spec.Access = ensureOperationAccess(spec.Access, manifest.OperationAccessSecret)
		spec.Access = ensureOperationAccess(spec.Access, manifest.OperationAccessAuth)
	}
	if len(spec.Access) == 0 {
		spec.Access = []manifest.OperationAccess{manifest.OperationAccessNone}
	} else if len(spec.Access) > 1 {
		spec.Access = removeOperationAccess(spec.Access, manifest.OperationAccessNone)
	}
	if spec.Render == nil && spec.Compact {
		spec.Render = &manifest.OperationRenderSpec{Preferred: "compact", Formats: []string{"text", "compact", "json", "yaml"}}
	}
	if spec.Render != nil {
		spec.Render.Formats = uniqueStringValues(spec.Render.Formats)
	}
	return spec
}

func normalizeOperationInputSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		raw = json.RawMessage(`{"properties":{},"type":"object"}`)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return raw
	}
	if schema == nil {
		schema = map[string]any{}
	}
	if schemaType, _ := schema["type"].(string); schemaType != "" && schemaType != "object" {
		return raw
	}
	schema["type"] = "object"
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
		schema["properties"] = properties
	}
	if _, ok := properties["endpoint_ref"]; !ok {
		properties["endpoint_ref"] = map[string]any{
			"type":        "string",
			"description": "Registered endpoint ref resolved by the host before invoking the operation.",
		}
	}
	normalized, err := json.Marshal(normalizeSchemaValue(schema))
	if err != nil {
		return raw
	}
	return normalized
}

func uniqueStringValues(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func uniqueOperationEffects(values []manifest.OperationEffect) []manifest.OperationEffect {
	seen := map[manifest.OperationEffect]bool{}
	var out []manifest.OperationEffect
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func uniqueOperationAccess(values []manifest.OperationAccess) []manifest.OperationAccess {
	seen := map[manifest.OperationAccess]bool{}
	var out []manifest.OperationAccess
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func ensureOperationAccess(values []manifest.OperationAccess, candidate manifest.OperationAccess) []manifest.OperationAccess {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func removeOperationAccess(values []manifest.OperationAccess, candidate manifest.OperationAccess) []manifest.OperationAccess {
	out := values[:0]
	for _, value := range values {
		if value != candidate {
			out = append(out, value)
		}
	}
	return out
}
