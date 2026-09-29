package router

import (
	"strconv"
	"testing"

	tinyworker "github.com/thorn/tiny-worker"
)

func benchmarkRouter() *Router {
	r := New()
	h := func(*tinyworker.Request) (*tinyworker.Response, error) { return nil, nil }
	for i := 0; i < 100; i++ {
		base := "/api/v1/resource" + strconv.Itoa(i)
		r.Handle("GET", base, h)
		r.Handle("GET", base+"/:id", h)
	}
	return r
}

func BenchmarkMatchStatic(b *testing.B) {
	r := benchmarkRouter()
	b.ReportAllocs()
	for b.Loop() {
		r.Match("GET", "/api/v1/resource99")
	}
}

func BenchmarkMatchParam(b *testing.B) {
	r := benchmarkRouter()
	b.ReportAllocs()
	for b.Loop() {
		r.Match("GET", "/api/v1/resource99/123456")
	}
}
