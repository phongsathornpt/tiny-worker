package rest

import (
	"reflect"

	tinyworker "github.com/phongsathornpt/tiny-worker"
)

// Router is the routing surface the registration helpers need; the concrete
// *router.Router satisfies it, so this package never imports router.
type Router interface {
	Handle(method, path string, handler tinyworker.Handler)
}

// Get, Post, Put, Patch and Delete register a handler on r.
func Get(r Router, path string, h tinyworker.Handler)   { r.Handle("GET", path, h) }
func Post(r Router, path string, h tinyworker.Handler)  { r.Handle("POST", path, h) }
func Put(r Router, path string, h tinyworker.Handler)   { r.Handle("PUT", path, h) }
func Patch(r Router, path string, h tinyworker.Handler) { r.Handle("PATCH", path, h) }
func Delete(r Router, path string, h tinyworker.Handler) {
	r.Handle("DELETE", path, h)
}

// JSON renders v as a JSON response with the given status. Marshalling
// failures come back as an error for the caller (or the typed wrapper) to map.
func JSON(status int, v any, opts ...Option) (*tinyworker.Response, error) {
	cfg := newConfig(opts)
	body, err := cfg.codec.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &tinyworker.Response{
		Status:  status,
		Headers: []tinyworker.Header{{Name: "content-type", Value: "application/json"}},
		Body:    body,
	}, nil
}

// OK renders v as 200 JSON.
func OK(v any, opts ...Option) (*tinyworker.Response, error) { return JSON(200, v, opts...) }

// Created renders v as 201 JSON.
func Created(v any, opts ...Option) (*tinyworker.Response, error) { return JSON(201, v, opts...) }

// NoContent is a 204 with no body.
func NoContent() *tinyworker.Response { return &tinyworker.Response{Status: 204} }

// Result lets a typed handler pick a status and extra headers; return it as
// the handler's output value. Body is marshalled as JSON unless it is nil or
// the status is 204.
type Result struct {
	Status  int
	Body    any
	Headers []tinyworker.Header
}

// Handler wraps a typed body handler into a tinyworker.Handler: it binds the
// request into In (body, query, path params), validates it, calls fn, and
// renders the result as 200 JSON — or with the status from a Result, a
// *tinyworker.Response passed through untouched, and 204 for a nil pointer.
// Bind, validation and handler errors all render as envelope responses, so a
// route behaves the same whether or not rest.Errors is installed.
//
//	rest.Post(r, "/users", rest.Handler(func(req *tinyworker.Request, in CreateUser) (User, error) {
//		return store.Create(in), nil
//	}))
func Handler[In any, Out any](fn func(*tinyworker.Request, In) (Out, error), opts ...Option) tinyworker.Handler {
	cfg := newConfig(opts)
	return func(req *tinyworker.Request) (*tinyworker.Response, error) {
		if fn == nil {
			return errorResponse(errInternal, cfg), nil
		}
		var in In
		if err := Bind(req, &in, opts...); err != nil {
			return errorResponse(err, cfg), nil
		}
		if err := Validate(&in); err != nil {
			return errorResponse(err, cfg), nil
		}
		out, err := fn(req, in)
		return finish(out, err, cfg)
	}
}

// HandlerNoInput wraps a handler that takes no input payload (GET-style
// routes). Use Handler with an input struct when query or path binding is
// needed, or the primitives directly.
func HandlerNoInput[Out any](fn func(*tinyworker.Request) (Out, error), opts ...Option) tinyworker.Handler {
	cfg := newConfig(opts)
	return func(req *tinyworker.Request) (*tinyworker.Response, error) {
		if fn == nil {
			return errorResponse(errInternal, cfg), nil
		}
		out, err := fn(req)
		return finish(out, err, cfg)
	}
}

// finish renders a typed handler's outcome, mapping errors to envelopes.
func finish[Out any](out Out, err error, cfg config) (*tinyworker.Response, error) {
	if err != nil {
		return errorResponse(err, cfg), nil
	}
	res, rerr := renderOut(out, cfg)
	if rerr != nil {
		return errorResponse(rerr, cfg), nil
	}
	return res, nil
}

func renderOut(out any, cfg config) (*tinyworker.Response, error) {
	switch v := out.(type) {
	case nil:
		return NoContent(), nil
	case *tinyworker.Response:
		if v == nil {
			return NoContent(), nil
		}
		return v, nil
	case Result:
		return resultResponse(v, cfg)
	case *Result:
		if v == nil {
			return NoContent(), nil
		}
		return resultResponse(*v, cfg)
	}
	rv := reflect.ValueOf(out)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return NoContent(), nil
		}
	}
	return JSON(200, out, WithCodec(cfg.codec))
}

func resultResponse(r Result, cfg config) (*tinyworker.Response, error) {
	status := r.Status
	if status == 0 {
		status = 200
	}
	if r.Body == nil || status == 204 {
		return &tinyworker.Response{Status: status, Headers: r.Headers}, nil
	}
	res, err := JSON(status, r.Body, WithCodec(cfg.codec))
	if err != nil {
		return nil, err
	}
	res.Headers = append(res.Headers, r.Headers...)
	return res, nil
}
