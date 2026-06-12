package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

// decodeFailure parses the unified invoke-failure envelope.
func decodeFailure(t *testing.T, raw []byte) management.OperationFailure {
	t.Helper()
	var failure management.OperationFailure
	if err := json.Unmarshal(raw, &failure); err != nil {
		t.Fatalf("decode failure envelope: %v\n%s", err, raw)
	}
	return failure
}

func TestUnknownFieldSuggestsNearestName(t *testing.T) {
	backend := invokeValidationBackend(`{"additionalProperties":false,"properties":{"key":{"type":"string"},"limit":{"type":"integer"}}}`)
	var errBuf bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}, Err: &errBuf})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--input", `{"issue_key":"DEV-1"}`})
	if err := cmd.Execute(); !errors.Is(err, ErrReported) {
		t.Fatalf("expected ErrReported, got %v", err)
	}
	failure := decodeFailure(t, errBuf.Bytes())
	if failure.Plugin != "demo" || failure.Operation != "demo.do" || failure.Err.Code != "invalid_input" {
		t.Fatalf("envelope = %#v", failure)
	}
	if reason := failure.Err.Fields["issue_key"]; !strings.Contains(reason, `did you mean "key"?`) {
		t.Fatalf("reason = %q, want did-you-mean suggestion", reason)
	}
}

func TestUnknownFieldListsValidFieldsWhenNothingIsClose(t *testing.T) {
	backend := invokeValidationBackend(`{"additionalProperties":false,"properties":{"jql":{"type":"string"},"limit":{"type":"integer"}}}`)
	var errBuf bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}, Err: &errBuf})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--input", `{"max_results":5}`})
	if err := cmd.Execute(); !errors.Is(err, ErrReported) {
		t.Fatalf("expected ErrReported, got %v", err)
	}
	failure := decodeFailure(t, errBuf.Bytes())
	reason := failure.Err.Fields["max_results"]
	if !strings.Contains(reason, "valid fields: jql, limit") {
		t.Fatalf("reason = %q, want valid-fields listing", reason)
	}
}

func TestUnknownOperationFailsFastWithCloseMatches(t *testing.T) {
	backend := &fakeBackend{
		listOpsFn: func(management.OperationListRequest) (management.OperationListResult, error) {
			return management.OperationListResult{Operations: []sdkmanifest.OperationSpec{
				{Name: "jira.issue.show"}, {Name: "jira.issue.list"}, {Name: "jira.test"},
			}}, nil
		},
	}
	var errBuf bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}, Err: &errBuf})
	cmd.SetArgs([]string{"operation", "invoke", "jira", "jira.issue.get", "--input", `{}`})
	if err := cmd.Execute(); !errors.Is(err, ErrReported) {
		t.Fatalf("expected ErrReported, got %v", err)
	}
	if backend.invokeCount != 0 {
		t.Fatalf("unknown operation must not reach the backend; count=%d", backend.invokeCount)
	}
	failure := decodeFailure(t, errBuf.Bytes())
	if failure.Err.Code != "unknown_operation" {
		t.Fatalf("envelope = %#v", failure)
	}
	if len(failure.Err.Details) == 0 || !strings.Contains(failure.Err.Details[0], "jira.issue.show") {
		t.Fatalf("details = %#v, want close matches naming jira.issue.show", failure.Err.Details)
	}
}

func TestDryRunNeverInvokesEvenWithEmptyOperationListing(t *testing.T) {
	// A backend whose listing degrades to empty must not turn --dry-run into a
	// real (potentially side-effecting) invocation.
	backend := &fakeBackend{
		listOpsFn: func(management.OperationListRequest) (management.OperationListResult, error) {
			return management.OperationListResult{}, nil
		},
	}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--input", `{"x":1}`, "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backend.invokeCount != 0 {
		t.Fatalf("dry-run must never invoke; count=%d", backend.invokeCount)
	}
	var r operationDryRunResult
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if !r.Valid {
		t.Fatalf("dry-run without schema should report valid: %#v", r)
	}
}

func TestUnknownOperationNoValidateBypasses(t *testing.T) {
	backend := &fakeBackend{
		listOpsFn: func(management.OperationListRequest) (management.OperationListResult, error) {
			return management.OperationListResult{Operations: []sdkmanifest.OperationSpec{{Name: "jira.issue.show"}}}, nil
		},
	}
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}})
	cmd.SetArgs([]string{"operation", "invoke", "jira", "jira.issue.get", "--input", `{}`, "--no-validate"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--no-validate should invoke: %v", err)
	}
	if backend.invokeCount != 1 {
		t.Fatalf("--no-validate should reach the backend; count=%d", backend.invokeCount)
	}
}

func TestPluginFailureUsesSameEnvelopeShape(t *testing.T) {
	backend := &fakeBackend{
		invokeFn: func(req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
			return management.OperationInvokeResult{}, &management.OperationFailure{
				Plugin: "demo", Operation: req.Operation,
				Err: protocol.Error{Code: "upstream_error", Message: "boom"},
			}
		},
	}
	var errBuf bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}, Err: &errBuf})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--input", `{}`})
	if err := cmd.Execute(); !errors.Is(err, ErrReported) {
		t.Fatalf("expected ErrReported, got %v", err)
	}
	failure := decodeFailure(t, errBuf.Bytes())
	if failure.Plugin != "demo" || failure.Operation != "demo.do" || failure.Err.Code != "upstream_error" {
		t.Fatalf("envelope = %#v", failure)
	}
}

func TestTransportFailureWrappedInEnvelope(t *testing.T) {
	backend := &fakeBackend{
		invokeFn: func(management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
			return management.OperationInvokeResult{}, errors.New("dial tcp: connection refused")
		},
	}
	var errBuf bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}, Err: &errBuf})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--input", `{}`})
	if err := cmd.Execute(); !errors.Is(err, ErrReported) {
		t.Fatalf("expected ErrReported, got %v", err)
	}
	failure := decodeFailure(t, errBuf.Bytes())
	if failure.Err.Code != "invoke_failed" || !strings.Contains(failure.Err.Message, "connection refused") {
		t.Fatalf("envelope = %#v", failure)
	}
}

func TestResultOnlyFailurePrintsEnvelopeOnStdout(t *testing.T) {
	backend := &fakeBackend{
		invokeFn: func(req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
			return management.OperationInvokeResult{}, &management.OperationFailure{
				Plugin: "demo", Operation: req.Operation,
				Err: protocol.Error{Code: "upstream_error", Message: "boom"},
			}
		},
	}
	var out, errBuf bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out, Err: &errBuf})
	cmd.SetArgs([]string{"operation", "invoke", "demo", "demo.do", "--input", `{}`, "--result-only"})
	if err := cmd.Execute(); !errors.Is(err, ErrReported) {
		t.Fatalf("expected ErrReported, got %v", err)
	}
	// The pipeline contract: stdout carries the parseable error, stderr stays
	// quiet so the message isn't shown twice in a terminal.
	failure := decodeFailure(t, out.Bytes())
	if failure.Err.Code != "upstream_error" {
		t.Fatalf("stdout envelope = %#v", failure)
	}
	if strings.Contains(errBuf.String(), "upstream_error") {
		t.Fatalf("error printed on both streams:\n%s", errBuf.String())
	}
}
