# tiny-worker

Go 1.27 framework targeting Cloudflare Workers via TinyGo/WASM.

## Runtime behavior

- **Panic recovery**: a panic anywhere in the request path (decode, handler, encode) is
  recovered in the Go bridge and returned as a 500; it never kills the WASM runtime.
- **Body limit**: `cloudflare.MaxBodyBytes` (default 10 MiB) caps request bodies. Set it
  before `cloudflare.Register(...)`. Oversized requests get 413 before buffering —
  enforced in `worker.js` via `content-length` and again in the bridge before any copy
  into linear memory. Values `<= 0` disable the limit.
- **Boot resilience**: WASM instantiation failures are not cached; the runtime is reset
  and the next request retries boot. A fatal runtime death is detected per-request and
  the runtime is rebuilt inline.

## Routing & status semantics

Attach a router to get real 404/405 behavior instead of fall-through to a catch-all:

```go
r := router.New()
r.Handle("GET", "/users/:id", getUser)
app := tinyworker.New(nil)
app.UseRouter(r)
```

- **404**: no node matches the path (a deeper path under a registered prefix is still
  a 404, never a 405).
- **405**: the path exists but not for this method; the response carries an `Allow`
  header listing registered methods (`HEAD` is auto-included where `GET` is set).
- Handlers and routers can return `tinyworker.NotFound(...)` or
  `tinyworker.MethodNotAllowed(allowed...)` / any `NewStatusError` value; the bridge
  renders the status, headers (e.g. `Allow`), and message. Other errors map to 500.
- `router.Match` keeps its original nil-handler semantics for compatibility; prefer
  `Lookup`.
- Request paths are derived from the URL (`Request.Path`, query string dropped) by
  the bridge; `tinyworker.ParsePath` does the same for plain strings.

## Middleware

`App.Use` composes middleware around the router (or catch-all handler). The first
registered middleware is the outermost: it sees the request first and its
post-processing runs last, so logging placed first observes panics recovered later in
the chain. Middleware also sees routing outcomes — 404/405 from misses — because it
wraps the dispatch itself.

```go
app := tinyworker.New(nil)
app.UseRouter(r)
app.Use(
    tinyworker.LogRequests(logf), // method, path, status, duration
    tinyworker.Recover(),          // panic -> 500 StatusError, runtime stays alive
    tinyworker.CORS(tinyworker.CORSOptions{
        AllowOrigins: []string{"https://app.example"},
        AllowMethods: []string{"GET", "POST"},
        MaxAge:       "86400",
    }),
)
```

Built-ins: `Recover` (panic → 500), `LogRequests` (one line per request, including
404/405/500 outcomes), and `CORS` (adds `Access-Control-Allow-Origin` to every
response — including error responses — and answers preflight `OPTIONS` before
routing; disallowed preflight origins get 403). Handlers returning `StatusError`
values render their own status; bare `ErrNotFound` maps to 404 everywhere, so
middleware status inference matches what the bridge sends on the wire.

## Tracing (W3C Trace Context)

`tinyworker.Tracing()` implements traceparent propagation:

- A valid incoming `traceparent` is continued: trace-id kept, a fresh span-id
generated, flags propagated unchanged (versions other than `00` are accepted and
answered with their own version; `ff` version/flags, uppercase hex, all-zero ids,
wrong lengths, and trailing extensions are rejected and fall back to a fresh root
context).
- Handlers read the context via `tinyworker.Trace(req)` (a `TraceContext` with
`TraceID`, `SpanID`, `ParentSpanID`, `Sample`) and render the downstream
`traceparent` for outbound calls with `tc.String()`.
- Responses carry `traceresponse` per the spec — the caller's span with ours, or the
root context for newly started traces — including on error responses.

```go
app := tinyworker.New(nil)
app.UseRouter(r)
app.Use(tinyworker.Tracing())
```

## Request-scoped values

Middleware can pass data (auth identity, tracing IDs) to handlers through the request:

```go
var userID = tinyworker.Value[int64]("auth.user_id")

// in middleware:   userID.Set(req, 42)
// in handler:      id, ok := userID.Get(req)
```

Values live for the duration of the request, are visible to later middleware and the
handler (and to outer middleware after the handler returns), and are gone when the
request is done. The store is created lazily, so requests that never use it pay
nothing. `Value[T]` is type-safe — a stored string read as `Get[int]` returns
`ok=false` — and the generic free functions `Set`/`Get` work for one-off keys.

## Testing the bridge

The Cloudflare bridge (`runtime/cloudflare`) is tested under `GOOS=js GOARCH=wasm` with
[wasmbrowsertest](https://github.com/agnivade/wasmbrowsertest), which runs the tests in
a real Chrome — covering the echo roundtrip, 413 body-limit paths, panic recovery and
post-panic survival, handler errors, and JS-facing globals:

```
go install github.com/agnivade/wasmbrowsertest@latest
make wasm-test   # skips automatically if no browser is available
```

CI (`.github/workflows/ci.yml`) runs the native suite, the bridge tests in Chrome, the
TinyGo build, and a wrangler dry-run bundle on every push and PR.