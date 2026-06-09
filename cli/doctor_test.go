package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
)

func TestParseGoVersionMod(t *testing.T) {
	dirty := "/home/u/go/bin/foo: go1.26\n" +
		"\tpath\tgithub.com/fluxplane/fluxplane-plugins/atlassian/cmd/fluxplane-plugin-jira\n" +
		"\tmod\tgithub.com/fluxplane/fluxplane-plugins/atlassian\tv0.2.0+dirty\t\n" +
		"\tbuild\tvcs.revision=28089a0\n" +
		"\tbuild\tvcs.modified=true\n"
	info := parseGoVersionMod(dirty)
	if info.Module != "github.com/fluxplane/fluxplane-plugins/atlassian" {
		t.Fatalf("module = %q", info.Module)
	}
	if info.Version != "v0.2.0+dirty" || !info.Modified || !info.dev() {
		t.Fatalf("expected dirty dev build, got %#v", info)
	}

	clean := "/home/u/go/bin/foo: go1.26\n" +
		"\tmod\tgithub.com/fluxplane/fluxplane-plugins/atlassian\tv0.3.0\th1:abc=\n"
	info = parseGoVersionMod(clean)
	if info.Version != "v0.3.0" || info.Modified || info.dev() {
		t.Fatalf("expected clean published build, got %#v", info)
	}
}

// doctorFakeBackend returns a configurable installed plugin and auth state.
type doctorFakeBackend struct {
	*fakeBackend
	plugins []management.Plugin
	auth    []management.AuthState
}

func (b *doctorFakeBackend) ListPlugins(context.Context, management.ListRequest) ([]management.Plugin, error) {
	return b.plugins, nil
}

func (b *doctorFakeBackend) AuthStatus(_ context.Context, req management.AuthStatusRequest) (management.AuthStatusResult, error) {
	return management.AuthStatusResult{Plugin: req.Ref, Auth: b.auth}, nil
}

func runDoctor(t *testing.T, backend management.Backend, args ...string) doctorResult {
	t.Helper()
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs(append([]string{"doctor"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result doctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	return result
}

func TestDoctorFlagsDevBuild(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "fluxplane-plugin-jira")
	if err := os.WriteFile(binPath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	backend := &doctorFakeBackend{
		fakeBackend: &fakeBackend{},
		plugins: []management.Plugin{{
			Ref:    management.Ref{Name: "jira"},
			Labels: map[string]string{"installed_binary_path": binPath, "go_install": "example.com/jira@latest"},
		}},
		auth: []management.AuthState{{Method: "token", Connected: true}},
	}

	prev := inspectBinary
	inspectBinary = func(context.Context, string) (binaryInfo, error) {
		return binaryInfo{Module: "example.com/atlassian", Version: "v0.2.0+dirty", Modified: true}, nil
	}
	defer func() { inspectBinary = prev }()

	result := runDoctor(t, backend)
	if result.Healthy {
		t.Fatalf("dirty build should be unhealthy: %#v", result)
	}
	if len(result.Plugins) != 1 || !result.Plugins[0].DevBuild || result.Plugins[0].OK {
		t.Fatalf("expected dev-build report, got %#v", result.Plugins)
	}
}

func TestDoctorFlagsVersionDrift(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "fluxplane-plugin-jira")
	if err := os.WriteFile(binPath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	backend := &doctorFakeBackend{
		fakeBackend: &fakeBackend{},
		plugins: []management.Plugin{{
			Ref:    management.Ref{Name: "jira"},
			Labels: map[string]string{"installed_binary_path": binPath, "go_install": "example.com/jira@latest"},
		}},
		auth: []management.AuthState{{Method: "token", Connected: true}},
	}

	prevInspect, prevLatest := inspectBinary, resolveLatestVersion
	inspectBinary = func(context.Context, string) (binaryInfo, error) {
		return binaryInfo{Module: "example.com/atlassian", Version: "v0.3.0"}, nil
	}
	resolveLatestVersion = func(context.Context, string) (string, error) { return "v0.4.0", nil }
	defer func() { inspectBinary, resolveLatestVersion = prevInspect, prevLatest }()

	result := runDoctor(t, backend, "--latest")
	if result.Healthy || !result.Plugins[0].Drift || result.Plugins[0].Latest != "v0.4.0" {
		t.Fatalf("expected drift report, got %#v", result.Plugins)
	}

	// At latest -> healthy, no drift.
	resolveLatestVersion = func(context.Context, string) (string, error) { return "v0.3.0", nil }
	result = runDoctor(t, backend, "--latest")
	if !result.Healthy || result.Plugins[0].Drift {
		t.Fatalf("expected healthy at-latest, got %#v", result.Plugins)
	}
}

func TestDoctorFlagsMissingBinary(t *testing.T) {
	backend := &doctorFakeBackend{
		fakeBackend: &fakeBackend{},
		plugins: []management.Plugin{{
			Ref:    management.Ref{Name: "jira"},
			Labels: map[string]string{"installed_binary_path": "/nonexistent/path/to/bin"},
		}},
	}
	result := runDoctor(t, backend)
	if result.Healthy || result.Plugins[0].BinaryExists {
		t.Fatalf("missing binary should be unhealthy, got %#v", result.Plugins)
	}
}
