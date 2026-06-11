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
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

// describeFakeBackend layers richer fixtures over fakeBackend for describe.
type describeFakeBackend struct {
	*fakeBackend
}

func (b *describeFakeBackend) PluginStatus(context.Context, management.StatusRequest) (management.StatusResult, error) {
	return management.StatusResult{Plugins: []management.Plugin{{
		Ref:              management.Ref{Name: "gitlab"},
		Description:      "GitLab plugin.",
		Source:           "marketplace",
		Installed:        true,
		Enabled:          true,
		InstalledVersion: "v0.4.0",
		Pinned:           "v0.4.0",
		PreviousVersion:  "v0.3.0",
	}}}, nil
}

func (b *describeFakeBackend) PluginManifest(context.Context, management.ManifestRequest) (management.ManifestResult, error) {
	manifest := sdkmanifest.PluginManifest{
		Name:        "gitlab",
		Version:     "0.19.0",
		Description: "GitLab plugin.",
		Aliases:     []string{"git-lab"},
		Endpoints:   []sdkmanifest.EndpointSpec{{Name: "gitlab.endpoints", Products: []string{"gitlab"}}},
	}
	raw, _ := json.Marshal(manifest)
	return management.ManifestResult{Manifest: raw, Format: "json"}, nil
}

func (b *describeFakeBackend) AuthStatus(context.Context, management.AuthStatusRequest) (management.AuthStatusResult, error) {
	return management.AuthStatusResult{Auth: []management.AuthState{{Method: "token", Connected: true}}}, nil
}

func (b *describeFakeBackend) ListOperations(context.Context, management.OperationListRequest) (management.OperationListResult, error) {
	return management.OperationListResult{Operations: []sdkmanifest.OperationSpec{
		{Name: "gitlab.issue.list", ReadOnly: true, Input: json.RawMessage(`{"type":"object","examples":[{"project":"x"}]}`)},
		{Name: "gitlab.issue.create", Input: json.RawMessage(`{"type":"object"}`)},
		{Name: "gitlab.mr.list", ReadOnly: true, Input: json.RawMessage(`{"type":"object"}`)},
	}}, nil
}

func (b *describeFakeBackend) ListEndpoints(context.Context, management.EndpointListRequest) (management.EndpointListResult, error) {
	return management.EndpointListResult{Endpoints: []fpendpoint.Record{
		{EndpointRef: fpendpoint.EndpointRef{ID: "gitlab-prod", URL: "https://gitlab.example.com", Product: "gitlab"}},
		{EndpointRef: fpendpoint.EndpointRef{ID: "homer-services", URL: "https://homer.example.com", Product: "homer"}},
	}}, nil
}

func TestDescribeAggregatesPluginView(t *testing.T) {
	backend := &describeFakeBackend{fakeBackend: &fakeBackend{}}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"describe", "gitlab"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result describeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if result.Plugin.Name != "gitlab" || !result.Installed || !result.Enabled {
		t.Fatalf("identity = %#v", result)
	}
	if result.Versions.Installed != "v0.4.0" || result.Versions.Pinned != "v0.4.0" || result.Versions.Previous != "v0.3.0" || result.Versions.Manifest != "0.19.0" {
		t.Fatalf("versions = %#v", result.Versions)
	}
	if result.Auth == nil || !result.Auth.Connected {
		t.Fatalf("auth = %#v", result.Auth)
	}
	if result.Operations == nil || result.Operations.Count != 3 || result.Operations.ReadOnly != 2 || !result.Operations.Examples {
		t.Fatalf("operations = %#v", result.Operations)
	}
	if got := result.Operations.Groups["gitlab"]; len(got) != 3 {
		t.Fatalf("groups = %#v", result.Operations.Groups)
	}
	// Only endpoints whose product the plugin declares survive the filter.
	if len(result.Endpoints) != 1 || result.Endpoints[0].ID != "gitlab-prod" {
		t.Fatalf("endpoints = %#v", result.Endpoints)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %#v", result.Errors)
	}
}

func TestDescribeSurvivesSectionFailures(t *testing.T) {
	backend := &describeFakeBackend{fakeBackend: &fakeBackend{
		listOpsFn: func(management.OperationListRequest) (management.OperationListResult, error) {
			return management.OperationListResult{}, context.DeadlineExceeded
		},
	}}
	result := describePlugin(context.Background(), &failingOpsBackend{describeFakeBackend: backend}, management.Ref{Name: "gitlab"}, "default")
	if result.Errors["operations"] == "" {
		t.Fatalf("errors = %#v, want operations section error", result.Errors)
	}
	if result.Auth == nil || !result.Auth.Connected {
		t.Fatalf("auth should still resolve: %#v", result.Auth)
	}
}

// failingOpsBackend makes ListOperations fail while keeping the rich fixtures.
type failingOpsBackend struct {
	*describeFakeBackend
}

func (b *failingOpsBackend) ListOperations(context.Context, management.OperationListRequest) (management.OperationListResult, error) {
	return management.OperationListResult{}, context.DeadlineExceeded
}

func TestInvokeTimeoutSetsDeadline(t *testing.T) {
	backend := &fakeBackend{}
	backend.invokeFn = func(req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
		return management.OperationInvokeResult{Result: []byte(`{"ok":true}`)}, nil
	}
	deadlineChecked := false
	backend.listOpsFn = func(management.OperationListRequest) (management.OperationListResult, error) {
		return management.OperationListResult{}, nil
	}
	var out bytes.Buffer
	cmd := New(Options{Backend: &deadlineRecordingBackend{fakeBackend: backend, checked: &deadlineChecked}, Out: &out})
	cmd.SetArgs([]string{"operation", "invoke", "gitlab", "noop", "--timeout", "5s", "--no-validate", "--input", "{}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !deadlineChecked {
		t.Fatal("InvokeOperation context had no deadline despite --timeout")
	}
}

type deadlineRecordingBackend struct {
	*fakeBackend
	checked *bool
}

func (b *deadlineRecordingBackend) InvokeOperation(ctx context.Context, req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) > 0 {
		*b.checked = true
	}
	return b.fakeBackend.InvokeOperation(ctx, req)
}

func TestCompletionListsPluginNames(t *testing.T) {
	var out bytes.Buffer
	cmd := New(Options{Backend: &fakeBackend{}, Out: &out})
	cmd.SetArgs([]string{"__complete", "describe", ""})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "gitlab") {
		t.Fatalf("completions = %q, want gitlab", out.String())
	}
}
