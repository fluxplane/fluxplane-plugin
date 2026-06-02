# fluxplane-plugin

Standalone Fluxplane plugin SDK and protocol module.

The module path is:

```text
github.com/fluxplane/fluxplane-plugin
```

## Current contents

- `protocol/`: stdio/framed plugin protocol package.
- `host/`: reusable host capability DTOs and client helpers.
- `datasource/`: SDK-facing aliases/helpers over `github.com/fluxplane/fluxplane-datasource`.
- `management/`: plugin management backend contracts.
- `management/local/`: local filesystem management backend.
- `cli/`: reusable plugin management command tree.

This SDK module is core-free and dex-free. Agent-runtime contribution bridging
belongs in `fluxplane-core`; concrete plugin catalogs and binaries belong in
`fluxplane-plugins`.

## Target direction

```text
fluxplane-plugin/
  protocol/       # stdio/framed protocol types and serve/client helpers
  manifest/       # plugin manifest helpers that compose dedicated modules
  host/           # host capability interfaces: http, env, secret, endpoint, blob, provider
  datasource/     # datasource specs and call/result contracts via fluxplane-datasource
  context/        # context provider contracts if SDK-only; otherwise move to fluxplane-context
  schema/         # schema helpers if SDK-only
  testkit/        # fake host, manifest lint, protocol parity helpers
```

Reusable domain concepts should live in dedicated modules first (`fluxplane-datasource`, `fluxplane-operation`, `fluxplane-endpoint`, `fluxplane-secret`, etc.) and be re-exported by SDK packages only where that improves plugin author ergonomics.

The default marketplace-aware `fluxplane-plugin` binary lives in `github.com/fluxplane/fluxplane-plugins/cmd/fluxplane-plugin`; this SDK module remains registry-agnostic.

## Validation

```sh
GOWORK=off go test ./...
```
