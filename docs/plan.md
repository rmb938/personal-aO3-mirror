# AO3 Personal Mirror — Master Plan + Part 1 (Scraper) Plan

## Context

Goal: a personal, offline-capable mirror of selected AO3 (archiveofourown.org) works, so they can be read when AO3 is down (e.g. the Oct 29 2026 security incident, still degraded on 2026-11-15). Three parts: (1) scraper, (2) API with AO3-like search and browse, (3) AO3 look-alike UI. Only Part 1 is built now, but its data model must already support Part 2 search and browse.

Hard constraints from the user:
- Go, single binary, no external services (SQLite, local files).
- Slow and polite. Few HTTP calls. Continuous, with polls every few hours.
- Every request carries the custom User-Agent, including robots.txt and discovery requests.
- AO3 forbids scraping for AI training. **Claude must never read fanwork content.** This is enforced mechanically, not only stated in CLAUDE.md.

Repo today: only `README.md` and `LICENSE`. Go 1.27 is installed. Greenfield project.

## Research findings (they drive the design)

- **robots.txt** (fetched with our UA): `User-agent: *` disallows `/works?` (works index with a query), `/downloads/`, `/autocomplete/`, `/external_works/`, `/works/search?`, `/tags/search?`, `/people/search?`, `/bookmarks/search?`.
  - These remain **allowed**: `/works/<id>?view_full_work=true` (+ `view_adult=true` cookie), `/tags/<tag>/works?work_search[...]`, `/users/<u>/works`, `/series/<id>`, `/collections/<c>/works`, `/tags/<tag>`.
  - We obey robots.txt strictly. So: no `/downloads/` (also HTML-only during the incident), no `/works/search`.
- **Rate limit:** AO3 does not publish one. The open-source code (otwcode/otwarchive) defaults to 300 requests per 300 s per IP (`config/config.yml` `RATE_LIMIT_NUMBER/PERIOD`, enforced in `config/initializers/rack_attack.rb`), and returns 429 with `Retry-After`. Production may override this, and Cloudflare sits in front. So we treat the number as unconfirmed and stay about 10x under it.
- **Listing cap:** `MAX_SEARCH_RESULTS: 100000`, so at most 5,000 pages of 20 per query. Larger sources must be sliced with `work_search[date_from/date_to]`, which filters on `revised_at`.
- **Tag listing defaults:** faceted listings (`/tags/X/works`) sort by `revised_at` desc. Unfiltered pages 1..N are server-cached, which makes them cheap for AO3.
- **Tag atom feeds:** they only show the 25 newest works by `created_at`. They miss updates, so we don't use them. Listing page 1 sorted by `revised_at` catches both new and updated works.
- **Work page:** `view_full_work=true` returns all chapters in **1 request**. The `view_adult=true` cookie skips the adult interstitial (the `view_adult` query param combined with `view_full_work` returned Cloudflare 525s during the incident, while each alone worked). Restricted works redirect to login.
- **Search scope:** AO3 search covers metadata only (title, summary, tags, creators, stats), not body text. So metadata FTS5 matches AO3 behaviour.
- **Cloudflare:** AO3 currently returns 525, `__cfwaitingroom` cookies, and challenge pages. The client must recognise these as "back off", not as parse failures.
- **HTML structure:** the otwarchive Rails views are the reference: `works/_work_blurb`, `_work_module`, `_meta`, `show`, `tags/show`, `series/show`. They are open-source code, not fanworks, so Claude may read them.

---

## Master plan (all parts)

