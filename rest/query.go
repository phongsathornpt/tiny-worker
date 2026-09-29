package rest

import (
	"reflect"
	"strconv"
	"strings"
)

// queryPair is one key/value from the query string, in order. value is
// decoded; rawValue keeps the undecoded text so slices can be split on literal
// commas before decoding (an escaped %2C must stay inside a single value).
type queryPair struct {
	key      string
	value    string
	rawValue string
}

// rawQuery returns the query component of rawURL: the text between the first
// '?' and the fragment. Hand-rolled rather than net/url-based to keep the
// dependency out of the wasm; behavior is pinned against url.ParseQuery by the
// differential tests.
func rawQuery(rawURL string) string {
	i := strings.IndexByte(rawURL, '?')
	if i < 0 {
		return ""
	}
	q := rawURL[i+1:]
	if j := strings.IndexByte(q, '#'); j >= 0 {
		q = q[:j]
	}
	return q
}

// parseQuery splits a raw query string into decoded pairs. ok is false when a
// percent-escape is malformed. Empty segments ("a=1&&b=2") are skipped, as
// url.ParseQuery does; ';' is not a separator (url.ParseQuery rejects it
// outright, so it stays part of the value).
func parseQuery(raw string) (pairs []queryPair, ok bool) {
	for raw != "" {
		field := raw
		if i := strings.IndexByte(raw, '&'); i >= 0 {
			field, raw = raw[:i], raw[i+1:]
		} else {
			raw = ""
		}
		if field == "" {
			continue
		}
		key, rawValue := field, ""
		if i := strings.IndexByte(field, '='); i >= 0 {
			key, rawValue = field[:i], field[i+1:]
		}
		k, ok := decodeComponent(key)
		if !ok {
			return nil, false
		}
		v, ok := decodeComponent(rawValue)
		if !ok {
			return nil, false
		}
		pairs = append(pairs, queryPair{key: k, value: v, rawValue: rawValue})
	}
	return pairs, true
}

// decodeComponent decodes one query component: '+' is a space, %XX is an
// escaped byte. ok is false for a malformed escape.
func decodeComponent(s string) (string, bool) {
	if strings.IndexByte(s, '%') < 0 && strings.IndexByte(s, '+') < 0 {
		return s, true
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '+':
			b = append(b, ' ')
		case '%':
			if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
				return "", false
			}
			b = append(b, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 2
		default:
			b = append(b, s[i])
		}
	}
	return string(b), true
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

// queryValueSource resolves a query parameter for a field: scalars take the
// first occurrence's decoded value, slices collect every occurrence plus the
// literal-comma-separated entries inside each (with escapes decoded after the
// split, so %2C stays part of one value).
func queryValueSource(pairs []queryPair, key string, wantSlice bool) []string {
	if !wantSlice {
		for _, p := range pairs {
			if p.key == key {
				return []string{p.value}
			}
		}
		return nil
	}
	var out []string
	for _, p := range pairs {
		if p.key != key {
			continue
		}
		for _, part := range strings.Split(p.rawValue, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if v, ok := decodeComponent(part); ok {
				out = append(out, v)
			}
		}
	}
	return out
}

// assignStrings binds decoded values into fv: a scalar takes the first value,
// a slice collects all of them. Splitting (repeated params, literal commas) is
// the value source's job — see Bind.
func assignStrings(fv reflect.Value, vals []string, path, source string) error {
	if fv.Kind() == reflect.Slice {
		out := reflect.MakeSlice(fv.Type(), 0, len(vals))
		for _, s := range vals {
			elem := reflect.New(fv.Type().Elem()).Elem()
			if err := assignScalar(elem, s, path, source); err != nil {
				return err
			}
			out = reflect.Append(out, elem)
		}
		fv.Set(out)
		return nil
	}
	if len(vals) == 0 {
		return nil
	}
	return assignScalar(fv, vals[0], path, source)
}

func assignScalar(fv reflect.Value, s, path, source string) error {
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(s)
	case reflect.Bool:
		v, err := strconv.ParseBool(s)
		if err != nil {
			return coercionError(source, path, s, "bool")
		}
		fv.SetBool(v)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(s, 10, fv.Type().Bits())
		if err != nil {
			return coercionError(source, path, s, fv.Kind().String())
		}
		fv.SetInt(v)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(s, 10, fv.Type().Bits())
		if err != nil {
			return coercionError(source, path, s, fv.Kind().String())
		}
		fv.SetUint(v)
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(s, fv.Type().Bits())
		if err != nil {
			return coercionError(source, path, s, fv.Kind().String())
		}
		fv.SetFloat(v)
	default:
		return &BindError{
			Code:    CodeBadRequest,
			Field:   path,
			Message: "cannot bind " + source + " value into " + fv.Type().String(),
		}
	}
	return nil
}

func coercionError(source, path, raw, want string) *BindError {
	return &BindError{
		Code:    CodeBadRequest,
		Field:   path,
		Message: "invalid " + source + " value " + quote(raw) + ": expected " + want,
	}
}

// quote renders a short, quoted, single-line form of s for error messages
// (long values are truncated) without pulling fmt into the wasm.
func quote(s string) string {
	const max = 48
	if len(s) > max {
		s = s[:max] + "..."
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			b.WriteString("\\x")
			const hexdigits = "0123456789abcdef"
			b.WriteByte(hexdigits[c>>4])
			b.WriteByte(hexdigits[c&0xf])
			continue
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}
