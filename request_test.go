package tinyworker

import (
	"strings"
	"testing"
)

func TestSetGetRoundTrip(t *testing.T) {
	req := &Request{Method: "GET", URL: "/"}
	req.Set("k", "v")

	got, ok := req.Get("k")
	if !ok || got != "v" {
		t.Fatalf("got=%v ok=%v", got, ok)
	}
}

func TestGetMissingAndNilSafety(t *testing.T) {
	req := &Request{}
	if _, ok := req.Get("nope"); ok {
		t.Fatal("expected missing key")
	}

	var nilReq *Request
	nilReq.Set("k", "v") // must not panic
	if _, ok := nilReq.Get("k"); ok {
		t.Fatal("nil request must not hold values")
	}
	nilReq.Delete("k")
}

func TestDelete(t *testing.T) {
	req := &Request{}
	req.Set("k", 1)
	req.Delete("k")
	if _, ok := req.Get("k"); ok {
		t.Fatal("value must be gone")
	}
	req.Delete("never-set") // no panic on absent key
}

func TestPerRequestIsolation(t *testing.T) {
	mw := func(next Handler) Handler {
		return func(req *Request) (*Response, error) {
			req.Set("who", "req-"+req.URL)
			return next(req)
		}
	}
	var seen []string
	handler := func(req *Request) (*Response, error) {
		v, _ := req.Get("who")
		seen = append(seen, v.(string))
		return &Response{Status: 200}, nil
	}
	app := New(handler)
	app.Use(mw)

	for _, url := range []string{"/a", "/b", "/c"} {
		if _, err := app.Serve(&Request{Method: "GET", URL: url}); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(seen, ",") != "req-/a,req-/b,req-/c" {
		t.Fatalf("seen = %v, want per-request values", seen)
	}
}

func TestMiddlewareToHandlerFlow(t *testing.T) {
	type ctxKey string
	userID := Value[int64](string(ctxKey("auth.user_id")))

	auth := func(next Handler) Handler {
		return func(req *Request) (*Response, error) {
			userID.Set(req, 42)
			return next(req)
		}
	}
	var got int64
	var ok bool
	handler := func(req *Request) (*Response, error) {
		got, ok = userID.Get(req)
		return &Response{Status: 200}, nil
	}

	app := New(handler)
	app.Use(auth)
	if _, err := app.Serve(&Request{Method: "GET", URL: "/me"}); err != nil {
		t.Fatal(err)
	}
	if !ok || got != 42 {
		t.Fatalf("got=%d ok=%v, want 42 true", got, ok)
	}
}

func TestValueTypedMismatch(t *testing.T) {
	req := &Request{}
	req.Set("n", "not-an-int")

	got, ok := Get[int](req, "n")
	if ok || got != 0 {
		t.Fatalf("got=%d ok=%v, want 0 false", got, ok)
	}
}

func TestPostHandlerRead(t *testing.T) {
	handler := func(req *Request) (*Response, error) {
		req.Set("status", "done")
		return &Response{Status: 200}, nil
	}
	logging := func(next Handler) Handler {
		return func(req *Request) (*Response, error) {
			res, err := next(req)
			if v, ok := req.Get("status"); !ok || v != "done" {
				t.Errorf("outer middleware lost value: %v %v", v, ok)
			}
			return res, err
		}
	}

	app := New(handler)
	app.Use(logging)
	if _, err := app.Serve(&Request{Method: "GET", URL: "/"}); err != nil {
		t.Fatal(err)
	}
}

func TestValuesSurviveRouterDispatch(t *testing.T) {
	r := newFakeRouterForValues()
	app := New(nil)
	app.UseRouter(r)
	app.Use(func(next Handler) Handler {
		return func(req *Request) (*Response, error) {
			Set(req, "trace", "t-1")
			return next(req)
		}
	})

	if _, err := app.Serve(&Request{Method: "GET", Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	if r.seen != "t-1" {
		t.Fatalf("handler saw %q, want t-1", r.seen)
	}
}

type routerForValues struct{ seen string }

func newFakeRouterForValues() *routerForValues {
	return &routerForValues{}
}

func (f *routerForValues) Lookup(method, path string) (Handler, []Param, error) {
	return func(req *Request) (*Response, error) {
		v, _ := Get[string](req, "trace")
		f.seen = v
		return &Response{Status: 200}, nil
	}, nil, nil
}
