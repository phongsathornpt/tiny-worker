package main

import (
	tinyworker "github.com/thorn/tiny-worker"
	"github.com/thorn/tiny-worker/router"
	"github.com/thorn/tiny-worker/runtime/cloudflare"
)

// requestID demonstrates request-scoped values: middleware generates an ID,
// stores it on the request, and the handler (or later middleware) reads it.
var requestID = tinyworker.Value[string]("request.id")

func main() {
	r := router.New()

	text := func(body string) tinyworker.Handler {
		return func(*tinyworker.Request) (*tinyworker.Response, error) {
			return &tinyworker.Response{
				Status:  200,
				Headers: []tinyworker.Header{{Name: "content-type", Value: "text/plain; charset=utf-8"}},
				Body:    []byte(body),
			}, nil
		}
	}

	r.Handle("GET", "/hello", text("hello"))
	r.Handle("GET", "/panic", func(*tinyworker.Request) (*tinyworker.Response, error) {
		panic("boom: deliberate panic for testing recovery")
	})
	r.Handle("POST", "/echo", func(req *tinyworker.Request) (*tinyworker.Response, error) {
		return &tinyworker.Response{
			Status:  200,
			Headers: []tinyworker.Header{{Name: "content-type", Value: "text/plain; charset=utf-8"}},
			Body:    req.Body,
		}, nil
	})
	r.Handle("GET", "/users/:id", func(req *tinyworker.Request) (*tinyworker.Response, error) {
		return &tinyworker.Response{
			Status:  200,
			Headers: []tinyworker.Header{{Name: "content-type", Value: "text/plain; charset=utf-8"}},
			Body:    []byte("user " + req.Params[len(req.Params)-1].Value),
		}, nil
	})
	r.Handle("GET", "/whoami", func(req *tinyworker.Request) (*tinyworker.Response, error) {
		id, _ := requestID.Get(req)
		return text("request " + id)(req)
	})
	r.Handle("GET", "/trace", func(req *tinyworker.Request) (*tinyworker.Response, error) {
		tc, ok := tinyworker.Trace(req)
		if !ok {
			return text("no trace context")(req)
		}
		return text(tc.String())(req)
	})

	assignRequestID := func(next tinyworker.Handler) tinyworker.Handler {
		return func(req *tinyworker.Request) (*tinyworker.Response, error) {
			id := headerValue(req, "x-request-id")
			if id == "" {
				id = "req-internal"
			}
			requestID.Set(req, id)
			res, err := next(req)
			// Stamp the ID on the way out so callers can correlate logs.
			if res != nil {
				res.Headers = append(res.Headers, tinyworker.Header{Name: "x-request-id", Value: id})
			}
			return res, err
		}
	}

	app := tinyworker.New(nil)
	app.UseRouter(r)
	// Outermost first: logging sees the outcome of recovery and CORS.
	app.Use(
		assignRequestID,
		tinyworker.Tracing(),
		tinyworker.LogRequests(func(method, path string, status int, ms int64) {
			println("tiny-worker:", method, path, status, ms)
		}),
		tinyworker.Recover(),
		tinyworker.CORS(tinyworker.CORSOptions{
			AllowOrigins: []string{"*"},
			AllowMethods: []string{"GET", "POST"},
			MaxAge:       "86400",
		}),
	)
	cloudflare.Register(app)
	select {}
}

func headerValue(req *tinyworker.Request, name string) string {
	for _, h := range req.Headers {
		if h.Name == name {
			return h.Value
		}
	}
	return ""
}
