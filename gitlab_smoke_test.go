package fluxplaneplugin_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"

	"github.com/fluxplane/fluxplane-core/core/resource"
	dex "github.com/fluxplane/fluxplane-dex"
	"github.com/fluxplane/fluxplane-dex/fluxplaneplugin"
)

// Sanity check on the real motivating case: a dex gitlab plugin produces
// "gitlab_mr"-style operation sets, an aggregate "gitlab" set, and one
// datasource provider with the manifest's entities. Resolves the plugin
// source from this module's location so the test runs without needing a
// pre-installed dex binary.
func TestGitlabAdapterShape(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	gitlabPath := filepath.Clean(filepath.Join(wd, "..", "plugins", "gitlab"))
	if _, err := os.Stat(gitlabPath); err != nil {
		t.Skipf("gitlab plugin source not found at %s: %v", gitlabPath, err)
	}
	e, err := dex.New(dex.Config{
		WorkDir:    t.TempDir(),
		DevPlugins: map[string]string{"gitlab": gitlabPath},
	})
	if err != nil {
		t.Fatalf("dex.New: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })

	plug, err := fluxplaneplugin.Wrap(e, "gitlab")
	if err != nil {
		t.Skipf("dex gitlab plugin not in marketplace: %v", err)
	}
	bundle, err := plug.Contributions(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "gitlab"}})
	if err != nil {
		// Manifest fetch shells out to the plugin binary; skip when it's
		// not installed in the test's ephemeral DEX_HOME.
		t.Skipf("gitlab manifest unavailable (binary not installed in test home): %v", err)
	}
	if len(bundle.Operations) == 0 {
		t.Fatalf("expected gitlab operation specs")
	}

	var setNames []string
	for _, set := range bundle.OperationSets {
		setNames = append(setNames, set.Name)
	}
	sort.Strings(setNames)
	t.Logf("gitlab operation sets: %v", setNames)

	hasAggregate := false
	hasMR := false
	for _, name := range setNames {
		if name == "gitlab" {
			hasAggregate = true
		}
		if name == "gitlab_mr" {
			hasMR = true
		}
		if !strings.HasPrefix(name, "gitlab") {
			t.Fatalf("set %q does not start with plugin name", name)
		}
	}
	if !hasAggregate {
		t.Fatalf("missing aggregate set %q", "gitlab")
	}
	if !hasMR {
		t.Fatalf("missing entity set %q (expected from gitlab.mr.* ops)", "gitlab_mr")
	}

	contributor, ok := plug.(pluginhost.DatasourceProviderContributor)
	if !ok {
		t.Fatalf("gitlab adapter missing DatasourceProviderContributor")
	}
	providers, err := contributor.DatasourceProviders(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "gitlab"}})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("expected one provider, got %d", len(providers))
	}
	entities := providers[0].Entities()
	if len(entities) == 0 {
		t.Fatalf("expected at least one entity from gitlab datasources")
	}
	var entityNames []string
	for _, e := range entities {
		entityNames = append(entityNames, string(e.Type))
	}
	sort.Strings(entityNames)
	t.Logf("gitlab datasource entities: %v", entityNames)
}
