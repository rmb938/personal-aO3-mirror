# Part 2 — REST API plan (`ao3mirror serve`)

> How to use this file: each stage below is one PR (or a small group), followed by `/clear`. A new
> session reads CLAUDE.md, then this file's "Conventions" and its own stage section, implements
> it, runs `make check`, opens the PR and ticks the stage's box (in the same PR). If a stage
> changes a decision or convention, update this file in that PR too.

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
| Search | Free text and complex criteria go in a JSON body using **QUERY** (RFC 10008, June 2026). There is no POST alias. Free-text fields (`q`, name lookups) return 400 `reveal_required` without reveal. |
| Router | `github.com/go-chi/chi/v5` (v5.5.0) for sub-routers and middleware. It supports QUERY natively (`r.Query(pattern, h)`, `mQUERY` in `tree.go`), so it needs no `RegisterMethod`. `github.com/go-chi/httprate` (v0.16.1) for rate limits. Run `go list -m X@latest` again when adding them. |
| Auth | Placeholder stage only. No design now. A future `users.db` is separate from meta/content. |

## Conventions (every stage follows these)

### Layout
```
cmd/ao3mirror/serve.go       serve command: flags, listeners, graceful shutdown
internal/api/                server, router, middleware, handlers (calls controllers only)
internal/api/redact/         hasher + response enforcer (no HTTP deps; fuzzed)
internal/api/dto/            response/request types, registered with the enforcer
internal/apiclient/          typed client over the admin socket, used by the CLI
docs/api/openapi.yaml        hand-written OpenAPI 3.1; a test checks that it matches the route table
```
Handlers call `internal/controllers`, never `store`, following the CLAUDE.md layering rules. New
read models go in `controllers` with one `InReadTx` each. New SQL goes in `store`.

**DTO rule (by design, no lint):** `internal/api/dto` imports only the standard library. It
never uses or imports `controllers`, `store` or `model` types, and never embeds them. Handlers
map controller results into DTOs field by field. The API contract then changes only when a DTO
is edited on purpose, and the enforcer's key registry is exactly the DTO fields. Reviewers check
this rule, and it is added to CLAUDE.md in stage 0.

### Fanwork protection (the core design)
1. **Single write path.** Handlers return `(any, error)`. Only `api.respond` encodes JSON.
   Handlers never get a raw `http.ResponseWriter` (a lint test greps handler files for it).
2. **Enforcer middleware** (`redact.Enforcer`) wraps the whole router, outermost after
   recovery. In hashed mode (the default) it buffers the body (with a cap) and requires a JSON
   content type. Any other content type is replaced with 403 `reveal_required`. It then walks the
   JSON:
   - numbers, booleans and null pass through;
   - every **object key** must be in the key registry, which is built by reflecting over the
     `json` tags of the registered DTO types. An unknown key gives 500 and a log line with the
     hashed key. This rules out maps with dynamic keys;
   - **string** values pass through only when allowed for their key:
     - an enum key whose value is in that key's registered closed set (e.g. `state`, `kind`,
       `status`, `type`, `code`, `order`);
     - a key ending in `_at` whose value is strict RFC3339 UTC;
     - a key ending in `_on` whose value is `YYYY-MM-DD`.
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
5. **Errors** use RFC 9457 `application/problem+json` with only enumerable fields: `code` (enum),
   `title` (a fixed string per code, registered), `status`, and `field` (a registered key name).
   There is no free-text `detail`. Job `last_error` is split into `last_error_code` (enum) and
   `last_error_detail` (hashed).
6. **Free-text inputs** (`q`, `name`) need reveal. In hashed mode they return 400
   `reveal_required` (the oracle rule). Structured inputs (IDs, enums, numeric ranges, dates) are
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
  `ErrNo*` → 404, `ErrStateConflict` → 409, `ExistsError` → 409, validation → 400/422, anything
  else → 500 (`internal`).

