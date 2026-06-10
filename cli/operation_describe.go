package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

// operationDescription is the agent-facing summary of a single operation: enough
// to construct a correct invocation in one read without parsing raw JSON Schema.
type operationDescription struct {
	Plugin      string                  `json:"plugin"`
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	ReadOnly    bool                    `json:"read_only"`
	Risk        string                  `json:"risk,omitempty"`
	Idempotency string                  `json:"idempotency,omitempty"`
	Effects     []string                `json:"effects,omitempty"`
	Access      []string                `json:"access,omitempty"`
	AuthScopes  []string                `json:"auth_scopes,omitempty"`
	Secrets     []string                `json:"secret_purposes,omitempty"`
	Fields      []operationFieldSummary `json:"input_fields,omitempty"`
	Example     string                  `json:"example"`
	// OutputFields summarizes the result shape (top-level plus one nesting
	// level); Pagination lists the truncation signals present so an agent can
	// tell a paginating operation at a glance. OutputKeys/OutputRaw stay for
	// backward compatibility and the raw-schema escape hatch.
	OutputFields []operationOutputFieldSummary `json:"output_fields,omitempty"`
	Pagination   []string                      `json:"pagination_fields,omitempty"`
	OutputKeys   []string                      `json:"output_keys,omitempty"`
	OutputRaw    json.RawMessage               `json:"output_schema,omitempty"`
}

func describeOperation(plugin string, op sdkmanifest.OperationSpec) operationDescription {
	schema := parseOperationInputSchema(op)
	desc := operationDescription{
		Plugin:      plugin,
		Name:        strings.TrimSpace(op.Name),
		Description: strings.TrimSpace(op.Description),
		ReadOnly:    op.ReadOnly,
		Risk:        strings.TrimSpace(string(op.Risk)),
		Idempotency: strings.TrimSpace(string(op.Idempotency)),
		Effects:     stringifyEffects(op.Effects),
		Access:      stringifyAccess(op.Access),
		AuthScopes:  trimmedNonEmpty(op.AuthScopes),
		Secrets:     trimmedNonEmpty(op.SecretPurposes),
		Fields:      summarizeOperationInput(schema),
		Example:     operationExample(plugin, op.Name, schema),
		OutputKeys:  topLevelSchemaKeys(op.Output),
		OutputRaw:   nonEmptyJSON(op.Output),
	}
	desc.OutputFields, desc.Pagination = summarizeOperationOutput(op.Output)
	return desc
}

func newOperationDescribeCommand(backend management.Backend) *cobra.Command {
	var instance string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "describe PLUGIN[@VERSION] OPERATION",
		Short: "Show one operation's input fields, types, enums, a runnable example, risk, and auth",
		Long: "Prints a compact, agent-friendly spec for a single operation: each input field with its " +
			"type, whether it's required, allowed enum values, and a one-line description; a runnable " +
			"invocation example; the output's top-level shape; and risk/idempotency/auth metadata. " +
			"Use this instead of reading the raw input_schema. --json emits the structured form.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			ref := parseRef(args[0])
			opName := strings.TrimSpace(args[1])
			list, err := backend.ListOperations(cmd.Context(), management.OperationListRequest{Ref: ref, Instance: instance})
			if err != nil {
				return err
			}
			for _, op := range list.Operations {
				if strings.TrimSpace(op.Name) == opName {
					desc := describeOperation(ref.Name, op)
					if asJSON {
						return printJSON(cmd.OutOrStdout(), desc)
					}
					return renderOperationDescription(cmd.OutOrStdout(), desc)
				}
			}
			return fmt.Errorf("fluxplane-plugin: operation %q not found on plugin %q", opName, ref.Name)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", defaultInstance(), "plugin instance")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the structured description as JSON")
	return cmd
}

