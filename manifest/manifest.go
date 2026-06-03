// Package manifest exposes reusable plugin manifest contracts.
package manifest

import (
	"strings"

	auth "github.com/fluxplane/fluxplane-auth"
	fpcontext "github.com/fluxplane/fluxplane-context"
	datasource "github.com/fluxplane/fluxplane-datasource"
	endpoint "github.com/fluxplane/fluxplane-endpoint"
	evidence "github.com/fluxplane/fluxplane-evidence"
	operation "github.com/fluxplane/fluxplane-operation"
	secret "github.com/fluxplane/fluxplane-secret"
)

type Marketplace struct {
	Version string        `json:"version"`
	Plugins []PluginEntry `json:"plugins"`
}

type PluginEntry struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Binary      string            `json:"binary"`
	GoInstall   string            `json:"go_install,omitempty"`
	LocalPath   string            `json:"local_path,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type PluginManifest struct {
	Name              string                 `json:"name"`
	Version           string                 `json:"version,omitempty"`
	Description       string                 `json:"description,omitempty"`
	Aliases           []string               `json:"aliases,omitempty"`
	Operations        []OperationSpec        `json:"operations,omitempty"`
	Auth              []AuthMethod           `json:"auth,omitempty"`
	Datasources       []DatasourceSpec       `json:"datasources,omitempty"`
	Context           []ContextSpec          `json:"context,omitempty"`
	Observers         []ObserverSpec         `json:"observers,omitempty"`
	AssertionDerivers []AssertionDeriverSpec `json:"assertion_derivers,omitempty"`
	Endpoints         []EndpointSpec         `json:"endpoints,omitempty"`
	Indexes           []IndexSpec            `json:"indexes,omitempty"`
	Metadata          map[string]string      `json:"metadata,omitempty"`
}

type OperationSpec = operation.Declaration
type OperationEffect = operation.Effect

const (
	OperationEffectRead        = operation.EffectRead
	OperationEffectWrite       = operation.EffectWrite
	OperationEffectNetwork     = operation.EffectNetwork
	OperationEffectProcess     = operation.EffectProcess
	OperationEffectBrowser     = operation.EffectBrowser
	OperationEffectFilesystem  = operation.EffectFilesystem
	OperationEffectLocalSystem = operation.EffectLocalSystem
)

type OperationRisk = operation.RiskLevel

const (
	OperationRiskLow         = operation.RiskLow
	OperationRiskMedium      = operation.RiskMedium
	OperationRiskHigh        = operation.RiskHigh
	OperationRiskDestructive = operation.RiskDestructive
)

type OperationIdempotency = operation.Idempotency

const (
	OperationIdempotent    = operation.IdempotencyIdempotent
	OperationNonIdempotent = operation.IdempotencyNonIdempotent
	OperationConditional   = operation.IdempotencyConditional
	OperationUnknown       = operation.IdempotencyUnknownText
)

type OperationAccess = operation.Access

const (
	OperationAccessNone        = operation.AccessNone
	OperationAccessAuth        = operation.AccessAuth
	OperationAccessSecret      = operation.AccessSecret
	OperationAccessNetwork     = operation.AccessNetwork
	OperationAccessProvider    = operation.AccessProvider
	OperationAccessProcess     = operation.AccessProcess
	OperationAccessBrowser     = operation.AccessBrowser
	OperationAccessFilesystem  = operation.AccessFilesystem
	OperationAccessLocalSystem = operation.AccessLocalSystem
)

type OperationRenderSpec = operation.RenderSpec

type AuthMethod struct {
	Name        string            `json:"name"`
	Kind        secret.Kind       `json:"kind"`
	Description string            `json:"description,omitempty"`
	Env         []string          `json:"env,omitempty"`
	Fields      []AuthField       `json:"fields,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func (m AuthMethod) MethodSpec() auth.MethodSpec {
	fields := make([]auth.FieldSpec, 0, len(m.Fields))
	for _, field := range m.Fields {
		fields = append(fields, field.FieldSpec())
	}
	return auth.MethodSpec{
		Name:        strings.TrimSpace(m.Name),
		Method:      auth.MethodStored,
		Scheme:      auth.SchemeBearerToken,
		Kind:        m.Kind,
		Description: strings.TrimSpace(m.Description),
		Env:         auth.EnvSpec{Aliases: trimStrings(m.Env)},
		SetupFields: fields,
		Annotations: cloneStringMap(m.Metadata),
	}.Normalize()
}

type AuthField struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Sensitive   bool     `json:"sensitive,omitempty"`
	Secret      bool     `json:"secret,omitempty"`
	Env         []string `json:"env,omitempty"`
}

func (f AuthField) FieldSpec() auth.FieldSpec {
	kind := auth.FieldString
	if f.Secret || f.Sensitive {
		kind = auth.FieldPassword
	}
	return auth.FieldSpec{
		Slot:        secret.Slot(strings.TrimSpace(f.Name)),
		Kind:        kind,
		Description: strings.TrimSpace(f.Description),
		Required:    f.Required,
		Sensitive:   f.Sensitive || f.Secret,
		Env:         auth.EnvSpec{Aliases: trimStrings(f.Env)},
	}.Normalize()
}

type DatasourceSpec = datasource.Declaration
type DatasourceEntitySchema = datasource.EntitySchema
type DatasourceFieldSpec = datasource.SchemaField
type DatasourceViewSpec = datasource.ViewSpec
type DatasourceRelationSpec = datasource.DeclarationRelationSpec
type DatasourceFallback = datasource.Fallback
type DatasourceCompletionSpec = datasource.CompletionSpec

const (
	DatasourceFallbackNone           = datasource.FallbackNone
	DatasourceFallbackHostIndex      = datasource.FallbackHostIndex
	DatasourceFallbackProviderFirst  = datasource.FallbackProviderFirst
	DatasourceFallbackHostIndexFirst = datasource.FallbackHostIndexFirst
)

type ContextSpec = fpcontext.Spec

type ObserverSpec = evidence.ObserverSpec
type AssertionDeriverSpec = evidence.AssertionDeriverSpec
type AssertionTemplate = evidence.AssertionTemplate
type Observation = evidence.Observation
type Assertion = evidence.Assertion
type EvidenceRef = evidence.Ref
type EvidenceName = evidence.Name
type ObservationPhase = evidence.ObservationPhase

const (
	ObservationPhaseStartup      = evidence.PhaseStartup
	ObservationPhaseSessionOpen  = evidence.PhaseSessionOpen
	ObservationPhaseTurn         = evidence.PhaseTurn
	ObservationPhaseToolFollowup = evidence.PhaseToolFollowup
	ObservationPhaseLazy         = evidence.PhaseLazy
)

type EndpointSpec = endpoint.EndpointSpec
type EndpointRef = endpoint.EndpointRef

type IndexSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Entities    []string `json:"entities,omitempty"`
}

type AuthMaterial struct {
	Method string            `json:"method,omitempty"`
	Values map[string]string `json:"values,omitempty"`
}

type EndpointCandidate = endpoint.Candidate

type ContextBlock = fpcontext.Block
type ContextSource = fpcontext.Source

func trimStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
