package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	"github.com/fluxplane/fluxplane-plugin/management"
)

type monitorFakeBackend struct {
	*fakeBackend
	saved   []fpendpoint.EndpointRef
	invokes []string
}

func (b *monitorFakeBackend) InvokeOperation(_ context.Context, req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
	b.invokes = append(b.invokes, req.Operation)
	var input map[string]any
	_ = json.Unmarshal(req.Input, &input)
	product, _ := input["product"].(string)
	switch req.Operation {
	case "kubernetes.endpoint.discover":
		if product == "grafana" {
			return management.OperationInvokeResult{Result: json.RawMessage(`{"candidates":[]}`)}, nil
		}
		return management.OperationInvokeResult{Result: json.RawMessage(`{"candidates":[{"url":"http://` + product + `.monitoring.svc:9090","labels":{"service":"` + product + `-main","namespace":"monitoring"}}]}`)}, nil
	case "kubernetes.portforward.list":
		// One live forward exists for prometheus-main:9090 — must be reused.
		return management.OperationInvokeResult{Result: json.RawMessage(`{"forwards":[{"id":"kpf-live","namespace":"monitoring","resource":"service/prometheus-main","remote_port":9090,"local_port":18080}]}`)}, nil
	case "kubernetes.portforward.start":
		return management.OperationInvokeResult{Result: json.RawMessage(`{"id":"kpf-new","local_port":18081}`)}, nil
	default:
		return management.OperationInvokeResult{}, nil
	}
}

func (b *monitorFakeBackend) SaveEndpoint(_ context.Context, req management.EndpointSaveRequest) (management.EndpointSaveResult, error) {
	b.saved = append(b.saved, req.Endpoint)
	return management.EndpointSaveResult{Endpoint: req.Endpoint, Saved: true}, nil
}

func TestMonitorConnectWiresProducts(t *testing.T) {
	backend := &monitorFakeBackend{fakeBackend: &fakeBackend{}}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"monitor", "connect", "--context", "arn:aws:eks:eu-central-1:1:cluster/dev-eu-central-1", "--product", "prometheus,loki,grafana"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result monitorConnectResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	byProduct := map[string]monitorConnectEntry{}
	for _, entry := range result.Endpoints {
		byProduct[entry.Product] = entry
	}
	// prometheus reuses the live forward.
	prometheus := byProduct["prometheus"]
	if !prometheus.Reused || prometheus.ForwardID != "kpf-live" || prometheus.URL != "http://127.0.0.1:18080" {
		t.Fatalf("prometheus = %#v", prometheus)
	}
	if prometheus.EndpointID != "prometheus-dev-eu-central-1" {
		t.Fatalf("endpoint id = %q", prometheus.EndpointID)
	}
	// loki starts a new forward.
	loki := byProduct["loki"]
	if loki.Reused || loki.ForwardID != "kpf-new" || loki.URL != "http://127.0.0.1:18081" {
		t.Fatalf("loki = %#v", loki)
	}
	// grafana has no service — skipped, not an error.
	if byProduct["grafana"].Skipped == "" || byProduct["grafana"].Error != "" {
		t.Fatalf("grafana = %#v", byProduct["grafana"])
	}
	// Saved endpoints carry the forward target annotations.
	if len(backend.saved) != 2 {
		t.Fatalf("saved = %#v", backend.saved)
	}
	if backend.saved[0].Annotations["resource"] != "service/prometheus-main" || backend.saved[0].Annotations["remote_port"] != "9090" {
		t.Fatalf("annotations = %#v", backend.saved[0].Annotations)
	}
	if backend.saved[0].Source != "monitor-connect" {
		t.Fatalf("source = %q", backend.saved[0].Source)
	}
}

func TestClusterAlias(t *testing.T) {
	cases := map[string]string{
		"arn:aws:eks:eu-central-1:178014766624:cluster/prod-eu-central-1": "prod-eu-central-1",
		"eu-central-1.infra.babelforce.com":                               "eu-central-1",
		"k3d-babelforce":                                                  "k3d-babelforce",
	}
	for input, want := range cases {
		if got := clusterAlias(input); got != want {
			t.Fatalf("clusterAlias(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMonitorConnectRequiresContext(t *testing.T) {
	cmd := New(Options{Backend: &fakeBackend{}, Out: &bytes.Buffer{}})
	cmd.SetArgs([]string{"monitor", "connect"})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--context") {
		t.Fatalf("err = %v, want missing-context error", err)
	}
}
