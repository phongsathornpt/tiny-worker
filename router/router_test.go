package router

import (
	"errors"
	"strings"
	"testing"

	tinyworker "github.com/phongsathornpt/tiny-worker"
)

func TestMatchStaticAndParam(t *testing.T) {
	r := New()
	h := func(*tinyworker.Request) (*tinyworker.Response, error) { return nil, nil }
	r.Handle("GET", "/users/:id", h)

	got, params := r.Match("GET", "/users/42")
	if got == nil {
		t.Fatal("expected route")
	}
	if len(params) != 1 || params[0].Name != "id" || params[0].Value != "42" {
		t.Fatalf("unexpected params: %#v", params)
	}
	if got, _ := r.Match("POST", "/users/42"); got != nil {
		t.Fatal("unexpected method match")
	}
}

func TestStaticWinsOverParam(t *testing.T) {
	r := New()
	static := func(*tinyworker.Request) (*tinyworker.Response, error) { return &tinyworker.Response{Status: 204}, nil }
	param := func(*tinyworker.Request) (*tinyworker.Response, error) { return &tinyworker.Response{Status: 200}, nil }
	r.Handle("GET", "/users/:id", param)
	r.Handle("GET", "/users/me", static)

	h, params := r.Match("GET", "/users/me")
	if h == nil || len(params) != 0 {
		t.Fatalf("unexpected match: %#v", params)
	}
	res, _ := h(nil)
	if res.Status != 204 {
		t.Fatalf("expected static route, got %d", res.Status)
	}
}

func TestNestedParams(t *testing.T) {
	r := New()
	r.Handle("GET", "/org/:org/users/:id", func(*tinyworker.Request) (*tinyworker.Response, error) { return nil, nil })
	_, params := r.Match("GET", "/org/acme/users/42")
	if len(params) != 2 || params[0].Value != "acme" || params[1].Value != "42" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

// ---- Lookup: 404/405 semantics ----

func TestLookupHit(t *testing.T) {
	r := New()
	h := func(*tinyworker.Request) (*tinyworker.Response, error) { return &tinyworker.Response{Status: 200}, nil }
	r.Handle("GET", "/users/:id", h)

	got, params, err := r.Lookup("GET", "/users/42")
	if err != nil || got == nil {
		t.Fatalf("Lookup: h=%v err=%v", got, err)
	}
	if len(params) != 1 || params[0].Value != "42" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestLookupNotFound(t *testing.T) {
	r := New()
	r.Handle("GET", "/users", func(*tinyworker.Request) (*tinyworker.Response, error) { return nil, nil })

	if _, _, err := r.Lookup("GET", "/nope"); !errors.Is(err, tinyworker.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	// A deeper path under an existing prefix is still a 404, not a 405.
	if _, _, err := r.Lookup("GET", "/users/42/posts"); !errors.Is(err, tinyworker.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestLookupMethodNotAllowed(t *testing.T) {
	r := New()
	r.Handle("GET", "/users", func(*tinyworker.Request) (*tinyworker.Response, error) { return nil, nil })
	r.Handle("POST", "/users", func(*tinyworker.Request) (*tinyworker.Response, error) { return nil, nil })

	_, _, err := r.Lookup("DELETE", "/users")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, tinyworker.ErrMethodNotAllowed) {
		t.Fatalf("err = %v, want ErrMethodNotAllowed", err)
	}
	allow, ok := tinyworker.AllowHeader(err)
	if !ok || allow != "GET, HEAD, POST" {
		t.Fatalf("Allow = %q (ok=%v), want \"GET, HEAD, POST\"", allow, ok)
	}
}

func TestLookupHEADAddedForGET(t *testing.T) {
	r := New()
	r.Handle("GET", "/x", func(*tinyworker.Request) (*tinyworker.Response, error) { return nil, nil })

	_, _, err := r.Lookup("PUT", "/x")
	allow, ok := tinyworker.AllowHeader(err)
	if !ok || allow != "GET, HEAD" {
		t.Fatalf("Allow = %q (ok=%v), want \"GET, HEAD\"", allow, ok)
	}
}

func TestLookup404WinsOver405(t *testing.T) {
	r := New()
	r.Handle("GET", "/users", func(*tinyworker.Request) (*tinyworker.Response, error) { return nil, nil })

	// /userz does not exist at all: 404, never 405, even though /users does.
	if _, _, err := r.Lookup("DELETE", "/userz"); !errors.Is(err, tinyworker.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestLookupParamsIsolated(t *testing.T) {
	r := New()
	r.Handle("GET", "/a/:x/c", func(*tinyworker.Request) (*tinyworker.Response, error) { return nil, nil })

	// A failed param backtrack must not leak values into later lookups.
	if _, _, err := r.Lookup("GET", "/a/zz/yy"); !errors.Is(err, tinyworker.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	_, params, err := r.Lookup("GET", "/a/1/c")
	if err != nil || len(params) != 1 || params[0].Value != "1" {
		t.Fatalf("params = %#v err = %v", params, err)
	}
}

func TestParsePath(t *testing.T) {
	cases := map[string]string{
		"https://x.dev/a/b?c=d": "/a/b",
		"https://x.dev":         "/",
		"/a/b?q=1#frag":         "/a/b",
		"/":                     "/",
		"":                      "/",
		"/plain":                "/plain",
	}
	for in, want := range cases {
		if got := tinyworker.ParsePath(in); got != want {
			t.Errorf("ParsePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAllowHeaderHelper(t *testing.T) {
	if _, ok := tinyworker.AllowHeader(errors.New("plain")); ok {
		t.Fatal("plain error must not carry Allow")
	}
	allow, ok := tinyworker.AllowHeader(tinyworker.MethodNotAllowed("GET", "POST"))
	if !ok || !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
		t.Fatalf("allow = %q ok = %v", allow, ok)
	}
}
