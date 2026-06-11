package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	"github.com/fluxplane/fluxplane-plugin/management"
)

type timelineFakeBackend struct {
	*fakeBackend
}

func (b *timelineFakeBackend) ListEndpoints(_ context.Context, req management.EndpointListRequest) (management.EndpointListResult, error) {
	switch req.Product {
	case "alertmanager":
		return management.EndpointListResult{Endpoints: []fpendpoint.Record{
			{EndpointRef: fpendpoint.EndpointRef{ID: "alertmanager-other", Product: "alertmanager"}},
			{EndpointRef: fpendpoint.EndpointRef{ID: "alertmanager-dev-eu-central-1", Product: "alertmanager"}},
		}}, nil
	case "loki":
		// No loki endpoint — the source must be skipped, not fatal.
		return management.EndpointListResult{}, nil
	}
	return management.EndpointListResult{}, nil
}

func (b *timelineFakeBackend) InvokeOperation(_ context.Context, req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
	switch req.Operation {
	case "kubernetes.event.list":
		return management.OperationInvokeResult{Result: json.RawMessage(`{"events":[
			{"type":"Warning","reason":"BackOff","message":"restarting failed container","count":7,"last_seen":"2026-06-11T11:30:00Z","involved_kind":"Pod","involved_name":"api-1"},
			{"type":"Normal","reason":"ScalingReplicaSet","message":"Scaled up replica set api-abc to 2","last_seen":"2026-06-11T11:00:00Z","involved_kind":"Deployment","involved_name":"api"},
			{"type":"Warning","reason":"OldEvent","message":"outside window","last_seen":"2026-06-11T08:00:00Z","involved_kind":"Pod","involved_name":"old"}
		]}`)}, nil
	case "alertmanager.alerts":
		// One alert started inside the window, one long before (preexisting).
		var input map[string]any
		_ = json.Unmarshal(req.Input, &input)
		if input["endpoint_ref"] != "alertmanager-dev-eu-central-1" {
			return management.OperationInvokeResult{}, nil
		}
		return management.OperationInvokeResult{Result: json.RawMessage(`{"alerts":[
			{"labels":{"alertname":"HighErrorRate","namespace":"latest","severity":"critical"},"annotations":{"summary":"errors spiking"},"starts_at":"2026-06-11T11:45:00Z"},
			{"labels":{"alertname":"Watchdog"},"starts_at":"2026-05-01T00:00:00Z"},
			{"labels":{"alertname":"OtherNamespace","namespace":"prod"},"starts_at":"2026-06-11T11:50:00Z"}
		]}`)}, nil
	case "gitlab.deployment.list":
		return management.OperationInvokeResult{Result: json.RawMessage(`{"items":[
			{"ref":"main","sha":"abcdef1234567890","status":"success","environment":"staging","created_at":"2026-06-11T11:15:00Z"}
		]}`)}, nil
	}
	return management.OperationInvokeResult{}, nil
}

func TestIncidentTimelineMergesSources(t *testing.T) {
	backend := &timelineFakeBackend{fakeBackend: &fakeBackend{}}
	now, err := time.Parse(time.RFC3339, "2026-06-11T12:00:00Z")
	if err != nil {
		t.Fatalf("parse now: %v", err)
	}
	params := incidentTimelineParams{
		Context:   "arn:aws:eks:eu-central-1:1:cluster/dev-eu-central-1",
		Namespace: "latest",
		Window:    3 * time.Hour,
		Project:   "group/app",
		Now:       now,
	}
	result := buildIncidentTimeline(context.Background(), backend, params)

	// Chronological merge across sources; out-of-window items excluded.
	var got []string
	for _, event := range result.Events {
		got = append(got, event.Source+"/"+event.Kind)
	}
	want := []string{"kubernetes/rollout", "gitlab/deployment", "kubernetes/k8s_warning", "alertmanager/alert"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("timeline order = %v, want %v\n%#v", got, want, result.Events)
	}
	// The endpoint matching the cluster alias was chosen (its alerts arrived);
	// the prod-namespace alert was filtered; the old alert counted as
	// preexisting.
	if result.PreexistingAlerts != 1 {
		t.Fatalf("preexisting = %d", result.PreexistingAlerts)
	}
	// Loki had no endpoint: skipped with the monitor-connect hint, not fatal.
	if len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0], "monitor connect") {
		t.Fatalf("skipped = %#v", result.Skipped)
	}
	if result.Count != 4 {
		t.Fatalf("count = %d", result.Count)
	}
}

func TestIncidentTimelinePlainRendering(t *testing.T) {
	var out bytes.Buffer
	cmd := New(Options{Backend: &fakeBackend{}, Out: &out})
	result := incidentTimelineResult{
		Since:     "2026-06-11T09:00:00Z",
		Namespace: "latest",
		Events: []timelineEvent{
			{Time: "2026-06-11T11:30:00Z", Source: "kubernetes", Kind: "k8s_warning", Summary: "BackOff Pod/api-1: restarting failed container"},
		},
		Count:             1,
		PreexistingAlerts: 1,
		Skipped:           []string{"loki error rate: no loki endpoint registered"},
	}
	if err := renderTimeline(cmd, result); err != nil {
		t.Fatalf("render: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "[kubernetes/k8s_warning] BackOff Pod/api-1") ||
		!strings.Contains(text, "1 alert(s) already firing") ||
		!strings.Contains(text, "skipped: loki error rate") {
		t.Fatalf("plain output = %s", text)
	}
}

