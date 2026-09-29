package router

import (
	"strconv"
	"testing"

	tinyworker "github.com/phongsathornpt/tiny-worker"
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

// Benchmarks for Lookup, the path the App actually dispatches through.
func BenchmarkLookupStatic(b *testing.B) {
	r := benchmarkRouter()
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := r.Lookup("GET", "/api/v1/resource99"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLookupParam(b *testing.B) {
	r := benchmarkRouter()
	b.ReportAllocs()
	for b.Loop() {
		h, _, err := r.Lookup("GET", "/api/v1/resource99/123456")
		if err != nil || h == nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLookupMethodNotAllowed(b *testing.B) {
	r := benchmarkRouter()
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := r.Lookup("POST", "/api/v1/resource99"); err == nil {
			b.Fatal("expected 405")
		}
	}
}
