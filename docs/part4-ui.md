# Part 4 — Web UI plan

> How to use this file: each stage below is one PR, followed by `/clear`. A new session reads
> CLAUDE.md, then this file's "Context", "Design" and its own stage section, implements it, ticks
> the stage's box, runs `make check`, then **stops and waits**: the user reviews the uncommitted
> changes and says when to commit and open the PR. Never commit, push or open a PR unasked. If a
> stage changes a decision, update this file in the same change. API work (routes, problems,
> request types, OpenAPI, `make generate`, the all-routes leak test) follows `docs/part2-api.md`
> "Conventions"; read only the parts a stage needs.

## Context

Parts 1–3 are done: the scraper, the JSON API (`ao3mirror serve`, `docs/part2-api.md`) and
master key rotation (`docs/part3-key-rotation.md`). Part 4 is the reading UI over that API,
used on both phones and desktops, reached through the same Cloudflare Tunnel as the API.

The user isn't a web developer and mostly reviews PRs, so the stack is chosen for quality,
readability in review and a mainstream ecosystem.

### Why a browser app (and why Svelte)

- **Not server-rendered.** html/template, templ or htmx would make `serve` turn fanwork into
  HTML outside the redact enforcer, breaking "no route outside it" (CLAUDE.md). The UI is a
  client app over the existing JSON API, so every fanwork string still passes the enforcer.
- **Not Go WASM** (e.g. go-app v10.1.11). The download is several MB even compressed and is
  re-downloaded on every release, which hurts on phones. Markup is a Go DSL, which makes
  AO3-like markup verbose. There is no good Go sanitizer for the browser, so it would call
  DOMPurify through JS interop anyway. go-app prerenders on the server by default (see the first
  point). It is a small, single-maintainer ecosystem. Little logic would be shared, since the
  business logic lives on the server.
- **Not Ember 7.x.** Modern Ember (template tag, Vite, Embroider) is very different from the
  Ember the user knew, and its community has shrunk.
- **Svelte 5 over Vue or React.** Its templates are close to plain HTML (easy to review), its
  bundles are the smallest, and SvelteKit brings a router. Vue was a close second; React's JSX is
  noisier to review for markup-heavy pages.

### Decisions made with the user (2026-11-25)

