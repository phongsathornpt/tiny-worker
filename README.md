# tiny-worker

[![CI](https://github.com/phongsathornpt/tiny-worker/actions/workflows/ci.yml/badge.svg)](https://github.com/phongsathornpt/tiny-worker/actions/workflows/ci.yml)
![Go 1.27](https://img.shields.io/badge/Go-1.27-00ADD8)
![TinyGo 0.42](https://img.shields.io/badge/TinyGo-0.42-00ADD8)
![hello worker 42.8 KB gzip](https://img.shields.io/badge/hello_worker-42.8_KB_gzip-brightgreen)

A small, dependency-free Go framework for [Cloudflare Workers](https://workers.cloudflare.com/),
compiled to WebAssembly with TinyGo. It provides routing, middleware, tracing,
request-scoped values, and an optional typed REST layer.

The core hello worker is **42.8 KB gzipped**. Importing the REST package adds
JSON support only when you need it.

**Guide:** [Project site](#project-site) · [Install](#install) · [Quickstart](#quickstart) · [Examples](#examples) ·
[CLI](#cli) · [Performance](#performance) · [Framework guide](#framework-guide) ·
[Testing and development](#testing-and-development)

## Install

The framework targets Go 1.27 and TinyGo 0.42+. Install the Go module to use
the framework in your own worker:

```bash
go get github.com/phongsathornpt/tiny-worker
```

To install the companion CLI, see [CLI installation](#install-the-cli). Worker
builds require [TinyGo](https://tinygo.org/getting-started/install/). Binaryen
(`wasm-opt`) is optional; without it, builds warn and skip size optimization.

## Project site

Read the documentation at [tiny-worker-site.thornz.workers.dev](https://tiny-worker-site.thornz.workers.dev/).

The open-source project site uses templ at build time to generate HTML,
tiny-worker for interactive fragment routes, htmx 4.0 for progressive
enhancement, and custom CSS. Generated pages and assets are served by Cloudflare;
the TinyGo Worker handles the htmx fragment endpoints. The site is a nested Go
module so its templ dependency does not enter the framework's dependency graph.

Install the pinned templ generator before building the site:

```bash
go install github.com/a-h/templ/cmd/templ@v0.3.1020
```

```bash
make site-generate  # templ components -> static pages and Worker fragments
make site-dev       # build and run the site with Wrangler
make site-deploy    # deploy using wrangler.site.jsonc
```

## Quickstart

A worker is a normal Go program: build a router, attach it to an app, hand the
app to the Cloudflare bridge, then block so the runtime keeps serving.

```go
package main

import (
	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/router"
	"github.com/phongsathornpt/tiny-worker/runtime/cloudflare"
)

func main() {
	text := func(body string) tinyworker.Handler {
		return func(*tinyworker.Request) (*tinyworker.Response, error) {
			return &tinyworker.Response{
				Status:  200,
				Headers: []tinyworker.Header{{Name: "content-type", Value: "text/plain; charset=utf-8"}},
				Body:    []byte(body),
			}, nil
		}
	}

	r := router.New()
	r.Handle("GET", "/", text("hello from tiny-worker\n"))
	r.Handle("GET", "/hello/:name", func(req *tinyworker.Request) (*tinyworker.Response, error) {
		// Path params are positional; the last one is the deepest :name match.
		return text("hello " + req.Params[len(req.Params)-1].Value + "\n")(req)
	})

	app := tinyworker.New(nil)
	app.UseRouter(r)
	app.Use(tinyworker.LogRequests(func(method, path string, status int, ms int64) {
		println("tiny-worker:", method, path, status, ms)
	}))

	cloudflare.Register(app) // expose the app to worker.js
	select {}                // never returns: keep serving requests
}
```

Install the [CLI](#install-the-cli), then scaffold, build, and run it:

```
tiny-worker new myworker && cd myworker   # REST API scaffold with Clean Architecture
tiny-worker dev                           # build wasm, then `wrangler dev`
```

`tiny-worker build` writes `dist/worker.wasm` and the `worker.js` glue, `dev`
runs the local Wrangler server, and `deploy` ships the same artifact. Requests
look like:

```
$ curl -i localhost:8787/hello/ada
HTTP/1.1 200 OK
content-type: text/plain; charset=utf-8

hello ada

$ curl -i -X DELETE localhost:8787/hello/ada
HTTP/1.1 405 Method Not Allowed
allow: GET, HEAD

method not allowed

$ curl -i localhost:8787/nope
HTTP/1.1 404 Not Found

not found
```

With the default flags the hello worker gzips to ~42 KB; see
[Build flags & size pipeline](#build-flags--size-pipeline).

For JSON in and JSON out, add the [`rest` package](#rest-apis-rest-package):

```go
type CreateUser struct {
	Name  string `json:"name" validate:"required,min=2,max=64"`
	Email string `json:"email" validate:"required,email"`
}

func createUser(req *tinyworker.Request, in CreateUser) (rest.Result, error) {
	u := store.Create(in)
	return rest.Result{Status: 201, Body: u}, nil
}

rest.Post(r, "/users", rest.Handler(createUser))
```

`rest.Handler` binds the JSON body (strict — unknown fields are 400), runs the
validation tags, and renders either the value as 200 JSON or the shared error
envelope. The [REST section](#rest-apis-rest-package) covers query/path
binding, custom rules, and the envelope in full.

## Examples

Two complete, buildable programs live under [`examples/`](examples/):

| example | what it demonstrates | gzip |
|---------|----------------------|------|
| [`examples/hello`](examples/hello/main.go) | router with 404/405, middleware chain (request IDs, tracing, logging, recovery, CORS), request-scoped values, the panic contract | 42.8 KB |
| [`examples/rest`](examples/rest/main.go) | typed REST handlers, strict JSON binding, query and path binding, validation (tags, `Validatable`, a custom rule), the error envelope, an in-memory store | 288 KB |

Build either one and serve it locally:

```bash
tiny-worker build -main ./examples/rest && tiny-worker dev   # or: make wasm-rest
```

`examples/hello` exposes `GET /`, `GET /hello`, `POST /echo`, `GET /users/:id`,
`GET /trace`, `GET /whoami`, and `GET /panic`. `examples/rest` exposes:

| route | shows |
|-------|-------|
| `GET /` | the primitives path (`rest.OK`) |
| `GET /users` | query binding, slices (`?tag=a&tag=b`), coercion |
| `POST /users` | strict body binding, validation, `201` + `Location` |
| `GET /users/:id` | path params, `404` envelope |
| `PUT /users/:id` | one struct carrying a path param *and* a body |
| `DELETE /users/:id` | `204` with no body |
| `GET /boom` | a deliberate panic (trap + rebuild on Workers) |

The complete REST worker, including its in-memory store and runnable route
handlers, is in [`examples/rest`](examples/rest/main.go). Use it as a starting
point for a full application.

Because the router, middleware, and `rest` helpers all speak
`tinyworker.Handler`, typed handlers drop into an existing app without
rewriting anything.

## Performance

Two numbers decide whether a worker feels fast: **how much wasm the client
downloads** and **how much work each request does**.

### Bundle size

Measured on the committed artifacts — TinyGo 0.42 with `-opt=z -no-debug
-panic=trap -gc=conservative`, then `wasm-opt -Oz`:

| worker | raw | gzip | links |
|--------|----:|-----:|-------|
| [`examples/hello`](examples/hello/main.go) | 100 KB | **42.8 KB** | core framework only |
| [`examples/rest`](examples/rest/main.go) | 674 KB | **288 KB** | + `rest` and stdlib `encoding/json` |

The lean core is a deliberate result: the request path avoids `fmt`, `net/url`,
`regexp`, and `net/mail` (path parsing and email validation are hand-rolled,
pinned by differential tests), so the only heavy dependency is the JSON codec —
and that one is opt-in by import. Adding each of these to the hello worker costs
roughly:

| added to the hello worker | gzip delta |
|---------------------------|-----------:|
| a reflection-based tag encoder | +2.3 KB |
| `net/mail` (a naive `email` rule) | +55 KB |
| `regexp` (a pattern rule) | +65 KB |
| stdlib `encoding/json` | +239 KB |

That table is why the codec is a seam (`rest.DefaultCodec`, `rest.WithCodec`) and
why the built-in `email` rule does not use `net/mail`. It also shows the one
number to watch when extending the REST layer: once `encoding/json` is linked,
nested structs, slices, and maps are essentially free — it is a fixed cost, not
a per-feature one.

### Request path

Native micro-benchmarks (`make bench`), on an AMD Ryzen 9 7940HS (linux/amd64):

| benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| route lookup, static | 81 | 112 | 2 |
| route lookup, `:param` | 93 | 128 | 2 |
| `ParsePath`, origin-form | 17 | 0 | 0 |
| `ParsePath`, absolute URL | 47 | 0 | 0 |
| bind JSON body | 1470 | 865 | 11 |
| bind query only | 817 | 640 | 15 |
| validate a struct | 718 | 224 | 7 |
| render the error envelope | 730 | 520 | 8 |
| typed handler, end to end | 2750 | 1340 | 24 |

Routing and path parsing are allocation-light on purpose — they run on every
request, including 404s. The REST numbers are dominated by `encoding/json`
reflection, which is the honest cost of the stdlib codec; the codec seam exists
so a leaner codec can be swapped in later without touching handlers.

These run natively rather than in a browser: wasm wall-clock in CI is too noisy
to gate on, so the committed-size gate stands in as the wasm-side budget.

```bash
make bench        # native micro-benchmarks
make size-check   # gzip both examples and compare to the baselines
```

## CLI

`cmd/tiny-worker` is a standard-library-only companion CLI.

### Install the CLI

Install the latest release with Go 1.27 or newer:

```bash
go install github.com/phongsathornpt/tiny-worker/cmd/tiny-worker@latest
```

Go installs the executable to `$(go env GOBIN)` when set, or otherwise to
`$(go env GOPATH)/bin`. Add that directory to your `PATH` if `tiny-worker` is
not found. From a source checkout, `make cli` installs to
`$(go env GOPATH)/bin` by default; choose another destination with
`make cli BIN_DIR=/path/to/bin`.

The CLI commands are:

```
tiny-worker new myworker       # scaffold a Clean Architecture REST API project
tiny-worker routes             # list router.Handle registrations found in source
tiny-worker build              # TinyGo -> dist/worker.wasm + worker.js glue
tiny-worker dev                # build + wrangler dev
tiny-worker deploy             # build + wrangler deploy
```

The scaffold includes `internal/handler`, `internal/usecase`, `internal/model`,
`internal/repository`, `pkg/middleware`, and `pkg/utils`, plus versioned CRUD
routes under `/api/v1/users` and a `/healthz` endpoint. Its in-memory
repository is an example only; Worker isolates are ephemeral, so production
data needs durable storage.

Building a worker requires TinyGo; Binaryen (`wasm-opt`) is optional. `dev`
and `deploy` also use `npx wrangler` (Node.js/npm required). To install
Wrangler yourself, run `npm install -g wrangler`.

`routes` is static extraction via `go/ast` — it lists what it can prove from
source, not dynamic registrations. `build` writes the same worker.js glue as
the repo root (pinned by `TestGlueMatchesRepoWorker`) and syncs `wasm_exec.js`
from the vendored/installed TinyGo.

### Build flags & size pipeline

`tiny-worker build|deploy|dev` accept:

| flag | default | meaning |
|--------|--------------|---------------------------------------------------------|
| `-panic` | `trap` | TinyGo panic strategy. `trap` (default) emits a clean `unreachable` and is ~34% smaller raw than `print`; neither one unwinds, so a panic kills the runtime and worker.js rebuilds it regardless. |
| `-gc` | `conservative` | TinyGo GC. `leaking` builds smaller/faster wasm that never frees — fine for tiny short-lived workers. |
| `-opt` | `Oz` | Binaryen `wasm-opt` level applied after TinyGo (`Oz`/`O2`/`O3`); `none` skips the pass. |

When `wasm-opt` (binaryen) is installed, builds are size-optimized with
`wasm-opt -Oz`; without it the build warns and skips. `make wasm` mirrors the
CLI defaults.

The size gate (`make size-check`, run in CI) gzips each built example and fails
if it grows more than 2% over its committed baseline; shrinkage ratchets the
baseline down. There are two, so the lean core stays measured separately from
the opt-in REST layer:

| artifact | baseline file |
|----------|---------------|
| `examples/hello` | `testdata/wasm-size-baseline.txt` |
| `examples/rest` | `testdata/wasm-size-rest-baseline.txt` |

For intentional growth, run `make size-baseline` and commit the new numbers.
The gate needs binaryen (the baselines are recorded from optimized artifacts),
so it warns and skips where `wasm-opt` is missing — CI always enforces it. The
current sizes and the native benchmarks are in [Performance](#performance).

## Framework guide

### Runtime behavior

- **Panic recovery**: TinyGo does not unwind for panics under either strategy,
  so a panic in the request path cannot be recovered inside Go. With the default
  `-panic=trap` it surfaces to JS as a thrown `RuntimeError`; `worker.js` catches
  that, rebuilds the runtime, and retries — the failing request gets a 500 and
  the isolate keeps serving. `-panic=trap` is the default because it is much
  smaller than `-panic=print` (106 KB vs 162 KB raw for the hello example,
  pre-wasm-opt) and traps cleanly instead of printing and exiting. `Recover()`
  middleware still earns its place wherever the Go runtime can unwind — stock Go
  (`GOOS=js GOARCH=wasm`) builds and the browser/native test suites — where it
  turns a panic into a 500 `StatusError` that outer middleware such as
  `LogRequests` can observe. `scripts/smoke-worker.mjs` pins this contract
  against the built artifact (trap reaches JS, a rebuilt runtime serves again).
- **Body limit**: `cloudflare.MaxBodyBytes` (default 10 MiB) caps request bodies. Set it
  before `cloudflare.Register(...)`. Oversized requests get 413 before buffering —
  enforced in `worker.js` via `content-length` and again in the bridge before any copy
  into linear memory. Values `<= 0` disable the limit.
- **Boot resilience**: WASM instantiation failures are not cached; the runtime is reset
  and the next request retries boot. A fatal runtime death is detected per-request and
  the runtime is rebuilt inline. `worker.js` accepts both bundler-provided
  `WebAssembly.Module` imports (workerd/wrangler) and raw bytes (tests, Node).

### Routing & status semantics

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

### Middleware

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

### Tracing (W3C Trace Context)

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

### Request-scoped values

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

### REST APIs (`rest` package)

`rest` adds JSON responses, request binding, input validation, and one error
envelope — additively; the core framework is untouched. Importing it links a
JSON codec (stdlib `encoding/json` by default), so it is **opt-in by import**:
workers that never touch REST keep their size. That is exactly what the two
size baselines measure.

```go
r := router.New()
app := tinyworker.New(nil)
app.UseRouter(r)
app.Use(rest.Errors(), tinyworker.Recover()) // Errors outermost: one contract

rest.Get(r, "/users", rest.Handler(listUsers))
rest.Post(r, "/users", rest.Handler(createUser))
rest.Get(r, "/users/:id", rest.Handler(getUser))
rest.Delete(r, "/users/:id", rest.Handler(deleteUser))

type CreateUser struct {
    Name  string `json:"name" validate:"required,min=2,max=64"`
    Email string `json:"email" validate:"required,email"`
}

type UserPath struct {
    ID int `param:"id"`
}

func createUser(req *tinyworker.Request, in CreateUser) (rest.Result, error) {
    u := store.Create(in)
    return rest.Result{Status: 201, Body: u,
        Headers: []tinyworker.Header{{Name: "location", Value: "/users/" + strconv.Itoa(u.ID)}}}, nil
}

func deleteUser(req *tinyworker.Request, p UserPath) (rest.Result, error) {
    store.Delete(p.ID)
    return rest.Result{Status: 204}, nil
}
```

Typed handlers wrap into ordinary `tinyworker.Handler`s, so they compose with
the existing router and middleware. `rest.Handler` binds and validates one
input struct; `rest.HandlerNoInput` covers payload-less routes. A handler
returning a plain value renders 200 JSON, `rest.Result` picks a status, a
`*tinyworker.Response` passes through untouched, and a nil pointer renders 204.
The primitives (`rest.Bind`, `rest.Validate`, `rest.OK`, `rest.Created`,
`rest.Fail`, `rest.WriteError`, `rest.JSON`) are there for routes that want to
do it by hand.

### Binding

`rest.Bind` fills a struct from three sources:

- the **JSON body**, rejecting unknown fields by default (400 `invalid_json`)
  unless the route is registered with `rest.Strict(false)` — catching client
  typos instead of silently dropping them;
- **query parameters** into `query:"..."` fields: scalars take the first value,
  slices collect repeated (`?tag=a&tag=b`) and comma-separated (`?tag=a,b`)
  entries, and escapes are decoded *after* splitting so `%2C` stays inside one
  value;
- **path parameters** into `param:"..."` fields (bound after the body, so a
  path value wins over a body field of the same name).

Scalars coerce into `string`, `bool`, the `int`/`uint` families, and
`float32/64`; anything else is reported as an unsupported field type rather
than silently ignored. Bad values are 400 `bad_request` naming the field and
the expected type.

### Validation

Rules live in tags or in a `Validate() error` method (or both — tags run
first):

```go
type Signup struct {
    Email    string `json:"email" validate:"required,email"`
    Name     string `json:"name" validate:"required,min=2,max=64"`
    Role     string `json:"role" validate:"oneof=admin user"`
    Tags     []string `json:"tags" validate:"max=5"`
    Address  Address  `json:"address"` // nested: paths become address.city
}
```

Built-ins are `required`, `len`, `min`, `max`, `oneof`, and `email`.
`min`/`max` compare numbers by value and strings/slices by length (runes and
items, respectively). Shape rules **skip unset values**, so a range describes
values that are present; combine with `required` when presence matters
(`validate:"required,min=18"`). `rest.RegisterRule` adds domain rules, and
`rest.Validatable` handles logic that tags cannot express. Failures render 422
with ordered `details`: `address.city` and `tags[0]` use the JSON dot paths the
client sent.

The `email` rule is hand-rolled rather than `net/mail`-based: `net/mail` would
add ~55 KB gzip to every worker that imports `rest`, and `regexp` (for a
pattern rule) would add ~65 KB. Its behavior is pinned against `net/mail` by a
differential test, including the deliberate deviations (display-name forms,
undotted domains, one-character TLDs).

### Errors

The envelope is `{"error":{"code","message","details"}}`:

| situation | status | code |
|-----------|--------|------|
| body unparseable / unknown field / unsupported type | 400 | `invalid_json` |
| query or path value could not be bound | 400 | `bad_request` |
| validation rules failed | 422 | `validation_failed` |
| no route or record | 404 | `not_found` |
| path exists, method does not (keeps `Allow`) | 405 | `method_not_allowed` |
| anything unexpected | 500 | `internal_error` |

`rest.Errors()` makes it uniform: routing misses (404/405), handler errors,
and recovered panics all render as the envelope, while successful responses
pass through untouched. Register it as the outermost middleware. Typed
handlers and helpers render the envelope themselves, so a route behaves
correctly with or without the middleware. Server-side messages are generic
(`internal server error`) with the detail logged; 4xx messages keep the
`tinyworker.StatusError` text. Custom codes ride along on any `StatusError`.

The one exception is deliberate: `worker.js` produces its own 413 (body over
`cloudflare.MaxBodyBytes`) and panic 500 in plain text, before Go sees the
request, as the isolate's safety net.

### A real exchange

```
$ curl -i -X POST localhost:8787/users -d '{"name":"Ada","email":"ada@example.com","handle":"ada"}'
HTTP/1.1 201 Created
content-type: application/json
location: /users/1

{"id":1,"name":"Ada","email":"ada@example.com"}

$ curl -i -X POST localhost:8787/users -d '{"name":"Ada","email":"not-an-email","handle":"ada"}'
HTTP/1.1 422 Unprocessable Entity
content-type: application/json

{"error":{"code":"validation_failed","message":"validation failed","details":[{"field":"email","code":"email","message":"must be a valid email address"}]}}

$ curl -i -X POST localhost:8787/users -d '{"name":"Ada","email":"ada@example.com","nmae":"typo"}'
HTTP/1.1 400 Bad Request
content-type: application/json

{"error":{"code":"invalid_json","message":"unknown field \"nmae\"","details":[{"field":"nmae","code":"invalid_json","message":"unknown field \"nmae\""}]}}

$ curl -i 'localhost:8787/users?limit=1'
HTTP/1.1 200 OK
content-type: application/json

[{"id":1,"name":"Ada","email":"ada@example.com"}]

$ curl -i localhost:8787/users/999
HTTP/1.1 404 Not Found
content-type: application/json

{"error":{"code":"not_found","message":"user 999 does not exist"}}

$ curl -i localhost:8787/users/abc
HTTP/1.1 400 Bad Request
content-type: application/json

{"error":{"code":"bad_request","message":"invalid param value \"abc\": expected int","details":[{"field":"id","code":"bad_request","message":"invalid param value \"abc\": expected int"}]}}
```

The full program behind this is [`examples/rest`](examples/rest/main.go);
`make smoke` replays the same contract against the built wasm.

### The codec seam

`rest.Codec` is `Marshal`/`Unmarshal` — the same shape as `encoding/json`, so
the default adapter is a thin wrapper and a lean codec can drop in later
without touching call sites. `rest.DefaultCodec` swaps the codec for the whole
worker; `rest.WithCodec` does it per call. Strict decoding comes from the
optional `rest.StrictCodec` interface (`DisallowUnknownFields` in the default
adapter); a codec that implements only `Codec` decodes leniently.

## Testing and development

### Testing the bridge

The Cloudflare bridge (`runtime/cloudflare`) is tested under `GOOS=js GOARCH=wasm` with
[wasmbrowsertest](https://github.com/agnivade/wasmbrowsertest), which runs the tests in
a real Chrome — covering the echo roundtrip, 413 body-limit paths, panic recovery and
post-panic survival, handler errors, and JS-facing globals:

```
go install github.com/agnivade/wasmbrowsertest@latest
make wasm-test   # skips automatically if no browser is available
```

On Ubuntu 23.10+ the kernel blocks Chrome's sandbox by default (`No usable
sandbox!` on launch); run
`echo 0 | sudo tee /proc/sys/kernel/apparmor_restrict_unprivileged_userns`
once to allow it. CI does the same in
[.github/workflows/ci.yml](.github/workflows/ci.yml).

Those tests run the stock-Go `js/wasm` build. The TinyGo artifacts that actually
deploy — including anything `wasm-opt` rewrote — are covered separately by
`make smoke`, which boots each wasm in Node through the bridge and exercises
it. The `hello` profile checks the routes, body roundtrip, 404/405 semantics,
and the panic contract (trap reaches JS, a rebuilt runtime serves again); the
`rest` profile drives the whole REST contract: strict binding, 400 vs 422,
envelope details, query/path coercion, the custom rule registry, `Result`
statuses, and uniform routing-miss envelopes. It skips when node is absent.
`make bench` runs the native benchmarks (router dispatch, `ParsePath` —
allocation-free — and the `rest` binding/validation/rendering paths);
`make size-check` is the size gate.

CI (`.github/workflows/ci.yml`) runs the native suite, the bridge tests in Chrome, the
TinyGo builds with a pinned binaryen (so the size gates compare like for like),
the gzip size gates for both examples, the artifact smoke tests (hello + rest),
and a wrangler dry-run bundle on every push and PR.

### Development commands

```bash
make cli          # build the tiny-worker CLI into $(go env GOPATH)/bin
make check        # gofmt + vet + native tests + browser bridge tests
make bench        # native micro-benchmarks
make wasm         # TinyGo-build examples/hello -> dist/worker.wasm
make wasm-rest    # TinyGo-build examples/rest  -> dist/rest/worker.wasm
make size-check   # gzip both examples, compare against the committed baselines
make smoke        # boot the built wasm in Node and exercise the bridge
```

`make install-binaryen` vendors the pinned `wasm-opt` into `.tools/binaryen`
when you do not have one on `PATH`; `make install-wasmbrowsertest` does the same
for the browser test runner. All of the toolchains are optional — the
corresponding targets skip with a message when one is missing, and CI never
skips.

Pull requests are welcome. Two house rules keep the size budget honest:

- keep `make check` green;
- if your change moves the bundle size, say so, and only refresh a baseline
  (`make size-baseline`) when the growth is intentional.

## License

Released under the [MIT License](LICENSE).
