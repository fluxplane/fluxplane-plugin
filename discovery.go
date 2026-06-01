package fluxplaneplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	fpendpoint "github.com/fluxplane/fluxplane-endpoint"

	dexcore "github.com/fluxplane/fluxplane-dex/core"
	"github.com/fluxplane/fluxplane-dex/protocol"
	"github.com/fluxplane/fluxplane-dex/runtime"
)

var _ pluginhost.DiscoveryProviderContributor = (*adapter)(nil)

func (a *adapter) DiscoveryProviders(ctx context.Context, pluginCtx pluginhost.Context) ([]fpendpoint.DiscoveryProvider, error) {
	manifest, err := a.cachedManifest(ctx)
	if err != nil || len(manifest.Endpoints) == 0 {
		return nil, nil
	}
	return []fpendpoint.DiscoveryProvider{dexDiscoveryProvider{plugin: a.name, instance: pluginCtx.Ref.Instance, manifest: manifest, runner: a.engine.Runner()}}, nil
}

type dexDiscoveryProvider struct {
	plugin   string
	instance string
	manifest dexcore.PluginManifest
	runner   runtime.Runner
}

func (p dexDiscoveryProvider) Spec() fpendpoint.ProviderSpec {
	products := map[string]bool{}
	for _, endpoint := range p.manifest.Endpoints {
		for _, product := range endpoint.Products {
			product = strings.TrimSpace(product)
			if product != "" {
				products[product] = true
			}
		}
	}
	out := fpendpoint.ProviderSpec{Name: p.plugin, Source: "dex", Products: sortedKeys(products), Description: strings.TrimSpace(p.manifest.Description)}
	if out.Description == "" {
		out.Description = fmt.Sprintf("Dex %s endpoint discovery provider.", p.plugin)
	}
	return out
}

func (p dexDiscoveryProvider) Discover(ctx context.Context, req fpendpoint.DiscoveryRequest) (fpendpoint.DiscoveryResult, error) {
	payload := map[string]any{}
	if product := strings.TrimSpace(req.Product); product != "" {
		payload["product"] = product
	}
	if req.Limit > 0 {
		payload["limit"] = req.Limit
	}
	for key, value := range req.Query {
		value = strings.TrimSpace(value)
		if value != "" {
			payload[key] = value
		}
	}
	if namespaces := strings.TrimSpace(req.Query["namespaces"]); namespaces != "" && strings.TrimSpace(req.Query["namespace"]) == "" {
		payload["namespace"] = firstCSV(namespaces)
	}
	resp, err := p.runner.InvokeInstance(ctx, p.plugin, runtime.NormalizeInstance(p.instance), protocol.CommandEndpointsDiscover, payload)
	if err != nil {
		return fpendpoint.DiscoveryResult{}, err
	}
	if resp.Error != nil {
		return fpendpoint.DiscoveryResult{}, fmt.Errorf("dex %s endpoint discovery: %s", p.plugin, resp.Error.Message)
	}
	var decoded struct {
		Candidates []dexcore.EndpointCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(resp.Result, &decoded); err != nil {
		return fpendpoint.DiscoveryResult{}, err
	}
	out := fpendpoint.DiscoveryResult{Candidates: make([]fpendpoint.DiscoveryCandidate, 0, len(decoded.Candidates))}
	for _, candidate := range decoded.Candidates {
		out.Candidates = append(out.Candidates, dexCandidateToCore(candidate))
	}
	return out, nil
}

func dexCandidateToCore(candidate dexcore.EndpointCandidate) fpendpoint.DiscoveryCandidate {
	labels := cloneStringMap(candidate.Labels)
	annotations := cloneStringMap(candidate.Annotations)
	return fpendpoint.DiscoveryCandidate{
		ID:          candidate.ID,
		URL:         candidate.URL,
		Scheme:      candidate.Protocol,
		ProductHint: candidate.Product,
		Protocol:    candidate.Protocol,
		AuthRef:     candidate.CredentialRef,
		Labels:      labels,
		Annotations: annotations,
		Source: fpendpoint.SourceRef{
			Kind:      firstNonEmptyString(candidate.Source, labels["source"], annotations["source"]),
			Name:      firstNonEmptyString(labels["service"], labels["name"], annotations["name"]),
			Namespace: firstNonEmptyString(labels["namespace"], annotations["namespace"]),
			Cluster:   firstNonEmptyString(labels["cluster"], annotations["cluster"], labels["context"]),
		},
		Reasons: splitReasons(firstNonEmptyString(labels["provenance"], annotations["provenance"])),
		Score:   candidate.Score,
	}
}

func firstCSV(value string) string {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			return part
		}
	}
	return ""
}

func splitReasons(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func sortedKeys(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