| Topic | Decision |
|---|---|
| Devices | Phone and desktop; mobile-first layouts. |
| Framework | Svelte 5 + TypeScript, SvelteKit as a static SPA (`adapter-static`, fallback page), built by Vite. |
| API client | A typed client generated from `docs/api/openapi.yaml` by orval (changed in stage 3 from openapi-typescript + openapi-fetch, now unmaintained; see "Build"). The reveal param's name is generated into a TS constant from `runtime.RevealParam` by `go generate`; its literal is never handwritten in UI code. |
| Look | AO3-inspired, not a clone: same layout ideas (blurbs, tag groups, chapter navigation), our own CSS tuned for phones. otwarchive's CSS is not reused. |
| Styling | Plain component-scoped Svelte CSS plus a small set of CSS variables (colors, spacing, reading size/width). No Tailwind or CSS library. |
| Serving | The built UI is embedded with `embed.FS` and served by `serve` on the public listener at `/`, same origin as `/api/v1` (no CORS). Non-API paths fall back to `index.html` so deep links work. A placeholder page is served when the UI wasn't built. The static files sit beside the enforcer as a narrow fixed-content exemption, which the user approves explicitly (stage 2). |
| Toolchain | Node is a build-time dependency only. `make build` builds the UI, then the Go binary; the result is one binary. |
| Release | GoReleaser on a git tag: build the UI, cross-compile linux/darwin × amd64/arm64, attach checksums to a GitHub release. Users never handle web assets. |
| npm deps | Few direct dependencies, exact versions, committed lockfile, `npm ci`, `npm audit` in `make check`, Dependabot for updates. Check versions with `npm view <pkg> version` before adding (as with `go list -m X@latest`). |
| Auth transport | Server-set cookies (see "Auth in the browser"). JS never sees a token. The CLI and `internal/apiclient` stay on bearer. |
| Chapter HTML | DOMPurify, configured to otwarchive's own HTML allowlist, then `{@html}`. A strict CSP with Trusted Types is the second layer. No iframes. |
| Work skins | Sanitized and scoped under `#workskin`, applied as constructable stylesheets, with a "hide creator's style" toggle. |
| Images | Only `<img src>` from the existing reveal-only `GET /api/v1/images/{sha256}`. |
| Reader comforts | Dark mode (follows the system), font size and reading width, remembered reading position. All per device in localStorage. |
| Offline | No PWA or service worker. Instead, EPUB download of a work, and of a whole series (which AO3 itself doesn't offer), built in the browser from the revealed JSON (JSZip). No server change. |
| TOTP setup | Render a QR code of the otpauth URI (one small QR dependency) and show the text secret. |
| MVP | Login and account pages, and reading works. Browse/search and source management come in Part 4b. |
| Dev data | A seeded fake server: a fresh temp data dir filled with lorem-ipsum works, tags, series, images and a test user. Claude reveals against it only in automated tests (Playwright under `make`, the server started by the test, like Go tests with `runtime.RevealParam`). **Claude never drives a browser with reveal**, against any server. |

## Design

### Layout

```
web/                     SvelteKit app (package.json, lockfile, vite.config.ts, eslint, tsconfig)
  go.mod                 not Go: keeps web/ (npm packages ship .go files) out of ./...
  scripts/               build helpers (externalize-boot.js, see "Build" below)
  orval.config.ts        generates src/lib/api/api.gen.ts from the OpenAPI spec
  src/lib/api/           api.gen.ts (generated client), http.ts (apiFetch: refresh/retry, CSRF header)
  src/lib/reveal.gen.ts  generated from runtime.RevealParam (go generate)
  src/lib/html/          TrustedHtml.svelte, the one component allowed to use {@html}
  src/lib/sanitize/      HTML and skin sanitizers (pure TS, no DOM state)
  src/routes/            pages
  playwright.config.ts   end-to-end tests (make ui-e2e)
  tests/                 Playwright tests (run against `ao3mirror dev ui-server`)
internal/e2eseed/        the synthetic works and test account ui-server serves
internal/web/ui/         the static handler, the UI's CSP, placeholder.html
  dist/                  the SvelteKit build (embed.FS), written there by `make ui`
  genreveal/             writes reveal.gen.ts (go generate; a test keeps it current)
.node-version            the Node version used locally (fnm) and in CI: the latest LTS
```

### Build (stage 3)

- **Versions.** Every npm package is on its latest release, and Node is the latest LTS (26).
- **TypeScript 7 type-checks; 6 is only a library for tools.** TypeScript 7.0 (the native
  compiler) ships no JS API; a new, different one is planned for 7.1. Kit, svelte-check and
  typescript-eslint call the old API, so they need 6.x installed.
  Microsoft services 6.x (security, high-severity regressions, 6/7 compatibility) for exactly
  this side-by-side use, and recommends TS 7 for checking with 6 for tooling. We do the same,
  as Kit itself does: `typescript` is 6.0.3 (the last 6.x) and `@typescript/native` is an npm
  alias of `typescript@7`, whose `tsc` is the one on the path. `svelte-check --tsgo` transpiles
  the Svelte files and type-checks everything with TS 7. Its transpiled copies under
  `.svelte-kit/.svelte-check` aren't removed when a component is deleted (a ghost keeps
  reporting errors), so `npm run check` clears that directory first. Drop `typescript@6` once
  these tools move to the 7.1 API.
