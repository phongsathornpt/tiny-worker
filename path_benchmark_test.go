package tinyworker

import "testing"

func BenchmarkParsePathAbsolute(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		ParsePath("https://example.com/api/v1/users/123?sort=asc&fields=name")
	}
}

func BenchmarkParsePathOriginForm(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		ParsePath("/api/v1/users/123?sort=asc")
	}
}
