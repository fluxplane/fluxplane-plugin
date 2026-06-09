# Changelog

All notable changes to `fluxplane-plugin` are documented here.

## Unreleased

### Added
- **`fluxplane-plugin dev sync [plugin...]`** — rebuilds installed plugins from
  their workspace `local_path`, writing each binary back to its installed path.
  Unlike `install`, it never consults the marketplace catalog and prefers the
  current workspace marketplace entry's `local_path`, so a stale cached
  `marketplace.json` can no longer shadow a workspace build (the recurring
  dev-loop footgun). Backed by the optional `management.LocalSyncer` capability.
- **`fluxplane-plugin doctor [plugin...]`** — diagnoses each installed plugin's
  binary provenance from `go version -m`, flagging dev/dirty builds (local
  `go build`, `(devel)`, or a modified tree at build time) that silently drift
  from source. `--latest` resolves each module's newest published version and
  flags version drift; `--check-auth` runs each plugin's live `auth.test`.
- **`fluxplane-plugin selftest [plugin...]`** — exercises each plugin's
  read-safe probes (its `auth.test` plus read-only operations requiring no input
  beyond `endpoint_ref`) and reports green/red per plugin, for use as a
  post-upgrade or release gate. `--auth-only` limits it to `auth.test`.
- **`pluginbinding.VerifyAppliedWarning` / `FieldCheck` / `UnappliedFields`** — a
  reusable write-verification convention. A plugin re-reads an entity after a
  write and compares requested vs applied field values; any field the backend
  accepted (2xx) but silently dropped is named in a warning, so `"ok": true`
  never masks a no-op write.
- **`pluginbinding.FieldError` + `protocol.Error.Fields`/`Details`** — a
  structured error envelope carrying field-level detail (field → reason) and
  extra messages, so callers identify the offending input programmatically
  instead of parsing the message string.

### Changed
- The skill invocation example generator now uses a JSON Schema `examples` entry
  on an operation's input schema when present, which is the only way to produce
  a runnable example for operations with one-of input requirements.

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
