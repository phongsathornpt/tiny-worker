//go:build js && wasm

package cloudflare

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"syscall/js"
	"testing"

	tinyworker "github.com/thorn/tiny-worker"
)

func buildApp(t *testing.T, handler tinyworker.Handler) *tinyworker.App {
	t.Helper()
	app := tinyworker.New(handler)
	Register(app)
	t.Cleanup(func() {
		app = nil
		if fetchFunc.Value.Get("id").Truthy() {
			fetchFunc.Release()
			fetchFunc = js.FuncOf(nil)
		}
		js.Global().Delete("tinyWorkerFetch")
		js.Global().Delete("tinyWorkerMaxBody")
	})
	return app
}

// request builds a plain JS object shaped like what worker.js passes to
// tinyWorkerFetch: method, url, headers as [name, value] pairs, body as a
// Uint8Array.
func request(t *testing.T, method, url string, headers map[string]string, body []byte) js.Value {
	t.Helper()
	obj := js.Global().Get("Object").New()
	obj.Set("method", method)
	obj.Set("url", url)

	jsHeaders := js.Global().Get("Array").New(len(headers))
	i := 0
	for name, value := range headers {
		pair := js.Global().Get("Array").New(2)
		pair.SetIndex(0, name)
		pair.SetIndex(1, value)
		jsHeaders.SetIndex(i, pair)
		i++
	}
	obj.Set("headers", jsHeaders)

	if body == nil {
		obj.Set("body", js.Global().Get("Uint8Array").New(0))
	} else {
		array := js.Global().Get("Uint8Array").New(len(body))
		js.CopyBytesToJS(array, body)
		obj.Set("body", array)
	}
	return obj
}

func invoke(t *testing.T, in js.Value) (status int, headers map[string]string, body []byte) {
	t.Helper()
	result := fetch(js.Undefined(), []js.Value{in})
	out, ok := result.(js.Value)
	if !ok {
		t.Fatal("fetch returned no js.Value")
	}
	status = out.Get("status").Int()

	headers = map[string]string{}
	jsHeaders := out.Get("headers")
	for i := 0; i < jsHeaders.Length(); i++ {
		pair := jsHeaders.Index(i)
		headers[strings.ToLower(pair.Index(0).String())] = pair.Index(1).String()
	}

	length := out.Get("body").Get("byteLength").Int()
	body = make([]byte, length)
	js.CopyBytesToGo(body, out.Get("body"))
	return status, headers, body
}

// ---- tests ----

func TestEchoRoundTrip(t *testing.T) {
	buildApp(t, func(req *tinyworker.Request) (*tinyworker.Response, error) {
		return &tinyworker.Response{
			Status:  200,
			Headers: []tinyworker.Header{{Name: "content-type", Value: "text/plain"}},
			Body:    []byte(req.Method + " " + req.URL + " " + string(req.Body)),
		}, nil
	})

	status, headers, body := invoke(t, request(t, "POST", "/echo?x=1", map[string]string{"x-trace": "t1"}, []byte("payload")))
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	if headers["content-type"] != "text/plain" {
		t.Fatalf("content-type = %q", headers["content-type"])
	}
	if string(body) != "POST /echo?x=1 payload" {
		t.Fatalf("body = %q", body)
	}
}

func TestMaxBodyGlobalPublished(t *testing.T) {
	buildApp(t, func(*tinyworker.Request) (*tinyworker.Response, error) { return &tinyworker.Response{Status: 204}, nil })

	v := js.Global().Get("tinyWorkerMaxBody")
	if v.Type() != js.TypeNumber || v.Int() != MaxBodyBytes {
		t.Fatalf("tinyWorkerMaxBody = %v, want %d", v, MaxBodyBytes)
	}
}

func TestPayloadTooLarge(t *testing.T) {
	buildApp(t, func(*tinyworker.Request) (*tinyworker.Response, error) {
		t.Error("handler must not be called for oversized bodies")
		return &tinyworker.Response{Status: 200}, nil
	})

	status, _, body := invoke(t, request(t, "POST", "/big", nil, bytes.Repeat([]byte("x"), MaxBodyBytes+1)))
	if status != 413 || string(body) != "payload too large" {
		t.Fatalf("status=%d body=%q, want 413 payload too large", status, body)
	}
}

