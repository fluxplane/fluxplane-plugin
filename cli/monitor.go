package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	"github.com/fluxplane/fluxplane-plugin/management"
)

// monitorDefaultProducts are the monitoring-stack products `monitor connect`
// wires by default.
var monitorDefaultProducts = []string{"prometheus", "alertmanager", "loki", "grafana"}

type monitorConnectEntry struct {
	Product    string `json:"product"`
	EndpointID string `json:"endpoint_id,omitempty"`
	URL        string `json:"url,omitempty"`
	Resource   string `json:"resource,omitempty"`
	ForwardID  string `json:"forward_id,omitempty"`
	Reused     bool   `json:"reused_forward,omitempty"`
	Skipped    string `json:"skipped,omitempty"`
	Error      string `json:"error,omitempty"`
}

type monitorConnectResult struct {
	Context   string                `json:"context"`
	Namespace string                `json:"namespace"`
	Endpoints []monitorConnectEntry `json:"endpoints"`
}

func newMonitorCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Wire a cluster's monitoring stack in one command",
	}
	cmd.AddCommand(newMonitorConnectCommand(backend))
	return cmd
}

func newMonitorConnectCommand(backend management.Backend) *cobra.Command {
	var contextName string
	var namespace string
	var instance string
	var products []string
	var durationSeconds int
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Discover, port-forward, and register a cluster's monitoring endpoints",
		Long: "For each product (default: prometheus, alertmanager, loki, grafana) this discovers the " +
			"in-cluster service via the kubernetes plugin, starts a managed port-forward (reusing a live " +
			"forward for the same target), and registers the local URL as endpoint <product>-<cluster-alias>. " +
			"One command turns a cold cluster into queryable monitoring endpoints; dead forwards self-heal " +
			"on next use.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			if strings.TrimSpace(contextName) == "" {
				return fmt.Errorf("fluxplane-plugin: --context is required")
			}
			result := monitorConnectResult{Context: contextName, Namespace: namespace}
			alias := clusterAlias(contextName)
			for _, product := range products {
				product = strings.ToLower(strings.TrimSpace(product))
				if product == "" {
					continue
				}
				result.Endpoints = append(result.Endpoints, connectMonitorProduct(cmd.Context(), backend, instance, contextName, namespace, product, alias, durationSeconds))
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&contextName, "context", "", "kubeconfig context of the cluster (required)")
	cmd.Flags().StringVar(&namespace, "namespace", "monitoring", "namespace to discover monitoring services in")
	cmd.Flags().StringVar(&instance, "instance", defaultInstance(), "plugin instance")
	cmd.Flags().StringSliceVar(&products, "product", monitorDefaultProducts, "products to wire (repeatable)")
	cmd.Flags().IntVar(&durationSeconds, "duration-seconds", 14400, "port-forward lifetime in seconds")
	return cmd
}

