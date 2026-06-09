# Changelog

All notable changes to `fluxplane-plugin` are documented here.

## v0.3.0

### Added
- **`conn.dial` host capability** — a generic byte-stream primitive
  (`conn.dial` / `conn.read` / `conn.write` / `conn.close`) for `tcp` and `unix`
  sockets, with optional host-terminated TLS. Plugins obtain a `net.Conn` via
  `pluginbinding.HostDialer(host)` (or `DialHostConn` for endpoint-ref/TLS dials)
  and route it through any library that accepts a custom dialer, so the plugin
  speaks its own wire protocol while performing no direct network IO. The
  capability is exposed as the optional `host.ConnDialer` interface, leaving the
  base `host.Client` (and existing host/test doubles) untouched.
- Host DTOs `ConnDialRequest`/`ConnDialResponse`/`ConnReadRequest`/
  `ConnReadResponse`/`ConnWriteRequest`/`ConnWriteResponse`/`ConnCloseRequest`/
  `ConnCloseResponse` and matching `pluginbinding` aliases.
- The local management backend implements the conn capability with a
  per-invocation connection registry that is closed when the invocation ends.

### Changed
- **Concurrent host calls.** The framed host channel now multiplexes responses
  by request id (plugin side) and dispatches each capability request on its own
  goroutine with serialized writes (host side). This is required when a plugin
  drives a host-dialed connection from separate read/write goroutines (e.g.
  `net/http`). The change is backward compatible with existing plugin binaries.
- **Deterministic endpoint resolution.** `InvokeOperation`/`BatchOperations`
  inject a default `endpoint_ref` when the caller omits one, resolved purely from
  durable state — the instance's wired endpoint, else the single registered
  endpoint for the plugin's product. Selection never reads the environment at
  call time; ambiguous cases still require an explicit `endpoint_ref`.

## v0.2.0

- Auto-fetch the marketplace catalog from remote on clean installs; `install --all`
  and `upgrade` lifecycle.

## v0.1.0

- Initial standalone plugin SDK and protocol module.