### Config (`api:` section in `internal/config`, with validation)
```yaml
api:
  listen: 127.0.0.1:8080     # public listener; Funnel points here
  admin_socket: api/admin.sock   # relative to data_dir
  trusted_proxies: [127.0.0.1/32, ::1/128]
  request_timeout: 10s
  hash_key_rotation: 24h     # in-memory random key; 0 = once per process; min 1h
  rate: { general_per_min: 120, search_per_min: 30, source_add_per_hour: 10 }
```

---

## Stages

Status: tick when the PR merges.

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
- [x] **1. `internal/api/redact`.** No HTTP server yet.
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
- [ ] **2. Server skeleton**
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
    - [x] **2a. API core** (`internal/api`, no server yet). As built:
      - `dto.Problem{code, title, status, field}`. `api.Error{Code, Field, Status}` is the error
        handlers return. `problemFor` (`errors.go`) is the single error → problem mapping: the
        store `ErrNo*` sentinels → 404, `ErrStateConflict` → 409 `conflict`, `ErrSourceExists`
        / `controllers.ExistsError` → 409 `exists`, context cancel/deadline → 503
        `unavailable`, anything else → 500 `internal` (logged with the route pattern).
      - `field` is only ever a name in `knownFields` (else dropped). `code`, `title` and
        `field` are registered enums, so problems pass the enforcer unhashed (tested).
      - Handler shape `func(*http.Request) (any, error)`, adapted by `handle`, which caps the
        body (16 KiB) and calls `respond`. A handler returns `*api.Response` for another status
        or a `Location`. 415 on QUERY adds `Accept-Query: application/json`.
      - `parseQuery(r, allowed...)` (≤ 32 values, no repeats, allowlist; `RevealParam` always
        allowed and stripped), `parseInt`/`parseID` (digits only, ranges), `query.page()`,
        `checkText`.
      - `decodeJSON` uses `encoding/json/v2` (GA in Go 1.27): it rejects duplicate keys,
        trailing data and invalid UTF-8 by default, plus `RejectUnknownMembers`; matching is
        case-sensitive. Errors never quote the body.
      - `newEnforcer` builds the redact registry from `responseTypes` and the enums.
    - [x] **2b. Router + middleware**: chi router, the full middleware chain, healthz/version,
      the all-routes leak test and the OpenAPI route-coverage test. As built:
      - `api.New(Options)` builds one hasher/enforcer shared by two chi routers: `Public()` and
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
        logged only when in `knownFields`, others only counted (`other_params`), since a client
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
    - [ ] **2c. `serve`**: `api:` config, the `serve` command (`--no-reveal`), TCP + admin
      socket listeners, graceful shutdown.
- [ ] **3. Read-only ops API**
  - `GET /sources`, `/sources/{id}`, `/jobs` (filters: state, kind, source_id, page),
    `/jobs/{id}`, `/status`, `/source-adds/{job_id}`.
  - DTOs are built from the existing controllers (`ListSources`, `GetSource`, `FindJobs`,
    `Status`, `SourceAddOutcome`).
  - `FindJobs` gets offset paging if needed (store PR in the same stage).
- [ ] **4. Add source (public)**
  - `POST /api/v1/sources` with body `{url, order?, priority?, poll_interval?}` returns
    202 + `{job_id}` + `Location: /api/v1/source-adds/{job_id}`. A single-work URL returns 201.
  - Uses `QueueSourceAdd`. `confirm` is **never** accepted publicly: large sources wait for
    `source confirm`.
  - URL ≤ 2048 bytes, validated by `ao3url.Parse`.
  - Has its own strict rate limit.
- [ ] **5. Admin endpoints (socket only)**
  - `POST /sources/{id}/confirm|pause|resume|cancel|refresh` (refresh body `{full}`),
    `PATCH /sources/{id}` (settings), `DELETE /sources/{id}`.
  - `GET /debug/jobs/{id}`, `/debug/works/{id}`.
  - A test asserts these return 404 on the public listener.
