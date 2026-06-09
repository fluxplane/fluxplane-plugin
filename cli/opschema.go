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
	Type        any    `json:"type"`
	Description string `json:"description"`
	Enum        []any  `json:"enum"`
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
	// Many operations resolve a target instance from endpoint_ref even when the
	// advertised schema does not mark it required; surface it so examples work.
	if _, ok := schema.Properties["endpoint_ref"]; ok {
		if _, set := obj["endpoint_ref"]; !set {
			obj["endpoint_ref"] = "<endpoint_ref>"
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
