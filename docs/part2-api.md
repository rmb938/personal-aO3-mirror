# Part 2 — REST API plan (`ao3mirror serve`)

> How to use this file: each stage below is one PR (or a small group), followed by `/clear`. A new
> session reads CLAUDE.md, then this file's "Conventions" and its own stage section, implements
> it, ticks the stage's box, runs `make check`, then **stops and waits**: the user reviews the
> uncommitted changes and says when to commit and open the PR. Never commit, push or open a PR
> unasked. If a stage changes a decision or convention, update this file in the same change.

## Context

Part 1 (the scraper) is done. Part 2 adds a JSON API that:
- powers a future JavaScript UI for browsing, searching and reading works the way AO3 does (works,
  chapters, tags, series, creators, collections);
- lets users add sources and view sources, jobs and status;
- replaces the direct DB access of the admin CLI commands.

It will eventually face the internet through Tailscale Funnel, which terminates TLS. So it needs
rate limiting, strict input validation, bounded sizes and content-free logs from day one. Auth comes
later (placeholder stage), before Funnel exposure.

The overriding constraint is the CLAUDE.md HARD RULE. The API must never expose fanwork content
unless a real user asks for it per request. By default, every string the API returns is replaced
with a keyed hash by a mandatory response enforcer, so a handler bug cannot leak content.

### Decisions made with the user

| Topic | Decision |
|---|---|
| Process | `ao3mirror serve` is a separate process from `run`. It never builds an `httpx` client, so the internet-facing process cannot reach AO3. It shares the DB the same way the CLI does. |
| Listeners | **Public TCP** (the one Funnel exposes): browse, read, search, view sources/jobs/status, add source. **Admin unix socket** (`data/api/admin.sock`, 0600): every route plus admin ops (confirm, pause, resume, cancel, remove, refresh, settings, debug). |
| CLI | The source, status, jobs and debug commands become API clients of the admin socket. They fail clearly if `serve` isn't running. `run`, `reparse`, `fetch-work`, `config`, `dev` and `version` stay direct. |
| Reveal | `reveal_fanwork=1` is the only way to get real content, per request, on every route. No flag, config or header reveals globally. |
| Strict server flag | `serve --no-reveal` makes the server refuse reveal (403 `reveal_disabled`). It can only make the server stricter. The guard forces it on every `serve` that Claude starts. |
| Search | Free text and complex criteria go in a JSON body using **QUERY** (RFC 10008, June 2026). There is no POST alias. Free-text fields (`q`, name lookups) return 400 `reveal-required` without reveal. |
| Router | `github.com/go-chi/chi/v5` (v5.5.0) for sub-routers and middleware. It supports QUERY natively (`r.Query(pattern, h)`, `mQUERY` in `tree.go`), so it needs no `RegisterMethod`. `github.com/go-chi/httprate` (v0.16.1) for rate limits. Run `go list -m X@latest` again when adding them. |
| Auth | Placeholder stage only. No design now. A future `users.db` is separate from meta/content. |

## Conventions (every stage follows these)

### Layout
```
cmd/ao3mirror/serve.go       serve command: flags, listeners, graceful shutdown
internal/web/api.go            API facade; UI belongs in internal/web/ui/
internal/web/api/               route assembly and API-specific packages
internal/web/api/runtime/       HTTP runtime, middleware, response enforcement
internal/web/api/handlers/      endpoint implementations (calls controllers only)
internal/web/api/redact/         hasher + response enforcer (no HTTP deps; fuzzed)
internal/web/api/dto/            response types, registered with the enforcer
internal/web/api/request/        request body types with their Validate methods (from stage 4)
internal/web/api/convert/        model/controller results -> DTOs, requests -> options (3a-4)
internal/web/api/problem/        problem Error, detail catalogue, Validation collector (from 3g)
internal/web/api/route/          route patterns + route.Path, shared by api and apiclient (6a-1)
internal/apiclient/          typed client over the admin socket, used by the CLI
docs/api/openapi.yaml        hand-written OpenAPI 3.1; a test checks that it matches the route table
```
Handlers call `internal/controllers`, never `store`, following the CLAUDE.md layering rules. New
read models go in `controllers` with one `InReadTx` each. New SQL goes in `store`.

**DTO rule (by design, no lint):** `internal/web/api/dto` imports only the standard library. It
never uses or imports `controllers`, `store` or `model` types, and never embeds them. Handlers
map controller results into DTOs field by field. The API contract then changes only when a DTO
is edited on purpose, and the enforcer's schemas are exactly the DTO types. Reviewers check
this rule, and it is added to CLAUDE.md in stage 0.

**Duration rule (from stage 3f):** a duration field is a `dto.Duration` (a `time.Duration`
that marshals as `time.Duration.String`, e.g. `"6h0m0s"`, and parses anything
`time.ParseDuration` accepts), with no unit suffix on the key (`poll_interval`, not
`poll_interval_s`). Go 1.27's `encoding/json/v2` has no default `time.Duration` format and
dropped the `format` tag option (go.dev/issue/79071), so the registry rejects a plain
`time.Duration` field.

**Enum rule (from stage 3a-3):** a string field with a closed value set has a named string
type in `dto` (`dto.SourceState`) with an `EnumValues() []string` method listing its values.
The registry finds these by reflection; nothing else lists enums. Each enum field allows only
its own type's values (from 3c-3: `status` on `Health`, `WorkStatus` and `ImageStatus` are three
separate sets). A plain `string` field is free text and is always hashed. The only exception is
`dto.Version.Version`, whose single value is known at runtime (`RegisterFieldEnum`).

**Conversion rule (from stage 3a-4):** the field-by-field mapping lives in
`internal/web/api/convert`, not in handlers. It exports one function per model or controller
result, named after it (`convert.Source(model.Source) dto.Source`, `convert.Job(...)`), and
keeps every helper (filter/cursor mapping, last_error split, null-for-unknown) unexported, with
its tests beside it. Each model or result has its own file (`source.go`, `source_add.go`, each
with its `_test.go`); `convert.go` holds only the helpers several models share (`list`,
`splitError`, `known`, `utc`, `unix`) and the cross-model enum-set test.
Handlers parse input, call a controller and return `convert.X(result)`.