- **API client: orval** (replacing the planned openapi-typescript + openapi-fetch, which went
  into maintenance mode with no further updates, security fixes included, on 2026-09-19:
  openapi-ts/openapi-typescript#2874). Compared on 2026-11-25:
  - orval 8.49.0: commits daily, 22 open issues, built with TypeScript 7 and needs no
    TypeScript at run time, clean `npm audit`, and generates all 67 operations including the 6
    OpenAPI 3.2 `QUERY` ones (openapi-typescript silently dropped them).
  - hey-api: no commits since 2026-09-30, 681 open issues, and its latest release pulls in a
    js-yaml with high-severity advisories (fails our `npm audit`).
  - Kubb: active, but its TypeScript plugin needs the TypeScript 6 API (`@typescript/typescript6`).
  - openapi-typesafe (an openapi-typescript drop-in): three weeks old, release candidates only,
    and an unknown maintainer: too new to trust with the supply chain.

  `orval.config.ts` generates `src/lib/api/api.gen.ts`: types plus one plain `fetch`-style
  function per operation, each response typed as a union by status, and no runtime library.
  Every call goes through `apiFetch` (`src/lib/api/http.ts`), where stage 5 adds refresh, the
  CSRF header and the reveal param. orval reads `tsconfig.json`, which extends Kit's generated
  `$app/tsconfig`, so the scripts run `svelte-kit sync` first. orval depends on esbuild, whose
  postinstall script only checks the platform binary npm already installed;
  `package.json` `allowScripts` denies it, so no install scripts run.
- **SvelteKit 3** takes its config as the argument to the `sveltekit()` Vite plugin (in
  `vite.config.ts`; there is no `svelte.config.js`), and `$lib` is gone: imports use `#lib/…`,
  a Node subpath import declared in `package.json`. The generated base tsconfig is
  `$app/tsconfig` (under `node_modules`).
- **No inline script.** Kit always writes its start-up code as an inline `<script>` in the
  page. `scripts/externalize-boot.js` wraps adapter-static: after the build it moves that script
  to `_app/immutable/boot.<hash>.js` (a classic script in the same place, since it finds its
  mount point with `document.currentScript`) and fails the build if the page still has an
  inline script, `<style>`, style attribute or event handler. `paths.relative` is off, so the
  one `index.html` works at any deep link. A Go test checks a real embedded build the same
  way and that every file it references is served (`internal/web/ui`).
- **Trusted Types work with `{@html}`** (checked in headless Chromium under the UI CSP):
  Svelte 5 creates its own policy, `svelte-trusted-html`, for its compiled templates, and
  `{@html}` passes a `TrustedHTML` through untouched, while a plain string is refused. So the
  CSP allows exactly two policy names, `ao3mirror-html` (the sanitizer's, stage 7) and
  `svelte-trusted-html`; a second policy with either name is refused.
- **No inline styles: the console stays free of CSP violations.** Kit's route announcer
  (`#svelte-announcer` in its `root.svelte`) hides itself with a style attribute, which
  `style-src 'self'` blocks. The browser logged that on every page load. The Vite plugin
  `scripts/no-inline-styles.js` removes that one attribute before Svelte compiles the component
  (it fails if the attribute isn't where it expects; a test runs it on Kit's real file), and
  `app.css` hides the announcer instead. The plugin also fails the client build if any compiled
  template still has a style attribute. ESLint's `svelte/no-inline-styles` rejects `style=` and
  `style:` in our components (dynamic ones too, which the build scan can't see) and transitions,
  which inject `<style>` elements. Style with classes in component CSS. Allowing Kit's
  attribute by hash (`'unsafe-hashes'`) would tie the Go CSP to Kit's markup, so we don't.
  Checked in headless Chromium under the UI CSP: no console messages at all.
- **`{@html}` rule.** ESLint's `svelte/no-at-html-tags` is on everywhere except
  `src/lib/html/TrustedHtml.svelte`, whose prop is a `TrustedHTML` (`@types/trusted-types`;
  TypeScript 6's DOM types lack it). `no-restricted-properties` also rejects `innerHTML`,
  `outerHTML`, `insertAdjacentHTML`, `setHTMLUnsafe` and `document.write` in `src/`.
- **Make.** `make ui` runs `npm ci` when the lockfile changes, builds, and copies `web/build`
  into `internal/web/ui/dist` (keeping `.gitkeep`); `make build` runs it first. `make ui-check`
  (part of `make check`) runs `orval --outdated` (fails on a stale `api.gen.ts`), svelte-check,
  ESLint, Vitest, the build and `npm audit`. `make generate` also writes `reveal.gen.ts` and
  `api.gen.ts`.

`internal/web/ui/dist` is not committed except `dist/.gitkeep` (go:embed needs a file), and
`make ui` writes the build into it (go:embed can't reach `web/build` from `internal/web/ui`).
Without an `index.html` there the handler serves the placeholder page, so `go build` from a
clean checkout works and serves "UI not built" (and `serve` logs a warning).

### E2E harness (stage 4)

- **`ao3mirror dev ui-server`** (`cmd/ao3mirror/uiserver.go`) makes a new temp dir
  (`os.MkdirTemp`; it takes no data dir, so it can never open a real one), seeds it, and runs
  the real `serve` code (`newServerOn`) on it with reveal on, on a loopback address only
  (default port 0, a free one; `public_url` is the bound address). Rate limits are raised so
  one test run never meets them. It prints one JSON line (URL, account, seeded IDs), and stops
  on SIGINT/SIGTERM or when its stdin closes, then deletes the dir.
- **The seed** (`internal/e2eseed`) writes rows with store calls directly, like the Go tests'
  seeds (test data, not business logic). Lorem and fixture names only. It refuses a non-empty
  directory. Contents: work 1001 (3 of 4 chapters, notes and chapter notes, a skin, a mirrored
  PNG and SVG (whose script must never run) and one still-queued image, series part 1); 1002
  (one-shot, series part 3); 1003 (anonymous); 1004 (two creators, a collection, XSS payloads
  in its summary, notes and chapter 2, and a hostile skin, for stages 7–8; a payload that runs
  sets `window.__ao3mirrorXSS`). Series 2001 has 4 parts, 2 mirrored. The account
  `e2e_reader` has a random TOTP secret and recovery codes, printed in the JSON line.
- **Why not in-memory storage** (asked 2026-11-25): SQLite files opened by path in WAL mode,
  the image files and the admin unix socket all need paths, and an in-memory variant would be
  code production never runs. The temp dir is private (0700) and deleted at the end.
- **Playwright** (`web/playwright.config.ts`, `web/tests/`): global setup spawns
  `$AO3MIRROR_E2E_BIN dev ui-server` and passes its line to the tests in an environment
  variable; `tests/fixtures.ts` sets `baseURL` from it and exposes it as the `server` fixture.
  Teardown closes the server's stdin. Chromium only, headless shell. `make ui-e2e` builds the
  UI, builds `bin/ao3mirror-e2e` with it embedded, installs Chromium if missing, and runs the
  tests.
- **Login in tests (stage 5):** a TOTP code is accepted once per 30 s step (replay check), so
  tests that sign in repeatedly should use the recovery codes or reuse a stored session.

### Serving (stage 2, approved by the user 2026-11-25)

On the public listener, a path that is exactly `/api` or under `/api/` (the decoded path, so an
escaped spelling still reaches the API) goes to the API router, unchanged. Every other path
goes to a small separate chain: request ID, client IP, access log (route `/*`, never the raw
path), recoverer, security headers, Host check, URL limits, its own rate-limit budget
(`api.rate.ui_per_min`, default 600; stage 3 split it from `general_per_min`, whose 120 a few
hard refreshes used up, since each fetches the page and every asset) and a throttle, then the
`internal/web/ui` handler. That chain has no enforcer, auth or reveal: it is the one approved
exemption from the enforcer, limited to files embedded at build time, with no store access and
nothing echoed from the request. The handler answers GET/HEAD only (else 405, plain text). An
existing file is served with an explicit content-type table and an ETag; a missing path under
`/_app/` or whose last segment has a dot is 404; anything else gets `index.html` (SPA
fallback). `/_app/immutable/` is cached a year (`immutable`), everything else `no-cache`. Every
response carries the UI CSP, `nosniff`, `no-referrer`, and same-origin CORP and COOP. The admin
socket has no UI. The CSP's trusted-types policy names are settled in "Build" below.

### Fanwork protection in the UI

- The UI adds the reveal param to every API request and every image URL: a signed-in user is
  reading their own mirror. `serve --no-reveal` still wins (403), and the UI shows a clear
  "this server refuses reveal" message instead of hashes.
- The UI never sends content anywhere but its own origin. There are no CDN fonts, analytics,
  error reporters or external links fetched in the background (`connect-src 'self'`).
- The UI never writes fanwork to localStorage. Reading position stores IDs and numbers only
  (work ID, chapter position, scroll fraction). EPUBs are built in memory and handed to the
  browser as a download.
- Claude's testing: see "Dev data" above and stage 4.

### Security model

| Threat | Defense |
|---|---|
| Malicious HTML in a chapter, notes or summary | DOMPurify, limited to otwarchive's sanitizer allowlist (tags, attributes, URL schemes; read from otwarchive's code and pinned to a commit in stage 7). Only sanitizer output may reach `{@html}`: an ESLint rule rejects any other use. |
| Script that gets past the sanitizer | The UI's CSP (below), with Trusted Types required, so the browser rejects HTML insertion that didn't go through the sanitizer's policy. Stage 3 verifies that Svelte's `{@html}` works with Trusted Types; if it doesn't, the doc is updated with the fallback before building on it. |
| Malicious work skin | The skin sanitizer parses the CSS, scopes every selector under `#workskin` (as AO3 does), and drops `@import`, `@font-face`, external `url()`, `position: fixed` and other overlays. It is applied with constructable stylesheets (`adoptedStyleSheets`), so the CSP needs no `'unsafe-inline'` for styles. |
| Malicious SVG, tracking pixels | Images come only from our image route via `<img src>`, where SVG can't run script. The route already sends `nosniff` and a sandbox CSP. `img-src 'self'` blocks any external image a chapter references; the sanitizer rewrites mirrored image references to our route. |
| Token theft | Tokens live only in HttpOnly cookies, which JS cannot read. |
| CSRF | SameSite=Strict cookies, plus a `Sec-Fetch-Site`/`Origin` same-origin check and a required custom header on every state-changing request authenticated by cookie. |
| Clickjacking | `frame-ancestors 'none'` on the UI too. |

UI CSP (on `index.html` and the fallback, set by `internal/web/ui`):
`default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self';
object-src 'none'; base-uri 'none'; frame-src 'none'; frame-ancestors 'none'; form-action 'self';
require-trusted-types-for 'script'; trusted-types ao3mirror-html svelte-trusted-html`. The
SvelteKit build must not emit inline scripts (see "Build": the boot script is moved to a file,
and both the build and a Go test check the HTML).

### Auth in the browser

The cookie transport `bearerToken(r)` (`internal/web/api/runtime/bearer.go`) was left room for.

- **Opt-in per login.** The two routes that issue tokens, `POST /api/v1/auth/login/totp` and
  `/api/v1/auth/refresh`, take an optional `"transport"`: `bearer` (the default when omitted,
  so the CLI, `internal/apiclient` and other API clients change nothing) or `cookie`. With
  `cookie` the response sets the tokens as cookies and its body is a `CookieSession`
  (`transport`, `expires_in`, `refresh_expires_at`) with no token; the spec's 200 is a
  `oneOf` of it and `TokenPair`. Signup and reset confirm issue no tokens (the user logs in
  afterwards), so they don't take it.
- **Cookies.** `__Host-ao3mirror-access` and `__Host-ao3mirror-refresh`: `Secure`,
  `HttpOnly`, `SameSite=Strict`, `Path=/`, no `Domain`. Lifetimes are `Max-Age` (not
  `Expires`, so the client's clock doesn't matter): the access TTL, and until the session's
  refresh expiry. The server is the only one that sets or clears them: a cookie JS can write
  can't be HttpOnly. They are not an OpenAPI security scheme (that would change the generated
  client's security source); the spec describes them in prose (the Transport schema).
- **Refresh.** The UI calls `POST /api/v1/auth/refresh` with `{"transport":"cookie"}` and no
  token (on a 401, or shortly before `expires_in`). The browser attaches the refresh cookie,
  the server rotates both cookies, and the UI retries the request. Reuse detection works
  unchanged and has no grace window, so **two refreshes with the same cookie end the
  session**: stage 5 must single-flight refresh across tabs (Web Locks API,
  `navigator.locks.request`), and after taking the lock first retry the request, since another
  tab may already have refreshed (the cookies are shared).
- **Logout** revokes the session and, when it was signed in by the access cookie, clears both
  cookies (`Max-Age=0`).
- **CSRF.** A request carrying either session cookie must send `X-Ao3mirror-Csrf: 1` on every
  method except GET/HEAD, and be same-origin: `Sec-Fetch-Site: same-origin`, or without that
  header an `Origin` naming the request's host; otherwise 403 `forbidden`. A cookie-transport
  login or refresh needs the same, checked before anything is spent (a challenge try, a
  refresh token). Go's CrossOriginProtection still runs first on every unsafe request. Bearer
  requests (no cookies) are unaffected. `Authorization` together with the access cookie is 400
  `bad-request` (ambiguous); the refresh cookie alone never signs a request in.
- **Admin socket** drops the `Cookie` header, so there is no cookie transport there.
- **Logs:** `auth` as before, plus `auth_transport` (`bearer` or `cookie`); never a token.
- **Invite and reset links** (`{api.public_url}/signup#token=…`) land on UI pages that read the
  fragment, so the token never reaches the server's logs or Cloudflare.

## Stages

Stages 1–2 are Go only; 3 onwards add the UI. Each stage keeps `make check` green.

- [x] **0. This doc.** Plus the Part 4 row in `docs/plan.md` and the status line in CLAUDE.md.
- [x] **1. Cookie auth transport (Go).** "Auth in the browser": the `transport` member on the
  token-issuing routes, cookie set/rotate/clear, `bearerToken(r)` reading the access cookie,
  the CSRF check, problems for the new 403s, OpenAPI and `make generate`, tests in
  `internal/web/api` (cookie attributes, refresh rotation and reuse via cookie, logout clears,
  CSRF rejections, both-credentials rejection, bearer unchanged, admin socket ignores cookies).
- [x] **2. Static UI serving (Go).** See "Serving". `internal/web/ui`: embedded files, the placeholder, SPA
  fallback for non-`/api/` paths, correct content types, `nosniff`, the UI CSP, long cache
  headers for hashed asset names and `no-cache` for `index.html`. Mounted on the public
  listener only. The enforcer exemption is limited to these embedded files (fixed content, no
  store access) and needs the user's explicit approval. The all-routes leak test covers the
  UI routes.
- [x] **3. Frontend scaffold.** As built: see "Build" under Design. `web/` with SvelteKit static, TS strict, generated schema
  types and `reveal.gen.ts` (`go generate`, so `make generate` also refreshes them), ESLint
  (with the `{@html}` rule), svelte-check, Vitest, `npm audit`. `make ui`, `make build` running
  it first, `make check` running the frontend checks. Node added to `check.yml` and
  Dependabot config for npm. Verifies no inline scripts and Trusted Types with `{@html}`.
- [x] **4. Seeded fake server + Playwright harness.** As built: see "E2E harness" under
  Design. `ao3mirror dev ui-server` over `internal/e2eseed`, Playwright 1.65.1 starting it in
  global setup, `make ui-e2e` (part of `make check`; CI installs Chromium with `--with-deps`).
  First tests: the app (not the placeholder) loads with the UI CSP and no console messages, a
  deep link reaches the client router, Trusted Types refuse a raw `innerHTML`, the API answers
  same-origin. The guard blocks Claude starting `dev ui-server` (CLAUDE.md updated).
- [ ] **5. Login UI.** Two-step login (password, then TOTP or a recovery code), the fetch
  wrapper (refresh and retry single-flighted across tabs, CSRF header), logout, the redirect to login on session loss, the
  lockout and rate-limit messages.
- [ ] **6. Signup and account pages.** Invite signup from the fragment link (password policy
  messages, TOTP QR + text secret, confirm code, recovery codes shown once with copy/download),
  reset from link, account page: password change, regenerate recovery codes, sessions list
  with revoke.
- [ ] **7. Sanitizers.** HTML sanitizer (otwarchive allowlist, image references rewritten to
  our route, external images dropped) and skin sanitizer (parse, scope, drop). Pure TS with
  heavy unit tests, including known XSS and CSS-injection payloads.
- [ ] **8. Work page.** Metadata and tags, chapter-by-chapter and full-work views, chapter
  navigation, notes and end notes, images, the work skin with the hide toggle, and the
  partial-series banner ("parts 3 and 5 are not mirrored"; the "mirror the whole series"
  action waits for Part 4b). A minimal home page listing works so there is a way in.
- [ ] **9. Reader settings.** Dark mode, font size, reading width, remembered position (per
  device, IDs and numbers only).
- [ ] **10a. EPUB of a work.** Built in the browser (EPUB 3, JSZip) from the revealed work and
  chapters, sanitized the same way, images included, skin left out.
- [ ] **10b. EPUB of a series.** The mirrored works in order, one EPUB, with a note on missing
  parts.
- [ ] **11. Release workflow.** GoReleaser on tags: UI build, cross-compiled binaries,
  checksums, GitHub release.

### Part 4b (later, planned when it starts)

Browse and search (tag, fandom, series, creator and collection pages; AO3-style filters;
QUERY search), "mirror this URL" (source add), sources/jobs/status pages.

## Verification

- Per stage: `make check` (Go and frontend checks, Playwright tests against the seeded fake
  server), plus the stage's own tests.
- Security: tests assert the UI CSP and cookie attributes on real responses, the sanitizers
  against payload lists, and that no inline script ships in the build.
- Manual checks against the real server (real login, real content) are done by the user only;
  Claude gives the commands.
