//go:build js && wasm

package cloudflare

import (
	"errors"
	"strconv"
	"syscall/js"

	tinyworker "github.com/phongsathornpt/tiny-worker"
)

var app *tinyworker.App
var fetchFunc js.Func

func Register(a *tinyworker.App) {
	app = a
	// Publish the body limit so worker.js can reject oversized requests
	// before buffering them, using the same value as the Go side.
	js.Global().Set("tinyWorkerMaxBody", MaxBodyBytes)
	fetchFunc = js.FuncOf(fetch)
	js.Global().Set("tinyWorkerFetch", fetchFunc)
}

func fetch(_ js.Value, args []js.Value) any {
	if app == nil || len(args) < 1 {
		return response(500, nil, []byte("tiny-worker: runtime not initialized"))
	}
	return safeServe(args[0])
}

// safeServe recovers panics anywhere in the request path (decode, app.Serve,
// encode) and converts them into a 500 response. Without it a panic escapes
// js.FuncOf and kills the Go runtime inside the isolate.
func safeServe(in js.Value) (out any) {
	defer func() {
		if r := recover(); r != nil {
			println("tiny-worker: recovered panic:", panicString(r))
			out = response(500, nil, []byte("internal server error"))
		}
	}()
	return serve(in)
}

func serve(in js.Value) any {
	size := bodySize(in.Get("body"))
	if overLimit(size) {
		return response(413, nil, []byte("payload too large"))
	}

	req := &tinyworker.Request{
		Method:  in.Get("method").String(),
		URL:     in.Get("url").String(),
		Path:    tinyworker.ParsePath(in.Get("url").String()),
		Headers: headersFromJS(in.Get("headers")),
		Body:    bytesFromJS(in.Get("body")),
	}
	res, err := app.Serve(req)
	if err != nil {
		return errorResponse(err)
	}
	if res == nil {
		return response(500, nil, []byte("internal server error"))
	}
	return response(res.Status, res.Headers, res.Body)
}

// errorResponse maps handler and router errors onto HTTP statuses: StatusError
// carries its own code (404/405 from the router), ErrNotFound is a bare 404,
// everything else is a 500.
func errorResponse(err error) any {
	var se *tinyworker.StatusError
	if errors.As(err, &se) {
		return response(se.Status, se.Headers, []byte(statusMessage(se)))
	}
	if errors.Is(err, tinyworker.ErrNotFound) {
		return response(404, nil, []byte("not found"))
	}
	println("tiny-worker: handler error:", err.Error())
	return response(500, nil, []byte("internal server error"))
}

func statusMessage(se *tinyworker.StatusError) string {
	if se.Err != nil {
		return se.Err.Error()
	}
	return "error"
}

// panicString renders a recovered panic value without fmt (which would drag
// its formatting graph into the wasm). Mirrors fmt.Sprint for the shapes a
// panic value takes: error, stringer, string, common scalars, else a
// constant (rare in practice; deliberate panics are strings or errors).
func panicString(v any) string {
	switch x := v.(type) {
	case error:
		return x.Error()
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		if s, ok := v.(interface{ String() string }); ok {
			return s.String()
		}
		return "panic"
	}
}

// bodySize reads byteLength without copying anything into linear memory.
func bodySize(value js.Value) int {
	if value.IsUndefined() || value.IsNull() {
		return 0
	}
	return value.Get("byteLength").Int()
}

func overLimit(size int) bool {
	return MaxBodyBytes > 0 && size > MaxBodyBytes
}
