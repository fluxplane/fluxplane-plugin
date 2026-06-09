package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
)

// devFakeBackend records SyncLocalPlugins calls and satisfies management.LocalSyncer.
type devFakeBackend struct {
	*fakeBackend
	syncReqs []management.SyncRequest
}

func (b *devFakeBackend) SyncLocalPlugins(_ context.Context, req management.SyncRequest) (management.SyncResult, error) {
	b.syncReqs = append(b.syncReqs, req)
	out := management.SyncResult{}
	if req.All {
		out.Plugins = append(out.Plugins, management.SyncPluginResult{Plugin: management.Ref{Name: "jira"}, Rebuilt: true})
	}
	for _, ref := range req.Refs {
		out.Plugins = append(out.Plugins, management.SyncPluginResult{Plugin: ref, Rebuilt: true})
	}
	return out, nil
}

func TestDevSyncAllWhenNoArgs(t *testing.T) {
	backend := &devFakeBackend{fakeBackend: &fakeBackend{}}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"dev", "sync"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(backend.syncReqs) != 1 || !backend.syncReqs[0].All || len(backend.syncReqs[0].Refs) != 0 {
		t.Fatalf("dev sync with no args should request All, got %#v", backend.syncReqs)
	}
	var result management.SyncResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if len(result.Plugins) != 1 || !result.Plugins[0].Rebuilt {
		t.Fatalf("result = %#v", result)
	}
}

func TestDevSyncNamedPlugins(t *testing.T) {
	backend := &devFakeBackend{fakeBackend: &fakeBackend{}}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"dev", "sync", "jira", "gitlab", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(backend.syncReqs) != 1 {
		t.Fatalf("expected one sync request, got %#v", backend.syncReqs)
	}
	req := backend.syncReqs[0]
	if req.All || !req.DryRun || len(req.Refs) != 2 || req.Refs[0].Name != "jira" || req.Refs[1].Name != "gitlab" {
		t.Fatalf("named dry-run sync request = %#v", req)
	}
}