| Part | Deliverable | Notes |
|---|---|---|
| 1 | `ao3mirror run` daemon + source CLI | This plan. Writes `meta.db`, `content.db`, `images/`. |
| 2 | `ao3mirror serve` JSON API | **Detailed staged plan: `docs/part2-api.md`.** chi router, separate process from `run`, public TCP listener + admin unix socket. Fanwork strings are hashed by default; `reveal_fanwork` per request reveals them. Browse/read works, tags, series, creators, collections; QUERY-method search; source add/view and job/status views; admin ops on the socket. Calls the same `internal/controllers` package as the CLI. |
| 3 | AO3-style UI | A JavaScript UI that calls the Part 2 API (requests will need auth, so no plain HTML form posts). Reuses otwarchive's open-source CSS (GPL-2.0, fine for personal use). Applies work skins. Image URLs point at the API's reveal-only image route. "Mirror this URL" box for on-demand scraping. Design details are decided when Part 3 starts. |

### Requirement (Part 3): serve mirrored images safely

Images are stored exactly as the host served them, including SVG, which can carry scripts.
The UI must only show them through `<img src>` (never inline the markup, `<object>` or
`<iframe>`). It must serve `images/` with the stored `Content-Type`, `X-Content-Type-Options: nosniff`
and a restrictive CSP (`default-src 'none'; style-src 'unsafe-inline'`). Fanwork HTML and work
skins are untrusted input too, so they get the same sanitizing and CSP treatment.

### Requirement (Parts 2 and 3): flag partial series

A series is partial when it is not a source and we mirror fewer works than AO3 lists. This happens
when a work from one source also belongs to another series. Readers must never hit a missing part
mid-series without warning.

- **API:** `GET /series/:id` returns `complete_locally: false`, plus `works_total` (AO3's count,
  from `series_info`), `works_mirrored`, and `missing_positions` (gaps in the part numbers of the
  works we have, e.g. we have 1, 2, 4 of 5, so 3 and 5 are missing). It also returns
  `missing_work_ids` where known, and a `mirror_source_url` that can be passed to source add.
- **Work views:** works that are part of a partial series carry the same flag in their series
  block (`GET /works/:id`), so readers see it from inside the work too.
- **UI:** a clear banner on the series page and on its works ("parts 3 and 5 are not mirrored"),
  with a one-click "mirror the whole series" action (adds the series as a source).
- **Status:** list partial series with their counts.
- **Data:** positions come from fetched work pages. Total comes from `series_info`. Missing IDs
  (optional, zero extra requests) come from recording the unknown blurbs on the series page 1
  that `series_info` already fetches. That page currently discards them in `handleSeriesInfo`.

---

## Part 1 — Scraper

### Architecture

```
cmd/ao3mirror/            main: run | source add/list/pause/resume/cancel/remove/confirm | fetch-work | reparse | status
cmd/skeletonize/          user-run tool: real page -> structure-only skeleton
internal/config/          ao3mirror.yaml (rate, poll intervals, thresholds, data dir, image limits, login placeholder)
internal/httpx/           THE ONLY HTTP entry point: UA transport, limiter, robots, backoff, response classifier, cookie jar
internal/ao3url/          AO3 URL <-> Source (parse any AO3 URL; build robots-allowed listing URLs)
internal/parse/           goquery parsers: listing blurbs, pagination/total, work page, tag page, series page, error pages
internal/store/           meta.db + content.db, embedded SQL migrations, repositories (the ONLY package with SQL)
internal/controllers/     business logic over store+fetch+parse: sources (add/estimate/confirm/pause/cancel),
                          scheduling decisions, poll/backfill pages, work/tag/series fetches, reparse
                          (shared by the CLI, the scheduler and the future API)
internal/scheduler/       persistent job queue loop: pick, run via controllers, retry/fail, heartbeat
internal/images/          image extraction + per-host fetch worker
internal/fakeao3/         httptest fake AO3 with SYNTHETIC fixtures (tests + `ao3mirror dev fake-server`)
testdata/fixtures/        hand-written fake pages (fake titles/authors/text)
testdata/skeletons/       *.skel.html produced by the user via cmd/skeletonize
```

