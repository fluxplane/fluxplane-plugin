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

## Host capabilities

Plugins never perform direct IO. Every side effect is requested from the host
over the framed protocol, which keeps plugins sandboxable and lets one generic
host implementation serve every product (the standalone CLI, coder, slack-bot,
any fp-core app). The capability set is intentionally generic — transport and OS
primitives, never app-specific providers:

- `http.do` — HTTP requests (most REST plugins use `pluginbinding.HostHTTPClient`).
- `conn.dial` / `conn.read` / `conn.write` / `conn.close` — raw byte streams over
  `tcp` or a `unix` socket, with optional host-terminated TLS. A plugin gets a
  `net.Conn` via `pluginbinding.HostDialer(host)` and hands it to any library
  that accepts a custom dialer (`database/sql` drivers, Kubernetes `client-go`,
  the Docker SDK, an Asterisk AMI client). The protocol/wire logic lives in the
  plugin; only the socket crosses the host boundary, where it can be audited and
  policy-gated.
- `process.run` / `process.start` / `process.stop` / `process.list` — child
  processes. Long-lived background processes (e.g. port-forwards) persist as
  records and are also visible from the CLI via `fluxplane-plugin process
  list|logs|stop`.
- `blob.read` / `blob.write` / `blob.info`, `env.lookup`, `secret`, `endpoint`.

The framed host channel multiplexes responses by request id, so a plugin may
issue concurrent host calls — required when a library (e.g. `net/http`) drives a
host-dialed connection from separate read and write goroutines.

Endpoints resolve from durable state (auth-wired or registered), never from the
environment at call time, so where a request is sent is deterministic. When an
operation omits `endpoint_ref`, the local backend injects the instance's wired
endpoint, or the single registered endpoint for the plugin's product.

## Shell completion

The CLI ships cobra's completion command with dynamic plugin, operation, and
process-ID arguments (served from local state and the operations cache, never
by spawning plugins). For fish:

```fish
fluxplane-plugin completion fish > ~/.config/fish/completions/fluxplane-plugin.fish
```

(bash: `source <(fluxplane-plugin completion bash)`; zsh: see
`fluxplane-plugin completion zsh --help`.)

## Version pinning and rollback

`fluxplane-plugin pin jira@v0.3.2` reinstalls that exact version and holds it —
`upgrade` and `install --all` skip pinned plugins (reported as
`"skipped": true, "reason": "pinned to v0.3.2"`). `unpin jira` releases the
hold, `rollback jira` swaps back to the previously installed version (rolling
back twice round-trips). Note `dev sync` intentionally still rebuilds pinned
plugins from the workspace — the dev loop trumps the pin.

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
