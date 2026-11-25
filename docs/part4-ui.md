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
| API client | TS types generated from `docs/api/openapi.yaml` (openapi-typescript) used with openapi-fetch. The reveal param's name is generated into a TS constant from `runtime.RevealParam` by `go generate`; its literal is never handwritten in UI code. |
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
web/                     SvelteKit app (package.json, lockfile, svelte.config.js, vite config)
  src/lib/api/           generated schema types, the fetch wrapper (refresh/retry, CSRF header)
  src/lib/reveal.gen.ts  generated from runtime.RevealParam (go generate)
  src/lib/sanitize/      HTML and skin sanitizers (pure TS, no DOM state)
  src/routes/            pages
  tests/                 Playwright tests (run against the seeded fake server)
internal/web/ui/         embed.FS of web/build, the static handler, the UI's CSP
```

`web/build` is not committed; `make ui` produces it. The Go embed always has at least the
placeholder page, so `go build` from a clean checkout works and serves "UI not built".

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
require-trusted-types-for 'script'; trusted-types <policy names>`. The SvelteKit build must not
emit inline scripts (configure it, and test the served headers and the built HTML).

### Auth in the browser

The cookie transport `bearerToken(r)` (`internal/web/api/runtime/bearer.go`) was left room for.

- **Opt-in per login.** The routes that issue tokens (login TOTP step, signup and reset
  confirm) accept `"transport": "cookie"`. Then the response sets the tokens as cookies and
  leaves them out of the body. Without it, behaviour is unchanged (bearer, for the CLI and
  `internal/apiclient`).
- **Cookies.** `__Host-` prefix, `Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/`. Access
  cookie lifetime = access TTL; refresh cookie = refresh TTL. The server is the only one that
  sets or clears them: a cookie JS can write can't be HttpOnly.
- **Refresh.** The UI calls `POST /api/v1/auth/refresh` with no token in the body (on a 401, or
  shortly before expiry). The browser attaches the refresh cookie, the server rotates both
  cookies, and the UI retries the request. Reuse detection works unchanged.
- **Logout** revokes the session and clears both cookies.
- **CSRF.** A request authenticated by cookie must carry the custom header on every method
  except GET/HEAD, and its `Sec-Fetch-Site` (or `Origin`) must be same-origin; otherwise 403.
  Bearer requests are unaffected. A request carrying both credentials is rejected (ambiguous).
- **Logs** stay as they are: `auth` records the transport, never a token.
- **Invite and reset links** (`{api.public_url}/signup#token=…`) land on UI pages that read the
  fragment, so the token never reaches the server's logs or Cloudflare.

## Stages

Stages 1–2 are Go only; 3 onwards add the UI. Each stage keeps `make check` green.

- [ ] **0. This doc.** Plus the Part 4 row in `docs/plan.md` and the status line in CLAUDE.md.
- [ ] **1. Cookie auth transport (Go).** "Auth in the browser": the `transport` member on the
  token-issuing routes, cookie set/rotate/clear, `bearerToken(r)` reading the access cookie,
  the CSRF check, problems for the new 403s, OpenAPI and `make generate`, tests in
  `internal/web/api` (cookie attributes, refresh rotation and reuse via cookie, logout clears,
  CSRF rejections, both-credentials rejection, bearer unchanged, admin socket ignores cookies).
- [ ] **2. Static UI serving (Go).** `internal/web/ui`: embedded files, the placeholder, SPA
  fallback for non-`/api/` paths, correct content types, `nosniff`, the UI CSP, long cache
  headers for hashed asset names and `no-cache` for `index.html`. Mounted on the public
  listener only. The enforcer exemption is limited to these embedded files (fixed content, no
  store access) and needs the user's explicit approval. The all-routes leak test covers the
  UI routes.
- [ ] **3. Frontend scaffold.** `web/` with SvelteKit static, TS strict, generated schema
  types and `reveal.gen.ts` (`go generate`, so `make generate` also refreshes them), ESLint
  (with the `{@html}` rule), svelte-check, Vitest, `npm audit`. `make ui`, `make build` running
  it first, `make check` running the frontend checks. Node added to `check.yml` and
  Dependabot config for npm. Verifies no inline scripts and Trusted Types with `{@html}`.
- [ ] **4. Seeded fake server + Playwright harness.** A test-only way to start `serve` on a
  fresh temp data dir seeded with lorem fixtures (works with chapters, notes, a skin, images
  including an SVG, a partial series, tags) and a test user, refusing any existing directory.
  Playwright starts it, runs, and tears it down, under `make` (a `make ui-e2e` target, also run
  by `make check`). First test: the placeholder-free app loads with the UI CSP. The guard and
  CLAUDE.md are updated (user approves) only as far as this needs.
- [ ] **5. Login UI.** Two-step login (password, then TOTP or a recovery code), the fetch
  wrapper (refresh and retry, CSRF header), logout, the redirect to login on session loss, the
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
