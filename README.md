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
- `cli/` and `cmd/fluxplane-plugin`: reusable plugin management CLI.

Runtime-specific adapters are intentionally not part of this module. The dex-to-core adapter remains in:

```text
github.com/fluxplane/fluxplane-dex/fluxplaneplugin
```

This keeps `fluxplane-plugin` focused on reusable contracts and SDK helpers rather than product/runtime glue.

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

## Validation

```sh
GOWORK=off go test ./...
```
