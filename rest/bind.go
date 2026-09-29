package rest

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"

	tinyworker "github.com/phongsathornpt/tiny-worker"
)

// Bind fills dst from the request:
//
//   - the JSON body (when present) through the configured codec, rejecting
//     unknown fields unless Strict(false) was given;
//   - query parameters into fields tagged `query:"name"`;
//   - path parameters into fields tagged `param:"name"`.
//
// dst must be a non-nil pointer to a struct. Scalar fields take the first
// value; slice fields collect repeated and comma-separated values. Failures
// come back as *BindError (mapped to 400 invalid_json or bad_request).
func Bind(req *tinyworker.Request, dst any, opts ...Option) error {
	cfg := newConfig(opts)
	if req == nil {
		return &BindError{Code: CodeBadRequest, Message: "rest: nil request"}
	}
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return &BindError{Code: CodeBadRequest, Message: "rest: Bind requires a non-nil pointer"}
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return &BindError{
			Code:    CodeBadRequest,
			Message: "rest: Bind requires a pointer to a struct, got " + elem.Kind().String(),
		}
	}

	if len(req.Body) > 0 {
		if err := decodeInto(cfg, req.Body, dst); err != nil {
			return err
		}
	}

	pairs, ok := parseQuery(rawQuery(req.URL))
	if !ok {
		return &BindError{Code: CodeBadRequest, Message: "malformed query string"}
	}
	if err := bindTagged(elem, "query", func(key string, wantSlice bool) []string {
		return queryValueSource(pairs, key, wantSlice)
	}, "query"); err != nil {
		return err
	}
	if err := bindTagged(elem, "param", func(key string, wantSlice bool) []string {
		for _, p := range req.Params {
			if p.Name == key {
				return []string{p.Value}
			}
		}
		return nil
	}, "param"); err != nil {
		return err
	}
	return nil
}

// BindValidate is Bind followed by Validate — the sequence typed handlers run.
func BindValidate(req *tinyworker.Request, dst any, opts ...Option) error {
	if err := Bind(req, dst, opts...); err != nil {
		return err
	}
	return Validate(dst)
}

func decodeInto(cfg config, body []byte, dst any) error {
	var err error
	if cfg.strict {
		if sc, ok := cfg.codec.(StrictCodec); ok {
			err = sc.UnmarshalStrict(body, dst)
		} else {
			// Strictness needs codec support; a plain Codec decodes leniently.
			err = cfg.codec.Unmarshal(body, dst)
		}
	} else {
		err = cfg.codec.Unmarshal(body, dst)
	}
	if err != nil {
		return jsonBindError(err)
	}
	return nil
}

// bindTagged assigns values looked up by a struct tag ("query" or "param").
// Errors name the field by its tag value — that is the name the client sent.
func bindTagged(elem reflect.Value, tag string, lookup func(key string, wantSlice bool) []string, source string) error {
	rt := elem.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.PkgPath != "" {
			continue // unexported
		}
		key := f.Tag.Get(tag)
		if key == "" || key == "-" {
			continue
		}
		fv := elem.Field(i)
		vals := lookup(key, fv.Kind() == reflect.Slice)
		if len(vals) == 0 {
			continue // absent: leave the zero value (required rule catches it)
		}
		if err := assignStrings(fv, vals, key, source); err != nil {
			return err
		}
	}
	return nil
}

// jsonBindError classifies a codec failure into the envelope's 400 vocabulary.
func jsonBindError(err error) *BindError {
	if err == nil {
		return nil
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		msg := "invalid value"
		if typeErr.Type != nil {
			msg += ": expected " + typeErr.Type.String()
		}
		return &BindError{Code: CodeInvalidJSON, Field: typeErr.Field, Message: msg}
	}
	if strings.Contains(err.Error(), "unknown field") {
		name := quotedName(err.Error())
		if name == "" {
			return &BindError{Code: CodeInvalidJSON, Message: "unknown field in request body"}
		}
		return &BindError{
			Code:    CodeInvalidJSON,
			Field:   name,
			Message: "unknown field " + quote(name),
		}
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return &BindError{
			Code:    CodeInvalidJSON,
			Message: "malformed JSON at byte offset " + strconv.FormatInt(syntaxErr.Offset, 10),
		}
	}
	return &BindError{Code: CodeInvalidJSON, Message: "malformed JSON request body"}
}

// quotedName pulls the first quoted name out of a codec error such as
// `json: unknown field "nickname"`.
func quotedName(msg string) string {
	i := strings.IndexByte(msg, '"')
	if i < 0 {
		return ""
	}
	rest := msg[i+1:]
	if j := strings.IndexByte(rest, '"'); j >= 0 {
		return rest[:j]
	}
	return ""
}

// fieldPath names a field the way clients see it: its JSON name when tagged,
// otherwise the Go field name.
func fieldPath(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	name := tag
	if i := strings.IndexByte(tag, ','); i >= 0 {
		name = tag[:i]
	}
	if name == "" || name == "-" {
		return f.Name
	}
	return name
}
