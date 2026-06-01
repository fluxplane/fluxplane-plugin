# fluxplane-plugin

Shared Fluxplane plugin SDK/protocol staging repository.

This repository was seeded from `fluxplane-dex/fluxplaneplugin` with a literal copy so the adapter can be extracted incrementally without losing working behavior. The module path is now:

```text
github.com/fluxplane/fluxplane-plugin
```

## Current contents

- `package fluxplaneplugin`: copied dex adapter package that bridges dex-managed plugins into Fluxplane host/plugin surfaces.
- `protocol/`: copied dex stdio/framed plugin protocol package.

## Current state

This is intentionally a staging step, not the final lean SDK shape yet. The package still depends on:

- `github.com/fluxplane/fluxplane-core`
- `github.com/fluxplane/fluxplane-dex`
- shared datasource/endpoint/system modules

The next refactor steps should split the copied adapter into smaller packages and move contracts out of core/dex where needed.

## Target direction

Desired final shape:

```text
fluxplane-plugin/
  protocol/       # stdio/framed protocol types and serve/client helpers
  manifest/       # plugin manifest contracts
  host/           # host capability interfaces: http, env, secret, endpoint, blob, provider
  operation/      # operation specs and call/result contracts
  datasource/     # datasource specs and call/result contracts
  context/        # context provider contracts
  binding/direct/ # direct in-process binding
  binding/stdio/  # external stdio binding
  testkit/        # fake host, manifest lint, protocol parity helpers
```

## Immediate TODO

1. Move dex/core plugin binding DTOs that are pure contracts into this module.
2. Move context-provider contracts currently tied to `fluxplane-core` into a standalone module/package so plugins can expose context without depending on core.
3. Replace direct imports of `fluxplane-dex` runtime types with protocol/host abstractions.
4. Replace direct imports of `fluxplane-core` pluginhost/resource/reaction/evidence types with smaller shared contracts or adapter-only packages.
5. Keep the current copied adapter working while the lean SDK packages are introduced.

## Validation

Run tests outside the parent workspace until the root `go.work` includes this module:

```sh
GOWORK=off go test ./...
```