**Empty list rule (from stage 5c-2's follow-up):** converters return nil for an empty list and
never turn nil into `[]` themselves (`convSlice` gives nil for empty input). `api.respond`
marshals with `encoding/json/v2`, which writes a nil slice as `[]`, so a list is never null on
the wire.
CLAUDE.md's API design rules get this line in 3a-4.

**Pagination rule (from stage 3a-2):** a paged route pages in SQL. The store takes
offset/limit and has a matching count, and the controller returns the page and the total from
one `InReadTx` (so they agree). Never fetch every row and slice in the handler.
Offset/limit is the contract, not keyset (cursor) pagination: the UI wants numbered pages and a
total, like AO3, and a keyset cursor carries the sort key, so a title or creator sort would put
fanwork strings in it. Offset's costs are deep-page skipping and rows drifting across pages when
data changes. Drift is accepted (AO3 has it too). The skipping cost is bounded by the
`(page-1)*per_page ≤ 100000` cap and handled per table (3b, 9).

### Fanwork protection (the core design)
1. **Single write path.** Handlers return `(any, error)`. Only `api.respond` encodes JSON.
   Handlers never get a raw `http.ResponseWriter` (a lint test greps handler files for it).
2. **Enforcer middleware** (`redact.Enforcer`) wraps the whole router, outermost after
   recovery. In hashed mode (the default) it buffers the body (with a cap) and requires a JSON
   content type. Any other content type is replaced with 403 `reveal-required`. It then walks the
   JSON against the **schema of the Go type** `respond` marshaled it from (from 3c-3), compiled
   at startup by reflecting over the registered DTO types. JSON not written by `respond` (no
   type) or of an unregistered type gives 500.
   - numbers and booleans pass through where the type has a number or bool field; null passes
     anywhere;
   - every **object key** must be a field of the struct at that position. An unknown key gives
     500 and a log line with the hashed key. A value of the wrong JSON kind (an object where
     the field is a string) gives 500. This rules out maps with dynamic keys;
   - **string** values pass through only when their own field allows them:
     - an enum field (a `dto` type with `EnumValues()`) whose value is in that type's set.
       Fields sharing a key don't share values: a `WorkStatus` value in `Health.status` is
       hashed;
     - a field whose key ends in `_at` and whose value is strict RFC3339 UTC;
     - a field whose key ends in `_on` and whose value is `YYYY-MM-DD`;
     - a `dto.Duration` field whose value is exactly a Go duration string (from 3f).
   - **every other string is replaced with `h_` + 24 hex of HMAC-SHA256(key, value)**.
     - Within one key epoch the hash is deterministic, so equal names hash equally across fields,
       routes and requests (Claude can match a work's tag hash with `/tags/{id}`).
     - It is keyed, so it can't be dictionary-checked.
     - **The key is never stored.** It is 32 bytes from `crypto/rand`, made at startup, held only
       in memory and never logged. It rotates every `api.hash_key_rotation` (default 24h, minimum
       1h; 0 means once per process).
     - Each epoch has a random ID, sent as the `X-Fanwork-Key-Epoch` header, so tests and clients
       can tell when hashes from two requests aren't comparable.
     - Rotation swaps the key atomically, and a response is hashed entirely with one epoch's key.
   - In reveal mode it still checks keys (structure), but does not hash. It adds
     `Cache-Control: no-store`.
   - Every response gets the header `X-Fanwork: hashed|revealed`.
3. **Reveal param.** `reveal_fanwork=1` exactly. Any other value, or the param repeated, gives
   400. It is parsed once by middleware into the context. `serve --no-reveal` turns it into 403.
4. **Non-JSON content** (images, in a later stage) is reveal-only by construction: the enforcer
   refuses non-JSON in hashed mode.
5. **Errors** are RFC 9457 `application/problem+json` (from 3d): `type` (a URI per problem
   type, `docs/api/problems.md#<slug>`), `title` (fixed per type), `status`, an optional
   `detail`, and on `validation-error` an `errors` extension of `{detail, parameter | pointer}`
   items listing every invalid part (from 3e). Every string is from a fixed set:
   `detail` messages are a catalogue (`problem.NewDetail`, package-level vars only),
   registered with the enforcer as the only values of the two `detail` fields, and never include
   client input. `parameter` is a `dto.ParamName` (query and path names only); `pointer` must
   name a member of a registered request type (`request.Types()`). Job `last_error`
   is split into `last_error_code` (enum) and `last_error_detail` (hashed).
6. **Free-text inputs** (`q`, `name`) need reveal. In hashed mode they return 400
   `reveal-required` (the oracle rule). Structured inputs (IDs, enums, numeric ranges, dates) are
   always allowed.
7. **The all-routes leak test** (added in stage 2, grows with every route):
   - Walk the chi route table. Every route must have a sample request in the test table, or the
     test fails.
   - Seed a temp store where every text field is `SENTINEL-<field>`.
   - Call each route in hashed mode. Assert that no sentinel appears in the body, the headers or
     the captured logs.
   - A reveal-mode subtest checks that the content is actually returned (positive control).
8. **Logs**:
   - access log: `req_id`, method, **route pattern**, numeric path IDs, status, bytes, duration,
     client IP, `funnel` bool, `reveal` bool, query param **names** only;
   - never query values, bodies or response strings.

### HTTP hardening
- **`http.Server`:**
  - ReadHeaderTimeout 5s, ReadTimeout 15s, WriteTimeout 60s, IdleTimeout 120s;
  - MaxHeaderBytes 16 KiB;
  - graceful `Shutdown` (10s) on SIGINT/SIGTERM;
  - `BaseContext` is the signal context.
- **Middleware order:**
  1. request ID: our own small middleware.
     - It always generates the server ID `req_id`: 16 random hex characters, with no hostname
       and no counter. It goes in the context and the logs, and is returned as `X-Request-Id`.
     - If the client sent `X-Request-Id`, it is kept as `client_req_id` (logged, and echoed as
       `X-Client-Request-Id`) when it is ≤ 64 bytes of `[A-Za-z0-9._:-]`. Otherwise it is
       dropped and `client_req_id_invalid=true` is logged.
     - Why not chi's `RequestID` (v5.5.0 source): it uses a client-sent ID verbatim as *the*
       request ID, with no length or charset check. A client could then pick IDs that collide
       with other requests' IDs in our logs, so IDs stop being trustworthy for tracing. Its
       generated IDs also embed `os.Hostname()` and a sequential counter, which would leak the
       hostname and the request volume once echoed to internet clients.
  2. trusted client IP (chi `ClientIPFromXFF`, see below)
  3. access log: our own slog middleware. chi's `Logger` is not used, because it logs the full
     URI including query values.
  4. recoverer: our own. It logs the panic type and stack, never the value. chi's `Recoverer`
     is not used, because it prints the panic value, which could hold content.
  5. security headers
  6. enforcer
  7. URL limits
  8. rate limit
  9. ThrottleBacklog
  10. per-request timeout (context deadline, default 10s; SQLite queries honour ctx)
  11. `http.CrossOriginProtection` (Go 1.25+)
  12. reveal parser
  13. routes
- **Client IP:** use `middleware.ClientIPFromXFF(api.trusted_proxies...)` (default
  `127.0.0.1/32`, `::1/128`) and read the result with `middleware.GetClientIPAddr`. I checked
  that it matches what we need (chi v5.5.0 `middleware/client_ip.go`, tailscale
  `ipn/ipnlocal/serve.go`):
  - tailscaled proxies from loopback and **overwrites** XFF with the real source address
    (`Header.Set`). With a loopback peer, chi walks XFF right to left past trusted entries and
    takes the first untrusted one, which is the Funnel or tailnet client.
  - A non-loopback peer is the client itself, and its XFF is ignored, so it can't be spoofed by
    hitting the port directly. Duplicate XFF headers are merged, v4-mapped and zoned addresses
    are normalised, and a garbage entry fails closed.
  - Gap 1: a loopback peer with no XFF (a local curl), or the unix socket, gets **no** IP set.
    Our rate-limit key function maps that to a fixed `local` key (public listener) or `admin`
    (socket), so missing IPs share one bucket and never bypass the limit.
  - Gap 2: if tailscaled ever proxied from a non-loopback address, every client would look like
    one IP. A startup log line prints the trusted prefixes; verify once with a real Funnel request.
  - Do **not** use chi's `RealIP`, because it trusts headers blindly.
  - `Tailscale-Funnel-Request: ?1` marks internet requests in the logs.
- **Rate limits** (httprate, keyed by trusted IP, configurable): general 120/min; QUERY search
  30/min; `POST /sources` 10/hour. 429 responses carry `Retry-After`. The admin socket has its own,
  higher limits.
- **Security headers:**
  - `X-Content-Type-Options: nosniff`
  - `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`
  - `Referrer-Policy: no-referrer`
  - `Cross-Origin-Resource-Policy: same-origin`
  - no CORS headers (same-origin UI)
- **Input:**
  - URL: path + query ≤ 4 KiB, ≤ 32 query params, duplicates rejected, unknown params rejected
    (per-route allowlist).
  - Integers: `strconv.ParseInt(s, 10, 64)` with explicit ranges. IDs are 1..2^53-1 (safe for JS).
    `page ≥ 1`, `per_page` 1..100, and `(page-1)*per_page ≤ 100000` (AO3's cap), checked before
    multiplying.
  - Bodies: `Content-Type: application/json` is required (otherwise 415; QUERY responses
    advertise `Accept-Query: application/json`); `http.MaxBytesReader` at 16 KiB; unknown fields
    rejected; trailing data rejected; duplicate keys rejected (use `encoding/json/v2` if it is
    GA in the toolchain, else a token pre-scan).
  - Strings: valid UTF-8, no control characters, max runes per field. Arrays have max lengths
    (e.g. ≤ 50 tag IDs).
  - FTS: user text is never passed as raw FTS5 syntax. It is tokenized and each term quoted, with
    ≤ 16 terms and ≤ 256 runes.
- **Responses:**
  - JSON, `snake_case`. IDs are numbers.
  - Lists use `{items, page, per_page, total}` with page-number pagination (AO3-like, and no
    content in cursors).
  - The hashed-mode buffer cap is 32 MiB (a 500 above it).
- **Errors** map controller/store sentinels to status codes in one place (`api/errors.go`):
  `ErrNo*` → 404, `ErrStateConflict` → 409, `ExistsError` → 409, invalid parameters → 422
  `validation-error`, unreadable query or body → 400, anything
  else → 500 (`internal`).

### Config (`api:` section in `internal/config`, with validation)
```yaml
api:
  listen: 127.0.0.1:8080     # public listener; Funnel points here
  admin_socket: api/admin.sock   # relative to data_dir
  trusted_proxies: ["127.0.0.1/32", "::1/128"]   # quoted: bare ::1 breaks a YAML flow list
  request_timeout: 10s
  hash_key_rotation: 24h     # in-memory random key; 0 = once per process; min 1h
  rate: { general_per_min: 120, search_per_min: 30, source_add_per_hour: 10 }
```

---

## Stages

Status: tick a stage when its code is done, in the same PR as that code, just before opening it.

- [x] **0. Plan + guardrails**
  - Copy this plan to `docs/part2-api.md`.
  - `docs/plan.md`: point the Part 2 row at it, and note that Part 3 is a JS UI that calls the
    API.
  - CLAUDE.md:
    - add a Part 2 section: the reveal rules, never send `reveal_fanwork`, `serve` only with
      `--no-reveal`, and no guessed free-text probing;
    - point at `docs/part2-api.md`.
  - `guard.py` (approved by the user 2026-11-18):
    - block `reveal_fanwork` followed by `=` (or `%3D`), and the URL-encoded name
      `reveal%5Ffanwork`, in Bash commands, WebFetch URLs and **all `mcp__*` tool inputs**
      (whole JSON);
    - block Write/Edit content containing the literal `reveal_fanwork=` outside `docs/`,
      `CLAUDE.md` and `.claude/hooks/` (Go code uses the constant `api.RevealParam`);
    - block `ao3mirror serve` (any binary path, or `go run ./cmd/ao3mirror ... serve`) without
      `--no-reveal`.
  - `settings.json`: extend the hook matcher with `mcp__.*`.
  - Add `guard_test.py` cases for each rule, both blocked and allowed (e.g. `grep -r
    reveal_fanwork internal/` is allowed, and `serve --no-reveal` is allowed).
- [x] **1. `internal/web/api/redact`.** No HTTP server yet.
  - Hasher: an in-memory `crypto/rand` key with an epoch ID, and rotation driven by an
    injectable clock. `Hash(s)` under one epoch snapshot per response.
  - Registry: `RegisterTypes(...)` collects keys, `RegisterEnum(key, values...)`.
  - `Enforce(mode, jsonBytes) ([]byte, error)`.
  - Tests: unit tests per rule, plus a fuzz test (random JSON with sentinel strings: no input
    string survives in hashed mode unless allowed) and benchmarks.
  - As built:
    - `Enforcer.Enforce(mode, body)` returns `Result{Body, Epoch}`. `Epoch` is the epoch ID
      for the `X-Fanwork-Key-Epoch` header, and is empty in revealed mode.
    - Errors are `*UnknownKeyError` (it carries only the key's hash) or `ErrInvalidJSON`.
      Neither message quotes the input.
    - `NewEnforcer(reg, hasher)` snapshots the registry, and every enum key must be a DTO key.
    - `RegisterTypes` requires an explicit snake_case json tag on every exported field. It
      rejects map, interface, chan and func fields. Types with their own JSON or text
      marshaling (e.g. `time.Time`) count as leaves.
    - Strings inside arrays use the key of the field that holds the array.
    - An `_at` value must be exactly what `time.Time` marshals for UTC (RFC3339Nano with `Z`).
    - `NewHasher` enforces the `MinRotation` (1h) rule, which config validation can reuse.
    - `Epoch` values print and log only their ID.
    - Benchmark: about 1 ms (~175 MB/s) for a 186 KB list page in hashed mode.
- [x] **2. Server skeleton**
  - `serve` command (`--no-reveal`), `api:` config, chi router.
  - The full middleware chain above.
  - `respond` / problem+json helpers; param parsing helpers (bounded ints, page, IDs); strict JSON
    body decoder.
  - Routes: `GET /api/v1/healthz` and `GET /api/v1/version`.
  - The all-routes leak test harness, a route-coverage test against `docs/api/openapi.yaml`, and
    tests for each middleware (timeouts, limits, 415/413/400 cases, XFF trust).
  - Admin socket listener: same router plus an admin sub-router (empty for now). Its file mode is
    asserted in tests.
  - Split into three PRs (the box is ticked by the last one):
    - [x] **2a. API core** (`internal/web/api`, no server yet). As built:
      - `dto.Problem{code, title, status, field}`. `api.Error{Code, Field, Status}` is the error
        handlers return. `problemFor` (`errors.go`) is the single error → problem mapping: the
        store `ErrNo*` sentinels → 404, `ErrStateConflict` → 409 `conflict`, `ErrSourceExists`
        / `controllers.ExistsError` → 409 `exists`, context cancel/deadline → 503
        `unavailable`, anything else → 500 `internal` (logged with the route pattern).
      - `field` is only ever a declared name (`knownFields`, now `dto.ParamName`; else dropped). `code`, `title` and
        `field` are registered enums, so problems pass the enforcer unhashed (tested).
      - Handler shape `func(*http.Request) (any, error)`, adapted by `handle`, which caps the
        body (16 KiB) and calls `respond`. A handler returns `*api.Response` for another status
        or a `Location`. 415 on QUERY adds `Accept-Query: application/json`.
      - `parseQuery(r, allowed...)` (≤ 32 values, no repeats, allowlist; `RevealParam` always
        allowed and stripped), `parseInt`/`parseID` (digits only, ranges), `query.page()`,
        `checkText`.
      - `runtime.DecodeJSON` uses `encoding/json/v2` (GA in Go 1.27): it rejects duplicate keys,
        trailing data and invalid UTF-8 by default, plus `RejectUnknownMembers`; matching is
        case-sensitive. Errors never quote the body.
      - `newEnforcer` builds the redact registry from `responseTypes` and the enums.
    - [x] **2b. Router + middleware**: chi router, the full middleware chain, healthz/version,
      the all-routes leak test and the OpenAPI route-coverage test. As built:
      - `api.New(Options, logger, controllers)` builds one hasher/enforcer shared by two chi routers: `Public()` and
        `Admin()` (public routes + an empty admin-only group). Options carry only what 2b needs
        (`Version`, `NoReveal`, `TrustedProxies`, `RequestTimeout`, `HashKeyRotation`,
        `GeneralPerMin`); the search and source-add limits arrive with their routes. The admin
        socket gets 10× the general limit, and never runs `ClientIPFromXFF` (no IP peer, so
        XFF there would be spoofable); its requests share the `admin` bucket.
      - Per-request state (`req_id`, client ID, reveal) lives in one context value set by the
        request-ID middleware. The reveal parser runs innermost and sets the flag; the enforcer
        and access log read it after the handler returns. Anything rejected before the reveal
        parser is therefore always hashed.
      - Enforcer middleware buffers into its own header map, so a replaced response (500 on an
        unknown key, invalid JSON or > 32 MiB; 403 `reveal_required` for non-JSON in hashed
        mode, including JSON without a JSON content type) drops the handler's headers. Empty
        bodies pass. Replacement problems also go through `Enforce`.
      - Throttle is our own (64 in flight, 256 backlog, 5s wait, 503 `unavailable` +
        `Retry-After: 1`): chi's `ThrottleBacklog` writes text/plain, which the enforcer would
        turn into a misleading 403. Cross-origin denials are 403 `forbidden` (new code).
      - Access log: method names outside the known set log as `other`; query param names are
        logged only when declared (`dto.ParamName` from 3a-3), others only counted (`other_params`), since a client
        picks them freely. Numeric path params log as `path_<name>`, others as
        `path_<name>_invalid`. A request rejected before routing (429, throttle 503, URL
        limits, cross-origin) gets its route resolved with chi's `Routes.Find` (no handler
        runs) and logs `prerouted=true`; an unmatched path logs `route=""`.
      - 405 sets `Allow` by matching the path against each routed method.
      - `docs/api/openapi.yaml` is **OpenAPI 3.2** (native `query` operations). Admin-only
        operations carry `x-admin-only: true`; the coverage test compares both routers.
      - The leak test (`router_test.go`) walks both routers against `routeSamples`, sends each
        route a normal and a hostile request (sentinels in param names/values, request ID,
        body), and has a positive control route. Store seeding starts with the first
        store-backed route (stage 3). Body 415/413/400 cases are covered by `body_test.go`
        until a route takes a body.
    - [x] **2c. `serve`**: `api:` config, the `serve` command (`--no-reveal`), TCP + admin
      socket listeners, graceful shutdown. As built:
      - `config.API` validates `listen` (host:port, port 1-65535), `admin_socket` (local path
        under data_dir), trusted proxies (CIDR, non-empty), `request_timeout` 1s-50s (below the
        60s write timeout, so the 503 gets out), `hash_key_rotation` via `redact.MinRotation`,
        and every rate ≥ 1. The search and source-add rates are config-only until their routes.
      - `serve` opens no store yet (stage 3 adds it) and never builds an `httpx` client.
      - Admin socket: its directory is created 0700 and **must** not be group/other
        accessible (else `serve` refuses), so there is no window before the socket's chmod
        0600. A stale socket (nothing listening) is replaced; a live one or a non-socket file is
        an error. The path must fit `sun_path` (107 bytes). It is unlinked on shutdown.
      - If either listener fails, both shut down. `http.Server.ErrorLog` goes to slog at warn.
      - A non-loopback `listen` logs a warning (no auth yet).
      - Tests (`cmd/ao3mirror/serve_test.go`): both listeners, `--no-reveal` → 403
        `reveal_disabled`, socket/dir modes, live/stale/non-socket/unsafe-dir cases, shutdown
        removes the socket.
- [x] **3. Read-only ops API**
  - `GET /sources`, `/sources/{id}`, `/jobs` (filters: state, kind, source_id, page),
    `/jobs/{id}`, `/status`, `/source-adds/{job_id}`.
  - DTOs are built from the existing controllers (`ListSources`, `GetSource`, `FindJobs`,
    `Status`, `SourceAddOutcome`).
  - Split into six PRs (the box is ticked by the last one). Each starts from main after the
    previous merges.
    - [x] **3a-1. Refactor: source and job types into `internal/model`** (no behaviour
      change). As built: `model/source.go` and `model/job.go`; the job state constants stay
      untyped strings (`Job.State` is a `string`). Store methods keep their names
      (`GetSourceRow` still means "without member counts").
      - `store.SourceRow` → `model.Source`, `store.SourceState` and its constants →
        `model.SourceState`; `store.Job` → `model.Job`, and the job state constants
        (`JobQueued` … `JobCancelled`) move with it.
      - The store still scans into them. Row-only and query types stay in `store`
        (`JobFilter`, `JobOrder`, `NewJob`, `RequestRow`, `Counts`, `WorkDiag`, `SourceAdd`, …)
        until a stage needs them.
      - Update every caller (store, controllers, scheduler, imagefetch, cmd). Keep the doc
        comment warning that `Target`/`FiltersJSON` may hold names (never log them).
    - [x] **3a-2. Store + controller: paged sources.**
      - Store: `ListSources` takes offset/limit (the member-count subqueries then only run for
        the page) and gets a `CountSources`.
      - Controller: `ListSources(ctx, offset, limit)` returns `(page, total)` from one
        `InReadTx`. Update the CLI `source list` caller (it can page through, or ask for a
        large limit) and decide what `Status` passes (all sources today; 3c may cap it).
      - Store tests for offset/limit/count, ordering stable by id.
      - As built: the store rejects `offset < 0` or `limit < 1` with an error (the API
        validates first, so this is a guard). `Status` still lists every source
        (`limit = math.MaxInt`). The CLI `source list` pages through 100 at a time, the API's
        max `per_page`, so stage 6's client keeps the same loop.
    - [x] **3a-3. Refactor: typed enums in the registry** (no change to responses). Today
      every enum is a key → values entry in `api/registry.go`, kept by hand. Nothing ties it
      to the struct: a field that should be an enum is only noticed when its value comes out
      hashed.
      - `redact` declares `type Enum interface{ EnumValues() []string }`. `dto` stays
        standard-library only: its types satisfy the interface structurally and never import
        `redact`.
      - `Registry.walk`: when a field's type (after unwrapping pointers, slices and arrays) is
        a string kind that implements `Enum`, it registers the field's json key with the zero
        value's `EnumValues()`. The same key on several enum types gets the union (e.g.
        `state` for sources and jobs).
      - The registry rejects (startup error, unit tested):
        - a key that is a typed enum in one field and a plain `string` in another (the plain
          field would let enum-looking values through);
        - an enum type with no values or an empty value;
        - an `Enum` whose kind is not string;
        - a `RegisterEnum` call on a key that also has a typed enum.
      - Move the existing enums to types: `dto.ProblemCode` (the `Code` constants move from
        `api` to `dto`), `dto.ProblemTitle` (the fixed titles; the code → title/status table
        can live in `dto` as data), `dto.ParamName` (`field`; replaces `knownFields`) and
        `dto.HealthStatus`. `version` stays on `RegisterEnum` (runtime value).
      - `api/registry.go` then only lists `responseTypes`, and has no enum map.
      - The enforcer still hashes a value outside its type's set (fail safe), and logs a warning
        with the key only. The convert tests (3a-4 on) are what catch a new model value
        missing from a dto type, before it ships.
      - As built:
        - `EnumValues` may have a value or pointer receiver (called on a new zero value). An
          `Enum` type that marshals itself (JSON or text) is also rejected: its JSON need not
          be its value. A non-string field (e.g. `Problem.Status int`) may share a key with an
          enum (`Health.Status`): only strings are checked.
        - `redact.Result.OffEnum` lists the enum keys that held an off-set value (both modes);
          the enforce middleware logs `api enum value outside its set` at warn with
          `req_id`, `route` and `keys`.
        - `dto/problem.go` holds `ProblemCode` (+ constants), `ProblemTitle`, the
          code → `ProblemInfo{Status, Title}` table (`ProblemCode.Info()`) and `ParamName`
          (constants + `Known()`, used by `problem()` and the access log). `api.RevealParam`
          is `string(dto.ParamReveal)`. `api.Error.Code` is a `dto.ProblemCode`; `Field`
          stays a string (it can hold a client-chosen name, dropped unless `Known`).
    - [x] **3a-4. Sources routes**: `GET /sources` (page, per_page), `/sources/{id}`,
      `/source-adds/{job_id}`, with mapping in `internal/web/api/convert`. Design (from a first
      attempt, reworked into 3a-1..4):
      - `serve` opens the store and builds controllers with no AO3 client
        (`newControllers(cfg, log, st, nil)`); `api.Options.Controllers` is required. The
        server owns the store and closes it when `serve` returns (and on a `newServer` error).
      - `dto.Source`: id, kind, target (hashed), `target_id` (numeric target of series/work
        sources, omitted otherwise), `filters`, path (hashed), state, order (omitted when
        empty), poll_interval_s, priority, `total_works`/`est_requests` (`*int`, null when the
        store has -1), member_works, fetched_works, `backfill`, the `_at` times (UTC, zero ones
        omitted) and `last_error` (hashed whole: a source's message is "poll page N: code",
        not code-first).
      - Filters (stored as a name → values JSON map) become `[{name, values}]` sorted by name,
        `[]` when none; maps are not allowed in DTOs.
      - The backfill cursor becomes `backfill` (omitted when empty): next_page, order,
        `window_start_on`/`window_end_on`, rescan, `rescan_started_at`, blocked_parser,
        `retry_at`.
      - `dto.SourceAdd`: job_id, state, `last_error_code`, `last_error_detail`, `result`
        (source, blurbs, estimate with `duration_s`). A job `last_error` is split at the first
        `:`.
      - Enum types (3a-3 rule): `dto.SourceState` and `dto.JobState` (both on key `state`),
        `dto.SourceKind` (`kind`; `dto.JobKind` joins in 3b), `dto.Order` (`order`) and
        `dto.JobErrorCode` (`last_error_code`: the ErrorCodes, `rate_limited`/`unavailable`,
        `crashed`, `internal`). `convert` casts the model value into the dto type. A code
        outside the set is hashed by the enforcer.
      - Convert tests check that every model-side value is in its dto type's set: all
        `model.SourceState`s, a new `ao3url.Kinds()` and `ao3url.OrderNames()`, and a new
        `controllers.JobErrorCodes()`. So a value added on the model side without its dto
        value fails a test instead of shipping hashed.
      - `SourceAddOutcome` returns `store.ErrNoJob` (404) for a job that is not a source add.
        A done add whose source was removed since is a 404 too (existing behaviour, kept).
      - `dto.ParamName` gains `job_id`. OpenAPI gets the paths plus shared `ID`, `Page`,
        `PerPage` parameters and the schemas.
      - Leak test: `testOptions(t, logs)` opens a temp store and seeds it (`seed_test.go`,
        fixed ids: a tag source with every text field a sentinel, a series source, a done and
        a failed source add, a non-add job). Samples gain a `content` flag: those also run
        revealed and must contain a sentinel (per-route positive control). The serve test
        checks `/sources` answers on a fresh data dir.
      - As built:
        - Lists are the generic `dto.List[T]` (`{items, page, per_page, total}`; `items` is
          `[]`, never null). `convert.SourceList(items, total, page, per_page)` builds it.
        - An unreadable `filters_json` or backfill cursor maps to `filters: []` / no
          `backfill` rather than an error (the scheduler reports those on the source). The
          cursor's internal counters (`span_days`, `rescan_listed`, …) are not exposed.
        - Value lists for the convert tests: `model.SourceStates()`, `model.JobStates()`,
          `ao3url.Kinds()`, `ao3url.OrderNames()`, `controllers.JobErrorCodes()`. The tests
          use `ElementsMatch`, so a dto value with no model value fails too. A controllers
          test parses the package source so every `ErrorCode` constant must be in
          `JobErrorCodes()`. The scheduler's `internal:` prefix is now
          `controllers.CodeInternal`.
        - A bad path ID is 400 `invalid_param` with `field` `id` / `job_id`. Handlers read
          it with `pathID(r, dto.ParamX)`.
        - `serve` closes the store when `serve()` returns, and `newServer` closes it on any
          error after opening it. The store and controllers log through the API's logger, so
          the leak test also checks their lines.
    - [x] **3b. Jobs**, in three PRs:
      - [x] **3b-1. Store + controller: paged jobs.** Store offset/limit + `CountJobs` for
        `JobFilter`, a controller returning `(page, total)`.
        - The jobs table grows with every work fetch. Check that its indexes cover each filter
          with `ORDER BY id DESC`, and benchmark a deep page and `CountJobs` on a large
          synthetic table.
        - Also verify (EXPLAIN or benchmark) the 3a-2 assumption that rows skipped by `OFFSET`
          don't evaluate `ListSources`' member-count subqueries. If they do, page ids first
          with a deferred join (see 9).
        - As built:
          - `JobFilter` gains `Offset`; `FindJobs` rejects `Offset < 0` or `Limit < 1`.
            `CountJobs(f)` shares the WHERE builder (`jobWhere`) and ignores
            Offset/Limit/Order. Controller `ListJobs(ctx, f)` returns `(page, total)` from
            one `InReadTx`.
          - Migration `meta/0002_jobs_state.sql` adds `jobs_state (state)`. Before it, a
            state filter used `jobs_finished (state, finished_at)` and sorted every job in that
            state per page: 35-60 ms on the benchmark table, now 0.2-3 ms.
          - Benchmark (`store/paging_bench_test.go`): 200k jobs (60% queued, ~90% work
            fetches, 20 sources), 100 per page, offsets 0 and 99,900. Worst cases now: deep
            page on state+kind 16 ms, on source_id alone 7 ms (it sorts that source's jobs,
            ~9.5k here); counts ≤ 8 ms. Accepted: more indexes would cost every job state change
            a write. Real tables are smaller (done jobs are pruned after 7 days).
          - `TestJobsPagePlans` asserts no page plan sorts (`TEMP B-TREE`), except the
            source_id filter without a state.
          - The `OFFSET` assumption holds: `BenchmarkSourcesPage` (1000 sources × 200
            members) costs the same at offset 900 as at 0, so no deferred join.
      - [x] **3b-2. Refactor: `JobFilter` into `internal/model`** (no behaviour change).
        `store.JobFilter` → `model.JobFilter`, and `store.JobOrder` with its constants
        (`JobsNewest`, `JobsOldest`, `JobsLastStarted`) → `model.JobOrder`, since the 3b-3
        handler builds one for `ListJobs`. `NewJob` and the other row types stay in `store`.
        Update every caller (store, controllers, scheduler tests, cmd).
        - As built: both live in `model/job.go`; `store.jobWhere` and the store tests take
          `model.JobFilter`. No other change.
      - [x] **3b-3. Jobs routes**: `GET /jobs` (state, kind, source_id, page, per_page), and
        `/jobs/{id}`. `convert.Job` splits `last_error`; `dto.JobKind` (key `kind`) gets a
        convert test against the `jobs.Kind*` constants.
        - As built:
          - `dto.Job`: id, kind, target (a hashed string until 6d-2, then an integer), `source_id` (omitted when none), priority,
            state, attempts, max_attempts, `last_error_code`/`last_error_detail`,
            `not_before_at`, `created_at`, `started_at`, `finished_at` (zero times omitted). The
            dedupe key is not exposed (debug, stage 5, may add it).
          - `JobState`/`JobErrorCode` moved to `dto/job.go` beside `JobKind`.
          - Jobs are newest first; `state=queued` lists them oldest first (queue order), as the
            CLI `jobs` command does. `state`/`kind` must be in their dto set (`query.enum`),
            else 400 `invalid_param`; `source_id` is an ID. `dto.ParamName` gains `state`,
            `kind`, `source_id`.
          - New `jobs.Kinds()` (a jobs test parses `jobs.go` so every `Kind*` constant is in
            it) and controller `GetJob` (`store.ErrNoJob` → 404).
          - Seed gains a queued backfill job of the tag source with a sentinel target.
    - [x] **3c. Status**: `GET /status`. Maps are not allowed in DTOs, so the heartbeat map
      becomes fixed fields (pid, phase, current_job, `heartbeat_at`, pacer fields,
      `backoff_until_at`, …) and the count maps (works by status, jobs by kind and state,
      images, requests by class) become lists of `{key-enum, count}`. In two PRs:
      - [x] **3c-1. Refactor: typed heartbeat, no source list in `Status`** (no API change).
        - As built:
          - `model.Heartbeat` (pid, phase, current_job, waiting_until, pacer fields,
            backoff_until, `At`) and `model.DaemonPhase` (`idle`, `running`, `between`,
            `stopped`; `model.DaemonPhases()`). The scheduler builds one; the store owns the
            `daemon_state` key/value encoding (`SetHeartbeat` / `Heartbeat`, replacing
            `SetDaemonState` / `DaemonState`). `At` is the newest row's `updated_at`; the old
            `heartbeat` key is no longer written (a leftover row is ignored). A bad value reads
            as zero.
          - `StatusReport.Daemon` is a `model.Heartbeat`, and `DaemonRunning` applies the
            staleness rule (`controllers.DaemonStaleAfter`, 2 min, and not `stopped`) that
            the CLI used to apply.
          - `StatusReport` no longer lists sources. `Counts.SourcesByState` has the totals,
            and CLI `status` pages its sources table through `ListSources` (100 at a time, like
            `source list`), as stage 6 will through `/sources`.
      - [x] **3c-2. Status route**: `GET /status` from `StatusReport`: heartbeat fields
        (`heartbeat_at`, `running`, `dto.DaemonPhase`), count lists with enum keys (work
        status, image status, request class, source state, job kind × state), tag info,
        partial series, recent request errors (path, reason and req id are plain strings, so
        hashed) and failed jobs (`dto.Job`). Convert tests check each key enum against its
        model-side values (`model.DaemonPhases()`, work and image statuses, `httpx` classes
        and kinds).
        - As built:
          - `dto.Status` (`dto/status.go`): `daemon` (running, `heartbeat_at`, pid, phase,
            current_job, `waiting_until_at`, `pacer` with `interval_ms` and
            `backoff_until_at`), `works` (`by_status` + fetched/never_fetched/parse_failures),
            tags/creators/series, `raw_pages`, `requests` (`last_1h`, `last_24h`,
            `by_class_24h`), `sources` (by state), `images` (`by_status`, bytes), `tag_info`,
            `jobs` (`{kind, state, count}`), `partial_series`, `request_errors`, `failed_jobs`.
            Count lists hold only values with rows, sorted by value (jobs by kind, then
            state); every list is `[]`, never null.
          - New enum types: `DaemonPhase` (`phase`), `WorkStatus` and `ImageStatus` (both on
            key `status`, with `HealthStatus`), `RequestClass` (`class`), `RequestKind` (joins
            `kind`) and `TagInfoType` (`types`). New value lists: `model.WorkStatuses()`,
            `store.ImageStatuses()`, `httpx.Classes()`, `httpx.Kinds()`; tag info types are
            checked against `config.TagInfoTypes`.
          - A request error's time is `requested_at`; durations are `_ms`.
          - Seed gains a heartbeat and a failed request with sentinel req id, path and reason.
          - OpenAPI: `SourceState` became a shared schema.
      - [x] **3c-3. Refactor: type-scoped enforcement** (no change to valid responses). Keyed
        by JSON key alone, enums sharing a key were merged (`status`: health ∪ work ∪ image;
        `kind`: job ∪ source ∪ request; `state`: source ∪ job), so one type's value passed in
        another's field. Two PRs: `redact` first (additive), then the API switch.
        - `redact.NewEnforcer` compiles each registered type into a schema tree (object node:
          key → field; array node: element; string node: its own enum set, an `_at`/`_on` rule
          or hashed; opaque node for self-marshaling types like `time.Time`). Struct nodes are
          shared per type, so recursive types work.
        - `Enforcer.Enforce(mode, reflect.Type, body)` walks the body against that tree: keys
          per struct (`*UnknownKeyError`), JSON kinds per field (`*SchemaError`, Go field path
          only), each string by its own field's rule. `Result.OffEnum` lists Go field paths
          (`dto.Health.Status`), logged as `fields`.
        - `Registry.RegisterFieldEnum(dto.Version{}, "version", v)` replaces
          `RegisterEnum("version", v)`. The key-only mode, `RegisterEnum` and the typed/plain
          key-mixing rules are gone: a key may be an enum in one struct and free text in another.
        - `RegisterTypes` also rejects byte slices and arrays (base64), json tag options other
          than `omitempty`/`omitzero`, and a key appearing twice in one struct via embedding.
        - `respond` records the body's type on the enforcer's `bufferWriter` (found through
          `Unwrap`); JSON without one is refused (500). Middleware problems go through
          `respondError`, so they are typed too; `writeProblem` passes `dto.Problem`.
        - Tests: per-field enums and per-struct keys, `TestEnumScopedToField` (every other dto
          enum value in `Health.status` is hashed), untyped and unregistered responses refused,
          and two fuzzers whose oracle reads allowed values from the Go type by reflection
          (structured random values marshaled with json/v2, and raw JSON).
- [x] **3d. Refactor: RFC 9457 problems.** The first problems had no `type` (so implicitly
  `about:blank`, whose `title` should be the status phrase; ours weren't), stopped at one bare
  `field`, and had no `detail`. As built:
  - `dto.Problem{type, title, status, detail, errors}` and `dto.ProblemError{detail, parameter}`.
    `dto.ProblemType` (URI enum, `ProblemTypeBase` + slug) replaces `ProblemCode`; the
    type → `{Status, Title}` table stays in `dto/problem.go`. Titles are status-phrase style
    ("Not Found", "Validation Error").
  - Our own 16 types, documented one `## <slug>` section each in `docs/api/problems.md` (a
    test checks the sections match the types). Invalid, unknown and repeated query/path
    parameters are 422 `validation-error` (was 400 `invalid_param` / `unknown_param` /
    `duplicate_param`); a malformed query string or too many parameters stay 400 `bad-request`.
  - `api.Error{Type, Status, Detail, Errors []ParamError}`; helpers `errType`, `errDetail`,
    `errParam`. The detail catalogue is `api/details.go`: `newDetail` at package init only,
    `oneOf(enum)` for "must be one of: …". `newEnforcer` registers the catalogue as the only
    values of `Problem.detail` and `ProblemError.detail`; anything else there is hashed.
    `q.int`, `q.enum` and `parseInt` take the detail describing their range.
  - An unknown parameter's error item names it only when it is a declared `ParamName` (e.g.
    `page` on a route without paging); otherwise it has no `parameter`.
  - `dto.ParamName` holds query and path names only.
- [x] **3e. Request validation.** Validation functions per request type that collect every
  error (not first-fail) into `errors`; query-parameter checks collect too. Body errors carry
  `pointer` (RFC 6901 JSON Pointer), checked by the enforcer against the request DTO's Go
  type (each segment a json key of the struct at that level, or an index into a slice), so
  nested keys and array items need no list. `ao3url` returns typed reasons, each with its own
  catalogue message (the CLI's URL hints). As built:
  - `api.validation` collects `ErrorItem{Param | Pointer, Detail}`; `err()` is nil or one
    422. `params` (from `runtime.ParseParams`, replacing `parseQuery`) embeds it: unknown and repeated
    names are recorded in name order, and the getters (`int`, `enum`, `page`, `pathID`)
    record and return zero values. Handlers read every parameter, then return `q.err()`;
    routes without parameters call `noParams(r)`. `page` skips the offset-cap check when
    `per_page` is itself invalid. Body validation (from stage 4): a `validate<Request>`
    function per request DTO taking the handler's `*validation`, recording members with
    `member(d, "filters", "2", "tag_id")` → `/filters/2/tag_id`.
  - `redact.Registry.RegisterPointerField(dto.ProblemError{}, "pointer", requestTypes...)`:
    a pointer passes unhashed only when it walks a request type (keys of the struct at each
    level, canonical indices < 1e6 into slices); anything else is hashed and listed in
    `Result.OffEnum` (logged as a bug). Request types follow the `RegisterTypes` rules. With
    no request types (until stage 4 adds `dto.SourceAddRequest` to `api.requestTypes`) every
    pointer is hashed. Unit tests and a fuzzer in `redact/pointer_test.go`.
  - `ao3url.Parse` errors are `*ao3url.URLError{Reason}` (still `errors.Is` `ErrNotAO3` /
    `ErrUnsupported`); `Reason.Hint()` is the fixed advice, also the end of the message.
    `ao3url.Reasons()` is checked against the constants by a source-parsing test.
    `api.urlDetails` makes one catalogue detail per reason; `urlDetail(err)` finds it.
  - `checkText` became `validText(s, maxRunes) bool` (the caller picks the detail).
- [x] **3f. Refactor: durations as `dto.Duration`.** As built:
  - `dto.Duration` (`type Duration time.Duration`, `MarshalText`/`UnmarshalText`; a bad input
    error doesn't quote it). `redact.Duration` (`encoding.TextMarshaler` plus
    `Duration() time.Duration`, an int64 kind) is matched structurally like `Enum`: such a
    field is a JSON string that passes unhashed only when `time.ParseDuration` accepts it and
    `String()` gives it back exactly (so `"6h"` from a client would be hashed in a response;
    responses only write `String()`). A plain `time.Duration` field is a registry error.
  - Renamed (API change): `Source.poll_interval_s` → `poll_interval`, `Estimate.duration_s` →
    `duration`, status `pacer.interval_ms` → `interval`, `request_errors[].duration_ms` /
    `waited_ms` → `duration` / `waited`. OpenAPI has a shared `Duration` schema.
- [x] **3g. Refactor: shared `internal/web/api/problem` package** for the detail catalogue and the
  validation collector, so request types can live in their own package (stage 4). As built:
  - `problem` imports only `dto`, `ao3url` and the standard library. It holds `Detail`
    (`Message()`), `NewDetail`, `OneOf` and `Catalogue()` (every message from any package,
    sorted, deduplicated); `Error{Type, Status, Detail, Errors []Item}` with `Problem()`, and
    the constructors `New(t)`, `WithDetail(t, d)` and `ForParam(name, d)` (were
    `errType`/`errDetail`/`errParam`); `Validation` with `Param`, `Member`, `Len` and `Err`;
    `ValidText`; and the details request types share: `DetailInvalidRequest`, `DetailText` and
    `URLDetail(err)` (the per-`ao3url.Reason` hints).
  - `api/details.go` keeps the api package's own details, made with `problem.NewDetail`;
    `newEnforcer` registers `problem.Catalogue()`. `params` embeds `problem.Validation`, so
    handlers return `q.Err()`. `problemFor` (store and controller errors → problems) stays
    in `api`.
  - No API change.
- [x] **4. Add source (public)**
  - `POST /api/v1/sources` with body `{url, order?, priority?, poll_interval?}` returns
    202 + `{job_id}` + `Location: /api/v1/source-adds/{job_id}`. A single-work URL returns 201.
  - Uses `QueueSourceAdd`. `confirm` is **never** accepted publicly: large sources wait for
    `source confirm`.
  - URL ≤ 2048 bytes, validated by `ao3url.Parse`.
  - Has its own strict rate limit.
  - As built:
    - New package `internal/web/api/request` (imports `dto`, `problem`, `ao3url` and the standard
      library; never `controllers`/`store`/`model`): the `Request` interface
      (`Validate(*problem.Validation)`, pointer receivers) and `Types()`, which
      `api.requestTypes` is, so every listed type's pointers pass the enforcer (a test checks
      each implements `Request`). `decodeJSON(r, dst request.Request, v)` decodes strictly and
      then calls `dst.Validate(v)`, so a handler can't skip validation; an unreadable body
      (415/413/400) is returned at once, before any parameter error. `convert.AddSourceOptions`
      maps the validated body to `controllers.AddSourceOptions`.
    - Body `request.SourceAdd`: `url`, `order` (`dto.Order`), `priority` (-1000..1000),
      `poll_interval` (a `dto.Duration`; absent = config default, else 10m..720h) and
      `confirm`. `Validate` records every invalid member, so query and body errors come back in
      one 422. The url must be 1..2048 bytes, pass `problem.ValidText` and parse with
      `ao3url.Parse`; a rejection gets its reason's detail (`problem.URLDetail`) at `/url`,
      which restores the CLI's hints. `confirm: true` is a validation error at `/confirm` on the
      public listener (checked by the handler, as it depends on the listener) and accepted on
      the admin socket, so stage 6's `source add --confirm` needs no new route.
    - Route patterns are constants in `api/routes.go`; `pathFor(route, ids...)` fills their
      `{param}` segments for `Location` (panics on a count mismatch; a test matches the paths
      against the router).
    - 202 body `dto.SourceAddQueued{job_id, created}`; `created` is false when the same add was
      already queued (the first add's options win). 201 body `dto.SourceAddResult` (the
      `result` of `/source-adds`, now a shared schema) with `Location: /api/v1/sources/{id}`.
    - A duplicate source is 409 `already-exists` with the existing source as `Location` (set by
      `respondError` from `controllers.ExistsError`), so the CLI can still print its id.
    - Rate limit: `api.rate.source_add_per_hour` (`Options.SourceAddPerHour`, ×10 on the
      socket), an hourly httprate limiter on the route only, on top of the general one.
    - Leak test samples gain a `body`; the POST sample's url holds a sentinel tag name, and a
      hostile body route request expects 400 `invalid-body`. Rejected sentinel URLs are checked
      absent from the problem and logs. The test controllers get `config.Default()`'s sources
      options and interval.
- [x] **5. Admin endpoints (socket only)**
  - `POST /sources/{id}/confirm|pause|resume|cancel|refresh` (refresh body `{full}`),
    `PATCH /sources/{id}` (settings), `DELETE /sources/{id}`.
  - `GET /debug/jobs/{id}`, `/debug/works/{id}`.
  - A test asserts these return 404 on the public listener.
  - Split into three PRs:
  - [x] **5a. Source state actions.** As built:
    - `POST /sources/{id}/confirm|pause|resume` and `DELETE /sources/{id}` return 204 with
      no body (`noContent()`; `handle` writes the status alone, and the enforcer passes the
      empty body and sets no `Content-Length`). `POST /sources/{id}/cancel` returns 200
      `dto.SourceCancelled{dropped_jobs}`. The controllers are unchanged; a client that
      wants the new state GETs the source.
    - A state the change does not allow is 409 `state-conflict` (`store.ErrStateConflict`);
      an unknown source is 404.
    - On the public listener the action paths are 404, but `DELETE /sources/{id}` is 405
      (`Allow: GET`), as the path exists there with GET only.
    - `onlyPathID(r, name)` parses a route taking one path ID and no query params.
    - Leak test samples gain `fresh` (each request goes to a new API over a freshly seeded
      store, since the route changes state) and `before` (POSTs sent first, e.g. a pause
      before a resume).
  - [x] **5b. Refresh + settings:** `POST /sources/{id}/refresh` (body `{full}`) and
    `PATCH /sources/{id}` (body `{poll_interval?, priority?}`), with request types. Refresh of a
    source in the wrong state must become 409: `refreshSource` returns a plain error today
    (500), so wrap `store.ErrStateConflict` there. As built:
    - `refreshSource` wraps `store.ErrStateConflict` (409 `state-conflict`); the CLI message is
      unchanged apart from the prefix.
    - Refresh: body `request.SourceRefresh{full}` (required, `{}` is a plain refresh) → 200
      `dto.SourceRefreshed{poll_now, requeued_works, rescan_started, unblocked_parser}`.
    - Settings: body `request.SourceSettings{poll_interval, priority}` → 204. An absent member
      keeps its value (`priority` is a `*int`, so `0` sets it and `null` keeps it), and `{}` is
      a no-op 204 (the CLI keeps its own "give --poll and/or --priority" check). Not
      `application/merge-patch+json`: null does not remove anything here.
    - The priority and poll_interval bounds and details are shared with `SourceAdd`
      (`request/source.go`); the poll_interval detail no longer says "(the default)".
    - On the public listener refresh is 404 and `PATCH /sources/{id}` 405 (`Allow: GET`).
  - [x] **5c. Debug:** `GET /debug/jobs/{id}`, `/debug/works/{id}` (DTOs for job events,
    request rows and the work diagnostics), in two PRs:
    - [x] **5c-1. Refactor: typed job event details** (no API or stored-format change). The
      `controllers.Detail` map is gone: each job kind's handler returns its own struct
      (`PollDetail`, `BackfillDetail`, `WorkDetail`, `TagInfoDetail`, `SeriesInfoDetail`,
      `SourceAddDetail`, all `JobDetail`), and the scheduler fills `JobOutcome` (attempt,
      target, ms, error, next_state, not_before). Every field is a pointer with `omitzero`, so a
      stored detail holds exactly the fields that were set, zeros included.
      - `store.JobEvent` takes parts and stores them merged into one flat object
        (`store.JobDetailJSON`; a key in two parts is an error). The json keys are today's, so
        older events still decode.
      - `controllers.DecodeJobEvent(kind, raw)` decodes the same object twice: into
        `JobOutcome` and into the kind's detail. The source add result reads `SourceAddDetail`.
      - A test checks that every kind has a detail type, every field is a pointer or slice, and
        no detail key is also a `JobOutcome` key.
    - [x] **5c-2. Debug routes**, with the event detail as typed fields. As built:
      - `GET /debug/jobs/{id}` → `dto.JobDebug{job, dedupe_key, events, requests}`. An event is
        `{occurred_at, event, outcome, <kind>}`: `event` is the enum `JobEventType` (from the
        new `model.JobEvent*` constants and `model.JobEvents()`, which the scheduler now uses),
        `outcome` is `dto.JobOutcome` and `<kind>` (`poll`, `backfill`, `work`, `tag_info`,
        `series_info`, `source_add`) is the job kind's detail, the other kinds omitted. Every
        outcome and detail field is present, `null` when the run did not set it. Enum-valued
        fields are typed (new enums `TagType` and `ContentChange`, matched against
        `model.TagTypes()` and `store.ContentChanges()`); `error` splits into `error_code` and
        `error_detail` like a job's last error; `ms` becomes `duration`, `not_before` and
        `retry_at` timestamps, the window `_on` dates. Free text (`target`, `req_id`,
        `meta_changed`, `error_detail`) and the dedupe key are hashed.
      - `controllers.DebugJob` returns decoded events (`controllers.JobEvent{Outcome, Detail}`,
        via `DecodeJobEvent`); `ao3mirror debug job` prints them re-merged, as stored.
      - `GET /debug/works/{id}` → `dto.WorkDebug{work, jobs, requests}`, `work` a
        `dto.WorkDiag` of numbers, `_on` dates, `_at` times, states and stored row counts
        (`rows`); no content. `controllers.WorkDebug` gains the work `ID`. Unknown work → 404
        with the new detail `detailNoWork`.
      - `dto.RequestError` is renamed `dto.Request` (same JSON; OpenAPI schema `Request`): the
        debug routes list every request, not only failed ones.
      - The seed adds work 7 (from a blurb, every text field a sentinel), an ok request for it
        and events on the backfill job; the backfill job's dedupe key is a sentinel.
      - On the public listener both paths are 404.
- [x] **6. `internal/apiclient` + CLI conversion**
  - 6a: client (unix-socket transport, problem+json errors) and `source *` commands, including
    `add --wait` polling `/source-adds/{job}`. Split into three PRs:
    - [x] **6a-1. Refactor: route patterns in `internal/web/api/route`.** As built: the route
      constants and `pathFor` (now `route.Path(pattern, ids...)`, same panics) move out of
      `internal/web/api` into a stdlib-only package, so the client builds its paths from the
      same patterns the router registers. No API change.
    - [x] **6a-2. `existing_source_id` on a failed source add.** A source add that fails
      because the source exists (`source_exists:<id>`) gives the id as a number on
      `dto.SourceAdd`, since `last_error_detail` is hashed. (The synchronous 409
      `already-exists` already carries `Location`, from stage 4.) As built: the controller
      owns the format, so `controllers.SourceAddOutcome` gains `ExistingSourceID`, parsed by
      `existingSourceID` (a malformed stored id is an error, not 0); `convert.SourceAdd`
      copies it and `source add --wait` uses it instead of cutting the error string itself.
    - [x] **6a-3. `internal/apiclient` + `source *` commands.** As built: `internal/apiclient`
      is generated by ogen from `docs/api/openapi.yaml` (`make generate`). `addSource` declares
      its 409 with `Location`, so `source add` names the existing source.
  - [x] 6b: `status`, `jobs`, `debug`.
    - CLI `--names` maps to the reveal param. It is still user-only (the guard already blocks it).
    - As built: the API commands share `cmd/ao3mirror/format.go` (problem+json errors shown as
      title, detail and invalid parts; a missing socket hints to start `serve`). `jobs` pages
      up to `--limit`. `run` and `fetch-work` stay direct and share `newAO3Client`.
  - [x] 6c: the `cmd/ao3mirror/main_test.go` end-to-end tests start an in-process `serve` (and
    `run`) on the test's data dir.
- [x] **6d. Refactor: integer job targets.** `jobs.target` is `TEXT` ("path or id") from the
  first schema, which planned `listing_page`, `verify` and `image` jobs. None was built: listing
  pages are `poll`/`backfill` jobs targeting a page number, images have their own worker and
  table, and a source add targets its `source_adds` row id so no names reach `jobs`. Every kind's
  target is an integer (page number; work, tag or series id; source add id), but each enqueue
  formats it with `strconv` and the scheduler's `atoi`/`atoi64` parse it back, dropping the
  error, so a bad target runs as 0. The API hashes it as free text, so `jobs` and `debug` need
  `--names` to show a work id. Two PRs:
  - [x] **6d-1. Store + model.** A meta migration (`0003` unless another lands first) rebuilds
    `jobs` with `target INTEGER NOT NULL` (copy-table pattern: SQLite cannot change a column's
    type), failing if a stored target is not an integer. `model.Job.Target` and
    `store.NewJob.Target` become `int64`; enqueues pass the number and the scheduler handlers
    use it directly (`atoi`/`atoi64` go). The start event's `JobOutcome.Target` becomes
    `*int64`. Dedupe keys are unchanged. No API change. As built:
    - `0003_job_target_int.sql` rebuilds `job_events` too: the migration runs in a transaction
      with foreign keys on (where `PRAGMA foreign_keys` is a no-op), so dropping `jobs` would
      cascade-delete every event. The copy references `jobs_new`, and the rename rewrites it.
    - `target INTEGER NOT NULL CHECK (typeof(target) = 'integer')`: the copy relies on INTEGER
      affinity, so `'12'` becomes 12 and anything else fails the CHECK (and the migration).
      Stored start events get their `target` rewritten to a JSON number.
    - `convert` formats the target as a string until 6d-2. A job with target 0 now shows `"0"`.
    - `migrateTo(ctx, db, name, upTo)` stops at a version, so tests can upgrade a 0002 db.
  - [x] **6d-2. API.** `dto.Job.Target` and the start event's `target` (`dto/debug.go`) become
    integers, copied by `convert`; `openapi.yaml` and `internal/apiclient` follow
    (`make generate`). The all-routes leak test drops the sentinel job targets (an integer
    cannot hold one). `jobs` and `debug` show plain targets without `--names`, which stays for
    the fields that are still strings. As built:
    - The seed's sentinel job targets were already gone in 6d-1 (the store took integers);
      the integration tests now assert the plain numbers.
    - OpenAPI `target` is `integer` (`[integer, "null"]` on the outcome), with no minimum.
- [x] **7. Store + controller: work detail**
  - Loaders for a work's creators, tags (with type), series parts (+ partial-series flag per
    `docs/plan.md`), collections, and chapter metadata (meta `chapters`).
  - Chapter bodies and skin come from content.db and are merged in Go (the two DBs are not
    attached).
  - Add a `work_skins` reader. Controller `GetWork(ctx, id)` with one read transaction.
  - As built:
    - `store.Work` returns a `WorkRow` (the works row plus `Status` and `LastFetched`; zero
      means known from blurbs only). New `ReadTx` readers in `store/workdetail.go`:
      `WorkCreators` (byline order), `WorkTags` (page order), `WorkCollections`, `WorkSeries`
      and `WorkChapters` (meta `chapters`); in `content.go`: `ChapterBody` (`ErrNoChapter`) and
      `WorkSkin` ("" for none). Refs carry local ids (`CreatorRef`, `TagRef`, `CollectionRef`)
      for the stage 12–13 routes.
    - Each `WorkSeriesPart` has a `SeriesCoverage` (fetched, source, total, mirrored, missing
      positions and work ids); `Partial()` is the `PartialSeriesList` rule, and both share
      `seriesMissing` and the `seriesIsSource` SQL fragment.
    - Controllers in `controllers/workview.go`, one read transaction each: `GetWork`
      (`WorkView`: row, relations, chapter list without bodies), `GetChapter(id, position)`,
      `GetWorkChapters` and `GetWorkSkin`. Unknown work → `store.ErrNoWork`; no body at a
      position (including blurb-only works) → `store.ErrNoChapter`. Bodies are merged with the
      chapter list by position, keeping every position in either (content.db commits first, so
      they can differ after a failed commit). The whole-work size cap is left to stage 8.
- [x] **8. Works read API**
  - `GET /works/{id}` (metadata, relations, chapter list).
  - `GET /works/{id}/chapters/{position}` (body, notes, end notes).
  - `GET /works/{id}/chapters` (entire work, size-capped).
  - `GET /works/{id}/skin`.
  - Every content string is in JSON, so it is hashed by default.
  - In three PRs:
  - [x] **8a. Refactor: no HTML escaping in the enforcer.** `respond` marshals with
    `encoding/json/v2`, which leaves `<`, `>` and `&` alone, but the enforcer re-quoted every
    string it kept with v1 `json.Marshal`, which escapes them as `<` (six bytes for one), so
    a revealed HTML chapter grew. It now quotes with `jsontext.AppendQuote` (minimal escaping,
    RFC 8785). Responses are JSON with `nosniff` and a `default-src 'none'` CSP, so the
    escaping protected nothing.
  - [x] **8b. Work + skin routes**: `GET /works/{id}` and `/works/{id}/skin` (public). As built:
    - `GET /works/{id}` → `dto.Work` (`convert.WorkView`): metadata, `stats` (`dto.WorkStats`,
      moved from `debug.go` and shared with `WorkDiag`), `creators` `{id, user, pseud}`, `tags`
      `{id, name, type}`, `collections` `{id, name, title}`, `series` and `chapters`
      (`dto.ChapterRef` `{id, position, title, published_on}`, no bodies). Optional strings
      (notes, language, dates) are omitted when empty; `last_fetched_at` is omitted for a
      blurb-only work, which has `chapters: []`.
    - Each `series` item carries the partial-series flag (`docs/plan.md`): `complete_locally`
      (`!SeriesCoverage.Partial()`), `works_total` (null until the series page was fetched),
      `works_mirrored`, `missing_positions`, `missing_work_ids`. `mirror_source_url` is left to
      the series route (stage 13).
    - `GET /works/{id}/skin` → `dto.WorkSkin{css}`, `css` omitted when the work has none (the
      enforcer allows only JSON in hashed mode, so the CSS is a hashed string, not `text/css`).
    - Unknown work → 404 `not-found` (`DetailNoWork`). The leak-test seed adds a fetched work
      (9) with chapters, a skin and a partial series (77), so `/status` now lists one partial
      series.
  - [x] **8c. Chapter routes**: `GET /works/{id}/chapters/{position}` and `/works/{id}/chapters`.
    The whole-work route adds up the raw content bytes first; over 16 MiB (a constant) is a new
    `work-too-large` problem (422) whose detail points to the per-chapter route. As built:
    - `GET /works/{id}/chapters/{position}` → `dto.Chapter` (`convert.Chapter`): `id`,
      `position`, `title`, `published_on`, the chapter's own `summary_html`, `notes_html`,
      `end_notes_html` (omitted when empty) and `body_html`. `position` is 1..`MaxPosition`
      (100000), read with the new `Params.PathInt` (`PathID` now calls it). No body at the
      position (including blurb-only works) → 404 `not-found` (`DetailNoChapter`).
    - `GET /works/{id}/chapters` → `dto.WorkChapters{chapters}` (`[]` for a blurb-only work).
    - The cap: `store.ChapterContentBytes` sums the uncompressed sizes recorded in the zstd
      frame headers (`substr(…, 1, 18)` of each blob, nothing decompressed). The encoder
      records the size for every frame of 256+ bytes; a frame without it counts as 255 (a
      test checks the bound against the real encoder). `GetWorkChapters` checks it in its read
      transaction before loading bodies: over `controllers.MaxWorkChaptersBytes` (16 MiB) is
      `controllers.ErrWorkTooLarge` → 422 `work-too-large` (`DetailWorkTooLarge`, naming
      `route.Chapter`).
- [x] **9. Store: work listing + filters**
  - One filter struct and query builder covering AO3 work_search equivalents:
    - include/exclude tag IDs (by type), rating/warning/category tag IDs;
    - complete, crossover, language, words/kudos/hits ranges, date ranges, status;
    - sort column + direction.
  - Bounded `IN` lists and parameterized SQL only.
  - A new meta migration (`0004` unless another lands first) adds reverse indexes
    (`tag_parents(parent_id)`, `work_collections(collection_id)`, `series_creators(creator_id)`)
    and any sort indexes.
  - Deep pages use a deferred join: select the page's ids over an index
    (`WHERE id IN (SELECT id … ORDER BY <sort>, id LIMIT ? OFFSET ?)`), then load the wide
    work rows for those ids only. Every sort ends with `id` as a tie-breaker so pages are stable.
  - Benchmark at a realistic size (synthetic, e.g. 200k works): a deep page up to the offset
    cap, and the filtered `total` count, which may cost more than the offset.
  - In four PRs:
  - [x] **9a. Scalar filters, sorts and paging.** As built:
    - `model.WorkFilter` (`internal/model/work_filter.go`): `Complete` (tri-state), `SingleChapter`
      (AO3: one chapter expected), `Language` (exact), `Statuses` (any of, at most one each),
      `Fetched` (tri-state: full page fetched vs blurb-only), inclusive `IntRange`s for words,
      kudos, hits, comments and bookmarks, inclusive `DateRange`s (by day) for revised (AO3
      `date_from`/`date_to`) and published. A published or revised bound excludes works with no
      known date (blurb-only works have no published date).
    - Sorts (`model.WorkSort`, after AO3's `sort_column`): revised (default), published, words,
      hits, kudos, comments, bookmarks; `model.SortDirection` "" means the column's default
      (descending for all of these). Every order ends with `id` in the same direction.
    - `store.FindWorks` / `CountWorks` (`store/works_list.go`) return `WorkRow`s (relation slices
      empty) with a deferred join: `WHERE id IN (SELECT id … ORDER BY … LIMIT ? OFFSET ?)`.
      Status is matched as `+status` so the planner walks the sort index rather than
      `works_status` (most works are ok) and sorting every match. `TestWorksPagePlans` checks
      that no sort × direction × unindexed-filter combination sorts in a temp b-tree.
      Controller `ListWorks` (page + total in one read transaction).
    - Meta migration `0004_work_listing.sql`: sort indexes for published, comments and bookmarks,
      and the reverse indexes above.
    - `BenchmarkWorksPage` / `BenchmarkWorksCount` at 200k synthetic works (100 per page, offset
      99,900): an unfiltered deep page takes ~1 ms; a filtered deep page walks the sort index
      and reads every row it skips, ~70–150 ms (worst when the filter matches fewer rows than
      the offset); first pages are under 2 ms; counts are at most ~11 ms. Stage 10's search rate
      limit bounds the deep-page cost.
  - [x] **9b. Tag filters.** Include/exclude tag ids, by type (rating, warning, category, fandom,
    relationship, character, freeform, other). A tag id matches like AO3's `filter_ids`: the
    tag, its synonyms (`merger_id`) and works tagged with any of its sub tags (`tag_meta`,
    transitive). Bounded id lists; reverse indexes for `tags(merger_id)` and `tag_meta(meta_id)`.
    As built:
    - AO3 folds its per-type lists (`rating_ids`, `fandom_ids`, …) into one `filter_ids` list
      (`app/models/search/taggable_query.rb`), so `model.WorkFilter` has one `Tags` list (every
      id must match) and one `ExcludeTags` list (none may match), each at most
      `model.MaxWorkFilterTags` (20). Per-type request fields, if any, are stage 10's.
    - Matching (`store.tagFilterWorks`): works tagged with the id, any sub tag below it
      (`tag_meta`, a recursive CTE; `UNION` ends a cycle) or a synonym of either. That is AO3's
      index set (a work's tags, their filters and every meta tag above them,
      `app/models/concerns/filterable.rb`) read from the tag side. A synonym id matches only
      works tagged with it, as on AO3. Each included id is its own `id IN (…)`; all excluded ids
      share one `id NOT IN (…)`.
    - Meta migration `0005_work_tag_filters.sql`: `tag_meta(meta_id)`, a partial
      `tags(merger_id)`, and `work_tags(tag_id, work_id)` replacing `work_tags_tag` so the work
      ids are read from the index alone. `TestWorksTagFilterPlans` checks the expansion uses
      them.
    - An included tag drives the page from its works and sorts them (a temp b-tree), so the
      cost grows with the number of matches, not the offset. At 200k works: a tag on a third
      of them takes ~25 ms for the first page and ~50 ms for a deep one, a tag on 10% (a meta
      tag over 100 subs and their synonyms) ~10–17 ms, and a rare tag under 1 ms; counts are
      ~0.2–30 ms, ~55 ms for excluding the popular tag. Walking the sort index instead (a
      correlated `EXISTS`) would make popular first pages cheap but scan the whole index for
      rare tags, so it was left out.
  - [x] **9c. Crossover.** AO3's rule (`lib/fandom_crossover.rb`): a work is a crossover when no
    fandom on it shares a meta tag ancestor (after mergers) with all the others. Too costly to
    work out per query, so a precomputed `works.crossover` column, recomputed on work upsert
    and when tag info changes a fandom's merger or meta tags. As built:
    - `model.WorkFilter.Crossover` (tri-state, AO3 `crossover` T/F), matched as
      `crossover = ?` with no index (like `complete`; `TestWorksPagePlans` covers it).
    - The rule is Go (`store/crossover.go`): `isCrossover` mirrors AO3's loop over each
      fandom's group (the fandom and every meta tag above it, a cached breadth-first walk up
      `tag_meta`); `updateCrossover(sel)` loads the selected works' fandoms (mergers applied)
      and writes only rows whose value changed. A set-based SQL version was tried first and
      dropped: its correlated pair check rebuilt automatic indexes per row (2.5 s for a
      popular fandom at 200k works).
    - Recomputed on every upsert that rewrites tags, and by `UpdateTagInfo` for the works a tag
      filter on the tag matches (`tagFilterWorks`), only when a Fandom tag's type, merger, meta
      tags or merged synonyms changed. Most tag info refetches change nothing and cost nothing.
    - Meta migration `0006_work_crossover.sql` adds the column; its backfill is a Go step
      (`migrationSteps` in `store.go`, run in the migration's transaction).
    - `BenchmarkCrossover` at 200k works: one work ~0.05 ms, a rare tag ~0.5 ms, a meta tag
      over ~10% of works ~55 ms, a fandom on a third of works ~150 ms, the full backfill
      ~0.3 s.
  - [x] **9d. Title and creator sorts.** Precomputed sort-key columns with indexes, after AO3's
    `sorted_title` and `authors_to_sort_on`. Default direction ascending. Hashed mode hides the
    text but the order still says a little about it (accepted). As built:
    - `model.WorkSortTitle` ("title") and `WorkSortCreator` ("creator");
      `WorkSort.DefaultDirection` is ascending for these two and descending for the rest.
    - The keys are Go (`store/sortkeys.go`), ported from `app/models/work.rb`. Title: lowercase,
      drop one leading `"`, `'`, `.` or `/`, move a leading "a "/"an " then "the " to the end
      ("the x" → "x, the"), and a key starting with a digit is left-padded with zeros to 5
      characters. That is Ruby's `rjust(5, "0")` on the whole key, so "12" → "00012" while
      "7 days" stays as it is. Creator: "Anonymous" (capitalised, so it sorts first) for an
      anonymous work, else the pseud names ordered like `Pseud#<=>` (lowercased name, then
      login), joined by ",  ", lowercased, with one leading `+-=_?!'"./` removed. Compared
      byte-wise like AO3's Elasticsearch keywords (SQLite BINARY). Go's simple lowercasing differs
      from Ruby's full case mapping only for a few special casings (accepted).
    - `updateSortKeys(sel)` recomputes from the stored title, anonymity and creators and writes
      only changed rows. It runs on every upsert that rewrites listing metadata. Creators are
      immutable rows (login + pseud), so nothing else can change a key.
    - Meta migration `0007_work_sort_keys.sql`: `title_sort`, `creator_sort` and an index on
      each (`TestWorksPagePlans` covers both sorts). Its backfill is a Go step.
    - `BenchmarkSortKeys` at 200k works: the backfill (clear, compute and write every row in one
      transaction) ~2.3 s, recomputing with keys current ~0.4 s, one work ~0.09 ms.
- [x] **10. Works list + QUERY API**
  - `GET /works?page&per_page&sort` (simple listing).
  - `QUERY /works` with the body filter object (structured fields only in this stage).
  - Search rate limit.
  - In three PRs:
  - [x] **10a. Store + controller: work blurbs.** `FindWorks` rows carry no relations, and
    loading them per work would be ~300 queries a page. As built:
    - Batch readers in `store/workdetail.go`: `WorksCreators`, `WorksTags` and `WorksSeries`
      take at most `store.MaxWorkBatch` (100, one page) work ids and return each relation by
      work id, one `IN (…)` query each over the `(work_id, …)` primary keys / `series_works_work`.
      The one-work readers (`WorkCreators`, `WorkTags`, `WorkSeries`) call them with one id, so
      the order rules live in one place.
    - `store.SeriesPart` (id, title, position) is a series membership without coverage;
      `WorkSeriesPart` embeds it and adds `Coverage` (the work view only).
    - `controllers.ListWorks` returns `[]WorkBlurb` (row, creators, tags, series; what AO3's
      blurb shows, so no collections) and the total from one read transaction. A limit over
      `MaxWorkBatch` is an error.
  - [x] **10b. `GET /works?page&per_page&sort&direction`** → `dto.WorkList{items, page, per_page,
    total}` of `dto.WorkBlurb`, with `dto.WorkSort` / `dto.SortDirection` enums. As built:
    - The page is `dto.List[dto.WorkBlurb]` (`WorkList` in the OpenAPI spec), like the job and
      source lists. No filters: every stored work, any status.
    - `dto.WorkBlurb` (`convert.WorkBlurb` / `WorkBlurbList`): the work view's fields minus
      notes, end notes, collections and the chapter list; `series` items are
      `dto.WorkSeriesPart` (id, title, position; no coverage).
    - `sort` and `direction` are new `dto.ParamName`s, read with `Params.Enum`
      (`DetailWorkSort`, `DetailDirection`). A convert test keeps the dto enums equal to
      `model.WorkSorts()` and the model directions; the integration test checks
      `runtime.MaxPerPage` ≤ `store.MaxWorkBatch`.
  - [x] **10c. `QUERY /works`**: a `request.WorkQuery` body with the stage 9 filters, sort and
    direction; `page` / `per_page` stay query params, as on `GET`. `language` is free text (the
    stored AO3 display name), so it is reveal-only (400 `reveal-required` in hashed mode). The
    search rate limit (`search_per_min`) and `Accept-Query: application/json`. As built:
    - `request.WorkQuery`: `tag_ids` / `exclude_tag_ids` (one list each, as in 9b; at most 20
      distinct ids of 1..2^53-1), `complete`, `crossover`, `fetched` (absent = either),
      `single_chapter`, `language`, `statuses` (each at most once), `words` / `kudos` / `hits` /
      `comments` / `bookmarks` as `{min, max}` (0..2^31-1, min ≤ max), `revised` / `published`
      as `{from, to}` (`YYYY-MM-DD`, from ≤ to), `sort`, `direction`. `{}` is the `GET` listing.
      `convert.WorkFilter` maps it to `model.WorkFilter`; a test keeps the tag cap equal to
      `model.MaxWorkFilterTags`.
    - The handler validates first (one 422 listing parameters and body pointers), then
      `WorkQuery.NeedsReveal` with the new `runtime.Revealed`: 400 `reveal-required`
      (`DetailFreeText`). Stage 11's `q` joins `NeedsReveal`.
    - `runtime.SearchRateLimit` (new `Options.SearchPerMin`, admin socket 10x) is built once per
      listener in `registerRoutes`, so every QUERY search route shares one budget.
    - `runtime.AcceptQuery` sets `Accept-Query: application/json` on every `/works` response
      (`GET` and `QUERY`), besides the existing 415 case. chi v5.5.0 routes `QUERY` natively
      (`r.Query`), and ogen v1.25.0 generates the OpenAPI 3.2 `query` operation
      (`apiclient.QueryWorks`).
- [x] **11. Full-text search**
  - Add `q` to the QUERY body (reveal-only). Sanitized FTS5 MATCH joined with the stage 9
    filters; total count; ranking.
  - Replaces the unused `SearchWorkIDs`.
  - In two PRs:
  - [x] **11a. Store: search query and ranking.** As built:
    - `model.WorkFilter.Query` is search text. `searchquery.Terms` (`model.SearchTerms` until
      11b) splits it into a subset of AO3's query syntax: words, `"quoted phrases"` (an unclosed quote runs to the end) and a
      leading `-` to exclude a word or phrase; every other term must match (AO3's
      `default_operator: AND`). AND, OR, `field:value`, wildcards and the like are plain words.
      A term with no letter or digit is dropped. At most `searchquery.MaxTerms` (16).
    - `store.ftsMatch` builds the MATCH expression: each term a quoted FTS5 string (so FTS5
      tokenizes it into a word or phrase and no user text is syntax), included terms joined
      with AND, excluded ones removed with `NOT (… OR …)`. No included term, or too many terms,
      is an error.
    - A query is one more `workWhere` condition (`id IN (SELECT rowid FROM works_fts WHERE
      works_fts MATCH ?)`), so it combines with every stage 9 filter and `CountWorks` counts it.
    - `model.WorkSortBestMatch` ("best_match", AO3 `_score` / "Best Match") ranks by
      `-bm25(works_fts, 7, 1, 5, 1, 1)`: AO3's field boosts (`title^7`, `creators^5`, summary,
      tags and series 1; `app/models/search/work_query.rb`). It is the default sort when there is a query
      (`WorkFilter.SortOrDefault`), as on AO3, defaults to descending (best first) and ends with
      `id` like the others. It needs a query, so it is not in `model.WorkSorts()` (the sorts for
      any listing); without one it is an error.
    - AO3 also searches notes and end notes; `works_fts` does not index them (they are only in
      the compressed content database; accepted). Series titles are 11a2.
    - A search page is driven by the match (`TestWorksSearchPlans`: works read by rowid, no
      scan, the matches sorted), so the cost grows with the matches. `BenchmarkWorksSearch` at
      200k synthetic works: a word in every work ~160 ms for a ranked first page (~210 ms at
      offset 10,000) and ~40 ms by revised date, a third of works ~70 / ~25 ms, a tenth ~27 /
      ~10 ms, a rare word under 1 ms; counts ~0.1–40 ms. The search rate limit bounds it.
  - [x] **11a2. Series titles in the search index**, as on AO3 (`series.title`). As built:
    - Meta migration `0008_works_fts_series.sql` rebuilds `works_fts` with a `series` column
      (FTS5 cannot add one); its Go step (`backfillFTS`) indexes every work. Series titles get
      weight 1 in `best_match` (`bm25(…, 7, 1, 5, 1, 1)`).
    - `store.updateFTS(sel)` (`store/fts.go`) builds rows from the meta tables, not the upserted
      `model.Work`, and writes only rows that changed, like `updateSortKeys`. It runs on a work
      upsert (the work, plus every work in a series the upsert inserted or renamed), on
      `UpsertSeries` (the series' works) and on `SetSeriesMembers` (current and dropped members).
    - `BenchmarkFTS` at 200k works: the backfill ~3 s, one work ~0.15 ms, a 20-work series ~2 ms.
  - [x] **11b. API: `q`.** `q` in `request.WorkQuery` (reveal-only, joins `NeedsReveal`; at most
    256 runes and 16 terms, at least one included term: 422 otherwise), `best_match` in
    `dto.WorkSort` (422 without `q`, on `GET` too), OpenAPI and the regenerated client. As built:
    - The tokenizer moved from `model` to the stdlib-only `internal/searchquery` (`Terms`,
      `Term`, `MaxTerms`), so `request` validates with the store's rules without using `model`.
    - `WorkQuery.Validate` records one `/q` error for a q over `MaxQueryRunes` (256) or with a
      control character (`detailQuery`), or with too many terms or none to include
      (`detailTerms`), so a valid body never makes the store refuse the search.
    - `dto.WorkSortBestMatch` is in the enum; `request.DetailBestMatch` is the 422 for it without
      `q`, at `/sort` in the body and on the `sort` parameter of `GET` (which has no `q`).
    - The all-routes leak sample stays without `q` (it must succeed hashed);
      `TestQueryWorksSearch` searches the seeded works revealed and checks q is never logged.
- [x] **12. Tags**
  - Store readers for parents, meta/sub tags, synonyms/merger, and work counts.
  - `GET /tags/{id}`; `GET` + `QUERY /tags/{id}/works` (stage 9 filters scoped to the tag and,
    for canonical tags, their synonyms).
  - A tag by name, for AO3's `/tags/{name}` and `/tags/{name}/works` URLs (reveal-only).
  - `QUERY /tags` (type filter; `name` is reveal-only).
  - `GET /media` and `/media/{id}/fandoms` (AO3 fandom browse).
  - In five PRs:
  - [x] **12a. Store + controller: tag view.** As built:
    - `store.TagInfoByID` (and `TagByName`, sharing one reader) return `TagInfo` with the new
      `CanonicalKnown`: `canonical` is NULL until the tag page is read, or until the tag is
      merged as a synonym (set to 0 then).
    - `store.RelatedTags(id, rel, limit)` for `TagParents`, `TagChildren`, `TagMetaTags`,
      `TagSubTags` (direct only) and `TagSynonyms`: `TagGroup`s (type, total, tags) in
      `model.TagTypes()` order, as AO3's tag page groups them, each cut to `limit` (at most
      `store.MaxTagGroup`, 100) tags by name (case-insensitive), then id. One window query per
      relation numbers and counts each type, so a fandom with thousands of children costs one
      read. Every relation reads an index on the tag's side (`TestRelatedTagsPlans`; the
      `tag_parents(parent_id)` index is 0004's).
    - `controllers.GetTag` / `GetTagByName` return a `TagView`: the tag, its merger (`TagRef`),
      the five relations, and `Works`, the stored works (any status) a stage 9 filter on the tag
      matches (`CountWorks`), so it equals the `/tags/{id}/works` total. One read transaction.
  - [x] **12b. `GET /tags/{id}` and `GET /tags/by-name/{name}`**: `dto.Tag`, `convert.Tag`, leak
    samples, OpenAPI and the client. `{name}` is AO3's path form (`*s*` for `/` and so on,
    `ao3url.TagFromParam`); it is free text, so reveal-only (400 `reveal-required` hashed) and
    bounded in runes. As built:
    - `dto.Tag`: `canonical` is null until recorded, `merger` a `TagRef` or null, the five
      relations as `TagGroup`s (`type`, `total`, `tags`), `works_count`, `info_fetched_at`.
      `TagType` moved to `dto/tag.go`. `convert.TagView` maps `controllers.TagView`.
    - `Params.PathTagName` decodes the name: percent escapes only when chi routed on
      `RawPath` (an escaped `/`), then `ao3url.TagFromParam`; 1 to 256 runes
      (`MaxTagNameRunes`; AO3 caps new names at 150), no control characters. Validation runs
      before the reveal check, so a bad name is 422 hashed too.
    - The access log logs only the path params in `runtime.PathIDParams` (`id`, `job_id`,
      `position`); any other, like `name`, is never logged, not even as invalid (a numeric tag
      name would otherwise be logged as an id). `TestPathParamsDeclared` fails on a route
      param that is neither an id nor a declared free-text name, and the by-name route is
      checked refused before its handler too (405, 414).
    - `store.ErrNoTag` is 404 with `DetailNoTag`. The leak test's samples gained
      `revealStatus` (the by-name sample is 400 hashed, 200 revealed); the seed reads tag info
      for the fandom tag (`seedTag`: canonical, a parent, a meta tag, a synonym).
  - [x] **12c. `GET` + `QUERY /tags/{id}/works`**: the works list and query handlers with the
    path tag as one more required tag filter. As built:
    - `controllers.ListTagWorks(tagID, f)`: in one read transaction, `store.ErrNoTag` (404) for
      an unknown tag, then `ListWorks`' page and total with the tag added to `f.Tags` (not
      repeated if the body lists it). It matches like any tag filter (synonyms, sub tags), so
      the total equals the tag view's `works_count`.
    - `model.MaxWorkFilterTags` is 21: the body's 20 `tag_ids` plus the path tag (the convert
      test checks it is `request.MaxWorkQueryTags+1`).
    - The `/works` handlers' bodies became `listWorks` / `queryWorks` (shared with the tag
      routes); a bad path id is reported with the other invalid params. Same `WorkList`
      response, `Accept-Query`, and the QUERY route shares the search rate limit.
  - [x] **12d. Store + `QUERY /tags`**: `store.FindTags` (type, canonical and parent filters,
    paging, name or use-count sort); `name` is reveal-only. In two PRs:
    - [x] **12d1. Store + controller: tag listing**, after AO3's tag search (`TagQuery`). As
      built:
      - `model.TagFilter`: `Name` (whitespace-separated words, each a case-insensitive
        substring of the name, ANDed like AO3's name query; at most `MaxTagNameWords`, 16),
        `Types` (any of), `Canonical` (false matches only a recorded non-canonical tag, not one
        never read), `ParentIDs` (each a direct parent, AO3's `fandoms` by id; at most 20),
        `Sort` (`name` asc by default, `uses` desc) and `Direction`. AO3's `created_at` sort
        is left out: the mirror knows when it first saw a tag, not when AO3 created it.
      - `store.FindTags` / `CountTags` (`store/tags_list.go`) return `TagRow`s (`TagInfo` plus
        `Uses`, the stored works tagged with the tag itself, as AO3's `uses`). A deferred join
        picks the page's ids first; by name it walks the `name` index, and a uses tie breaks
        by name ascending, then id, as on AO3. Name words are bound `LIKE … ESCAPE` patterns
        with `%`, `_` and `\` escaped. `MaxTagPage` (100) bounds the limit.
      - `controllers.ListTags` returns the page and total from one read transaction.
      - `BenchmarkTags` (seedWorks' ~2k tags on ~1M taggings plus 50k unused): a page by name
        under 5 ms, by uses ~20–35 ms (every match's uses is counted), counts under 3 ms.
    - [x] **12d2. `QUERY /tags`**: `request.TagQuery`, `dto.TagList`, convert, leak samples,
      OpenAPI and the client; the route shares the search rate limit. As built:
      - `request.TagQuery`: `name` (1 to 256 runes, 1 to 16 words, no control characters;
        free text, so `reveal-required` hashed), `types` (distinct `TagType`s), `canonical`,
        `parent_ids` (at most 20 distinct ids), `sort` (`dto.TagSort`: `name`, `uses`) and
        `direction`. Its caps equal the store's (`TestTagQueryBoundsMatchModel`), so a valid
        body never makes `FindTags` fail. `validateIDs` is shared with the work query's id lists.
      - Response `dto.List[dto.TagListItem]` (OpenAPI `TagList`): `id`, `name`, `type`,
        `canonical` (null until recorded), `merger_id` (null for none), `uses` and
        `info_fetched_at`. `convert.TagFilter` and `convert.TagList` map it.
      - QUERY only (no `GET /tags`): `Accept-Query`, the search rate limit, paging in the query
        string. The leak sample filters by type and sorts by uses.
  - [x] **12e. `GET /media` and `/media/{id}/fandoms`** over 12d, after AO3's `MediaController`
    and `FandomsController#index`. In two PRs:
    - [x] **12e1. Store + controller: media.** As built:
      - Media tags were never typed: tag pages stored parents as `Unknown` until seen on a work,
        and a medium is never on a work. AO3 gives a fandom only media parents
        (`Fandom#parent_types`; meta tags are listed apart), so `UpdateTagInfo` now types a
        fandom's parents `Media` (other tags' parents still start `Unknown`), and meta migration
        0009 types the `Unknown` parents of stored fandoms.
      - `controllers.ListMedia(offset, limit)`: a page of the stored media tags by name, each a
        `Medium` with its `MediumTopFandoms` (5, as AO3) most used fandoms and `FandomsTotal`,
        plus the total, in one read transaction. AO3 lists canonical media only; the mirror
        never reads a medium's tag page, so it lists every medium a fandom page named.
      - `controllers.ListMediumFandoms(mediumID, f)`: `ListTags` with the type `Fandom` and the
        medium as the parent, or `ErrNoMedium` (no tag, or not a media tag). AO3 lists canonical
        fandoms (all in use for Uncategorized Fandoms); this lists every fandom, and `canonical`
        tells them apart.
    - [x] **12e2. Routes**: `GET /media` (page, per_page) and `GET /media/{id}/fandoms` (sort,
      direction, page, per_page), DTOs, convert, `ErrNoMedium` as 404, leak samples, OpenAPI and
      the client. As built:
      - `GET /media` answers `dto.List[dto.Medium]` (OpenAPI `MediumList`): `id`, `name`,
        `canonical` (null until recorded, so in practice always null), `fandoms` (at most 5
        `TagListItem`s, most used first) and `fandoms_total`. `convert.MediumList` maps it.
      - `GET /media/{id}/fandoms` answers the `TagList` of `QUERY /tags`, through
        `convert.TagList`. `sort` is a `dto.TagSort` (`DetailTagSort`), `direction` a
        `SortDirection`; a bad id is reported with the other invalid params.
      - `controllers.ErrNoMedium` is 404 with `DetailNoMedium` (an unknown tag, or one of
        another type). Plain GETs: no QUERY form, so no `Accept-Query` and no search rate limit.
      - The seed's `seedMedium` (4) is `seedTag`'s parent, checked while seeding; both routes
        have leak samples.
- [x] **13. Series, creators, collections**
  - `GET /series/{id}` (with `complete_locally`, `works_total`, `works_mirrored`,
    `missing_positions`, `missing_work_ids`, `mirror_source_url`) and `/series/{id}/works`.
  - `GET /creators/{id}` and `/creators/{id}/works`; `QUERY /creators` (name is reveal-only).
  - `GET /collections/{id}` and `/collections/{id}/works`.
  - A creator is a pseud (`creators.id`, as the work view's `creators` items), after AO3's
    `/users/{login}/pseuds/{pseud}/works`; users grouping pseuds are not modelled. Series works
    are a plain `GET` in part order, as AO3's series page; creator and collection works are
    `GET` + `QUERY` with the stage 9 filters, as `/tags/{id}/works`.
  - In six PRs:
  - [x] **13a. Store + controller: series view.** As built:
    - `store.SeriesCreators` (by creator id: `series_creators` keeps no byline order),
      `SeriesWorks(id, offset, limit)` (the series' stored works, fetched or blurb-only, by part
      number with unknown parts last, then work id; at most `MaxWorkBatch`; the page is picked
      from `series_works` first) and `CountSeriesWorks`. Members with no works row are not
      listed. `seriesCoverage` is exported as `SeriesCoverage`. `TestSeriesWorksPlans` checks
      both read the `series_works` primary key.
    - `controllers.GetSeries` returns a `SeriesView`: the row, creators, coverage and `Works`
      (the stored works, so it equals the `/series/{id}/works` total; `Coverage.Mirrored`
      counts fetched works only). `ListSeriesWorks(id, offset, limit)` returns `WorkBlurb`s and
      the total. Both are one read transaction and return `store.ErrNoSeries` for no row; a
      series named by a work page but never fetched has a row (title only).
  - [x] **13b. Series routes**: `GET /series/{id}` (with `mirror_source_url`, the AO3 series URL
    for source add) and `GET /series/{id}/works` (page, per_page), DTOs, convert,
    `ErrNoSeries` as 404, leak samples, OpenAPI and the client. As built:
    - `GET /series/{id}` answers `dto.Series` (`convert.SeriesView`): `id`, `title`, `creators`
      (`WorkCreator`s by id), `summary_html` and `notes_html` (omitted when empty), `begun_on`
      and `updated_on` (omitted until fetched), `words`, `bookmarks`, `complete`, the coverage
      fields of `WorkSeries` (`worksTotal` is now shared), `works_stored` (the works route's
      total) and `last_fetched_at` (omitted if never fetched).
    - `mirror_source_url` is `httpx.DefaultBaseURL` plus `ao3url.BasePath` (the public AO3
      URL, which `ao3url.Parse` accepts), hashed like any plain string. It is always set: a
      re-add of a series that already is a source is refused as `already-exists`.
    - `GET /series/{id}/works` answers the `WorkList` of `GET /works` through
      `convert.WorkBlurbList`; plain GET, no sort (part order, as AO3).
    - `store.ErrNoSeries` is 404 with `DetailNoSeries`. The seed's series 77 now has a
      creator, description, notes, dates and stats, every string a sentinel; both routes have
      leak samples.
  - [x] **13c. Store + controller: creators**: a creator scope on `model.WorkFilter` (AO3
    `pseud_id`), `GetCreator` (login, pseud, works count) and a creator listing (name filter,
    paging, name or works sort). As built:
    - Meta migration 0010 replaces `work_creators_creator` with a covering `(creator_id,
      work_id)` index and adds `creators_name` on `(pseud, user_login)`, both `NOCASE`.
    - `model.WorkFilter.CreatorID` (0 = any) keeps works with the creator in the byline.
    - `model.CreatorFilter`: `Name` (words, each a case-insensitive substring of the pseud or
      the login, ANDed; at most `MaxCreatorNameWords`, 16; `TagNameWords` is now `NameWords`,
      shared), `Sort` (`name`: pseud, then login, ignoring case, asc by default; `works` desc)
      and `Direction`.
    - `store.CreatorByID` (`ErrNoCreator`), `FindCreators` / `CountCreators`
      (`store/creators.go`) returning `CreatorRow`s (`CreatorRef` plus `Works`, equal to
      `CountWorks` with the creator scope). A deferred join as `FindTags`; a works tie breaks by
      name ascending, then id. `MaxCreatorPage` (100) bounds the limit.
    - `controllers.GetCreator` returns a `CreatorView` (the creator and its works count; a
      creator named only by a series page has 0), `ListCreators` the page and total, and
      `ListCreatorWorks(id, f)` the works with the path creator as the scope, or
      `ErrNoCreator`; each one read transaction.
    - `BenchmarkCreators` (50k creators on seedWorks' works): a page by name under 0.5 ms, by
      works ~16–24 ms (every match's works are counted), counts under 5 ms, a creator's works
      page under 0.1 ms.
  - [x] **13d. Creator routes**: `GET /creators/{id}`, `GET` + `QUERY /creators/{id}/works`,
    `QUERY /creators` (`name` is reveal-only). As built:
    - `GET /creators/{id}` answers `dto.Creator` (`convert.CreatorView`): `id`, `user`, `pseud`
      (the work view's hashes), `works` (the works route's total) and `mirror_source_url`, the
      AO3 pseud works URL (`ao3url.KindPseud`) for source add, hashed like the series one.
    - `QUERY /creators`: `request.CreatorQuery` (`name`, 1 to 128 runes and 1 to 16 words, no
      control characters, free text so `reveal-required` hashed; `sort` a `dto.CreatorSort`:
      `name`, `works`; `direction`), mapped by `convert.CreatorFilter`, answered as
      `dto.List[dto.Creator]` (OpenAPI `CreatorList`) through `convert.CreatorList`. The word
      cap equals the store's (`TestCreatorQueryBoundsMatchModel`); the name check is shared with
      the tag query (`validNameWords`). QUERY only, with the search rate limit, as `/tags`.
    - `GET` + `QUERY /creators/{id}/works` are the `/works` handlers within
      `controllers.ListCreatorWorks`: the works handlers now take a scope controller (the tag
      routes pass `ListTagWorks`) instead of a tag flag. QUERY shares the search rate limit.
    - `store.ErrNoCreator` is 404 with `DetailNoCreator`. The seed's `seedCreator` (1) is the
      byline of both seeded works, checked while seeding; every route has a leak sample.
  - [x] **13e. Store + controller: collections**: a meta migration indexing
    `work_collections(collection_id)`, a collection scope on `model.WorkFilter`, and
    `GetCollection` (name, title, works count). As built:
    - Meta migration 0011 replaces migration 0004's `work_collections_collection` (its entries
      hold the rowid, not the work id) with a covering `(collection_id, work_id)` index.
    - `model.WorkFilter.CollectionID` (0 = any, AO3 `collection_ids`) keeps works in the
      collection.
    - `store.CollectionByID` returns a `CollectionRef` (`ErrNoCollection`,
      `store/collections.go`).
    - `controllers.GetCollection` returns a `CollectionView` (the collection and its works count;
      a collection whose works all left it keeps its row, with 0) and `ListCollectionWorks(id,
      f)` the works with the path collection as the scope, or `ErrNoCollection`; each one read
      transaction. No collection listing: AO3's `/collections` index is not mirrored.
    - `BenchmarkCollectionWorks` (1000 collections on seedWorks' works): a page ~0.5 ms, a count
      ~0.1 ms.
  - [x] **13f. Collection routes**: `GET /collections/{id}` and `GET` + `QUERY
    /collections/{id}/works`. As built:
    - `GET /collections/{id}` answers `dto.Collection` (`convert.CollectionView`): `id`, `name`,
      `title` (the work view's hashes), `works` (the works route's total) and
      `mirror_source_url`, the AO3 collection works URL (`ao3url.KindCollection`) for source
      add, hashed like the series and creator ones.
    - `GET` + `QUERY /collections/{id}/works` are the `/works` handlers within
      `controllers.ListCollectionWorks`. QUERY shares the search rate limit.
    - `store.ErrNoCollection` is 404 with `DetailNoCollection`. The seed's `seedCollection` (1)
      holds `seedFetchedWork` only, checked while seeding; every route has a leak sample.
- [x] **14. Images (reveal-only)**
  - `GET /images/{sha256}`: the stored Content-Type, `nosniff`, and
    `CSP: default-src 'none'; style-src 'unsafe-inline'` (per `docs/plan.md`; 14b adds
    `frame-ancestors 'none'; sandbox`).
  - Hashed mode returns 403 by enforcer construction.
  - Add a store read by sha. Work DTOs list image refs as sha256 values, which are hashed in
    hashed mode.
  - In two PRs:
  - [x] **14a. Store + controller: images.** As built:
    - Meta migration 0012 adds `images_sha256`, a partial index on `images (sha256) WHERE status
      = 'ok'` (only downloaded rows have a hash).
    - `store.ImageBySHA` returns a `StoredImage` (row id, hash, content type, bytes) for an `ok`
      row, or `ErrNoImage`. Rows of equal bytes from different URLs share a hash; the first
      stored (lowest id) wins, so the answer is stable. `store.WorkImages(workID)` lists a
      work's `WorkImage`s (`url`, `status`, `sha256`, the hash empty until downloaded) by image
      id from `work_images`. `TestImagePlans` checks both use their index.
    - `controllers.GetImage(sha)` refuses a hash that is not 64 lowercase hex (so
      `imagefetch.Path` only sees well-formed names), reads the row in one read transaction,
      then the file (`Options.ImageDir`, `serve` passes `data_dir/images`) after it, at most
      `MaxImageFileBytes` (32 MiB, the API's buffer cap), and checks it against its hash. A
      missing file is `ErrNoImage` (logged with the image id); an unreadable, oversized or
      mismatched one is an internal error. Bytes are never logged.
    - `controllers.GetWork`'s `WorkView` gains `Images`.
  - [x] **14b. Image route and work image refs**: `GET /api/v1/images/{sha256}` and the work
    DTO's `images`. As built:
    - `{sha256}` (`dto.ParamSHA256`, never logged, like the tag name) is read by
      `Params.PathSHA256` with the other params, checked by `controllers.ValidSHA256` (now
      exported): anything but 64 lowercase hex is a `validation-error` (`DetailSHA256`), hashed
      or not.
    - In hashed mode the handler refuses with 403 `reveal-required` (`DetailNotJSON`, as the
      enforcer's replacement) before calling `GetImage`, so an unknown hash is refused the same
      way. The enforcer's 403 stays the backstop.
    - A handler returning `*runtime.Blob` (`ContentType`, `Body`) has `Handle` write the bytes
      with their Content-Type (`application/octet-stream` when empty, never sniffed),
      Content-Length and `runtime.BlobCSP` (`default-src 'none'; style-src 'unsafe-inline';
      frame-ancestors 'none'; sandbox`) in place of the API's CSP. It records no body type, so
      a Blob claiming JSON is a 500; `Respond` stays the only JSON writer.
    - `store.ErrNoImage` is 404 with `DetailNoImage`.
    - `dto.Work` gains `images`: `dto.WorkImage` (`src_url`, `sha256` omitted until
      downloaded, both hashed; `status` a `dto.ImageStatus`), mapped by `convert.workImage`.
    - The seed makes `seedFetchedWork` embed a downloaded sentinel image (`seedImageSHA`, its
      file in the controllers' temp `ImageDir`) and a queued one; every route has a leak sample
      (the image route: 403 hashed, 200 revealed). OpenAPI declares the `image/*` binary
      response; the client is regenerated.
- [ ] **15. Hardening pass**
  - Fuzz tests for the param and body parsers.
  - Run the `/security-review` skill and `make deadcode`; govulncheck.
  - Update the OpenAPI spec; load-test the rate limits.
  - Review the OWASP API Security Top 10 (2023) item by item.
- [ ] **16. Auth (placeholder, design later)**
  - Must land before Funnel exposure. Expected: a separate `users.db`.
  - Revisit then: reveal requiring auth, per-user rate limits, Tailscale identity headers
    (`Tailscale-User-Login`) as an option for tailnet users, and moving admin ops off the
    socket.
  - Nothing is designed now.

## Verification (per stage, and overall)
- `make check` passes (lint, vulncheck, `go test -race ./...`, guard tests). Tests use temp stores
  and synthetic fixtures only.
- The all-routes leak test passes, and every new route has a sample request in it.
- Manual: `./ao3mirror --config ao3mirror.local.yaml serve --no-reveal`, then curl the public
  port and the admin socket (`curl --unix-socket data/api/admin.sock`). The guard blocks Claude
  from naming `data/`, so Claude asks the user to run these commands and share the output, and
  does not work around it with a scratch data dir. It checks that strings are `h_…` hashes,
  that `X-Fanwork: hashed` is set, that free text gives `reveal-required`, and that rate limits
  return 429. Claude never sends `reveal_fanwork`; the user checks reveal mode themselves.
- Stage 0: `make guard-test` covers the new guard rules.
