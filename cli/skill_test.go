package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fpcontext "github.com/fluxplane/fluxplane-context"
	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

// skillFakeBackend reuses fakeBackend but returns a manifest rich enough to
// exercise the skill renderer (auth methods, operations, datasources, context)
// and a marketplace catalog with a not-installed plugin (discovery).
type skillFakeBackend struct {
	*fakeBackend
}

func (s *skillFakeBackend) PluginManifest(_ context.Context, req management.ManifestRequest) (management.ManifestResult, error) {
	m := sdkmanifest.PluginManifest{
		Name:        req.Ref.Name,
		Version:     "v1.2.3",
		Description: "GitLab operations and datasources.",
		Auth: []sdkmanifest.AuthMethod{{
			Name: "token",
			Env:  []string{"GITLAB_TOKEN"},
			Fields: []sdkmanifest.AuthField{{
				Name:     "access_token",
				Required: true,
				Secret:   true,
				Env:      []string{"GITLAB_TOKEN"},
			}},
		}},
		Operations: []sdkmanifest.OperationSpec{{
			Name:        "gitlab.project.list",
			Description: "List projects.",
			ReadOnly:    true,
			Input:       json.RawMessage(`{"type":"object","required":["limit"],"properties":{"limit":{"type":"integer","description":"Max records"},"endpoint_ref":{"type":"string"}}}`),
		}},
		Datasources: []sdkmanifest.DatasourceSpec{{
			Name:         "gitlab.issues",
			Entity:       "gitlab.issue",
			Capabilities: []string{"search", "lookup"},
		}},
		Context: []sdkmanifest.ContextSpec{{Name: fpcontext.ProviderName("gitlab.context")}},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return management.ManifestResult{}, err
	}
	return management.ManifestResult{Ref: req.Ref, Manifest: raw, Format: "json"}, nil
}

func (s *skillFakeBackend) SearchPlugins(_ context.Context, _ management.SearchRequest) (management.SearchResult, error) {
	return management.SearchResult{Plugins: []management.Plugin{
		{Ref: management.Ref{Name: "gitlab"}, Installed: true, Enabled: true},
		{Ref: management.Ref{Name: "slack"}, Description: "Slack messaging.", Source: "marketplace", Labels: map[string]string{"go_install": "github.com/fluxplane/fluxplane-plugins/slack/cmd/fluxplane-plugin-slack@latest"}},
	}}, nil
}

func newSkillBackend() *skillFakeBackend {
	return &skillFakeBackend{fakeBackend: &fakeBackend{}}
}

var staleDexInvocations = []string{"dex op ", "dex operation", "dex plugin ", "dex auth ", "dex search", "dex lookup", "dex datasource", "dex context", "dex install", "dex index", "alias `dex`"}

func assertNoStaleDex(t *testing.T, label, content string) {
	t.Helper()
	for _, stale := range staleDexInvocations {
		if strings.Contains(content, stale) {
			t.Fatalf("%s contains stale dex reference %q:\n%s", label, stale, content)
		}
	}
}

