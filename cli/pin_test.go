package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
)

// pinFakeBackend records VersionManager calls and returns a pinned plugin in
// its installed list so skip-pinned behavior is observable.
type pinFakeBackend struct {
	*lifecycleFakeBackend
	pinReqs      []management.PinRequest
	rollbackReqs []management.RollbackRequest
	installedVer string
	pinnedVer    string
}

func (b *pinFakeBackend) ListPlugins(context.Context, management.ListRequest) ([]management.Plugin, error) {
	return []management.Plugin{{
		Ref:              management.Ref{Name: "gitlab"},
		Installed:        true,
		Enabled:          true,
		InstalledVersion: b.installedVer,
		Pinned:           b.pinnedVer,
	}}, nil
}

func (b *pinFakeBackend) PinPlugin(_ context.Context, req management.PinRequest) (management.PinResult, error) {
	b.pinReqs = append(b.pinReqs, req)
	pinned := req.Ref.Version
	if req.Unpin {
		pinned = ""
	}
	return management.PinResult{Plugin: management.Plugin{Ref: management.Ref{Name: req.Ref.Name}}, Pinned: pinned, Changed: true}, nil
}

func (b *pinFakeBackend) RollbackPlugin(_ context.Context, req management.RollbackRequest) (management.RollbackResult, error) {
	b.rollbackReqs = append(b.rollbackReqs, req)
	return management.RollbackResult{Plugin: management.Plugin{Ref: management.Ref{Name: req.Ref.Name}}, RolledBack: true, From: "v0.4.0", To: "v0.3.0"}, nil
}

func newPinBackend() *pinFakeBackend {
	return &pinFakeBackend{lifecycleFakeBackend: newLifecycleBackend(), installedVer: "v0.4.0"}
}

func TestPinAtVersionReinstallsFirst(t *testing.T) {
	backend := newPinBackend()
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"pin", "gitlab@v0.3.0"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(backend.installs) != 1 || backend.installs[0].Ref.Version != "v0.3.0" || !backend.installs[0].Force || !backend.installs[0].PreferRemote {
		t.Fatalf("pin@version should force-reinstall remote at that version, installs=%#v", backend.installs)
	}
	if len(backend.pinReqs) != 1 || backend.pinReqs[0].Ref.Version != "v0.3.0" || backend.pinReqs[0].Unpin {
		t.Fatalf("pinReqs = %#v", backend.pinReqs)
	}
}

func TestPinAtInstalledVersionSkipsReinstall(t *testing.T) {
	backend := newPinBackend()
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"pin", "gitlab@v0.4.0"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(backend.installs) != 0 {
		t.Fatalf("pin at the installed version should not reinstall, installs=%#v", backend.installs)
	}
	if len(backend.pinReqs) != 1 {
		t.Fatalf("pinReqs = %#v", backend.pinReqs)
	}
}

func TestPinBareAndUnpin(t *testing.T) {
	backend := newPinBackend()
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"pin", "gitlab"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(backend.installs) != 0 || len(backend.pinReqs) != 1 || backend.pinReqs[0].Ref.Version != "" {
		t.Fatalf("bare pin should only call PinPlugin, installs=%#v pinReqs=%#v", backend.installs, backend.pinReqs)
	}
	cmd = New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"unpin", "gitlab"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute unpin: %v", err)
	}
	if len(backend.pinReqs) != 2 || !backend.pinReqs[1].Unpin {
		t.Fatalf("unpin should set Unpin, pinReqs=%#v", backend.pinReqs)
	}
}

func TestRollbackCommand(t *testing.T) {
	backend := newPinBackend()
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"rollback", "gitlab"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(backend.rollbackReqs) != 1 || backend.rollbackReqs[0].Ref.Name != "gitlab" {
		t.Fatalf("rollbackReqs = %#v", backend.rollbackReqs)
	}
	var result management.RollbackResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if !result.RolledBack || result.From != "v0.4.0" || result.To != "v0.3.0" {
		t.Fatalf("result = %#v", result)
	}
}

func TestPinRequiresVersionManagerBackend(t *testing.T) {
	var out bytes.Buffer
	cmd := New(Options{Backend: &fakeBackend{}, Out: &out})
	cmd.SetArgs([]string{"pin", "gitlab"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "does not support version pinning") {
		t.Fatalf("err = %v, want unsupported-backend error", err)
	}
}

func TestUpgradeSkipsPinnedPlugins(t *testing.T) {
	backend := newPinBackend()
	backend.pinnedVer = "v0.4.0"
	results := installAllPlugins(context.Background(), backend, true, false)
	var gitlab *batchInstallResult
	for i := range results {
		if results[i].Plugin.Name == "gitlab" {
			gitlab = &results[i]
		}
	}
	if gitlab == nil || !gitlab.Skipped || gitlab.Reason != "pinned to v0.4.0" {
		t.Fatalf("results = %#v, want gitlab skipped with pin reason", results)
	}
	for _, req := range backend.installs {
		if req.Ref.Name == "gitlab" {
			t.Fatalf("pinned gitlab must not be installed: %#v", backend.installs)
		}
	}
}