func connectMonitorProduct(ctx context.Context, backend management.Backend, instance, contextName, namespace, product, alias string, durationSeconds int) monitorConnectEntry {
	entry := monitorConnectEntry{Product: product}
	kubernetes := management.Ref{Name: "kubernetes"}

	// 1. Discover the product's service (candidates arrive ranked, canonical
	//    API port first).
	var discovered struct {
		Candidates []struct {
			URL    string            `json:"url"`
			Labels map[string]string `json:"labels"`
		} `json:"candidates"`
	}
	if err := invokeInto(ctx, backend, kubernetes, instance, "kubernetes.endpoint.discover", map[string]any{
		"context": contextName, "namespace": namespace, "product": product,
	}, &discovered); err != nil {
		entry.Error = err.Error()
		return entry
	}
	if len(discovered.Candidates) == 0 {
		entry.Skipped = "no " + product + " service discovered"
		return entry
	}
	candidate := discovered.Candidates[0]
	service := strings.TrimSpace(candidate.Labels["service"])
	candidateNamespace := firstNonEmptyString(candidate.Labels["namespace"], namespace)
	remotePort := portFromURL(candidate.URL)
	if service == "" || remotePort == 0 {
		// Ingress-style candidates carry an externally reachable URL instead
		// of a service+port — register it directly, no forward needed.
		if external := strings.TrimSpace(candidate.URL); strings.HasPrefix(external, "http") && !strings.Contains(external, ".svc") {
			saved, err := backend.SaveEndpoint(ctx, management.EndpointSaveRequest{Endpoint: fpendpoint.EndpointRef{
				ID:          product + "-" + alias,
				URL:         external,
				Product:     product,
				Protocol:    "http",
				Source:      "monitor-connect",
				Annotations: map[string]string{"context": contextName, "via": "ingress"},
			}})
			if err != nil {
				entry.Error = err.Error()
				return entry
			}
			entry.EndpointID = saved.Endpoint.ID
			entry.URL = external
			return entry
		}
		entry.Error = fmt.Sprintf("candidate %q lacks a service name or port", candidate.URL)
		return entry
	}
	resource := "service/" + service
	entry.Resource = candidateNamespace + "/" + resource

	// 2. Reuse a live forward for the same target, else start one.
	var forwards struct {
		Forwards []struct {
			ID         string `json:"id"`
			Namespace  string `json:"namespace"`
			Resource   string `json:"resource"`
			RemotePort int    `json:"remote_port"`
			LocalPort  int    `json:"local_port"`
		} `json:"forwards"`
	}
	localPort := 0
	if err := invokeInto(ctx, backend, kubernetes, instance, "kubernetes.portforward.list", map[string]any{
		"context": contextName, "namespace": candidateNamespace, "live": true,
	}, &forwards); err == nil {
		for _, forward := range forwards.Forwards {
			if forward.Resource == resource && forward.RemotePort == remotePort {
				localPort = forward.LocalPort
				entry.ForwardID = forward.ID
				entry.Reused = true
				break
			}
		}
	}
	if localPort == 0 {
		var started struct {
			ID        string `json:"id"`
			LocalPort int    `json:"local_port"`
		}
		if err := invokeInto(ctx, backend, kubernetes, instance, "kubernetes.portforward.start", map[string]any{
			"context": contextName, "namespace": candidateNamespace, "resource": resource,
			"remote_port": remotePort, "duration_seconds": durationSeconds,
		}, &started); err != nil {
			entry.Error = err.Error()
			return entry
		}
		localPort = started.LocalPort
		entry.ForwardID = started.ID
	}
	if localPort == 0 {
		entry.Error = "port-forward reported no local port"
		return entry
	}

	// 3. Register the endpoint, annotated with the forward target for
	//    debuggability.
	endpointID := product + "-" + alias
	localURL := "http://127.0.0.1:" + strconv.Itoa(localPort)
	saved, err := backend.SaveEndpoint(ctx, management.EndpointSaveRequest{Endpoint: fpendpoint.EndpointRef{
		ID:       endpointID,
		URL:      localURL,
		Product:  product,
		Protocol: "http",
		Source:   "monitor-connect",
		Annotations: map[string]string{
			"context":     contextName,
			"namespace":   candidateNamespace,
			"resource":    resource,
			"remote_port": strconv.Itoa(remotePort),
		},
	}})
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	entry.EndpointID = saved.Endpoint.ID
	entry.URL = localURL
	return entry
}

// invokeInto runs one plugin operation and decodes its result payload.
func invokeInto(ctx context.Context, backend management.Backend, ref management.Ref, instance, operation string, input map[string]any, out any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	resp, err := backend.InvokeOperation(ctx, management.OperationInvokeRequest{Ref: ref, Instance: instance, Operation: operation, Input: raw})
	if err != nil {
		return err
	}
	if out == nil || len(resp.Result) == 0 {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}

// clusterAlias derives a short, endpoint-id-friendly cluster name from a
// kubeconfig context: the segment after "cluster/" for EKS ARNs, the first
// DNS label otherwise.
func clusterAlias(contextName string) string {
	contextName = strings.TrimSpace(contextName)
	if _, after, found := strings.Cut(contextName, "cluster/"); found {
		contextName = after
	} else if label, _, found := strings.Cut(contextName, "."); found {
		contextName = label
	}
	return pathSafeAlias(contextName)
}

func pathSafeAlias(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func portFromURL(raw string) int {
	raw = strings.TrimSpace(raw)
	idx := strings.LastIndex(raw, ":")
	if idx < 0 {
		return 0
	}
	port, err := strconv.Atoi(strings.TrimRight(raw[idx+1:], "/"))
	if err != nil {
		return 0
	}
	return port
}
