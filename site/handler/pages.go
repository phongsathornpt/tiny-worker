package handler

import (
	"io/fs"

	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/router"
)

func Register(r *router.Router, fragments fs.FS) error {
	for path, file := range map[string]string{
		"/fragments/example/hello": "rendered/fragments/hello.html",
		"/fragments/example/rest":  "rendered/fragments/rest.html",
	} {
		body, err := fs.ReadFile(fragments, file)
		if err != nil {
			return err
		}
		r.Handle("GET", path, html(body))
	}
	r.Handle("GET", "/healthz", text([]byte("ok")))
	return nil
}

func html(body []byte) tinyworker.Handler {
	return func(*tinyworker.Request) (*tinyworker.Response, error) {
		return &tinyworker.Response{
			Status:  200,
			Headers: []tinyworker.Header{{Name: "content-type", Value: "text/html; charset=utf-8"}},
			Body:    body,
		}, nil
	}
}

func text(body []byte) tinyworker.Handler {
	return func(*tinyworker.Request) (*tinyworker.Response, error) {
		return &tinyworker.Response{
			Status:  200,
			Headers: []tinyworker.Header{{Name: "content-type", Value: "text/plain; charset=utf-8"}},
			Body:    body,
		}, nil
	}
}