func TestSkillCommandRendersDiscoveryAndInvocations(t *testing.T) {
	var out bytes.Buffer
	cmd := New(Options{Backend: newSkillBackend(), Out: &out})
	cmd.SetArgs([]string{"skill"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "fluxplane-plugin operation invoke gitlab gitlab.project.list --input '{\"endpoint_ref\":\"<endpoint_ref>\",\"limit\":0}'") {
		t.Fatalf("missing generated operation invocation with endpoint_ref:\n%s", rendered)
	}
	if !strings.Contains(rendered, "name: fluxplane-plugin") {
		t.Fatalf("front matter name not honored:\n%s", rendered)
	}
	if !strings.Contains(rendered, "## Endpoints") || !strings.Contains(rendered, "fluxplane-plugin endpoint list") {
		t.Fatalf("missing endpoints guidance:\n%s", rendered)
	}
	if !strings.Contains(rendered, "## Available to install") || !strings.Contains(rendered, "fluxplane-plugin install slack") {
		t.Fatalf("missing discoverable (not-installed) plugin:\n%s", rendered)
	}
	if !strings.Contains(rendered, "## Authenticating a plugin") {
		t.Fatalf("missing auth onboarding section:\n%s", rendered)
	}
	assertNoStaleDex(t, "combined skill", rendered)
}

func TestSkillInstallWritesInstalledAndAvailable(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	cmd := New(Options{Backend: newSkillBackend(), Out: &out})
	// A custom --name proves the skill identity is configurable.
	cmd.SetArgs([]string{"skill", "install", "--name", "my-tools", "--output-dir", dir, "--no-claude-link"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result skillInstallResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\n%s", err, out.String())
	}
	if result.Name != "my-tools" || result.Linked {
		t.Fatalf("unexpected install result: %#v", result)
	}

	main := readFileString(t, filepath.Join(dir, "SKILL.md"))
	if !strings.Contains(main, "name: my-tools") {
		t.Fatalf("SKILL.md front matter name not honored:\n%s", main)
	}
	if !strings.Contains(main, "[gitlab](references/gitlab.md)") {
		t.Fatalf("SKILL.md missing installed integration link:\n%s", main)
	}
	if !strings.Contains(main, "fluxplane-plugin install slack") {
		t.Fatalf("SKILL.md missing available-to-install entry:\n%s", main)
	}
	assertNoStaleDex(t, "SKILL.md", main)

	// Installed plugins get references; available (not-installed) ones do not.
	if _, err := os.Stat(filepath.Join(dir, "references", "slack.md")); !os.IsNotExist(err) {
		t.Fatalf("available plugin should not get a reference page (err=%v)", err)
	}
	ref := readFileString(t, filepath.Join(dir, "references", "gitlab.md"))
	for _, want := range []string{
		"fluxplane-plugin operation invoke gitlab gitlab.project.list",
		"fluxplane-plugin auth connect auto gitlab",
		"--field access_token=<value>",
		"GITLAB_TOKEN",
	} {
		if !strings.Contains(ref, want) {
			t.Fatalf("gitlab reference missing %q:\n%s", want, ref)
		}
	}
	assertNoStaleDex(t, "gitlab reference", ref)
}

func TestSkillRefreshRegeneratesAndPrunes(t *testing.T) {
	root := t.TempDir()
	t.Setenv(skillsDirEnv, root)

	// Pre-create an installed skill with a stale reference for a now-removed plugin.
	skillDir := filepath.Join(root, "fluxplane-plugin")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "stale")
	writeFile(t, filepath.Join(refsDir, "old.md"), "stale reference for an uninstalled plugin")

	var out bytes.Buffer
	cmd := New(Options{Backend: newSkillBackend(), Out: &out})
	cmd.SetArgs([]string{"skill", "refresh"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result skillRefreshResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode refresh result: %v\n%s", err, out.String())
	}
	if len(result.Refreshed) != 1 || result.Refreshed[0] != skillDir {
		t.Fatalf("unexpected refresh result: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(refsDir, "old.md")); !os.IsNotExist(err) {
		t.Fatalf("stale reference was not pruned (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(refsDir, "gitlab.md")); err != nil {
		t.Fatalf("installed plugin reference missing after refresh: %v", err)
	}
	main := readFileString(t, filepath.Join(skillDir, "SKILL.md"))
	if !strings.Contains(main, "name: fluxplane-plugin") {
		t.Fatalf("SKILL.md not regenerated in place:\n%s", main)
	}
}

func TestStateChangingCommandAutoRefreshesSkill(t *testing.T) {
	root := t.TempDir()
	t.Setenv(skillsDirEnv, root)
	skillDir := filepath.Join(root, "fluxplane-plugin")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "stale")

	var out, errOut bytes.Buffer
	cmd := New(Options{Backend: newSkillBackend(), Out: &out, Err: &errOut})
	cmd.SetArgs([]string{"install", "slack"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(out.String(), "\"installed\"") {
		t.Fatalf("install stdout result changed unexpectedly:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "skill: refreshed "+skillDir) {
		t.Fatalf("expected auto-refresh note on stderr, got:\n%s", errOut.String())
	}
	main := readFileString(t, filepath.Join(skillDir, "SKILL.md"))
	if !strings.Contains(main, "name: fluxplane-plugin") {
		t.Fatalf("skill was not regenerated by the auto-refresh hook:\n%s", main)
	}
}

func TestDryRunDoesNotAutoRefresh(t *testing.T) {
	root := t.TempDir()
	t.Setenv(skillsDirEnv, root)
	skillDir := filepath.Join(root, "fluxplane-plugin")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "stale")

	var out, errOut bytes.Buffer
	cmd := New(Options{Backend: newSkillBackend(), Out: &out, Err: &errOut})
	cmd.SetArgs([]string{"install", "slack", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(errOut.String(), "skill: refreshed") {
		t.Fatalf("dry-run should not refresh skills, got:\n%s", errOut.String())
	}
	if got := readFileString(t, filepath.Join(skillDir, "SKILL.md")); got != "stale" {
		t.Fatalf("dry-run regenerated the skill: %q", got)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