Dependencies are pure Go, so there is no cgo and the result is a static binary:
- `modernc.org/sqlite` (includes FTS5)
- `github.com/PuerkitoBio/goquery`
- `github.com/klauspost/compress/zstd`
- `github.com/temoto/robotstxt`
- `gopkg.in/yaml.v3`

### HTTP client (`internal/httpx`), the politeness core

- **User-Agent:** `personal-ao3-mirror/<ver> (personal offline archive; contact: discord <contact.discord>)`.
  - `contact.discord` is a **required** config value with no default.
  - The binary refuses to start, and builds no HTTP client, if the value is missing, empty, or not of the form `@name`.
  - The local dev copy is `ao3mirror.local.yaml`, set to `@rmb938`. It is gitignored. The committed `ao3mirror.example.yaml` has a placeholder that fails validation.
  - A RoundTripper sets the UA on every request, including robots.txt, images, and any future login. The UA cannot be removed: config may only append to it.
  - Unit tests assert that the fake server saw the UA on every request, and that startup fails without a contact.
- **Concurrency:** one AO3 request in flight, globally.
- **Pacing:** base interval `rate.interval` (default 10 s, configurable) with ±20% jitter. There is a hard-coded floor of 3 s that config cannot go below. An optional `rate.daily_budget` caps requests per day.
- **robots.txt:** fetched with the UA and cached for 24 h. Every URL is checked before it is sent. A disallowed URL is refused with an error, never sent.
- **Response classifier.** Each response is classified as one of:
  - `ok`
  - `not_found` (404, the work was deleted)
  - `restricted` (redirect to `/users/login`)
  - `hidden` (hidden by admin, or unrevealed)
  - `adult_gate`
  - `rate_limited` (429)
  - `unavailable` (5xx, Cloudflare 52x, challenge or waiting-room page, maintenance page; detected by missing AO3 page markers or by Cloudflare markers)
  - `parse_unexpected`
- **Backoff:**
  - 429: sleep `Retry-After`, or 15 min if absent. Then raise the interval ×1.5 for 24 h (adaptive slowdown).
  - `unavailable`: exponential backoff from 1 min up to a 1 h cap.
  - Circuit breaker: after 5 consecutive failures, pause all AO3 work for 2 h.
- **Cookie jar:** handles Cloudflare cookies now and a future login session.
- **Login (future):** an `Authenticator` interface with a no-op implementation now. The future version does a CSRF login and stores the session cookie in `meta.db`. Works seen as `restricted` are recorded, so they can be fetched once login exists.
- **Request log:** the `requests` table stores ts, kind, path pattern, status, bytes and ms. It never stores content.

### Sources and on-demand

- **Source kinds**, each mapped to its robots-allowed URL:
  - `tag`: `/tags/<name>/works` + optional `work_search` filters (language, rating, complete, words, kudos, include/exclude tags)
  - `user`/`pseud`: `/users/<u>/works`
  - `user_bookmarks`: `/users/<u>/bookmarks`
  - `series`: `/series/<id>`
  - `collection`: `/collections/<c>/works`
  - `work`: `/works/<id>`
- **URL input:** `ao3url.Parse(anyAO3URL)` turns any pasted AO3 URL into a Source. This is what the future UI/API "mirror this URL" button uses. A filtered `/works?tag_id=X&work_search[...]` index URL is rejected with a pointer to the `/tags/X/works?work_search[...]` form: the index is robots-disallowed, and its ids are not decoded like path params, so accepting it could add one tag as two sources.
- **Add flow:**
  0. `source add` only validates the URL and queues an `add_source` job (the URL waits in
     `source_adds`; the job targets that row id). Only the daemon talks to AO3, so page 1 goes
     through its one paced client like every other request. With a daemon running, the CLI waits
     (`--wait`, default 2m) and prints the result; otherwise the add runs at the next `run`.
     Single-work sources need no request and are added at once.
  1. Fetch page 1 (1 request). It gives the total count and its blurbs are stored at once.
  2. Compute an estimate: listing pages + work fetches + days at the current rate + approximate disk.
  3. If the count is at or below `sources.auto_confirm_max` (default 2000), the source goes `active`. Otherwise it waits in `pending_confirm` until `source confirm <id>` or a narrowed re-add.
