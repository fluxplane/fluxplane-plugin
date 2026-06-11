package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
)

func TestExtractPath(t *testing.T) {
	var v any
	_ = json.Unmarshal([]byte(`{"ok":true,"issue":{"fields":{"status":{"name":"Done"}}},"rows":[{"key":"A"},{"key":"B"}]}`), &v)

	cases := []struct {
		path string
		want any
		ok   bool
	}{
		{"ok", true, true},
		{"issue.fields.status.name", "Done", true},
		{"rows.1.key", "B", true},
		{"rows.5.key", nil, false},
		{"missing.path", nil, false},
		{"ok.deeper", nil, false}, // scalar can't descend
	}
	for _, c := range cases {
		got, ok := extractPath(v, c.path)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("extractPath(%q) = (%v,%v), want (%v,%v)", c.path, got, ok, c.want, c.ok)
		}
	}
}

func outputBackend() *fakeBackend {
	return &fakeBackend{invokeFn: func(req management.OperationInvokeRequest) (management.OperationInvokeResult, error) {
		return management.OperationInvokeResult{
			Plugin:    req.Ref,
			Operation: req.Operation,
			Result:    json.RawMessage(`{"ok":true,"user":{"displayName":"Ada"},"rows":[{"ok":true}]}`),
		}, nil
	}}
}

func runInvoke(t *testing.T, backend management.Backend, extra ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs(append([]string{"operation", "invoke", "demo", "demo.do", "--no-validate"}, extra...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	return out.String()
}

func TestInvokeDefaultPrintsEnvelope(t *testing.T) {
	out := runInvoke(t, outputBackend())
	if !contains(out, `"operation"`) || !contains(out, `"result"`) {
		t.Fatalf("default output should be the full envelope:\n%s", out)
	}
}

func TestInvokeResultOnly(t *testing.T) {
	out := runInvoke(t, outputBackend(), "--result-only")
	if contains(out, `"operation"`) || contains(out, `"plugin"`) {
		t.Fatalf("--result-only should drop the envelope:\n%s", out)
	}
	if !contains(out, `"displayName"`) {
		t.Fatalf("--result-only should keep the result:\n%s", out)
	}
}

func TestInvokeFieldSingleAndMulti(t *testing.T) {
	single := runInvoke(t, outputBackend(), "--field", "user.displayName")
	if trimSpace(single) != `"Ada"` {
		t.Fatalf("single field = %q", single)
	}
	multi := runInvoke(t, outputBackend(), "--field", "ok,rows.0.ok,nope")
	var m map[string]any
	if err := json.Unmarshal([]byte(multi), &m); err != nil {
		t.Fatalf("decode multi: %v\n%s", err, multi)
	}
	if m["ok"] != true || m["rows.0.ok"] != true {
		t.Fatalf("multi field values = %#v", m)
	}
	if miss, ok := m["missing"].([]any); !ok || len(miss) != 1 || miss[0] != "nope" {
		t.Fatalf("missing not recorded: %#v", m["missing"])
	}
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }
func trimSpace(s string) string   { return string(bytes.TrimSpace([]byte(s))) }

func TestMissingFieldListsAvailableKeys(t *testing.T) {
	res := management.OperationInvokeResult{Result: []byte(`{"record":{"iid":1,"title":"x"},"count":1}`)}
	var out bytes.Buffer
	missing, err := printOperationResultStrict(&out, res, false, []string{"record.content"})
	if err != nil || !missing {
		t.Fatalf("missing=%v err=%v", missing, err)
	}
	var decoded struct {
		Missing   []string `json:"missing"`
		Available []string `json:"available"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if len(decoded.Available) != 2 || decoded.Available[0] != "iid" || decoded.Available[1] != "title" {
		t.Fatalf("available = %#v, want keys at the deepest resolvable point", decoded.Available)
	}
	// Top-level miss lists top-level keys.
	out.Reset()
	_, _ = printOperationResultStrict(&out, res, false, []string{"content"})
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decode top: %v", err)
	}
	if len(decoded.Available) != 2 || decoded.Available[0] != "count" || decoded.Available[1] != "record" {
		t.Fatalf("available top = %#v", decoded.Available)
	}
}
