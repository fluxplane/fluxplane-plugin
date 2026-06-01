package fluxplaneplugin_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	coredatasource "github.com/fluxplane/fluxplane-datasource"

	dex "github.com/fluxplane/fluxplane-dex"
	"github.com/fluxplane/fluxplane-dex/fluxplaneplugin"
)

func newEngine(t *testing.T) *dex.Engine {
	t.Helper()
	return newEngineWithConfig(t, dex.Config{})
}

func newEngineWithConfig(t *testing.T, cfg dex.Config) *dex.Engine {
	t.Helper()
	cfg.WorkDir = t.TempDir()
	e, err := dex.New(cfg)
	if err != nil {
		t.Fatalf("dex.New: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestWrapUnknownPlugin(t *testing.T) {
	e := newEngine(t)
	if _, err := fluxplaneplugin.Wrap(e, "does-not-exist"); err == nil {
		t.Fatalf("expected error for unknown plugin")
	}
}

func TestWrapManifestNameMatches(t *testing.T) {
	e := newEngine(t)
	plug, err := fluxplaneplugin.Wrap(e, "websearch")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if got := plug.Manifest().Name; got != "websearch" {
		t.Fatalf("Manifest().Name = %q, want websearch", got)
	}
}

func TestContributionsCarriesOperations(t *testing.T) {
	e := newEngine(t)
	plug, err := fluxplaneplugin.Wrap(e, "websearch")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	bundle, err := plug.Contributions(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "websearch"}})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Operations) == 0 {
		t.Fatalf("expected at least one operation spec")
	}
	for _, spec := range bundle.Operations {
		if !strings.HasPrefix(string(spec.Ref.Name), "websearch.") {
			t.Fatalf("operation name %q is not qualified with plugin prefix", spec.Ref.Name)
		}
		if strings.HasPrefix(string(spec.Ref.Name), "websearch.websearch.") {
			t.Fatalf("operation name %q is double-prefixed", spec.Ref.Name)
		}
	}
}

func TestContributionsCarriesOperationSets(t *testing.T) {
	e := newEngine(t)
	plug, err := fluxplaneplugin.Wrap(e, "websearch")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	bundle, err := plug.Contributions(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "websearch"}})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.OperationSets) == 0 {
		t.Fatalf("expected operation sets, got none")
	}
	var pluginSet *operation.Set
	for i, set := range bundle.OperationSets {
		if set.Name == "" {
			t.Fatalf("OperationSet[%d] has empty name", i)
		}
		if len(set.Operations) == 0 {
			t.Fatalf("OperationSet %q has no operations", set.Name)
		}
		for _, ref := range set.Operations {
			if !strings.HasPrefix(string(ref.Name), "websearch.") {
				t.Fatalf("set %q includes op ref %q outside plugin scope", set.Name, ref.Name)
			}
		}
		if set.Name == "websearch" {
			pluginSet = &bundle.OperationSets[i]
		}
	}
	if pluginSet == nil {
		t.Fatalf("expected an aggregate set named %q listing every op", "websearch")
	}
}

// Verifies that for a plugin whose ops are dotted (e.g. dex gitlab uses
// "gitlab.mr.create"), the adapter groups them under a "gitlab_mr"-style set
// keyed on the second dotted segment.
func TestOperationSetsGroupedBySecondSegment(t *testing.T) {
	e := newEngine(t)
	plug, err := fluxplaneplugin.Wrap(e, "websearch")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	bundle, err := plug.Contributions(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "websearch"}})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	// For each per-entity set, every ref must share the same "<plugin>.<entity>." prefix.
	for _, set := range bundle.OperationSets {
		if !strings.HasPrefix(set.Name, "websearch_") {
			continue
		}
		entity := strings.TrimPrefix(set.Name, "websearch_")
		wantPrefix := "websearch." + entity + "."
		for _, ref := range set.Operations {
			if !strings.HasPrefix(string(ref.Name), wantPrefix) {
				t.Fatalf("set %q includes op %q which does not match %q", set.Name, ref.Name, wantPrefix)
			}
		}
	}
}

