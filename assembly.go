package fluxplaneplugin

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"

	dex "github.com/fluxplane/fluxplane-dex"
	dexruntime "github.com/fluxplane/fluxplane-dex/runtime"
)

// Config configures an Assembly: which dex plugins to expose, optional
// native plugins to bundle alongside them, and an engine override.
type Config struct {
	// Engine, if set, replaces the default lazy engine. When nil an engine
	// is constructed via dex.New(dex.Config{}), which uses $DEX_HOME (or
	// the platform default) for plugin install state, secrets, and
	// marketplace data.
	Engine *dex.Engine

	// System backs dex host capabilities with the caller's fluxplane-core
	// runtime boundaries. When set and Engine is nil, dex HTTP, blob, and
	// environment capability calls go through System instead of dex's local
	// fallback host.
	System fpsystem.System

	// Workspace backs dex blob capability calls with the caller's selected runtime workspace.
	Workspace runtimeworkspace.Workspace

	// Capabilities overrides the capability host passed to dex.New. It is
	// useful when a caller wants to supply a custom host while still using
	// the default dex engine construction.
	Capabilities dexruntime.CapabilityHost

	// HostProviders are optional provider implementations exposed through
	// SystemCapabilityHost for provider calls not handled by dex runtime
	// itself.
	HostProviders map[string]dexruntime.HostProvider

	// IgnoredDexPlugins is a list of glob patterns (matched with
	// filepath.Match against the plugin name) that should NOT be exposed
	// by the Assembly. The default ("empty list") loads every dex plugin
	// the engine knows about. Use this to drop first-level surfaces the
	// consumer doesn't want to attach by default — e.g. ["gitlab","jira"]
	// or ["prom*"]. Surfaces still reachable through agent activation are
	// unaffected by this list; it only filters what surfaces appear on
	// the default plugin list.
	IgnoredDexPlugins []string

	// NativePlugins are pluginhost.Plugin instances the consumer wants
	// registered alongside dex plugins (e.g. workspace, identity, a slack
	// channel adapter, OpenAPI). They appear first in Plugins() output so
	// duplicate-name collisions surface as errors at host registration
	// rather than being silently shadowed.
	NativePlugins []pluginhost.Plugin
}

// Assembly is the ergonomic facade over Wrap/All/Bundles/Register. It
// gathers a dex engine, an optional allowlist of dex plugin names, and a
// caller-supplied set of native plugins, and exposes the four shapes a
// fluxplane-core launch path needs:
//
//   - Plugins() — the slice for launch.RuntimeOptions.PluginFactory.
//   - Bundles(ctx) — the slice to splice into the consumer's bundle list.
//   - Register(host) — direct registration into a pluginhost.Host.
//   - Engine() — handle for advanced/escape-hatch callers.
//
// Consumers like the fluxplane-apps/slack-bot can collapse the dex-side
// boilerplate into a single Assembly construction.
type Assembly struct {
	engine         *dex.Engine
	nativePlugins  []pluginhost.Plugin
	ignorePatterns []string
}

// New constructs an Assembly. Returns an error only if cfg.Engine is nil
// and the default engine constructor fails, or if an ignore pattern is
// not a valid filepath.Match pattern.
func New(cfg Config) (*Assembly, error) {
	engine := cfg.Engine
	if engine == nil {
		capabilities := cfg.Capabilities
		if capabilities == nil && cfg.System != nil {
			capabilities = NewSystemCapabilityHost(cfg.System, cfg.Workspace, cfg.HostProviders)
		}
		e, err := dex.New(dex.Config{Capabilities: capabilities})
		if err != nil {
			return nil, fmt.Errorf("fluxplaneplugin.New: dex.New: %w", err)
		}
		engine = e
	}
	patterns := make([]string, 0, len(cfg.IgnoredDexPlugins))
	for _, raw := range cfg.IgnoredDexPlugins {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if _, err := filepath.Match(p, ""); err != nil {
			return nil, fmt.Errorf("fluxplaneplugin.New: invalid ignore pattern %q: %w", p, err)
		}
		patterns = append(patterns, p)
	}
	return &Assembly{
		engine:         engine,
		nativePlugins:  append([]pluginhost.Plugin(nil), cfg.NativePlugins...),
		ignorePatterns: patterns,
	}, nil
}