func renderOperationDescription(w io.Writer, d operationDescription) error {
	var b strings.Builder
	header := d.Plugin + " " + d.Name
	if d.Description != "" {
		header += " — " + d.Description
	}
	fmt.Fprintln(&b, header)

	var meta []string
	if d.ReadOnly {
		meta = append(meta, "read-only")
	}
	if d.Risk != "" {
		meta = append(meta, "risk="+d.Risk)
	}
	if d.Idempotency != "" {
		meta = append(meta, d.Idempotency)
	}
	if len(d.Effects) > 0 {
		meta = append(meta, "effects="+strings.Join(d.Effects, ","))
	}
	if len(meta) > 0 {
		fmt.Fprintln(&b, "  ["+strings.Join(meta, " · ")+"]")
	}
	if len(d.AuthScopes) > 0 {
		fmt.Fprintln(&b, "  auth scopes: "+strings.Join(d.AuthScopes, ", "))
	}
	if len(d.Secrets) > 0 {
		fmt.Fprintln(&b, "  secrets: "+strings.Join(d.Secrets, ", "))
	}

	if len(d.Fields) > 0 {
		fmt.Fprintln(&b, "  input:")
		width := 0
		for _, f := range d.Fields {
			if len(f.Name) > width {
				width = len(f.Name)
			}
		}
		for _, f := range d.Fields {
			req := "optional"
			if f.Required {
				req = "required"
			}
			line := fmt.Sprintf("    %-*s  %-8s  %s", width, f.Name, f.Type, req)
			if len(f.Enum) > 0 {
				line += "  enum: " + joinEnum(f.Enum)
			}
			if f.Description != "" {
				line += "  — " + f.Description
			}
			fmt.Fprintln(&b, line)
		}
	} else {
		fmt.Fprintln(&b, "  input: (none)")
	}

	fmt.Fprintln(&b, "  example: "+d.Example)
	switch {
	case len(d.OutputFields) > 0:
		fmt.Fprintln(&b, "  output:")
		for _, f := range d.OutputFields {
			fmt.Fprintln(&b, "    "+outputFieldLine(f))
			for _, child := range f.Fields {
				fmt.Fprintln(&b, "      "+outputFieldLine(child))
			}
		}
		if len(d.Pagination) > 0 {
			fmt.Fprintln(&b, "  paginates: "+strings.Join(d.Pagination, ", ")+" — partial results are signaled; fetch more when set")
		}
	case len(d.OutputKeys) > 0:
		fmt.Fprintln(&b, "  output keys: "+strings.Join(d.OutputKeys, ", "))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func outputFieldLine(f operationOutputFieldSummary) string {
	kind := f.Type
	if f.Items != "" {
		kind += "[" + f.Items + "]"
	}
	line := fmt.Sprintf("%s  %s", f.Name, kind)
	if f.Description != "" {
		line += "  — " + f.Description
	}
	return line
}

func joinEnum(values []any) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%v", v))
	}
	return strings.Join(parts, ", ")
}

func stringifyEffects(effects []sdkmanifest.OperationEffect) []string {
	out := make([]string, 0, len(effects))
	for _, e := range effects {
		if s := strings.TrimSpace(string(e)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func stringifyAccess(access []sdkmanifest.OperationAccess) []string {
	out := make([]string, 0, len(access))
	for _, a := range access {
		if s := strings.TrimSpace(string(a)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func trimmedNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func nonEmptyJSON(raw json.RawMessage) json.RawMessage {
	if t := strings.TrimSpace(string(raw)); t == "" || t == "null" {
		return nil
	}
	return raw
}

// topLevelSchemaKeys returns the sorted top-level property names of an output
// JSON Schema, never recursing — a one-line hint at the result shape.
func topLevelSchemaKeys(raw json.RawMessage) []string {
	if len(nonEmptyJSON(raw)) == 0 {
		return nil
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil || len(schema.Properties) == 0 {
		return nil
	}
	keys := make([]string, 0, len(schema.Properties))
	for k := range schema.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
