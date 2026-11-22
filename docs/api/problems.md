# API problem types

Every error from `ao3mirror serve` is an [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457)
problem details object, sent as `application/problem+json`:

```json
{
  "type": "https://github.com/rmb938/personal-aO3-mirror/blob/main/docs/api/problems.md#validation-error",
  "title": "Validation Error",
  "status": 422,
  "detail": "one or more parameters are invalid; see errors",
  "errors": [
    { "detail": "must be an integer from 1 to 100", "parameter": "per_page" }
  ]
}
```

| Member | Meaning |
|---|---|
| `type` | The problem type: this page's URL plus the type's section anchor. Clients switch on it. |
| `title` | The type's fixed summary. It never changes between occurrences. |
| `status` | The HTTP status code, always the same as the response's. |
| `detail` | Optional. What went wrong in this occurrence and how to fix it. Clients should not parse it. |
| `errors` | Extension member on `validation-error`: one item per invalid part of the request. |

Each `errors` item has a `detail` and, when the API can name it, where the problem is:
`parameter` for a query or path parameter, or `pointer` for a body member, as an
[RFC 6901](https://www.rfc-editor.org/rfc/rfc6901) JSON Pointer (`/url`, `/filters/2/tag_id`).
The server checks every part of a request before answering, so `errors` lists all of them.

**Fanwork safety.** Every string in a problem comes from a fixed set built into the server:
the type URIs, the titles, a catalogue of `detail` messages, the declared parameter names, and
pointers made of the request types' own member names and array indices. The response enforcer
passes only those unhashed, so a problem never carries fanwork content
and never echoes client input. A parameter name the API does not declare is left out of its
error item rather than echoed. There is no `instance` member: the `X-Request-Id` response
header identifies the request.

## bad-request

**400 Bad Request.** The request could not be read at all: a malformed query string (bad
percent-encoding, `;` separators) or more than 32 query parameters, or on a route that needs an
access token, an `Authorization` header that is repeated, over 2048 bytes, or `Bearer` without
a single token (that 400 carries `WWW-Authenticate: Bearer realm="ao3mirror",
error="invalid_request"`, RFC 6750 §3.1).

## validation-error

**422 Validation Error.** The request is well formed, but one or more parameters or body
members are invalid: out of range, not one of the allowed values, given more than once, not
taken by this route, or (for a URL) not a supported AO3 page. `errors` lists every one, each
with its own `detail`.

## invalid-body

**400 Invalid Request Body.** The body is not a single JSON object with only the documented
members, of the documented types: malformed JSON, trailing data, a duplicate or unknown member,
a member of the wrong JSON type, or invalid UTF-8. The member is not named (its name may be
client input).

## body-too-large

**413 Content Too Large.** The body is over 16 KiB.

## unsupported-media-type

**415 Unsupported Media Type.** A body was sent without `Content-Type: application/json` (an
optional `charset=utf-8` is allowed). For a QUERY request the response carries
`Accept-Query: application/json`.

## reveal-required

**400 Reveal Required**, or **403** when it replaces a response. The request needs the
per-request reveal query parameter: a free-text input (a search term or a name) in hashed mode,
or a response that is not JSON (e.g. an image), which is only served revealed.

## reveal-disabled

**403 Reveal Disabled.** The request asked for revealed content, but the server runs with
`--no-reveal` and never returns real content.

## forbidden

**403 Forbidden.** A cross-origin browser request with an unsafe method was refused, or a
signed-in change (`PUT /api/v1/account/password`, `POST /api/v1/account/recovery-codes`) was
sent with a wrong current password or TOTP code. The access token itself was fine, so this is
not a 401; the failure counts against the account like a failed login.

## not-found

**404 Not Found.** No route matches the path, or the resource (source, job, work, chapter, ...,
and on the admin socket an account, invite or session) does not exist.

## method-not-allowed

**405 Method Not Allowed.** The path exists but not with this method. The `Allow` header lists
the methods it supports.

## state-conflict

**409 State Conflict.** The resource is not in a state that allows the operation (e.g.
confirming a source that is not pending confirmation).

## already-exists

**409 Already Exists.** The resource already exists (e.g. adding a source that was already
added, or signing up with a username an account already has).

## work-too-large

**422 Work Too Large.** The work's chapters (bodies, notes, end notes and summaries) are over
16 MiB together, too large for `GET /api/v1/works/{id}/chapters`. Read them one at a time from
`GET /api/v1/works/{id}/chapters/{position}`; the work's chapter list is in
`GET /api/v1/works/{id}`.

## misdirected-request

**421 Misdirected Request.** The `Host` header (without its port) is not one of the public
listener's `api.allowed_hosts` (default `127.0.0.1`, `::1`, `localhost`). This stops a web
page from reaching the API through DNS rebinding. Use one of the configured names, or add the
name you serve under (e.g. the Cloudflare Tunnel's custom domain). The admin socket does not check `Host`.

## uri-too-long

**414 URI Too Long.** The path plus query string is over 4096 bytes.

## rate-limited

**429 Too Many Requests.** The client is over a rate limit, or a login step was tried while
its username is backed off after failed attempts. Retry after the `Retry-After` delay (the
limit's window, or the backoff left, in seconds). Every response that passed a limit carries
`X-RateLimit-Limit`, `X-RateLimit-Remaining` and `X-RateLimit-Reset` for the last limit it
passed (`openapi.yaml` lists the limits).

## unauthorized

**401 Unauthorized.** On the public listener every route but `/api/v1/healthz` and the auth
entry points (signup, reset, login, refresh) needs an access token (`Authorization: Bearer
<access_token>` from `/api/v1/auth/login/totp` or `/api/v1/auth/refresh`), and so does a path
that matches no route (so it is 401, not 404, without one). The request had none, or the
token was refused: malformed, badly signed, expired, or its session revoked or expired, or its
account disabled or deleted. A refused token's 401 carries `WWW-Authenticate: Bearer
realm="ao3mirror", error="invalid_token"` (RFC 6750 §3.1): refresh the token pair, or log in
again if the refresh is refused too. Without a token it is just `Bearer realm="ao3mirror"`.
The admin socket needs no token, except that the signed-in user's own routes
(`/api/v1/account...`, logout) are always 401 there: it never reads `Authorization` and has no
signed-in account.

## invalid-credentials

**401 Invalid Credentials.** An auth step was refused: a wrong password, TOTP code or recovery
code, or a token from the body (invite, enrollment, login challenge, refresh) that is unknown,
expired, already used or revoked. The detail says only which of the two it was; it never says
whether an account exists. Start the flow again (a new invite or reset link if that one is
spent). Like every 401, it carries `WWW-Authenticate: Bearer realm="ao3mirror"`.

## unavailable

**503 Service Unavailable.** The server is busy (too many requests in flight, or too many
password hashes at once) or the request did not finish within the request timeout. Retry
later; a busy response carries `Retry-After`.

## internal-error

**500 Internal Server Error.** An unexpected server error. The server logs it with the request
ID (`X-Request-Id`); the response never includes error text.