func TestOperationsReturnExecutableBindings(t *testing.T) {
	e := newEngine(t)
	plug, err := fluxplaneplugin.Wrap(e, "websearch")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	contributor, ok := plug.(pluginhost.OperationContributor)
	if !ok {
		t.Fatalf("expected pluginhost.OperationContributor")
	}
	ops, err := contributor.Operations(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "websearch"}})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	if len(ops) == 0 {
		t.Fatalf("expected executable operations")
	}
	for _, op := range ops {
		if op.Spec().Ref.Name == "" {
			t.Fatalf("operation spec missing name")
		}
	}
}

func TestDatasourceProviderExposesEntities(t *testing.T) {
	e := newEngine(t)
	plug, err := fluxplaneplugin.Wrap(e, "websearch")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	contributor, ok := plug.(pluginhost.DatasourceProviderContributor)
	if !ok {
		t.Fatalf("adapter does not implement DatasourceProviderContributor")
	}
	providers, err := contributor.DatasourceProviders(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "websearch"}})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("expected exactly one provider, got %d", len(providers))
	}
	_ = providers[0].Entities()
}

func TestDatasourceProviderOpenUnknownReturnsError(t *testing.T) {
	e := newEngine(t)
	plug, err := fluxplaneplugin.Wrap(e, "websearch")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	contributor, _ := plug.(pluginhost.DatasourceProviderContributor)
	providers, err := contributor.DatasourceProviders(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "websearch"}})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	if _, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "definitely-not-a-real-datasource"}); err == nil {
		t.Fatalf("expected error opening unknown datasource")
	}
}

func TestDatasourceProviderListsIndexedRecords(t *testing.T) {
	slackDir, err := filepath.Abs("../plugins/slack")
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	e, err := dex.New(dex.Config{WorkDir: t.TempDir(), DevPlugins: map[string]string{"slack": slackDir}})
	if err != nil {
		t.Fatalf("dex.New: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	if _, err := e.Runner().State.SaveIndexRecords("slack", "work", "slack.users", []json.RawMessage{
		json.RawMessage(`{"entity":"slack.user","id":"U2","title":"Beta User","name":"beta"}`),
		json.RawMessage(`{"entity":"slack.user","id":"U1","title":"Alpha User","name":"alpha"}`),
	}); err != nil {
		t.Fatalf("SaveIndexRecords: %v", err)
	}
	plug, err := fluxplaneplugin.Wrap(e, "slack")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	contributor, ok := plug.(pluginhost.DatasourceProviderContributor)
	if !ok {
		t.Fatalf("adapter does not implement DatasourceProviderContributor")
	}
	providers, err := contributor.DatasourceProviders(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: "slack", Instance: "work"}})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{
		Name:     "slack.users",
		Kind:     "slack",
		Entities: []coredatasource.EntityType{"slack.user"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	entity := accessor.Entities()[0]
	if !entity.Supports(coredatasource.EntityCapabilityList) {
		t.Fatalf("entity capabilities = %#v, want list", entity.Capabilities)
	}
	lister, ok := accessor.(coredatasource.Lister)
	if !ok {
		t.Fatalf("accessor does not implement Lister")
	}
	result, err := lister.List(context.Background(), coredatasource.ListRequest{Entity: "slack.user", Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if result.Datasource != "slack.users" || result.Entity != "slack.user" || result.Total != 1 || !result.Complete {
		t.Fatalf("result metadata = %#v", result)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "U1" {
		t.Fatalf("records = %#v", result.Records)
	}
}

func TestAllWrapsEveryMarketplaceEntry(t *testing.T) {
	e := newEngine(t)
	plugs, err := fluxplaneplugin.All(e)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(plugs) == 0 {
		t.Fatalf("expected at least one wrapped plugin")
	}
	seen := map[string]bool{}
	for _, p := range plugs {
		name := p.Manifest().Name
		if seen[name] {
			t.Fatalf("duplicate manifest name %q", name)
		}
		seen[name] = true
	}
}
