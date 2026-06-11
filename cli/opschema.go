package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

// operationInputSchema is the minimal JSON-schema shape the CLI needs to surface
// an operation's input fields, types, enums, and a runnable example. It is the
// single shared parser used by the skill, describe, search, and validation.
type operationInputSchema struct {
	Required   []string                       `json:"required"`
	Properties map[string]operationInputField `json:"properties"`
	// Examples are JSON Schema example objects. When an operation declares one,
	// it is used verbatim for the invocation example — the only way to produce a
	// runnable example for operations with one-of input requirements (e.g. provide
	// exactly one of transition_id / transition_name) that a flat required list
	// cannot express.
	Examples []map[string]any `json:"examples"`
	// additionalProps is set only when the schema explicitly declares
	// additionalProperties as the literal true/false; nil otherwise (unspecified
	// or an object form). It is decoded out-of-band so an object form never blanks
	// the rest of the schema.
	additionalProps *bool
}

type operationInputField struct {
	Type        any                            `json:"type"`
	Description string                         `json:"description"`
	Enum        []any                          `json:"enum"`
	Properties  map[string]operationInputField `json:"properties"`
	Items       *operationInputField           `json:"items"`
}

// operationFieldSummary is the agent-facing per-field view used by describe.
type operationFieldSummary struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Enum        []any  `json:"enum,omitempty"`
	Description string `json:"description,omitempty"`
}

func parseOperationInputSchema(op sdkmanifest.OperationSpec) operationInputSchema {
	// Decode additionalProperties as a raw message: JSON Schema permits a bool OR
	// an object, and a bare *bool target would fail to unmarshal an object form
	// and silently blank the entire schema.
	var wire struct {
		Required             []string                       `json:"required"`
		Properties           map[string]operationInputField `json:"properties"`
		Examples             []map[string]any               `json:"examples"`
		AdditionalProperties json.RawMessage                `json:"additionalProperties"`
	}
	_ = json.Unmarshal(op.Input, &wire)
	schema := operationInputSchema{Required: wire.Required, Properties: wire.Properties, Examples: wire.Examples}
	if schema.Properties == nil {
		schema.Properties = map[string]operationInputField{}
	}
	switch strings.TrimSpace(string(wire.AdditionalProperties)) {
	case "false":
		f := false
		schema.additionalProps = &f
	case "true":
		t := true
		schema.additionalProps = &t
	}
	return schema
}

// closedTopLevel reports whether the schema explicitly forbids unknown
// top-level keys (additionalProperties: false).
func (s operationInputSchema) closedTopLevel() bool {
	return s.additionalProps != nil && !*s.additionalProps
}

func (s operationInputSchema) requiredSet() map[string]bool {
	set := make(map[string]bool, len(s.Required))
	for _, r := range s.Required {
		if r = strings.TrimSpace(r); r != "" {
			set[r] = true
		}
	}
	return set
}

