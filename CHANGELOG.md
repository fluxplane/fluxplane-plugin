# Changelog

All notable changes to `fluxplane-plugin` are documented here.

## v0.14.0

Field-report fixes (fluxplane-plugins#8).

### Added
- **`blob put PLUGIN FILE`** stores a local file in a plugin's blob store and
  prints its `blob_ref` — the sanctioned way to feed large payloads (file
  uploads) to operations without inlining base64 `content_bytes`, which can
  exceed the OS argv limit.
- **`--field` array projection**: `*` maps the remaining path over array
  elements (`streams.*.packets`, `items.*.name`), so array-shaped results no
  longer require piping `--result-only` into an external JSON tool.
- **`operation describe` prints `field_path_examples`** — ready-to-paste
  `--field` paths derived from the output schema (nested objects as dotted
  paths, arrays of objects as `*` projections), ending the result-nesting
  guessing game.

### Changed
- The skill cheat sheet documents `--input-file payload.json` / `--input -`
  for payloads that exceed the argv limit, and the `blob put` upload flow.

## v0.13.1

### Fixed
- The CLI host now honors `HTTPAuthRequest.UsernamePurpose`/`PasswordPurpose`:
  HTTP basic auth is composed from stored secrets (`Authorization: Basic …`)
  when either purpose has material, and skipped entirely when neither is
  stored — plugins can declare optional basic auth (loki) without breaking
  unauthenticated endpoints. The DTO fields existed but were silently
  ignored (fluxplane-plugins#5).

## v0.13.0

Field-report-3 fixes (fluxplane-plugins#6, fluxplane-plugins#7).

### Changed
- **Unknown input fields are rejected uniformly** (#6): the unknown-key check
  now also runs for operations that declare schema examples — examples signal
  one-of *required* shapes, but a typo'd field name is never valid under
  `additionalProperties: false`. Previously example-decorated operations
  (most gitlab ops) silently ignored unknown fields while others rejected
  them.
- **Endpoint store self-heals "listed but not stored" records** (#7): a
  record stored under a stale state key (older ID normalization or an
  external writer) now resolves by its own normalized ID and is rekeyed in
  place; the invoke-time error gained remediation hints (`endpoint list` /
  `endpoint save` / `endpoint import`).
- **`endpoint list` returns one array** (#6): `endpoints` now carries the
  full stored records (ref + `created_at`/`updated_at`/`last_health`); the
  duplicate bare-ref array is gone.
- **`list` uses the same `{"plugins": [...]}` envelope as `status`** (#6) —
  jq written against one works on the other.
- **`install` of an already-installed plugin is a no-op success** (#6):
  exits 0 with `"already installed, nothing to do"` like a package manager;
  `--force` still reinstalls, `update` upgrades.
- **`operation search` emits JSON by default** (#6), matching every other
  subcommand and the skill page's "Output is JSON" claim; `--plain` renders
  the old human-readable lines.
- **`lookup`/`lookup-all` separate setup noise from results** (#6):
  unconfigured plugins are reported once as `skipped_unconfigured` names
  instead of per-plugin errors, and per-plugin "index not built" hints are
  muted when another plugin already matched.
- **Skill pages embed their state source** (#6): the header shows the state
  file path and installed-plugin count, so a page generated against a
  divergent state dir ("fresh timestamp, stale plugin list") is
  self-diagnosing.

## v0.12.0

Field-report-2 fixes (fluxplane-plugins#5).

### Changed
- **`operation search` uses OR semantics with coverage ranking** — any term
  may match (name hits and full-coverage matches rank higher,
  `matched_terms` shows partial hits), so "thread replies history" finds
  `slack.thread` instead of returning nothing.
- **Blob store preserves filenames.** Auto-generated blob refs carry the
  sanitized requested filename, and the stored path keeps its real extension
  (`blob-<id>-screenshot.png` instead of `blob-<id>.bin`). Explicit refs are
  unchanged.
- **`--field` misses list the available keys** at the deepest resolvable
  point of the path (`"missing": [...], "available": [...]`) — no more
  guessing whether the payload nests under `record`.
- **The "index not built" hint only appears on empty results**; lookups and
  searches that returned matches no longer repeat it.

## v0.11.0

### Changed
- **`endpoint discover` fans out** (fluxplane-plugins#4 loki/1): when the
  named plugin returns no candidates (product plugins usually can't discover
  their own endpoints), the command queries every other installed plugin for
  the same product — `endpoint discover loki` now surfaces the loki services
  the kubernetes plugin finds, under `fanout`, with a hint showing how to
  register one.

## v0.10.0

Field-report fixes from a real incident-debugging session
(fluxplane-plugins#4).

### Changed
- **`auth status` is informative.** It now reports `connected` (any recorded
  connect/test), `ready` (some method has all required fields configured),
  and per-method field status: required/optional, secret, configured (from
  the persisted secret store or recorded metadata), and the missing required
  fields. All-optional methods (e.g. loki's `tenant_id`) read as ready.
- **`auth connect auto` no-ops are explicit successes.** When no declared
  env hints are set and nothing required is missing, the result carries a
  "nothing to connect" message (exit 0); missing required fields are named.
  Env ingestion now falls back to the stored manifest when the plugin
  runtime cannot be invoked.
- **Skill pages no longer claim "needs auth" for all-optional methods** —
  they read "auth optional" unless a required field is actually missing.
- **Endpoint credentials are redacted in display output.** `endpoint
  list`/`get`/`save` and `describe` mask URL userinfo passwords as `xxxxx`
  (stored values untouched; the host resolves real URLs at invoke time).
- **Generated invocation examples stop emitting `endpoint_ref`-only stubs.**
  Without a schema example, samples are built from required fields, then
  representative fields (`ref`, `id`, `query`, …); `endpoint_ref` is never
  auto-injected (the backend resolves the wired endpoint when omitted).
- **Lookup fan-out marks unconfigured plugins as `skipped`** (with a
  reason) instead of erroring mid-results when a plugin needs an endpoint
  ref or auth to participate.
- **URL queries skip the host-index token fallback.** A hostname fragment
  can no longer make an unrelated record outrank the plugin that owns the
  URL; direct URL-field hits still match.

### Added
- **`operation list --names`** — compact summary (name, first sentence,
  read_only) without dumping every input/output schema.
- **`version` command and `--version`** — module version, VCS revision,
  go/os/arch from the binary's build info, for bug reports.

## v0.9.0

CLI lifecycle polish: version pinning, process visibility, one-stop describe,
shell completion.

### Added
- **Version pinning + rollback.** Installed plugins now record
  `installed_version` (from the binary's module stamp) and
  `previous_version`. `pin PLUGIN[@VERSION]` holds a plugin (reinstalling at
  the requested version first when it differs); `upgrade` and `install --all`
  skip pinned plugins with `"reason": "pinned to vX"`; `unpin` releases the
  hold; `rollback PLUGIN` swaps back to the previous version (twice
  round-trips; refuses while pinned). Batch install results now echo the
  resolved `version`. Backend capability: optional `management.VersionManager`.
- **`install plugin@version` actually installs that version.** The requested
  version is threaded into the `go install` spec (previously it only changed
  the state key, silently installing `@latest`). Marketplace installs are now
  stored under the bare plugin name; legacy `name@version` records migrate
  automatically.
- **`process` command group.** `process list [--group|--plugin|--label]`
  lists host-managed background processes (e.g. kubernetes port-forwards)
  across plugins with PID liveness; `process logs ID [-n N] [--follow]` tails
  the process log; `process stop ID` signals the process group and removes
  the record. Process records now carry the owning plugin/instance
  (`host.ProcessRecord.Plugin/Instance`, `ProcessListRequest.Plugin` filter).
  Backend capability: optional `management.ProcessManager`.
- **`describe PLUGIN`** aggregates status, versions (installed/pinned/
  previous/manifest), binary provenance, auth state, product-matched
  endpoints, an operation summary (count, read-only count, groups, examples
  present), and datasources in one JSON view; per-section failures land under
  `errors` without failing the command.
- **Dynamic shell completion** for plugin names (all PLUGIN-arg commands),
  operation names (`operation invoke|describe`, cache-served with a 2s
  guard), and process IDs. `completion fish|bash|zsh` documented in README.
- **`operation invoke|batch --timeout 30s`** bounds an invocation with a
  context deadline.

## v0.8.0

### Added
- **`process.list` host capability** (`host.ProcessLister`, optional interface
  mirroring the ConnDialer pattern — existing hosts and test doubles are
  unaffected). Lists host-managed background processes started via
  `ProcessStart`, filtered by group/label, each record probed for PID
  liveness (`alive`) so a caller can tell a running forward/tunnel from a
  dead one. Stopped processes are removed from the store; self-died ones stay
  listed with `alive:false`. `ProcessStart`'s `label` is now persisted.

## v0.7.0

Agent-usability pass driven by friction hit in real sessions:

### Added
- **`operation describe` summarizes the output schema.** New `output_fields`
  (top-level fields plus one nesting level, including array element fields) and
  `pagination_fields` (truncation signals present among
  `has_more`/`next_page_token`/`truncated`, plus `total` when accompanying
  them) in both text and `--json` forms. `output_keys`/`output_schema` remain.
- **`datasource lookup`/`search` results carry a `hint`** when the call fell
  through to the plugin while the plugin declares indexes that were never
  built (`index not built — run: fluxplane-plugin index build <plugin>`).
  `lookup-all`/`search-all` surface the hint per plugin.

### Changed
- **`--arg` values coerce to the operation schema's declared type.**
  `--arg page_id=33729` on a declared-string field now stays the string
  `"33729"` instead of JSON-parsing to a number and failing schema decode.
  Explicit JSON quoting (`--arg page_id='"33729"'`) still unquotes once;
  ambiguous unions, undeclared fields, and unavailable schemas keep the old
  JSON-when-valid heuristic. Coercion also applies under `--no-validate`.
- **Host index lookup/search/get on a never-built index fail actionably**
  (`no index built for plugin … — run: fluxplane-plugin index build <plugin>`)
  instead of silently returning zero matches, so a plugin resolving a ref like
  `#general` reports the real cause. A built-but-empty index still returns
  empty results.

## v0.6.0

Agent-efficiency pass (10 improvements):

### Added
- **`operation invoke --arg key=value`** — build input without hand-writing JSON;
  dotted keys nest (`--arg fields.priority=High`) and values parse as JSON when
  valid (numbers/bools/arrays) else as strings. **`--input -`** reads stdin.
- **`operation invoke --result-only` / `--field <dot.path>`** already existed;
  now **`--strict`** makes a missing `--field` path exit non-zero.
- **`operation search --full`** folds each match's input fields + a runnable
  example into the result, so search→invoke needs no separate `describe`.
- **`pluginbinding.NewPagedListResult`** + `ListResult.Total/HasMore/NextPageToken`
  — a standard truncation/pagination signal for list operations.
- **`FLUXPLANE_PLUGIN_INSTANCE`** sets the default `--instance` for a session.
- **`FLUXPLANE_PLUGIN_TIMEOUT_SECONDS`** overrides the new default per-call
  plugin timeout (120s); a wedged plugin now fails with a clear message instead
  of hanging forever.

### Changed
- **Fan-out commands run concurrently.** `operation search`, `doctor`,
  `selftest`, `datasource search-all`, and `context build-all` invoke plugins in
  parallel (bounded pool) instead of serially.
- **Failed `operation invoke` emits a structured error** (`{plugin, operation,
  error:{code,message,fields,details}}`) so callers read the detail
  programmatically; the duplicate error line is gone (the entrypoint prints
  once).
- **`operation list`/`describe`/input-validation are served from a cached
  operations list** keyed by the installed binary's mtime — no plugin process
  spawn when the binary is unchanged; a rebuild/upgrade invalidates it
  automatically.
- **`selftest` batches all probes through one plugin process** per plugin
  instead of one spawn per probe.
- **`operation invoke --dry-run` redacts secret-ish input fields** (token,
  secret, password, api_key, …) when echoing the input.

## v0.5.0

### Added — agent-ergonomic operation tooling

- **`operation describe PLUGIN OPERATION`** — a compact, agent-friendly spec for
  one operation: each input field with type, required/optional, allowed enum
  values, and a one-line description; a runnable invocation example; the output's
  top-level keys; and risk/idempotency/effects/auth metadata. Read this instead
  of the raw `input_schema`. `--json` for the structured form.
- **`operation search QUERY`** — keyword search across installed plugins' op
  names + descriptions (all whitespace terms must match), ranked best-first.
  `--plugin` restricts the search, `--read-only` filters, `--json` for structure.
- **`operation invoke --dry-run`** — validate `--input` against the operation's
  schema locally and report, without calling the backend. Validation is
  conservative/high-confidence (top-level missing-required, enum violations, and
  unknown keys when `additionalProperties:false`); it skips when the op declares
  a schema example (one-of inputs). By default a real invoke fails fast on these
  problems before any backend round-trip; `--no-validate` opts out, and a schema
  it can't discover never blocks a valid call.
- **`operation invoke --result-only` / `--field <dot.path[,…]>`** — print just the
  result (drop the envelope) or extract specific values by dot-path (e.g.
  `user.displayName`, `rows.0.ok`), so callers stop hand-extracting JSON.

### Changed
- The skill cheat-sheet now points at `operation describe` and `operation search`
  as the on-demand way to learn an operation's exact input shape.

## v0.4.1

### Fixed
- `upgrade` (and any `--remote` install) no longer reuses a binary already on
  PATH when the marketplace entry has a `go_install` source — it now always
  fetches the latest published version. Previously an existing binary short-
  circuited resolution, so `upgrade` could silently keep a stale/dev build.
  (`doctor` surfaces exactly this drift.)

## v0.4.0

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