- **Per-source settings:** `backfill_order` (`kudos_desc` | `revised_desc` | `revised_asc` | ..., any AO3 sort column, default from config), `poll_interval` (default 6 h), `priority`.
- **Very large sources:** if the total is over 100k, or the order can't cover everything within 5,000 pages, the backfill is split into `date_from/date_to` windows on `revised_at`.
- **States:** `pending_confirm | active | paused | cancelled | done_backfill`.
  - Pause or cancel takes effect at the next scheduler pick. Only one request is ever in flight, so that is within seconds.
  - Cancel drops the source's queued jobs. Works already fetched are kept.
  - Remove deletes the source and its membership rows, but never the works.

### Scheduler (`internal/scheduler`)

- **Queue:** jobs are persisted in `meta.db` (`jobs`: kind, source_id, target, priority_class, state, not_before, attempts, last_error), so the daemon resumes after a restart.
- **Control:** the CLI and the daemon share the DB in WAL mode. The CLI writes commands and the daemon polls the DB every 5 s.
- **Pick order:** by priority class, then round-robin across sources inside a class, so one big fandom can't starve the others.
  1. On-demand single works.
  2. Update polls: walk a source's listing sorted by `revised_at` desc. Stop once a page holds only works already seen with an unchanged `(revised_date, chapters, words)`.
  3. Work fetches discovered by polls.
  4. Backfill: listing pages and their work fetches.
  5. Tag info fetches (TTL 30 d).
  6. Maintenance, below.
- **Maintenance** (class 6):
  - Re-walk each source's full listing every `sources.full_rescan_interval` (default 30 d). This refreshes stats and finds missing works.
  - Each missing work gets 1 verification request. The result sets it to `deleted`, `restricted` or `hidden`. **Local content is always kept.**
- **Blurb processing:** each listing blurb upserts metadata and stats for free, with no extra request. A work fetch is queued only when the work is new or its `revised date`, `chapters` or `word count` changed.
- **Images:** a separate worker, rate-limited per host (default 1 request / 3 s per host). It uses the same UA transport and honors each host's robots.txt.

### Storage

**`meta.db`** (SQLite, WAL):
- `works`: id, title, summary_html, notes, language, rating, category, warnings via tags, words, chapters_posted, chapters_expected, complete, published, revised_at, kudos, hits, comments, bookmarks, status (`ok|deleted|hidden|restricted|unrevealed`), first_seen, last_listed, last_fetched, parser_version.
- `chapters`: work_id, chapter_id, position, title, published. Metadata only.
- `creators` (user, pseud), `work_creators`.
- `tags`: id, name, type (Rating/ArchiveWarning/Category/Fandom/Relationship/Character/Freeform/Media), canonical, merger_id, info_fetched_at. Also `tag_parents` and `work_tags`.
- `series`, `series_works(position)`, `collections`, `work_collections`.
- `sources`, `source_works` (membership, so the API can "browse my source"), `jobs`, `requests`.
- `images`: url, sha256, status, bytes, content_type. Also `work_images`.
- `works_fts` (FTS5 over title, summary, creators, tags). This is enough for AO3-equivalent search in Part 2.

**`content.db`:**
- `raw_pages`: work_id, fetched_at, zstd raw HTML. This is the source of truth.
- `chapter_bodies`: work_id, chapter_id, zstd body HTML, notes, end notes.
- `work_skins`: work_id, css.
- `ao3mirror reparse` rebuilds parsed tables from `raw_pages` with **zero requests** when the parser improves.
- `content.keep_versions` (default 1) controls how many raw versions are kept. The last good copy is never deleted when a work disappears.

