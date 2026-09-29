package tinyworker

import (
	"net/url"
	"strings"
	"testing"
)

// wantStdlib is the reference behavior the hand-rolled parser must match:
// the net/url-based ParsePath that shipped before path.go existed.
func wantStdlib(raw string) string {
	if raw == "" {
		return "/"
	}
	if !strings.Contains(raw, "://") {
		origin := raw
		if i := strings.IndexAny(origin, "?#"); i >= 0 {
			origin = origin[:i]
		}
		if origin == "" {
			return "/"
		}
		return origin
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "/"
	}
	if u.Path == "" {
		return "/"
	}
	return u.Path
}

// TestParsePathDifferential pins parseURLPath to the net/url reference on
// every shape the Workers bridge and users can produce.
func TestParsePathDifferential(t *testing.T) {
	cases := []string{
		"", "/", "//", "///",
		"/hello", "/hello?x=1", "/hello#frag", "/hello?x=1#frag",
		"/a/b/c", "/a/b/c/", "/a//b", "/a/b/",
		"/users/123?sort=asc", "/users/:id",
		"?query", "?query#frag", "#frag", "#",
		"a/b/c", "relative/path?x", "relative#f",
		"https://example.com",
		"https://example.com/",
		"https://example.com/hello",
		"https://example.com/hello?x=1#f",
		"https://example.com:8443/a/b?y=2",
		"http://host/a?b#c",
		"HTTPS://EXAMPLE.com/Path",
		"ftp://files.example.com/pub/go+1.27.tar.gz?sig=ab",
		"wss://edge.example/ws?room=1",
		"https://user:pw@example.com/deep/path",
		"https://example.com",
		"https://example.com?q=1",
		"https://example.com#f",
		"https://example.com/a#f?b",
		"mailto:someone@example.com",
		"//example.com/path",
		"/path://not-a-scheme",
		"a/b://c",
		"1://digits",
		"+.://plusdot",
		"https://example.com/%E4%BD%A0%E5%A5%BD",
		"https://example.com/a%2Fb",
		"/%2e%2e/trunk",
		"https://example.com/%zz",
		"https://example.com/a%2",
		"https://example.com/a b",
		"x",
		"/x",
		" ",
		"/ ",
		"https://ex.com/ space",
		":notascheme",
		"://missing",
	}
	for _, in := range cases {
		got := parseURLPath(in)
		want := wantStdlib(in)
		if got != want {
			t.Errorf("parseURLPath(%q) = %q, want %q (net/url reference)", in, got, want)
		}
	}
}

// TestParsePathRandomDifferential cross-checks against the stdlib on
// randomly generated URL shapes (bounded and seeded, so it runs in plain
// `go test` without -fuzz).
//
// Scope: the shapes the bridge and users actually produce — absolute URLs
// with well-formed hosts (name, userinfo, port, IPv6), origin-form paths,
// bare paths, queries, fragments, and percent-escapes. Deliberately excluded
// are degenerate authority constructions such as the protocol-relative
// "//:\/\/..." family, which only reach the url.Parse branch because they
// contain "://" later, and where url.Parse's authority parser (e.g.
// host ":" with path "///") differs from any sensible hand-rolled reading.
// workerd only ever supplies absolute URLs, so those cannot arrive here;
// TestParsePathDifferential covers the malformed inputs worth pinning.
func TestParsePathRandomDifferential(t *testing.T) {
	segments := []string{"a", "users", "123", "a+b", "%41", "%2E", "%2F", "x.y", "-", "", "a b"}
	schemes := []string{"https", "http", "wss", "FTP", "x"}
	hosts := []string{"example.com", "api.example.com:8443", "user:pw@example.com", "[::1]:80", "h"}
	queries := []string{"", "?q=1", "?q=%41&x=2", "?", "?a=b+c"}
	fragments := []string{"", "#f", "#", "#frag/../x"}

	rng := newSeededRand(20260930)
	for i := 0; i < 20000; i++ {
		var sb strings.Builder
		switch rng.intn(3) {
		case 0: // absolute URL
			sb.WriteString(schemes[rng.intn(len(schemes))])
			sb.WriteString("://")
			sb.WriteString(hosts[rng.intn(len(hosts))])
		case 1: // origin-form
			sb.WriteString("/")
		case 2: // bare path
		}
		for n := rng.intn(4); n > 0; n-- {
			if sb.Len() > 0 && !strings.HasSuffix(sb.String(), "/") {
				sb.WriteString("/")
			}
			sb.WriteString(segments[rng.intn(len(segments))])
		}
		sb.WriteString(queries[rng.intn(len(queries))])
		sb.WriteString(fragments[rng.intn(len(fragments))])

		in := sb.String()
		got := parseURLPath(in)
		want := wantStdlib(in)
		if got != want {
			t.Fatalf("parseURLPath(%q) = %q, want %q (net/url reference)", in, got, want)
		}
	}
}

// Tiny deterministic PRNG so the differential fuzz is reproducible without
// importing math/rand (and its wasm weight) into the framework package's
// non-test binary.
type seededRand struct{ s uint64 }

func newSeededRand(seed uint64) *seededRand { return &seededRand{s: seed} }

func (r *seededRand) intn(n int) int {
	r.s = r.s*6364136223846793005 + 1442695040888963407
	return int((r.s >> 33) % uint64(n))
}
