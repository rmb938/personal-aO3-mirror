# Design rules for API code

Loaded when working under `internal/web/api`. The API prohibitions ("Claude must NOT") and the
auth rule stay in the root `CLAUDE.md`.

- Handlers call controllers (see Layering) and return values; only `runtime.Respond` writes JSON.
- `internal/web/api/dto` imports only the standard library. It never uses, imports or embeds
  `controllers`, `store` or `model` types; `internal/web/api/convert` maps controller results into
  DTOs field by field.
- Conversion lives in `internal/web/api/convert`, not handlers: one exported function per model or
  controller result, named after it (`convert.Source`), every helper unexported and tested
  beside it. Each model gets its own file (`source.go`, `source_add.go`, with matching
  `_test.go`); `convert.go` holds only helpers shared across models. Handlers parse input, call a controller and return `convert.X(result)`.
- A string field with a closed value set is a named string type in `dto` with an
  `EnumValues() []string` method; the enforcer registry finds it by reflection. A plain
  `string` field is free text and always hashed.
- Server-generated or strictly-shaped non-fanwork values (tokens, TOTP secrets and URIs,
  recovery codes, usernames, invite URLs; stage 16) are named string types in `dto` with a
  `VerbatimPattern() string` method. The enforcer passes them only on a full regex match, else
  500. Never give a pattern type to anything that could hold fanwork or free text, and keep each
  pattern as tight as the value allows.
- Errors are RFC 9457 problems (`docs/api/problems.md`). A problem `detail` is only ever a
  catalogue message from `problem.NewDetail` (`internal/web/api/problem`; package-level vars only,
  e.g. `internal/web/api/details.go`), never built from input or content; a new problem type gets
  a `## <slug>` section there.
- Validation reports every invalid part at once: handlers read all params through
  `runtime.ParseParams` and return `q.Err()`. Request bodies are types in `internal/web/api/request`
  (same import rule as `dto`), each with a `Validate(*problem.Validation)` method recording
  every invalid member and listed in `request.Types()`; `runtime.DecodeJSON` takes a `request.Request`
  and validates it. Route patterns are constants in `internal/web/api/route` (stdlib
  only, shared with `internal/apiclient`); `Location` and client paths come from
  `route.Path(pattern, ids...)`.
- Untrusted input is always bounded: body size, string runes, array lengths, integer ranges,
  query param allowlists. Logs carry route patterns and param names, never query values or
  bodies.
