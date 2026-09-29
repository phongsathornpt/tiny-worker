package tinyworker

import (
	"errors"
	"strings"
	"testing"
)

func ok(*Request) (*Response, error) { return &Response{Status: 200}, nil }

func TestChainOrder(t *testing.T) {
	var order []string
	mark := func(name string) Middleware {
		return func(next Handler) Handler {
			return func(req *Request) (*Response, error) {
				order = append(order, name+":in")
				res, err := next(req)
				order = append(order, name+":out")
				return res, err
			}
		}
	}

	app := New(ok)
	app.Use(mark("first"), mark("second"))

	if _, err := app.Serve(&Request{Method: "GET", URL: "/"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"first:in", "second:in", "second:out", "first:out"}
	if strings.Join(order, " ") != strings.Join(want, " ") {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestMiddlewareWrapsRouterMisses(t *testing.T) {
	var statuses []int
	recorder := func(status int) Middleware {
		return func(next Handler) Handler {
			return func(req *Request) (*Response, error) {
				_, err := next(req)
				statuses = append(statuses, statusOf(err, 200))
				return nil, err
			}
		}
	}

	app := New(nil)
	app.Use(recorder(0))
	app.UseRouter(&fakeRouter{err: MethodNotAllowed("GET")})

	_, err := app.Serve(&Request{Method: "DELETE", Path: "/x"})
	if !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("err = %v", err)
	}
	if len(statuses) != 1 || statuses[0] != 405 {
		t.Fatalf("middleware observed statuses %v, want [405]", statuses)
	}
}

func TestRecoverMiddleware(t *testing.T) {
	app := New(func(*Request) (*Response, error) {
		panic("boom")
	})
	app.Use(Recover())

	res, err := app.Serve(&Request{Method: "GET", URL: "/panic"})
	if err == nil {
		t.Fatal("expected error")
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 500 {
		t.Fatalf("err = %v, want 500 StatusError", err)
	}
	if res != nil {
		t.Fatalf("res = %#v, want nil", res)
	}
}

func TestRecoverWithLogRequests(t *testing.T) {
	var logged []int
	app := New(func(*Request) (*Response, error) { panic("kaboom") })
	app.Use(
		LogRequests(func(_, _ string, status int, _ int64) { logged = append(logged, status) }),
		Recover(),
	)

	if _, err := app.Serve(&Request{Method: "GET", Path: "/x"}); err == nil {
		t.Fatal("expected error")
	}
	if len(logged) != 1 || logged[0] != 500 {
		t.Fatalf("logged = %v, want [500]", logged)
	}
}

func TestLogRequests(t *testing.T) {
	var lines []string
	log := func(method, path string, status int, ms int64) {
		lines = append(lines, method+" "+path+" "+itoa(status))
	}

	app := New(func(*Request) (*Response, error) { return &Response{Status: 201}, nil })
	app.Use(LogRequests(log))
	if _, err := app.Serve(&Request{Method: "POST", Path: "/a"}); err != nil {
		t.Fatal(err)
	}

	app2 := New(nil)
	app2.Use(LogRequests(log))
	app2.UseRouter(&fakeRouter{err: ErrNotFound})
	if _, err := app2.Serve(&Request{Method: "GET", Path: "/missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}

	if len(lines) != 2 || lines[0] != "POST /a 201" || lines[1] != "GET /missing 404" {
		t.Fatalf("lines = %v", lines)
	}
}

func TestCORSActualResponse(t *testing.T) {
	app := New(func(*Request) (*Response, error) {
		return &Response{Status: 200, Headers: []Header{{Name: "content-type", Value: "text/plain"}}}, nil
	})
	app.Use(CORS(CORSOptions{AllowOrigins: []string{"https://good.dev"}}))

	res, err := app.Serve(&Request{
		Method: "GET", Path: "/",
		Headers: []Header{{Name: "Origin", Value: "https://good.dev"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := responseHeaderValue(res, "Access-Control-Allow-Origin"); got != "https://good.dev" {
		t.Fatalf("ACAO = %q", got)
	}
	if responseHeaderValue(res, "content-type") != "text/plain" {
		t.Fatal("existing headers must be preserved")
	}
}

func TestCORSWildcard(t *testing.T) {
	app := New(ok)
	app.Use(CORS(CORSOptions{})) // defaults to "*"

	res, err := app.Serve(&Request{Method: "GET", Path: "/", Headers: []Header{{Name: "Origin", Value: "https://any.dev"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := responseHeaderValue(res, "Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("ACAO = %q, want *", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	app := New(ok)
	app.Use(CORS(CORSOptions{
		AllowOrigins: []string{"https://good.dev"},
		AllowMethods: []string{"GET", "POST"},
		AllowHeaders: []string{"content-type", "x-api"},
		MaxAge:       "86400",
	}))

	req := &Request{
		Method: "OPTIONS", Path: "/anything", // not routed; preflight answered anyway
		Headers: []Header{
			{Name: "Origin", Value: "https://good.dev"},
			{Name: "Access-Control-Request-Method", Value: "POST"},
		},
	}
	res, err := app.Serve(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 204 {
		t.Fatalf("status = %d, want 204", res.Status)
	}
	for _, want := range [][2]string{
		{"Access-Control-Allow-Origin", "https://good.dev"},
		{"Access-Control-Allow-Methods", "GET, POST"},
		{"Access-Control-Allow-Headers", "content-type, x-api"},
		{"Access-Control-Max-Age", "86400"},
	} {
		if got := responseHeaderValue(res, want[0]); got != want[1] {
			t.Fatalf("%s = %q, want %q", want[0], got, want[1])
		}
	}
}

func TestCORSDisallowedOrigin(t *testing.T) {
	app := New(ok)
	app.Use(CORS(CORSOptions{AllowOrigins: []string{"https://good.dev"}}))

	// Preflight from a bad origin: rejected before routing.
	req := &Request{
		Method: "OPTIONS", Path: "/",
		Headers: []Header{
			{Name: "Origin", Value: "https://evil.dev"},
			{Name: "Access-Control-Request-Method", Value: "POST"},
		},
	}
	if _, err := app.Serve(req); err == nil {
		t.Fatal("expected rejection for disallowed origin")
	}

	// Actual request from a bad origin: served, but without ACAO.
	res, err := app.Serve(&Request{Method: "GET", Path: "/", Headers: []Header{{Name: "Origin", Value: "https://evil.dev"}}})
	if err != nil {
		t.Fatal(err)
	}
	if responseHeaderValue(res, "Access-Control-Allow-Origin") != "" {
		t.Fatal("must not leak ACAO to disallowed origin")
	}
}

func TestCORSHeadersOnError(t *testing.T) {
	app := New(nil)
	app.Use(CORS(CORSOptions{AllowOrigins: []string{"https://good.dev"}}))
	app.UseRouter(&fakeRouter{err: ErrNotFound})

	res, err := app.Serve(&Request{
		Method: "GET", Path: "/missing",
		Headers: []Header{{Name: "Origin", Value: "https://good.dev"}},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if res == nil || res.Status != 404 {
		t.Fatalf("res = %#v, want 404 response rebuilt for CORS", res)
	}
	if got := responseHeaderValue(res, "Access-Control-Allow-Origin"); got != "https://good.dev" {
		t.Fatalf("ACAO on error = %q", got)
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}
