package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type validationProblem struct {
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason"`
}

// operationDryRunResult is the report printed by `operation invoke --dry-run`.
type operationDryRunResult struct {
	Plugin    string              `json:"plugin"`
	Operation string              `json:"operation"`
	Valid     bool                `json:"valid"`
	Problems  []validationProblem `json:"problems,omitempty"`
	Input     json.RawMessage     `json:"input,omitempty"`
}

// validateOperationInput applies ONLY high-confidence, top-level checks against
// an operation's input schema, to catch the common agent mistakes (missing
// required field, bad enum value, typo'd field name) BEFORE a backend round-trip
// — without risking false rejects of valid input.
//
// It deliberately does NOT validate: nested required, types, oneOf/anyOf/allOf,
// $ref, formats, or numeric bounds. When an operation declares a schema example
// (a signal of one-of/conditional input a flat schema cannot express), the
// missing-required check is suppressed. Unknown-key and enum checks always run:
// one-of shapes change which fields are required, never which fields exist, so
// a typo'd field name is flagged uniformly across plugins.
func validateOperationInput(schema operationInputSchema, payload json.RawMessage) []validationProblem {
	if len(strings.TrimSpace(string(payload))) == 0 {
		return nil // no input given — let the backend decide if input is required
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil // not a JSON object — out of scope for top-level checks
	}
	if len(schema.Properties) == 0 && len(schema.Required) == 0 {
		return nil
	}
	exampleDriven := len(schema.Examples) > 0

	var problems []validationProblem
	if !exampleDriven {
		for _, name := range schema.Required {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := obj[name]; !ok {
				problems = append(problems, validationProblem{Field: name, Reason: "required field is missing"})
			}
		}
	}

	for key, value := range obj {
		field, known := schema.Properties[key]
		if known && len(field.Enum) > 0 && isScalar(value) && !valueInEnum(value, field.Enum) {
			problems = append(problems, validationProblem{
				Field:  key,
				Reason: fmt.Sprintf("value %s is not one of the allowed values: %s", jsonValue(value), enumList(field.Enum)),
			})
		}
		if !known && schema.closedTopLevel() {
			problems = append(problems, validationProblem{Field: key, Reason: "unknown field (additionalProperties is false)"})
		}
	}

	sort.Slice(problems, func(i, j int) bool {
		if problems[i].Field != problems[j].Field {
			return problems[i].Field < problems[j].Field
		}
		return problems[i].Reason < problems[j].Reason
	})
	return problems
}

func isScalar(v any) bool {
	switch v.(type) {
	case string, float64, bool, json.Number:
		return true
	default:
		return false
	}
}

func valueInEnum(value any, enum []any) bool {
	vb, err := json.Marshal(value)
	if err != nil {
		return true // cannot compare confidently — do not flag
	}
	for _, e := range enum {
		if eb, err := json.Marshal(e); err == nil && string(eb) == string(vb) {
			return true
		}
	}
	return false
}

func jsonValue(v any) string {
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

func enumList(enum []any) string {
	parts := make([]string, 0, len(enum))
	for _, e := range enum {
		parts = append(parts, jsonValue(e))
	}
	return strings.Join(parts, ", ")
}
