package tinyworker

import (
	"errors"
	"testing"
)

type fakeRouter struct {
	handler Handler
	err     error
	gotPath string
}

func (f *fakeRouter) Lookup(method, path string) (Handler, []Param, error) {
	f.gotPath = path
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.handler, []Param{{Name: "id", Value: "42"}}, nil
}

func TestAppServe(t *testing.T) {
	app := New(func(req *Request) (*Response, error) {
		return &Response{Status: 200, Body: []byte(req.Method + " " + req.URL)}, nil
	})

	res, err := app.Serve(&Request{Method: "GET", URL: "/health"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || string(res.Body) != "GET /health" {
		t.Fatalf("unexpected response: %#v", res)
	}
}

func TestRequestResponseHeadersAndBody(t *testing.T) {
	app := New(func(req *Request) (*Response, error) {
		if len(req.Headers) != 1 || req.Headers[0].Name != "x-test" {
			t.Fatalf("unexpected headers: %#v", req.Headers)
		}
		return &Response{Status: 201, Headers: req.Headers, Body: req.Body}, nil
	})
	res, err := app.Serve(&Request{
		Method: "POST", URL: "/echo",
		Headers: []Header{{Name: "x-test", Value: "ok"}}, Body: []byte("payload"),
	})
	if err != nil || res.Status != 201 || string(res.Body) != "payload" {
		t.Fatalf("unexpected result: res=%#v err=%v", res, err)
	}
}

func TestAppRouterDispatch(t *testing.T) {
	routed := &fakeRouter{
		handler: func(req *Request) (*Response, error) {
			return &Response{Status: 200, Body: []byte(req.Params[0].Value)}, nil
		},
	}
	app := New(nil)
	app.UseRouter(routed)

	res, err := app.Serve(&Request{Method: "GET", URL: "https://x.dev/users/42?x=1", Path: "/users/42"})
	if err != nil || res.Status != 200 || string(res.Body) != "42" {
		t.Fatalf("res=%#v err=%v", res, err)
	}
}

func TestAppRouterPathFallback(t *testing.T) {
	routed := &fakeRouter{
		handler: func(*Request) (*Response, error) { return &Response{Status: 200}, nil },
	}
	app := New(nil)
	app.UseRouter(routed)

	// Empty Path: App must derive it from URL, minus the query string.
	if _, err := app.Serve(&Request{Method: "GET", URL: "/a/b?q=1"}); err != nil {
		t.Fatal(err)
	}
	if routed.gotPath != "/a/b" {
		t.Fatalf("router got path %q, want /a/b", routed.gotPath)
	}
}

func TestAppRouterErrorsPropagate(t *testing.T) {
	app := New(nil)
	app.UseRouter(&fakeRouter{err: ErrNotFound})
	if _, err := app.Serve(&Request{Method: "GET", Path: "/"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	se := MethodNotAllowed("GET")
	app.UseRouter(&fakeRouter{err: se})
	if _, err := app.Serve(&Request{Method: "DELETE", Path: "/"}); !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("err = %v, want ErrMethodNotAllowed", err)
	}
}

func TestAppServeNil(t *testing.T) {
	var app *App
	if _, err := app.Serve(&Request{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestStatusErrorMapping(t *testing.T) {
	se := NotFound("no such widget")
	if se.Status != 404 || se.Err.Error() != "no such widget" {
		t.Fatalf("se = %#v", se)
	}
	if !IsStatusError(se) || IsStatusError(errors.New("plain")) {
		t.Fatal("IsStatusError misbehaving")
	}
	// Unwrap exposes the message and the status-derived sentinel; errors.Is
	// finds ErrNotFound through either route.
	if !errors.Is(se, ErrNotFound) {
		t.Fatal("errors.Is(se, ErrNotFound) = false")
	}
	found := false
	for _, e := range se.Unwrap() {
		if errors.Is(e, ErrNotFound) {
			found = true
		}
	}
	if !found {
		t.Fatal("Unwrap chain does not contain ErrNotFound")
	}
}
