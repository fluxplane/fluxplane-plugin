package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

func TestSafeProbeOperationsSelection(t *testing.T) {
	ops := []sdkmanifest.OperationSpec{
		{Name: "jira.auth.test", ReadOnly: true},
		{Name: "jira.issue.search", ReadOnly: true, Input: json.RawMessage(`{"properties":{"endpoint_ref":{"type":"string"}}}`)},
		{Name: "jira.issue.show", ReadOnly: true, Input: json.RawMessage(`{"required":["key"],"properties":{"key":{"type":"string"}}}`)},
		{Name: "jira.issue.create", ReadOnly: false, Input: json.RawMessage(`{"required":["summary"]}`)},
	}
	probes := safeProbeOperations(ops, false)
	want := []string{"jira.auth.test", "jira.issue.search"}
	if strings.Join(probes, ",") != strings.Join(want, ",") {
		t.Fatalf("probes = %v, want %v", probes, want)
	}

	authOnly := safeProbeOperations(ops, true)
	if len(authOnly) != 1 || authOnly[0] != "jira.auth.test" {
		t.Fatalf("auth-only probes = %v", authOnly)
	}
}

// selftestFakeBackend returns a fixed plugin + operations and a programmable
// invoke outcome per operation.
type selftestFakeBackend struct {
	*fakeBackend
	ops      []sdkmanifest.OperationSpec
	invokeOK map[string]bool // operation -> ok; missing => error
	invoked  []string
}

func (b *selftestFakeBackend) ListPlugins(context.Context, management.ListRequest) ([]management.Plugin, error) {
	return []management.Plugin{{Ref: management.Ref{Name: "jira"}, Installed: true, Enabled: true}}, nil
}

func (b *selftestFakeBackend) ListOperations(_ context.Context, req management.OperationListRequest) (management.OperationListResult, error) {
	return management.OperationListResult{Plugin: req.Ref, Operations: b.ops}, nil
}

func (b *selftestFakeBackend) InvokeOperation(_ context.Context, req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
	b.invoked = append(b.invoked, req.Operation)
	if ok, found := b.invokeOK[req.Operation]; found && ok {
		return management.OperationInvokeResult{Operation: req.Operation, Result: json.RawMessage(`{"ok":true}`)}, nil
	}
	if _, found := b.invokeOK[req.Operation]; found {
		// present but false -> result reports ok:false
		return management.OperationInvokeResult{Operation: req.Operation, Result: json.RawMessage(`{"ok":false}`)}, nil
	}
	return management.OperationInvokeResult{}, &opError{op: req.Operation}
}

type opError struct{ op string }

func (e *opError) Error() string { return e.op + " failed" }

func TestSelftestReportsPassAndFail(t *testing.T) {
	backend := &selftestFakeBackend{
		fakeBackend: &fakeBackend{},
		ops: []sdkmanifest.OperationSpec{
			{Name: "jira.auth.test", ReadOnly: true},
			{Name: "jira.issue.search", ReadOnly: true, Input: json.RawMessage(`{"properties":{"endpoint_ref":{}}}`)},
		},
		invokeOK: map[string]bool{"jira.auth.test": true}, // search -> error
	}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"selftest"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result selftestResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if result.Healthy {
		t.Fatalf("a failing probe should make the run unhealthy: %#v", result)
	}
	if len(result.Plugins) != 1 {
		t.Fatalf("expected one plugin, got %#v", result.Plugins)
	}
	p := result.Plugins[0]
	if p.Passed != 1 || p.Failed != 1 || p.OK {
		t.Fatalf("expected 1 pass / 1 fail, got %#v", p)
	}
}

func TestSelftestAuthOnly(t *testing.T) {
	backend := &selftestFakeBackend{
		fakeBackend: &fakeBackend{},
		ops: []sdkmanifest.OperationSpec{
			{Name: "jira.auth.test", ReadOnly: true},
			{Name: "jira.issue.search", ReadOnly: true, Input: json.RawMessage(`{"properties":{}}`)},
		},
		invokeOK: map[string]bool{"jira.auth.test": true, "jira.issue.search": true},
	}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"selftest", "--auth-only"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(backend.invoked) != 1 || backend.invoked[0] != "jira.auth.test" {
		t.Fatalf("--auth-only should invoke only auth.test, got %v", backend.invoked)
	}
}
