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

// lifecycleFakeBackend records every InstallPlugin call and returns a small
// marketplace catalog (entries carry go_install labels so they are installable).
type lifecycleFakeBackend struct {
	*fakeBackend
	installs []management.InstallRequest
}

func (b *lifecycleFakeBackend) SearchPlugins(context.Context, management.SearchRequest) (management.SearchResult, error) {
	return management.SearchResult{Plugins: []management.Plugin{
		{Ref: management.Ref{Name: "gitlab"}, Installed: true, Enabled: true, Labels: map[string]string{"go_install": "example.com/gitlab@latest"}},
		{Ref: management.Ref{Name: "slack"}, Source: "marketplace", Labels: map[string]string{"go_install": "example.com/slack@latest"}},
		{Ref: management.Ref{Name: "bananawix"}, Installed: true, Enabled: true}, // no marketplace labels -> skipped
	}}, nil
}

func (b *lifecycleFakeBackend) InstallPlugin(_ context.Context, req management.InstallRequest) (management.InstallResult, error) {
	b.installs = append(b.installs, req)
	return management.InstallResult{Plugin: management.Plugin{Ref: req.Ref}, Installed: true}, nil
}

func newLifecycleBackend() *lifecycleFakeBackend {
	return &lifecycleFakeBackend{fakeBackend: &fakeBackend{}}
}

func TestInstallAllInstallsMarketplacePlugins(t *testing.T) {
	backend := newLifecycleBackend()
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"install", "--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(backend.installs) != 2 {
		t.Fatalf("expected 2 installs (gitlab, slack), got %d: %#v", len(backend.installs), backend.installs)
	}
	for _, req := range backend.installs {
		if !req.Force {
			t.Fatalf("install --all should force: %#v", req)
		}
		if req.PreferRemote {
			t.Fatalf("install --all (no --remote) should build local: %#v", req)
		}
	}
	var results []batchInstallResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("decode results: %v\n%s", err, out.String())
	}
	// gitlab + slack installed, bananawix skipped.
	var skipped int
	for _, r := range results {
		if r.Skipped {
			skipped++
		}
	}
	if skipped != 1 {
		t.Fatalf("expected bananawix skipped, results=%#v", results)
	}
}

func TestInstallAllRemoteForcesPublishedSource(t *testing.T) {
	backend := newLifecycleBackend()
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"install", "--all", "--remote"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, req := range backend.installs {
		if !req.PreferRemote {
			t.Fatalf("install --all --remote should prefer remote: %#v", req)
		}
	}
}

func TestUpgradeInstallsPluginsRemoteAndRefreshesSkill(t *testing.T) {
	root := t.TempDir()
	t.Setenv(skillsDirEnv, root)
	skillDir := filepath.Join(root, "fluxplane-plugin")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "stale")

	backend := newLifecycleBackend()
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	// --skip-cli avoids running `go install` against the network in tests.
	cmd.SetArgs([]string{"upgrade", "--skip-cli"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(backend.installs) == 0 {
		t.Fatalf("upgrade should install plugins")
	}
	for _, req := range backend.installs {
		if !req.PreferRemote || !req.Force {
			t.Fatalf("upgrade installs should force from remote: %#v", req)
		}
	}
	var result upgradeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode upgrade result: %v\n%s", err, out.String())
	}
	if result.CLIUpdated {
		t.Fatalf("--skip-cli should not update the CLI")
	}
	if len(result.Skill.Refreshed) != 1 || result.Skill.Refreshed[0] != skillDir {
		t.Fatalf("upgrade should refresh installed skills: %#v", result.Skill)
	}
}