// orderedFieldNames lists required fields first (in declared order) then the
// remaining properties sorted, for stable agent-facing output.
func (s operationInputSchema) orderedFieldNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range s.Required {
		if r = strings.TrimSpace(r); r != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	rest := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func summarizeOperationInput(schema operationInputSchema) []operationFieldSummary {
	required := schema.requiredSet()
	names := schema.orderedFieldNames()
	out := make([]operationFieldSummary, 0, len(names))
	for _, name := range names {
		field := schema.Properties[name]
		out = append(out, operationFieldSummary{
			Name:        name,
			Type:        schemaFieldType(field),
			Required:    required[name],
			Enum:        field.Enum,
			Description: strings.TrimSpace(field.Description),
		})
	}
	return out
}

// operationOutputFieldSummary is the agent-facing per-field view of an
// operation's output schema: top-level fields plus exactly one nesting level
// (object children, or the element fields of an array of objects).
type operationOutputFieldSummary struct {
	Name        string                        `json:"name"`
	Type        string                        `json:"type"`
	Items       string                        `json:"items,omitempty"`
	Description string                        `json:"description,omitempty"`
	Fields      []operationOutputFieldSummary `json:"fields,omitempty"`
}

// summarizeOperationOutput parses an output JSON Schema into a compact field
// summary plus the truncation/pagination signal fields present (has_more,
// next_page_token, truncated — with total included only alongside one of
// those, since a bare total is usually just a count).
func summarizeOperationOutput(raw json.RawMessage) ([]operationOutputFieldSummary, []string) {
	if t := strings.TrimSpace(string(raw)); t == "" || t == "null" {
		return nil, nil
	}
	var wire struct {
		Properties map[string]operationInputField `json:"properties"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || len(wire.Properties) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(wire.Properties))
	for name := range wire.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]operationOutputFieldSummary, 0, len(names))
	for _, name := range names {
		field := wire.Properties[name]
		summary := operationOutputFieldSummary{
			Name:        name,
			Type:        outputFieldType(field),
			Description: strings.TrimSpace(field.Description),
		}
		switch {
		case summary.Type == "object" && len(field.Properties) > 0:
			summary.Fields = childFieldSummaries(field.Properties)
		case summary.Type == "array" && field.Items != nil:
			summary.Items = outputFieldType(*field.Items)
			if len(field.Items.Properties) > 0 {
				summary.Fields = childFieldSummaries(field.Items.Properties)
			}
		}
		out = append(out, summary)
	}
	var pagination []string
	for _, name := range []string{"has_more", "next_page_token", "truncated"} {
		if _, ok := wire.Properties[name]; ok {
			pagination = append(pagination, name)
		}
	}
	if len(pagination) > 0 {
		if _, ok := wire.Properties["total"]; ok {
			pagination = append(pagination, "total")
		}
	}
	return out, pagination
}

// childFieldSummaries renders exactly one nesting level — name, type, and
// description, never recursing into grandchildren.
func childFieldSummaries(props map[string]operationInputField) []operationOutputFieldSummary {
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]operationOutputFieldSummary, 0, len(names))
	for _, name := range names {
		field := props[name]
		child := operationOutputFieldSummary{Name: name, Type: outputFieldType(field), Description: strings.TrimSpace(field.Description)}
		if child.Type == "array" && field.Items != nil {
			child.Items = outputFieldType(*field.Items)
		}
		out = append(out, child)
	}
	return out
}

// outputFieldType is schemaFieldType with structural inference: a typeless
// node that declares properties/items still reads as object/array instead of
// defaulting to string.
func outputFieldType(field operationInputField) string {
	if field.Type == nil {
		if len(field.Properties) > 0 {
			return "object"
		}
		if field.Items != nil {
			return "array"
		}
	}
	return schemaFieldType(field)
}

func operationExample(plugin, operation string, schema operationInputSchema) string {
	return fmt.Sprintf("fluxplane-plugin operation invoke %s %s --input '%s'", plugin, operation, sampleInputJSON(schema))
}

func sampleInputJSON(schema operationInputSchema) string {
	// A schema-declared example is authoritative: it is the only form that can
	// express one-of input requirements as a runnable invocation.
	for _, example := range schema.Examples {
		if len(example) > 0 {
			return compactJSON(example)
		}
	}
	obj := map[string]any{}
	for _, field := range schema.Required {
		obj[field] = samplePlaceholder(schemaFieldType(schema.Properties[field]))
	}
	// Nothing required and no declared example: surface a few representative
	// optional fields so the example shows the real input shape. endpoint_ref
	// is deliberately NOT auto-injected — when omitted, the backend resolves
	// the instance's wired endpoint, and an endpoint_ref-only stub hides the
	// fields that actually matter.
	if len(obj) == 0 {
		for _, key := range []string{"ref", "id", "query", "name", "channel", "path", "project", "url", "group"} {
			property, ok := schema.Properties[key]
			if !ok {
				continue
			}
			obj[key] = samplePlaceholder(schemaFieldType(property))
			if len(obj) >= 2 {
				break
			}
		}
	}
	if len(obj) == 0 {
		names := make([]string, 0, len(schema.Properties))
		for name := range schema.Properties {
			if name == "endpoint_ref" {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			obj[name] = samplePlaceholder(schemaFieldType(schema.Properties[name]))
			if len(obj) >= 2 {
				break
			}
		}
	}
	return compactJSON(obj)
}

// compactJSON marshals to compact JSON without HTML-escaping so placeholders
// like <endpoint_ref> render literally instead of as <.
func compactJSON(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "{}"
	}
	return strings.TrimRight(b.String(), "\n")
}

func samplePlaceholder(fieldType string) any {
	switch fieldType {
	case "integer", "number":
		return 0
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	default:
		return ""
	}
}

func schemaFieldType(spec operationInputField) string {
	switch value := spec.Type.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		for _, item := range value {
			if text, ok := item.(string); ok && text != "null" {
				return strings.TrimSpace(text)
			}
		}
	}
	return "string"
}

// declaredScalarType returns the schema's declared type only when it is
// unambiguous: the single type string, or the non-null member of a nullable
// union like ["string","null"]. Ambiguous unions (["string","integer"]) and
// absent/anyOf declarations return "" — unlike schemaFieldType it never
// defaults to "string", so callers can fall back to heuristics safely.
func declaredScalarType(spec operationInputField) string {
	switch value := spec.Type.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		var nonNull []string
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "null" {
				nonNull = append(nonNull, strings.TrimSpace(text))
			}
		}
		if len(nonNull) == 1 {
			return nonNull[0]
		}
	}
	return ""
}
