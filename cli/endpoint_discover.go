package cli

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/fluxplane/fluxplane-plugin/management"
)

// endpointDiscoverOutput wraps the primary discovery result with best-effort
// fan-out results from other plugins able to discover the same product.
type endpointDiscoverOutput struct {
	management.EndpointDiscoverResult
	Fanout []management.EndpointDiscoverResult `json:"fanout,omitempty"`
	Hint   string                              `json:"hint,omitempty"`
}

// discoverCandidateCount counts candidates in a raw discovery result payload.
func discoverCandidateCount(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var decoded struct {
		Candidates []json.RawMessage `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return 0
	}
	return len(decoded.Candidates)
}

// discoverEndpointsFanout asks every other installed, enabled plugin to
// discover the product. Failures (most plugins don't implement discovery) are
// silently skipped; only results with candidates are returned.
func discoverEndpointsFanout(ctx context.Context, backend management.Backend, request management.EndpointDiscoverRequest, excludePlugin string) []management.EndpointDiscoverResult {
	plugins, err := backend.ListPlugins(ctx, management.ListRequest{All: true})
	if err != nil {
		return nil
	}
	results := make([]management.EndpointDiscoverResult, len(plugins))
	found := make([]bool, len(plugins))
	runConcurrent(len(plugins), 0, func(i int) {
		plugin := plugins[i]
		if !plugin.Installed || !plugin.Enabled || strings.EqualFold(plugin.Ref.Name, excludePlugin) {
			return
		}
		pluginRequest := request
		pluginRequest.Ref = plugin.Ref
		result, err := backend.DiscoverEndpoints(ctx, pluginRequest)
		if err != nil || discoverCandidateCount(result.Result) == 0 {
			return
		}
		results[i] = result
		found[i] = true
	})
	var out []management.EndpointDiscoverResult
	for i := range results {
		if found[i] {
			out = append(out, results[i])
		}
	}
	return out
}
