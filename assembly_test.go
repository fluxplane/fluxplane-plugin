package fluxplaneplugin_test

import (
	"context"
	"testing"

	corereaction "github.com/fluxplane/fluxplane-core/core/reaction"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"

	dex "github.com/fluxplane/fluxplane-dex"
	"github.com/fluxplane/fluxplane-dex/fluxplaneplugin"
)

// fakeNative is a no-op pluginhost.Plugin used in NativePlugins fields so
// we don't have to drag the whole pluginbinding into a unit test.
type fakeNative struct{ name string }

func (n fakeNative) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: n.name, Description: "test native"}
}

func (n fakeNative) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{Plugins: []resource.PluginRef{{Name: n.name}}}, nil
}

func newAssemblyEngine(t *testing.T) *dex.Engine {
	t.Helper()
	e, err := dex.New(dex.Config{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("dex.New: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestAssemblyNewConstructsEngineWhenNil(t *testing.T) {
	// Smoke: New with empty Config builds a default engine without erroring.
	asm, err := fluxplaneplugin.New(fluxplaneplugin.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if asm.Engine() == nil {
		t.Fatalf("Engine() == nil")
	}
}

func TestAssemblyPluginsIncludesNativesAndAllDexWhenAllowAll(t *testing.T) {
	e := newAssemblyEngine(t)
	asm, err := fluxplaneplugin.New(fluxplaneplugin.Config{
		Engine:        e,
		NativePlugins: []pluginhost.Plugin{fakeNative{name: "workspace"}, fakeNative{name: "identity"}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plugs := asm.Plugins()
	if len(plugs) < 3 {
		t.Fatalf("len(plugs) = %d, want at least 2 natives + 1 dex marketplace plugin", len(plugs))
	}
	if plugs[0].Manifest().Name != "workspace" || plugs[1].Manifest().Name != "identity" {
		t.Fatalf("natives not first in output: %v", pluginNames(plugs))
	}
}

func TestAssemblyPluginsFiltersByIgnoreList(t *testing.T) {
	e := newAssemblyEngine(t)
	asm, err := fluxplaneplugin.New(fluxplaneplugin.Config{
		Engine:            e,
		IgnoredDexPlugins: []string{"websearch"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plugs := asm.Plugins()
	for _, p := range plugs {
		if p.Manifest().Name == "websearch" {
			t.Fatalf("websearch should be filtered out by IgnoredDexPlugins; got %v", pluginNames(plugs))
		}
	}
}

func TestAssemblyBundlesFiltersByIgnoreList(t *testing.T) {
	e := newAssemblyEngine(t)
	asm, err := fluxplaneplugin.New(fluxplaneplugin.Config{
		Engine:            e,
		IgnoredDexPlugins: []string{"websearch"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bundles, err := asm.Bundles(context.Background())
	if err != nil {
		t.Fatalf("Bundles: %v", err)
	}
	for _, b := range bundles {
		if b.Source.ID == "dex-intent" {
			continue
		}
		if len(b.Plugins) == 0 {
			continue
		}
		if b.Plugins[0].Name == "websearch" {
			t.Fatalf("ignored plugin %q leaked through Bundles", b.Plugins[0].Name)
		}
	}
}

func TestAssemblyBundlesFiltersIntentReactions(t *testing.T) {
	e := newAssemblyEngine(t)
	asm, err := fluxplaneplugin.New(fluxplaneplugin.Config{
		Engine:            e,
		IgnoredDexPlugins: []string{"websearch"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bundles, err := asm.Bundles(context.Background())
	if err != nil {
		t.Fatalf("Bundles: %v", err)
	}
	for _, b := range bundles {
		if b.Source.ID != "dex-intent" {
			continue
		}
		for _, rule := range b.Reactions {
			if rule.When.Assertion != fluxplaneplugin.AssertionKindDexIntent {
				t.Errorf("rule %q assertion = %q", rule.Name, rule.When.Assertion)
			}
			target := rule.When.Target
			if target == "websearch" || startsWith(target, "websearch_") {
				t.Errorf("rule %q targets ignored activation set %q", rule.Name, target)
			}
			if len(rule.Actions) != 1 || rule.Actions[0].Kind != corereaction.ActionEnableActivationSet {
				t.Errorf("rule %q action shape = %#v", rule.Name, rule.Actions)
			}
		}
	}
}

func TestAssemblyRegisterPushesToHost(t *testing.T) {
	e := newAssemblyEngine(t)
	asm, err := fluxplaneplugin.New(fluxplaneplugin.Config{
		Engine:        e,
		NativePlugins: []pluginhost.Plugin{fakeNative{name: "workspace"}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	host, err := pluginhost.New()
	if err != nil {
		t.Fatalf("pluginhost.New: %v", err)
	}
	if err := asm.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res, err := host.Resolve(context.Background(), resource.PluginRef{Name: "websearch"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Operations) == 0 {
		t.Fatalf("expected websearch executable operations")
	}
}

func TestAssemblyIgnoreGlobPatternFiltersFamily(t *testing.T) {
	e := newAssemblyEngine(t)
	asm, err := fluxplaneplugin.New(fluxplaneplugin.Config{
		Engine:            e,
		IgnoredDexPlugins: []string{"web*"},
		NativePlugins:     []pluginhost.Plugin{fakeNative{name: "workspace"}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plugs := asm.Plugins()
	for _, p := range plugs {
		name := p.Manifest().Name
		if name == "workspace" {
			continue
		}
		if startsWith(name, "web") {
			t.Fatalf("glob web* should filter %q, got %v", name, pluginNames(plugs))
		}
	}
}

func TestAssemblyRegisterRejectsNilHost(t *testing.T) {
	asm, err := fluxplaneplugin.New(fluxplaneplugin.Config{Engine: newAssemblyEngine(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := asm.Register(nil); err == nil {
		t.Fatalf("expected error for nil host")
	}
}

func pluginNames(plugs []pluginhost.Plugin) []string {
	out := make([]string, 0, len(plugs))
	for _, p := range plugs {
		out = append(out, p.Manifest().Name)
	}
	return out
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
