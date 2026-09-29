package tinyworker

import "strings"

// parseURLPath extracts the path component of an HTTP URL without net/url,
// which drags a large dependency graph into the wasm binary for work the
// Workers bridge never needs. net/url remains used by ParsePath on
// non-wasm builds; the differential test in path_test.go pins both
// implementations to identical results on HTTP-relevant inputs.
//
// Handles the shapes the runtime actually sees:
//
//   - absolute URLs:     scheme://host[:port]/path?query#fragment
//   - origin-form paths: /path?query#fragment
//   - bare paths:        path (no leading slash)
//   - degenerate inputs: "", "?query", "#fragment"
//
// Semantics match the old net/url-based ParsePath exactly, quirks included
// (differential tests in path_test.go pin all of this):
//
//   - any raw string containing "://" went through url.Parse: its path is
//     percent-DECODED (/%41 routes as /A), an invalid escape anywhere or an
//     escape in the host makes it return "/", and a first ':' means error;
//   - everything else (origin-form, bare paths) passes through raw — the old
//     code never consulted url.Parse for them, so no decoding/validation.
//
// The Workers bridge always supplies absolute URLs, so decoded paths are
// what production routing sees; the raw pass-through only affects direct
// ParsePath callers, and changing it would change behavior. Clean input on
// the decoded branch takes a zero-allocation fast path.
func parseURLPath(raw string) string {
	if raw == "" {
		return "/"
	}
	// Mirror the old code's branch order exactly: containing "://" at all
	// sent the raw string through url.Parse (with its error fallback and
	// percent-decoding); anything else was handled without url.Parse.
	if strings.Contains(raw, "://") {
		if i := indexOfScheme(raw); i >= 0 {
			return absoluteURLPath(raw, i)
		}
		// Contains "://" but not as a valid scheme separator: url.Parse
		// semantics on a weird input. ':' first means "missing protocol
		// scheme" (error → "/"); otherwise it parses as a relative path
		// (e.g. "a/b://c" → "a/b://c", decoded, query/fragment cut).
		if raw[0] == ':' {
			return "/"
		}
		path := cutQueryFragment(raw)
		if !validEscapes(path) {
			return "/"
		}
		return unescape(path)
	}
	// No scheme: origin-form or bare path. Historical behavior: raw
	// pass-through (the old code never invoked url.Parse here), so escapes
	// are neither validated nor decoded.
	if raw[0] == '?' || raw[0] == '#' {
		return "/"
	}
	path := cutQueryFragment(raw)
	if path == "" {
		return "/"
	}
	return path
}

// absoluteURLPath extracts and decodes the path of "scheme://..." raw, where
// the scheme ends at schemeEnd (the ':' index), following url.Parse's
// precedence: the fragment is cut from everything first, then the authority
// runs to the first '/', then the query is cut from the path.
func absoluteURLPath(raw string, schemeEnd int) string {
	rest := raw[schemeEnd+3:]
	if i := indexByte(rest, '#'); i >= 0 {
		rest = rest[:i]
	}
	slash := indexByte(rest, '/')
	host, path := rest, "/"
	if slash >= 0 {
		host, path = rest[:slash], rest[slash:]
	}
	// url.Parse validates the host's character set (space, controls, and
	// other strays are "invalid character in host name" errors; any % escape
	// in the host is rejected outright — see the differential tests), and
	// rejects invalid escapes in the path. All map to "/" like the old
	// error fallback did.
	if !hostValid(host) || !validEscapes(path) {
		return "/"
	}
	if i := indexByte(path, '?'); i >= 0 {
		path = path[:i]
		if path == "" {
			return "/"
		}
	}
	return unescape(path)
}

// validEscapes reports whether every '%' in s introduces a valid two-hex
// escape (url.Parse's rule: any other '%' is an "invalid URL escape" error).
func validEscapes(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return false
		}
		i += 2
	}
	return true
}

// unescape percent-decodes s, which validEscapes has already accepted.
// Returns s unchanged (no allocation) when it contains no '%'.
func unescape(s string) string {
	if indexByte(s, '%') < 0 {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b = append(b, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 2
			continue
		}
		b = append(b, s[i])
	}
	return string(b)
}

// hostValid mirrors url.Parse's host character set for the region before
// the first '/' (userinfo '@', port ':', and IPv6 brackets included).
// Allowed: RFC 3986 unreserved + sub-delims, ':', '[', ']', '<', '>', '"',
// and '@'. Everything else — spaces, controls, '%', '|', '\\', '^', '{',
// '}' — is a host parse error in net/url. (Escaped non-ASCII in hosts,
// which url.Parse uniquely accepts, is out of scope: real Worker request
// URLs carry punycode hosts.)
func hostValid(host string) bool {
	for i := 0; i < len(host); i++ {
		c := host[i]
		switch {
		case isAlpha(c) || isDigit(c):
		case c == '-' || c == '.' || c == '_' || c == '~':
		case strings.IndexByte("!$&'()*+,;=:[]<>\"@", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// indexOfScheme returns the index of "://" if raw starts with a valid
// scheme (letter followed by letters/digits/+/-/.), else -1.
func indexOfScheme(raw string) int {
	for i := 0; i < len(raw); i++ {
		if raw[i] == ':' {
			if i == 0 {
				return -1
			}
			// Scheme validity barely matters for path extraction: what we
			// actually need is the authority delimiter. Require at least
			// one leading scheme-ish byte to avoid treating "a/b://c"
			// path segments as a scheme, then let "://" decide.
			if i+2 < len(raw) && raw[i+1] == '/' && raw[i+2] == '/' {
				return i
			}
			return -1
		}
		c := raw[i]
		if !isAlpha(c) && !isDigit(c) && c != '+' && c != '-' && c != '.' {
			return -1
		}
	}
	return -1
}

func cutQueryFragment(path string) string {
	if i := indexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	if i := indexByte(path, '#'); i >= 0 {
		path = path[:i]
	}
	if path == "" {
		return "/"
	}
	return path
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