func TestBodyAtLimitAccepted(t *testing.T) {
	var gotLen int
	buildApp(t, func(req *tinyworker.Request) (*tinyworker.Response, error) {
		gotLen = len(req.Body)
		return &tinyworker.Response{Status: 200, Body: []byte(strconv.Itoa(gotLen))}, nil
	})

	status, _, body := invoke(t, request(t, "POST", "/big", nil, bytes.Repeat([]byte("x"), MaxBodyBytes)))
	if status != 200 || string(body) != strconv.Itoa(MaxBodyBytes) {
		t.Fatalf("status=%d body=%q, want 200 len=%d", status, body, MaxBodyBytes)
	}
}

func TestPayloadTooLargeDisabled(t *testing.T) {
	original := MaxBodyBytes
	MaxBodyBytes = 0
	t.Cleanup(func() { MaxBodyBytes = original })

	buildApp(t, func(*tinyworker.Request) (*tinyworker.Response, error) { return &tinyworker.Response{Status: 204}, nil })

	status, _, _ := invoke(t, request(t, "POST", "/big", nil, bytes.Repeat([]byte("x"), MaxBodyBytes+1+original)))
	if status != 204 {
		t.Fatalf("status = %d, want 204 (limit disabled)", status)
	}
}

func TestPanicRecovered(t *testing.T) {
	buildApp(t, func(*tinyworker.Request) (*tinyworker.Response, error) {
		panic("boom")
	})

	status, _, body := invoke(t, request(t, "GET", "/panic", nil, nil))
	if status != 500 || string(body) != "internal server error" {
		t.Fatalf("status=%d body=%q, want 500 internal server error", status, body)
	}

	// The isolate must survive the panic: a follow-up request still works.
	after, ok := fetch(js.Undefined(), []js.Value{request(t, "GET", "/alive", nil, nil)}).(js.Value)
	if !ok || after.Get("status").Int() != 500 {
		t.Fatalf("follow-up request after panic: %v", after)
	}
}

func TestHandlerErrorYields500(t *testing.T) {
	buildApp(t, func(*tinyworker.Request) (*tinyworker.Response, error) {
		return nil, errors.New("handler failed")
	})

	status, _, body := invoke(t, request(t, "GET", "/fail", nil, nil))
	if status != 500 || string(body) != "internal server error" {
		t.Fatalf("status=%d body=%q, want 500 internal server error", status, body)
	}
}

func TestRuntimeNotInitialized(t *testing.T) {
	app = nil // fetch with no registered app

	status, _, body := invoke(t, request(t, "GET", "/", nil, nil))
	if status != 500 || string(body) != "tiny-worker: runtime not initialized" {
		t.Fatalf("status=%d body=%q, want 500 not-initialized message", status, body)
	}
}

func TestRouterMissReturns404(t *testing.T) {
	// A nil-handler App behaves like a router with no match: Serve returns
	// ErrNotFound and the bridge must map it to a 404, never a 500.
	buildApp(t, nil)

	status, _, body := invoke(t, request(t, "GET", "/missing", nil, nil))
	if status != 404 || string(body) != "not found" {
		t.Fatalf("status=%d body=%q, want 404 not found", status, body)
	}
}

func TestStatusErrorFromHandler(t *testing.T) {
	buildApp(t, func(*tinyworker.Request) (*tinyworker.Response, error) {
		return nil, tinyworker.NotFound("no such widget")
	})

	status, _, body := invoke(t, request(t, "GET", "/widgets/9", nil, nil))
	if status != 404 || string(body) != "no such widget" {
		t.Fatalf("status=%d body=%q, want 404 no such widget", status, body)
	}
}

func TestMethodNotAllowedCarriesAllowHeader(t *testing.T) {
	// The 405 comes back from the router as a MethodNotAllowed StatusError;
	// render it through the bridge's error mapping exactly as Serve would.
	status, headers, body := invokeStatus(t, tinyworker.MethodNotAllowed("GET", "POST"))
	if status != 405 {
		t.Fatalf("status = %d, want 405", status)
	}
	if headers["allow"] != "GET, POST" {
		t.Fatalf("Allow = %q, want GET, POST", headers["allow"])
	}
	if body == nil {
		t.Fatal("405 must have a body")
	}
}

func TestResponseDecoding(t *testing.T) {
	buildApp(t, func(*tinyworker.Request) (*tinyworker.Response, error) {
		return &tinyworker.Response{
			Status: 201,
			Headers: []tinyworker.Header{
				{Name: "x-multi", Value: "a"},
				{Name: "x-multi", Value: "b"},
			},
			Body: []byte("created"),
		}, nil
	})

	status, headers, body := invoke(t, request(t, "GET", "/", nil, nil))
	if status != 201 || string(body) != "created" {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if headers["x-multi"] != "b" {
		t.Fatalf("duplicate headers not decoded: %v", headers)
	}
}
