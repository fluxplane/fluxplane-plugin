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
	saved         []fpendpoint.EndpointRef
	invokes       []string
	secretReads   []map[string]any
	authConnected bool
}

func (b *monitorFakeBackend) AuthStatus(_ context.Context, req management.AuthStatusRequest) (management.AuthStatusResult, error) {
	return management.AuthStatusResult{Plugin: req.Ref, Instance: req.Instance, Connected: b.authConnected}, nil
}

func (b *monitorFakeBackend) InvokeOperation(_ context.Context, req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
	b.invokes = append(b.invokes, req.Operation)
	var input map[string]any
	_ = json.Unmarshal(req.Input, &input)
	product, _ := input["product"].(string)
	switch req.Operation {
	case "kubernetes.endpoint.discover":
		if product == "grafana" {
			// Ingress-style candidate carrying a discovered credential secret.
			return management.OperationInvokeResult{Result: json.RawMessage(`{"candidates":[{"url":"https://grafana.infra.example.com","labels":{},"credential_ref":"kubernetes://monitoring/secrets/grafana-admin-creds?context=infra-eks","annotations":{"credential_fields":"password=adminpassword,username=adminuser"}}]}`)}, nil
		}
		if product == "tempo" {
			return management.OperationInvokeResult{Result: json.RawMessage(`{"candidates":[]}`)}, nil
		}
		if product == "alertmanager" {
			// Ingress-style candidate: external URL, no service label.
			return management.OperationInvokeResult{Result: json.RawMessage(`{"candidates":[{"url":"https://alertmanager.infra.example.com","labels":{}}]}`)}, nil
		}
		return management.OperationInvokeResult{Result: json.RawMessage(`{"candidates":[{"url":"http://` + product + `.monitoring.svc:9090","labels":{"service":"` + product + `-main","namespace":"monitoring"}}]}`)}, nil
	case "kubernetes.portforward.list":
		// One live forward exists for prometheus-main:9090 — must be reused.
		return management.OperationInvokeResult{Result: json.RawMessage(`{"forwards":[{"id":"kpf-live","namespace":"monitoring","resource":"service/prometheus-main","remote_port":9090,"local_port":18080}]}`)}, nil
	case "kubernetes.portforward.start":
		return management.OperationInvokeResult{Result: json.RawMessage(`{"id":"kpf-new","local_port":18081}`)}, nil
	case "kubernetes.secret.read":
		b.secretReads = append(b.secretReads, input)
		return management.OperationInvokeResult{Result: json.RawMessage(`{"namespace":"monitoring","name":"grafana-admin-creds","values":{"adminuser":"admin","adminpassword":"hunter2"}}`)}, nil
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
	cmd.SetArgs([]string{"monitor", "connect", "--context", "arn:aws:eks:eu-central-1:1:cluster/dev-eu-central-1", "--product", "prometheus,loki,grafana,alertmanager,tempo"})
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
	// tempo has no service — skipped, not an error.
	if byProduct["tempo"].Skipped == "" || byProduct["tempo"].Error != "" {
		t.Fatalf("tempo = %#v", byProduct["tempo"])
	}
	// Ingress-style candidates register their external URL directly.
	alertmanager := byProduct["alertmanager"]
	if alertmanager.URL != "https://alertmanager.infra.example.com" || alertmanager.ForwardID != "" || alertmanager.Error != "" {
		t.Fatalf("alertmanager = %#v", alertmanager)
	}
	// grafana's discovered credential secret is read and stored as auth.
	grafana := byProduct["grafana"]
	if grafana.URL != "https://grafana.infra.example.com" || grafana.Error != "" || grafana.AuthError != "" {
		t.Fatalf("grafana = %#v", grafana)
	}
	if !grafana.AuthConnected || strings.Join(grafana.AuthFields, ",") != "password,username" {
		t.Fatalf("grafana auth = %#v", grafana)
	}
	if len(backend.secretReads) != 1 {
		t.Fatalf("secret reads = %#v", backend.secretReads)
	}
	read := backend.secretReads[0]
	if read["namespace"] != "monitoring" || read["name"] != "grafana-admin-creds" || read["context"] != "infra-eks" {
		t.Fatalf("secret read input = %#v", read)
	}
	if backend.connected.Ref.Name != "grafana" || backend.connected.Metadata["username"] != "admin" || backend.connected.Metadata["password"] != "hunter2" {
		t.Fatalf("auth connect = %#v", backend.connected)
	}
	// Secret values must never reach the command output.
	if strings.Contains(out.String(), "hunter2") || strings.Contains(out.String(), `"admin"`) {
		t.Fatalf("output leaks secret values:\n%s", out.String())
	}
	// Saved endpoints carry the forward target annotations.
	if len(backend.saved) != 4 {
		t.Fatalf("saved = %#v", backend.saved)
	}
	if backend.saved[0].Annotations["resource"] != "service/prometheus-main" || backend.saved[0].Annotations["remote_port"] != "9090" {
		t.Fatalf("annotations = %#v", backend.saved[0].Annotations)
	}
	if backend.saved[0].Source != "monitor-connect" {
		t.Fatalf("source = %q", backend.saved[0].Source)
	}
}

func TestMonitorConnectSkipsAlreadyConnectedAuth(t *testing.T) {
	backend := &monitorFakeBackend{fakeBackend: &fakeBackend{}, authConnected: true}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"monitor", "connect", "--context", "infra-eks", "--product", "grafana"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result monitorConnectResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	entry := result.Endpoints[0]
	if entry.AuthConnected || !strings.Contains(entry.AuthSkipped, "already connected") {
		t.Fatalf("entry = %#v", entry)
	}
	if len(backend.secretReads) != 0 || backend.connected.Ref.Name != "" {
		t.Fatalf("auth import ran despite connected auth: %#v", backend.connected)
	}
}

func TestMonitorConnectRefreshAuthOverridesConnected(t *testing.T) {
	backend := &monitorFakeBackend{fakeBackend: &fakeBackend{}, authConnected: true}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"monitor", "connect", "--context", "infra-eks", "--product", "grafana", "--refresh-auth"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result monitorConnectResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	entry := result.Endpoints[0]
	if !entry.AuthConnected || entry.AuthSkipped != "" || entry.AuthError != "" {
		t.Fatalf("entry = %#v", entry)
	}
	if backend.connected.Ref.Name != "grafana" {
		t.Fatalf("auth connect = %#v", backend.connected)
	}
}

func TestParseKubernetesCredentialRef(t *testing.T) {
	namespace, name, contextName, err := parseKubernetesCredentialRef("kubernetes://monitoring/secrets/grafana-admin-creds?context=infra-eks")
	if err != nil || namespace != "monitoring" || name != "grafana-admin-creds" || contextName != "infra-eks" {
		t.Fatalf("parsed = %q %q %q %v", namespace, name, contextName, err)
	}
	for _, invalid := range []string{"", "https://example.com", "kubernetes://monitoring/configmaps/foo", "kubernetes:///secrets/foo"} {
		if _, _, _, err := parseKubernetesCredentialRef(invalid); err == nil {
			t.Fatalf("expected error for %q", invalid)
		}
	}
}

func TestParseCredentialFields(t *testing.T) {
	fields := parseCredentialFields("password=adminpassword, username=adminuser,broken,=x,y=")
	if len(fields) != 2 || fields["password"] != "adminpassword" || fields["username"] != "adminuser" {
		t.Fatalf("fields = %#v", fields)
	}
	if len(parseCredentialFields("")) != 0 {
		t.Fatalf("empty annotation should yield no fields")
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