// Engine returns the underlying dex engine. Useful when a consumer needs
// to call engine.Auth(), engine.Operations(), engine.Plugins(), etc.
// outside the contribution path.
func (a *Assembly) Engine() *dex.Engine {
	return a.engine
}

// Plugins returns the native plugins (in the order supplied to Config)
// followed by every allowed dex marketplace plugin wrapped as a
// pluginhost.Plugin. Suitable as the return value of
// launch.RuntimeOptions.PluginFactory.
func (a *Assembly) Plugins() []pluginhost.Plugin {
	out := append([]pluginhost.Plugin(nil), a.nativePlugins...)
	dexPlugins, err := All(a.engine)
	if err != nil {
		return out
	}
	for _, p := range dexPlugins {
		if a.allows(p.Manifest().Name) {
			out = append(out, p)
		}
	}
	return out
}

// Bundles returns dex contribution bundles filtered to the allowed
// plugins. The trailing dex-intent reaction bundle is rewritten to drop
// reactions whose activation-set target belongs to a disallowed plugin —
// preventing auto-activation of surfaces the consumer didn't opt into.
func (a *Assembly) Bundles(ctx context.Context) ([]resource.ContributionBundle, error) {
	bundles, err := Bundles(ctx, a.engine)
	if err != nil {
		return nil, err
	}
	out := make([]resource.ContributionBundle, 0, len(bundles))
	for _, b := range bundles {
		if b.Source.ID == intentBundleSourceID {
			filtered := a.filterIntentBundle(b)
			if len(filtered.Reactions) > 0 {
				out = append(out, filtered)
			}
			continue
		}
		if len(b.Plugins) == 0 {
			// Defensive: keep stub bundles without a PluginRef.
			out = append(out, b)
			continue
		}
		if a.allows(b.Plugins[0].Name) {
			out = append(out, b)
		}
	}
	return out, nil
}

// Register pushes the Assembly's plugin list into host. Equivalent to
// calling host.Register on each entry of Plugins(); on the first
// duplicate-name collision it returns the underlying pluginhost error.
func (a *Assembly) Register(host *pluginhost.Host) error {
	if host == nil {
		return fmt.Errorf("fluxplaneplugin.Assembly.Register: host is nil")
	}
	for _, p := range a.Plugins() {
		if err := host.Register(p); err != nil {
			return fmt.Errorf("fluxplaneplugin.Assembly.Register %q: %w", p.Manifest().Name, err)
		}
	}
	return nil
}

// allows reports whether a dex plugin name should be loaded — i.e. it
// matches none of the configured ignore patterns.
func (a *Assembly) allows(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, pat := range a.ignorePatterns {
		if ok, _ := filepath.Match(pat, name); ok {
			return false
		}
	}
	return true
}

// filterIntentBundle drops reactions whose activation-set target maps to
// an ignored plugin. Activation-set names emitted by Bundles are either
// the plugin name (aggregate set) or "<plugin>_<entity>" (per-entity
// set). We resolve the plugin name by checking direct match first, then
// by the segment before the first underscore.
func (a *Assembly) filterIntentBundle(b resource.ContributionBundle) resource.ContributionBundle {
	out := b
	out.Reactions = nil
	for _, rule := range b.Reactions {
		if a.allowsActivationSetTarget(rule.When.Target) {
			out.Reactions = append(out.Reactions, rule)
		}
	}
	return out
}

func (a *Assembly) allowsActivationSetTarget(target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if a.allows(target) {
		return true
	}
	if i := strings.IndexByte(target, '_'); i > 0 {
		return a.allows(target[:i])
	}
	return false
}

// intentBundleSourceID is the SourceRef.ID used by buildIntentBundle for
// the trailing reaction-rules bundle. Kept as a constant so filtering
// stays in sync with the producer.
const intentBundleSourceID = "dex-intent"
