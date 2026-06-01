package fluxplaneplugin

import (
	"context"
	"fmt"
	"sync"

	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"

	dex "github.com/fluxplane/fluxplane-dex"
)

const maxConcurrentBundleBuilds = 8

type bundleJob struct {
	index int
	name  string
}

// Bundles emits one resource.ContributionBundle per dex marketplace plugin
// plus a final "intent" bundle that carries the reaction rules driving
// keyword-based auto-activation. Consumers splice the slice into their
// product bundle path (e.g. coder's product.Bundle()) and the bundles flow
// through the standard surface/activation discovery alongside native plugins.
//
// Each per-plugin bundle carries the plugin's operation specs, operation sets
// grouped by second dotted segment (e.g. "gitlab_mr" for every
// "gitlab.mr.*"), datasource specs, datasource entity declarations, the
// plugin's resource.PluginRef, and a stable resource.SourceRef tagged with
// the "dex" ecosystem so downstream catalogs can distinguish dex-sourced
// contributions.
//
// The trailing intent bundle carries one reaction rule per activation-set
// name across all dex plugins so that the AssertionDeriver returned by the
// adapter (see Register / AssertionDerivers) can auto-enable surfaces from
// channel-message tokens. Without the reactions the deriver would emit
// assertions that nothing listens for.
//
// Plugins whose manifest can't be fetched — typically because the binary
// isn't installed in the active DEX_HOME — yield a stub bundle that still
// carries the PluginRef plus a warning Diagnostic. This keeps every
// marketplace entry discoverable for activation surfaces independent of
// install/auth state; the agent runs `dex plugin install <name>` (and any
// required `dex auth connect`) on demand before the ops will actually run.
//
// Bundles does NOT register executable bindings with a pluginhost. Pair it
// with Register so static contribution discovery and executable resolution
// stay on parallel rails.
func Bundles(ctx context.Context, engine *dex.Engine) ([]resource.ContributionBundle, error) {
	if engine == nil {
		return nil, fmt.Errorf("fluxplaneplugin: engine is nil")
	}
	entries := engine.Marketplace().Plugins()
	out := make([]resource.ContributionBundle, len(entries), len(entries)+1)
	if len(entries) > 0 {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		workerCount := len(entries)
		if workerCount > maxConcurrentBundleBuilds {
			workerCount = maxConcurrentBundleBuilds
		}

		jobs := make(chan bundleJob)
		var wg sync.WaitGroup
		var errOnce sync.Once
		var firstErr error
		setErr := func(err error) {
			if err == nil {
				return
			}
			errOnce.Do(func() {
				firstErr = err
				cancel()
			})
		}

		wg.Add(workerCount)
		for range workerCount {
			go func() {
				defer wg.Done()
				for {
					select {
					case <-ctx.Done():
						setErr(ctx.Err())
						return
					case job, ok := <-jobs:
						if !ok {
							return
						}
						out[job.index] = bundleFor(ctx, engine, job.name)
						if err := ctx.Err(); err != nil {
							setErr(err)
							return
						}
					}
				}
			}()
		}

	sendJobs:
		for i, entry := range entries {
			select {
			case <-ctx.Done():
				setErr(ctx.Err())
				break sendJobs
			case jobs <- bundleJob{index: i, name: entry.Name}:
			}
		}
		close(jobs)
		wg.Wait()
		if firstErr != nil {
			return nil, firstErr
		}
	}
	if intentBundle, ok := buildIntentBundle(ctx, engine); ok {
		out = append(out, intentBundle)
	}
	return out, nil
}

// buildIntentBundle constructs a bundle that wires the dex intent reaction
// rules. The bundle has no PluginRef of its own — it's a pure resource
// contribution that gives meaning to the assertions the intent deriver
// emits.
func buildIntentBundle(ctx context.Context, engine *dex.Engine) (resource.ContributionBundle, bool) {
	idx := buildIntentIndex(ctx, engine)
	sets := idx.activationSets()
	if len(sets) == 0 {
		return resource.ContributionBundle{}, false
	}
	return resource.ContributionBundle{
		Source: resource.SourceRef{
			ID:        intentBundleSourceID,
			Ecosystem: "dex",
			Scope:     resource.ScopeEmbedded,
			Location:  "dex/intent",
			Trust: policy.Trust{
				Kind:  policy.TrustSource,
				Level: policy.TrustVerified,
			},
		},
		Reactions: reactionsForActivationSets(sets),
	}, true
}

// Register adds one pluginhost.Plugin per dex marketplace plugin to host so
// the host can resolve executable bindings for dex operations and
// datasources. Pair with Bundles for the static contribution side.
//
// Returns an error if any plugin's name conflicts with an existing
// registration (pluginhost.Register is strict on duplicates) so wiring bugs
// fail loudly at startup rather than silently dropping a plugin.
func Register(engine *dex.Engine, host *pluginhost.Host) error {
	if engine == nil {
		return fmt.Errorf("fluxplaneplugin: engine is nil")
	}
	if host == nil {
		return fmt.Errorf("fluxplaneplugin: host is nil")
	}
	plugins, err := All(engine)
	if err != nil {
		return err
	}
	for _, p := range plugins {
		if err := host.Register(p); err != nil {
			return fmt.Errorf("fluxplaneplugin: register %q: %w", p.Manifest().Name, err)
		}
	}
	return nil
}

func bundleFor(ctx context.Context, engine *dex.Engine, name string) resource.ContributionBundle {
	plug, err := Wrap(engine, name)
	if err != nil {
		return stubBundle(name, err.Error())
	}
	bundle, err := plug.Contributions(ctx, pluginhost.Context{Ref: resource.PluginRef{Name: name}})
	if err != nil {
		return stubBundle(name, fmt.Sprintf("manifest unavailable: %v", err))
	}
	if !hasPluginRef(bundle.Plugins, name) {
		bundle.Plugins = append(bundle.Plugins, resource.PluginRef{Name: name})
	}
	if bundle.Source.ID == "" {
		bundle.Source = dexSource(name)
	}
	return bundle
}

func stubBundle(name, reason string) resource.ContributionBundle {
	source := dexSource(name)
	return resource.ContributionBundle{
		Source:  source,
		Plugins: []resource.PluginRef{{Name: name}},
		Diagnostics: []resource.Diagnostic{{
			Severity: resource.SeverityWarning,
			Source:   source,
			Message:  fmt.Sprintf("dex plugin %q: %s", name, reason),
		}},
	}
}

func dexSource(name string) resource.SourceRef {
	return resource.SourceRef{
		ID:        "dex-plugin:" + name,
		Ecosystem: "dex",
		Scope:     resource.ScopeEmbedded,
		Location:  "dex/" + name,
		Trust: policy.Trust{
			Kind:  policy.TrustSource,
			Level: policy.TrustVerified,
		},
	}
}

func hasPluginRef(refs []resource.PluginRef, name string) bool {
	for _, ref := range refs {
		if ref.Name == name {
			return true
		}
	}
	return false
}
