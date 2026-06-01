// Package fluxplaneplugin adapts dex marketplace plugins so they flow through
// fluxplane-core's surface/activation discovery on parallel rails from native
// plugins.
//
// Two entry points, two rails:
//
//   - Bundles(ctx, engine) returns one resource.ContributionBundle per dex
//     marketplace plugin (operation specs, operation sets like "gitlab_mr"
//     grouping every "gitlab.mr.*" op, datasource specs + entities, a
//     PluginRef, and a stable dex-tagged SourceRef). Splice these into the
//     consumer's product bundle path — e.g. coder's product.Bundle() — so
//     activation sets, surface_prepare, and the resource catalog all see dex
//     contributions exactly like native ones.
//
//   - Register(engine, host) pushes one pluginhost.Plugin per dex plugin
//     into a pluginhost.Host so executable operation and datasource
//     bindings resolve when the surface actually fires.
//
// Bundles supplies discoverability for every marketplace entry independent
// of dex install/auth state. Plugins whose manifest can't be fetched
// (typically: binary not installed) yield a stub bundle carrying just the
// PluginRef + a warning Diagnostic — they remain visible to activation
// flows, and the agent runs `dex plugin install <name>` / `dex auth connect`
// on demand before the operations actually run.
//
// Higher-level facade for consumers that want minimal boilerplate (e.g.
// fluxplane-apps/slack-bot, coder, custom apps):
//
//   - Assembly via New(Config{...}) — bundles native + dex plugins with an
//     optional EnabledDexPlugins allowlist, then exposes Plugins(),
//     Bundles(ctx), Register(host), and Engine() in the shapes
//     fluxplane-core's launch path expects.
//
// Lower-level building blocks are also exported:
//
//   - Wrap(engine, name)  — one pluginhost.Plugin for a single dex plugin.
//   - All(engine)         — pluginhost.Plugin slice for every marketplace
//     plugin.
//
// Scope: operations + operation sets + datasource specs + datasource
// providers (with Searcher/Getter). Auth methods, identity resolvers, and
// host-system bridging are deliberately not bridged today — dex owns its
// own secrets/state under DEX_HOME, and dex plugins are subprocesses that
// hold their own HTTP/filesystem boundaries.
package fluxplaneplugin
