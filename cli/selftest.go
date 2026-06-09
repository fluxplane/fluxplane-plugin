package cli

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

type selftestOpResult struct {
	Operation string `json:"operation"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

type selftestPluginResult struct {
	Plugin     management.Ref     `json:"plugin"`
	Operations []selftestOpResult `json:"operations"`
	Passed     int                `json:"passed"`
	Failed     int                `json:"failed"`
	OK         bool               `json:"ok"`
	Message    string             `json:"message,omitempty"`
}

type selftestResult struct {
	Healthy bool                   `json:"healthy"`
	Plugins []selftestPluginResult `json:"plugins"`
}

func newSelftestCommand(backend management.Backend) *cobra.Command {
	var authOnly bool
	cmd := &cobra.Command{
		Use:   "selftest [plugin...]",
		Short: "Exercise each plugin's read-safe operations (auth.test and zero-input read-only ops)",
		Long: "Runs a plugin's declared read-safe probes against the live backend and reports green/red. " +
			"Safe probes are the auth.test operation plus read-only operations that require no input beyond " +
			"endpoint_ref, so nothing is created or mutated.\n\n" +
			"With no arguments it tests every installed plugin. Useful as a release or post-upgrade gate. " +
			"This contacts the configured backends (network).",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			plugins, err := backend.ListPlugins(cmd.Context(), management.ListRequest{All: true})
			if err != nil {
				return err
			}
			want := map[string]bool{}
			for _, name := range args {
				if name = strings.TrimSpace(name); name != "" {
					want[name] = true
				}
			}
			var eligible []management.Plugin
			for _, plugin := range plugins {
				name := strings.TrimSpace(plugin.Ref.Name)
				if name == "" || (len(want) > 0 && !want[name]) {
					continue
				}
				eligible = append(eligible, plugin)
			}
			reports := make([]selftestPluginResult, len(eligible))
			runConcurrent(len(eligible), 0, func(i int) {
				reports[i] = selftestPlugin(cmd.Context(), backend, eligible[i].Ref, authOnly)
			})
			result := selftestResult{Healthy: true}
			for _, report := range reports {
				if !report.OK {
					result.Healthy = false
				}
				result.Plugins = append(result.Plugins, report)
			}
			sort.Slice(result.Plugins, func(i, j int) bool { return result.Plugins[i].Plugin.Name < result.Plugins[j].Plugin.Name })
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().BoolVar(&authOnly, "auth-only", false, "only run the auth.test probe, skip read-only operations")
	return cmd
}

func selftestPlugin(ctx context.Context, backend management.Backend, ref management.Ref, authOnly bool) selftestPluginResult {
	report := selftestPluginResult{Plugin: ref, OK: true}
	list, err := backend.ListOperations(ctx, management.OperationListRequest{Ref: ref})
	if err != nil {
		report.OK = false
		report.Message = "list operations: " + err.Error()
		return report
	}
	probes := safeProbeOperations(list.Operations, authOnly)
	if len(probes) == 0 {
		report.Message = "no read-safe operations to probe"
		return report
	}
	// Run all probes through a single batched call so the plugin process is
	// spawned once per plugin instead of once per probe.
	calls := make([]protocol.OperationCall, len(probes))
	for i, op := range probes {
		calls[i] = protocol.OperationCall{Name: op, Input: json.RawMessage(`{}`)}
	}
	batch, err := backend.BatchOperations(ctx, management.OperationBatchRequest{Ref: ref, Calls: calls})
	if err != nil {
		report.OK = false
		report.Message = "batch probes: " + err.Error()
		return report
	}
	var parsed protocol.OperationBatchResult
	if err := json.Unmarshal(batch.Result, &parsed); err != nil {
		report.OK = false
		report.Message = "decode batch result: " + err.Error()
		return report
	}
	for _, called := range parsed.Results {
		res := selftestOpResult{Operation: strings.TrimSpace(called.Name)}
		switch {
		case !called.OK:
			res.Error = batchCallError(called)
		case resultReportsFailure(called.Result):
			res.Error = "operation reported ok=false"
		default:
			res.OK = true
		}
		if res.OK {
			report.Passed++
		} else {
			report.Failed++
			report.OK = false
		}
		report.Operations = append(report.Operations, res)
	}
	return report
}

func batchCallError(result protocol.OperationResult) string {
	if result.Error != nil {
		if result.Error.Code != "" {
			return result.Error.Code + ": " + result.Error.Message
		}
		return result.Error.Message
	}
	return "operation failed"
}

// safeProbeOperations selects the operations that can be invoked without
// creating or mutating anything: the auth.test probe plus read-only operations
// whose only required input (if any) is endpoint_ref.
func safeProbeOperations(ops []sdkmanifest.OperationSpec, authOnly bool) []string {
	var out []string
	for _, op := range ops {
		name := strings.TrimSpace(op.Name)
		if name == "" {
			continue
		}
		if strings.HasSuffix(name, ".auth.test") {
			out = append(out, name)
			continue
		}
		if authOnly || !op.ReadOnly {
			continue
		}
		if requiresOnlyEndpointRef(parseOperationInputSchema(op)) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return dedupeStrings(out)
}

func requiresOnlyEndpointRef(schema operationInputSchema) bool {
	for _, field := range schema.Required {
		if strings.TrimSpace(field) != "endpoint_ref" {
			return false
		}
	}
	return true
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// resultReportsFailure reports whether an operation result explicitly carries
// a top-level "ok": false.
func resultReportsFailure(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var payload struct {
		OK *bool `json:"ok"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	return payload.OK != nil && !*payload.OK
}