**`images/ab/cd/<sha256>.<ext>`:** content-addressed, size cap 25 MB, `image/*` only, `data:` URIs skipped.

### Parsers (`internal/parse`)

- Built from the otwarchive view templates.
- Blurbs: `li.work.blurb#work_N`, `h4.heading`, `h5.fandoms`, `ul.required-tags`, `ul.tags`, `blockquote.summary`, `dl.stats`.
- Work page: `#workskin .preface`, `dl.work.meta`, `#chapters .chapter[id^=chapter-]`, `.userstuff`, `.notes`, `.end.notes`.
- Pagination: `ol.pagination`, plus the "1 - 20 of N Works" heading for the total.
- Tag page: tag type, canonical/synonym, parent tags, meta tags.
- `parser_version` is stored per work.
- Unknown structure gives `parse_unexpected` with a CSS-path error code, never content in errors.
- **Validation loop:** the user runs `cmd/skeletonize` on a real saved page. It replaces every text node with `x`, keeps only `class`, `id`, `role` and `rel` attributes, and turns href paths into patterns (`/works/0/chapters/0`, `/tags/x/works`). The output goes to `testdata/skeletons/*.skel.html`. Claude may read skeletons only. Parser tests run on the skeletons plus the synthetic fixtures.

### Claude guardrails (built FIRST, in milestone 0)

- **`CLAUDE.md`:**
  - Never read, print, query or summarize fanwork content: work text, titles, summaries, notes, comments, images.
  - Never fetch archiveofourown.org or ao3.org pages by any tool except the project binary.
  - Fixtures must be synthetic.
  - Claude may run the live scraper, but may inspect only `ao3mirror status`, which shows counts, IDs, job states and error codes.
  - The AI-training ban is the reason for these rules.
- **`.claude/settings.json`:**
  - `permissions.deny` for `Read(./data/**)`, `Read(**/*.db)`, `Read(**/*.zst)`, `Read(./images/**)`, `Bash(sqlite3:*)`, `WebFetch(domain:archiveofourown.org)`, `WebFetch(domain:ao3.org)`.
  - A **PreToolUse hook** `.claude/hooks/guard.sh` (bash + grep, no jq) on Bash, Read, Grep, Glob, Edit and WebFetch. It blocks any input that names the data dir, `*.db`, `*.zst`, `images/`, the daemon log, or an AO3 hostname. The only exceptions are the `ao3mirror` binary itself and `*.skel.html` files.
- **Logging: rich enough to debug, but content-free.** See "Debug logging" below. A test seeds fixtures with a sentinel string and asserts it never appears in log, status or debug output.

### Debug logging (all tools, parsers, scheduler)

Structured `log/slog`. JSON goes to `data/logs/ao3mirror.log` with rotation, plus text to stderr. `--log-level debug|info`. Every line carries `req_id`, `job_id` and `source_id` where they apply, so one job can be traced end-to-end.

- **httpx:** method and URL path, where work, chapter and series IDs are numeric. Tag, user and collection segments are replaced by their local numeric ID or a hash. Also logged:
  - status, final URL after redirects, `Location` chain
  - `Content-Type`, `Content-Length`, `Retry-After`, `cf-ray`, `cf-cache-status`, Cloudflare/waiting-room marker hits
  - DNS/connect/TLS/TTFB/total timings, bytes
  - robots decision (allowed, or disallowed + rule), limiter wait time and current interval
  - backoff state changes, circuit-breaker open/close
- **Classifier:** the chosen class plus the reason, i.e. which markers matched or were missing (e.g. `missing=#workskin cf_marker=challenge-platform`).
- **Parsers:** per page type, a "parse report":
  - for each expected selector: found or missing, and match count
  - for each field: present yes/no plus value length or number. No text. Numbers like words, kudos and chapters are fine.
  - counts of blurbs, tags per type, chapters and images
  - `parser_version` and duration
  - on `parse_unexpected`: the CSS path where parsing failed