- [ ] **6. `internal/apiclient` + CLI conversion**
  - 6a: client (unix-socket transport, problem+json errors) and `source *` commands, including
    `add --wait` polling `/source-adds/{job}`.
  - 6b: `status`, `jobs`, `debug`.
  - CLI `--names` maps to the reveal param. It is still user-only (the guard already blocks it).
  - Update `cmd/ao3mirror/main_test.go` end-to-end tests to start an in-process `serve`.
- [ ] **7. Store + controller: work detail**
  - Loaders for a work's creators, tags (with type), series parts (+ partial-series flag per
    `docs/plan.md`), collections, and chapter metadata (meta `chapters`).
  - Chapter bodies and skin come from content.db and are merged in Go (the two DBs are not
    attached).
  - Add a `work_skins` reader. Controller `GetWork(ctx, id)` with one read transaction.
- [ ] **8. Works read API**
  - `GET /works/{id}` (metadata, relations, chapter list).
  - `GET /works/{id}/chapters/{position}` (body, notes, end notes).
  - `GET /works/{id}/chapters` (entire work, size-capped).
  - `GET /works/{id}/skin`.
  - Every content string is in JSON, so it is hashed by default.
- [ ] **9. Store: work listing + filters**
  - One filter struct and query builder covering AO3 work_search equivalents:
    - include/exclude tag IDs (by type), rating/warning/category tag IDs;
    - complete, crossover, language, words/kudos/hits ranges, date ranges, status;
    - sort column + direction.
  - Bounded `IN` lists and parameterized SQL only.
  - Migration `0002` adds reverse indexes (`tag_parents(parent_id)`,
    `work_collections(collection_id)`, `series_creators(creator_id)`) and any sort indexes.
- [ ] **10. Works list + QUERY API**
  - `GET /works?page&per_page&sort` (simple listing).
  - `QUERY /works` with the body filter object (structured fields only in this stage).
  - Search rate limit.
- [ ] **11. Full-text search**
  - Add `q` to the QUERY body (reveal-only). Sanitized FTS5 MATCH joined with the stage 9
    filters; total count; ranking.
  - Replaces the unused `SearchWorkIDs`.
- [ ] **12. Tags**
  - Store readers for parents, meta/sub tags, synonyms/merger, and work counts.
  - `GET /tags/{id}`; `GET` + `QUERY /tags/{id}/works` (stage 9 filters scoped to the tag and,
    for canonical tags, their synonyms).
  - `QUERY /tags` (type filter; `name` is reveal-only).
  - `GET /media` and `/media/{id}/fandoms` (AO3 fandom browse).
- [ ] **13. Series, creators, collections**
  - `GET /series/{id}` (with `complete_locally`, `works_total`, `works_mirrored`,
    `missing_positions`, `missing_work_ids`, `mirror_source_url`) and `/series/{id}/works`.
  - `GET /creators/{id}` and `/creators/{id}/works`; `QUERY /creators` (name is reveal-only).
  - `GET /collections/{id}` and `/collections/{id}/works`.
- [ ] **14. Images (reveal-only)**
  - `GET /images/{sha256}`: the stored Content-Type, `nosniff`, and
    `CSP: default-src 'none'; style-src 'unsafe-inline'` (per `docs/plan.md`).
  - Hashed mode returns 403 by enforcer construction.
  - Add a store read by sha. Work DTOs list image refs as sha256 values, which are hashed in
    hashed mode.
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
- Manual: Claude runs `./ao3mirror --config ao3mirror.local.yaml serve --no-reveal` and curls the
  public port and the admin socket (`curl --unix-socket`). It checks that strings are `h_…` hashes,
  that `X-Fanwork: hashed` is set, that free text gives `reveal_required`, and that rate limits
  return 429. Claude never sends `reveal_fanwork`; the user checks reveal mode themselves.
- Stage 0: `make guard-test` covers the new guard rules.