- **Auto skeleton on failure:** when a parse fails or the classifier returns `parse_unexpected`, the page is run through the same skeletonize code. It is saved as `data/debug/<kind>-<id>-<ts>.skel.html`, which keeps structure only and no text. This is the one data path the guard hook allows Claude to read.
- **Scheduler:** job enqueue, pick, start, finish and fail with timings. Each pick logs its reason (priority class, round-robin position). Poll-walk decisions log stop-page and changed-work counts. Source state transitions are logged, and so is the estimate math.
- **Store:** migration versions, slow queries over 200 ms, and row counts touched per job.
- **`ao3mirror status`:** queue depth by class and state, last 20 errors (code + IDs), current interval, backoff and breaker state, requests in the last 1 h and 24 h, and per-source progress.
- **`ao3mirror debug job <id>`** / **`debug work <id>`:** the full content-free trace (requests, classifier reasons, parse reports) for one job or work.
- **Rule:** fanwork text, titles, summaries, notes, author names and tag names never appear in any log field.

### Config (`ao3mirror.yaml`, defaults)

```yaml
data_dir: ./data
contact: { discord: "" }       # REQUIRED, e.g. "@rmb938" in ao3mirror.local.yaml; startup fails if empty
user_agent_suffix: ""          # appended; base UA always present
log: { level: info, file: logs/ao3mirror.log }
rate: { interval: 10s, jitter: 0.2, daily_budget: 0 }   # floor 3s enforced in code
sources: { auto_confirm_max: 2000, default_backfill_order: kudos_desc, poll_interval: 6h, full_rescan_interval: 720h }
tags: { info_ttl: 720h }
images: { enabled: true, per_host_interval: 3s, max_bytes: 26214400 }
content: { keep_versions: 1 }
auth: { access_ttl: 15m, refresh_ttl: 720h, max_session_age: 720h, invite_ttl: 24h, totp_issuer: ao3mirror }  # API user accounts (docs/part2-api.md stage 16)
```

### Milestones

0. Scaffolding: `go.mod`, layout, CLAUDE.md, `.claude/settings.json`, guard hook, config loader.
1. `internal/httpx` + `internal/fakeao3`: UA, limiter, floor, robots, classifier, backoff. Tests cover UA on every request, robots refusal, 429 handling, and 525/waiting-room handling.
2. `internal/store`: migrations, repositories, FTS5.
3. `internal/parse` from the templates plus synthetic fixtures. Add `cmd/skeletonize`. The user supplies skeletons and the parsers are adjusted to match.
4. `internal/ao3url` + `internal/sources` + `source` CLI (add, estimate, confirm, pause, cancel).
5. `internal/scheduler` + `run` daemon: polls, backfill, fairness, date slicing, resume after restart.
6. `reparse` (offline rebuild from raw pages) and `fetch-work [--now]`. (Work fetching itself
   landed in milestone 5 so `run` mirrors works end to end.)
7. Tag info, deletion/restricted verification, images.
8. Live smoke test once AO3 is stable: add 1 small `series` or `work` source and run. Claude checks `status` only.

### Verification

- `go test ./...`: all tests run against `fakeao3` with synthetic fixtures. They assert the UA on 100% of requests, that robots-disallowed URLs are never sent, that the pacing floor holds, that 429/52x cause backoff, that cancel stops queued jobs, that round-robin fairness works, that reparse makes zero requests, and that the sentinel string never leaks into logs.
- `ao3mirror dev fake-server` + `ao3mirror run` against it: an end-to-end backfill and update-poll cycle.
- Guard hook test: scripted inputs (Read of `data/meta.db`, `curl archiveofourown.org/works/1`) must be blocked, and `cat testdata/skeletons/x.skel.html` must be allowed.
- Live: one small source. `ao3mirror status` shows the expected counts. The `requests` table shows the expected spacing (≥ interval) and no disallowed paths.
