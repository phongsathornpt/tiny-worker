package rest

import (
	"errors"
	"strconv"
	"strings"

	tinyworker "github.com/phongsathornpt/tiny-worker"
)

// Documented error codes. Any tinyworker.StatusError passes through with its
// own status, so custom codes are possible by returning one.
const (
	CodeInvalidJSON      = "invalid_json"       // body was not parseable / had unknown fields
	CodeBadRequest       = "bad_request"        // request could not be bound (query/path/params)
	CodeValidationFailed = "validation_failed"  // parsed, but failed validation rules
	CodeNotFound         = "not_found"          // no route/record
	CodeMethodNotAllowed = "method_not_allowed" // path exists, method does not
	CodePayloadTooLarge  = "payload_too_large"  // 413
	CodeUnauthorized     = "unauthorized"       // 401
	CodeForbidden        = "forbidden"          // 403
	CodeInternalError    = "internal_error"     // 500
)

// FieldError is one problem with one input field. Field uses JSON dot paths
// (address.city, tags[0]) and is empty for problems that are not field-scoped.
type FieldError struct {
	Field   string `json:"field,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ValidationErrors collects field errors; returning one renders 422.
type ValidationErrors []FieldError

func (ve ValidationErrors) Error() string {
	var b strings.Builder
	b.WriteString("validation failed")
	for i, fe := range ve {
		if i == 0 {
			b.WriteString(": ")
		} else {
			b.WriteString("; ")
		}
		if fe.Field != "" {
			b.WriteString(fe.Field)
			b.WriteString(": ")
		}
		b.WriteString(fe.Message)
	}
	return b.String()
}

// BindError reports that a request could not be decoded or bound (400).
type BindError struct {
	Code    string // defaults to CodeBadRequest
	Message string
	Field   string // optional, JSON dot path
}

func (e *BindError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "invalid request"
	}
	if e.Field != "" {
		return "rest: bad request: " + e.Field + ": " + msg
	}
	return "rest: bad request: " + msg
}

// ErrorBody is the envelope's payload.
type ErrorBody struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details []FieldError `json:"details,omitempty"`
}

type errorEnvelope struct {
	Error ErrorBody `json:"error"`
}

var errInternal = errors.New("rest: internal error")

const fallbackInternalBody = `{"error":{"code":"internal_error","message":"internal server error"}}`

func envelopeResponse(codec Codec, status int, code, message string, details []FieldError, headers []tinyworker.Header) *tinyworker.Response {
	if codec == nil {
		codec = DefaultCodec
	}
	body, err := codec.Marshal(errorEnvelope{Error: ErrorBody{Code: code, Message: message, Details: details}})
	if err != nil {
		body = []byte(fallbackInternalBody)
	}
	hs := make([]tinyworker.Header, 0, len(headers)+1)
	hs = append(hs, tinyworker.Header{Name: "content-type", Value: "application/json"})
	hs = append(hs, headers...)
	return &tinyworker.Response{Status: status, Headers: hs, Body: body}
}

// Fail renders an enveloped error response. Handy for primitives-based routes:
//
//	return rest.Fail(404, rest.CodeNotFound, "user not found")
func Fail(status int, code, message string, details ...FieldError) *tinyworker.Response {
	return envelopeResponse(DefaultCodec, status, code, message, details, nil)
}

// WriteError maps any error onto an enveloped response. BindError becomes 400,
// ValidationErrors becomes 422, tinyworker.StatusError keeps its status and
// headers, ErrNotFound/ErrMethodNotAllowed map to 404/405, and anything else
// becomes a logged 500.
func WriteError(err error, opts ...Option) *tinyworker.Response {
	return errorResponse(err, newConfig(opts))
}

func errorResponse(err error, cfg config) *tinyworker.Response {
	if err == nil {
		err = errInternal
	}

	var ve ValidationErrors
	if errors.As(err, &ve) {
		return envelopeResponse(cfg.codec, 422, CodeValidationFailed, "validation failed", []FieldError(ve), nil)
	}

	var be *BindError
	if errors.As(err, &be) {
		code := be.Code
		if code == "" {
			code = CodeBadRequest
		}
		msg := be.Message
		if msg == "" {
			msg = "invalid request"
		}
		var details []FieldError
		if be.Field != "" {
			details = []FieldError{{Field: be.Field, Code: code, Message: msg}}
		}
		return envelopeResponse(cfg.codec, 400, code, msg, details, nil)
	}

	var se *tinyworker.StatusError
	if errors.As(err, &se) {
		return envelopeResponse(cfg.codec, se.Status, codeForStatus(se.Status), statusMessage(se), nil, se.Headers)
	}
	if errors.Is(err, tinyworker.ErrNotFound) {
		return envelopeResponse(cfg.codec, 404, CodeNotFound, "not found", nil, nil)
	}
	if errors.Is(err, tinyworker.ErrMethodNotAllowed) {
		return envelopeResponse(cfg.codec, 405, CodeMethodNotAllowed, "method not allowed", nil, nil)
	}

	println("rest: handler error:", err.Error())
	return envelopeResponse(cfg.codec, 500, CodeInternalError, "internal server error", nil, nil)
}

// statusMessage keeps the StatusError's own message when it has one, and
// avoids leaking internals for server errors.
func statusMessage(se *tinyworker.StatusError) string {
	if se.Status >= 500 {
		return "internal server error"
	}
	if se.Err != nil {
		if msg := se.Err.Error(); msg != "" {
			return msg
		}
	}
	return defaultMessage(se.Status)
}

func defaultMessage(status int) string {
	switch status {
	case 400:
		return "bad request"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not found"
	case 405:
		return "method not allowed"
	case 413:
		return "payload too large"
	case 422:
		return "validation failed"
	case 500:
		return "internal server error"
	}
	return "error"
}

func codeForStatus(status int) string {
	switch status {
	case 400:
		return CodeBadRequest
	case 401:
		return CodeUnauthorized
	case 403:
		return CodeForbidden
	case 404:
		return CodeNotFound
	case 405:
		return CodeMethodNotAllowed
	case 413:
		return CodePayloadTooLarge
	case 422:
		return CodeValidationFailed
	}
	if status >= 500 {
		return CodeInternalError
	}
	return "error"
}

// Errors returns middleware that renders every framework-produced failure —
// routing misses (404/405), handler errors, and recovered panics — in the
// envelope shape, so clients see one contract. Successful responses pass
// through untouched. Leave it out if you want the framework's plain-text
// misses instead.
//
// Register it as the outermost middleware so it also envelopes errors from
// handlers wrapped by other middleware.
func Errors(opts ...Option) tinyworker.Middleware {
	cfg := newConfig(opts)
	return func(next tinyworker.Handler) tinyworker.Handler {
		return func(req *tinyworker.Request) (res *tinyworker.Response, err error) {
			defer func() {
				if r := recover(); r != nil {
					println("rest: recovered panic:", panicText(r))
					res, err = envelopeResponse(cfg.codec, 500, CodeInternalError, "internal server error", nil, nil), nil
				}
			}()
			res, err = next(req)
			if err != nil {
				return errorResponse(err, cfg), nil
			}
			if res == nil {
				println("rest: handler returned no response and no error")
				return envelopeResponse(cfg.codec, 500, CodeInternalError, "internal server error", nil, nil), nil
			}
			return res, nil
		}
	}
}

// panicText renders a recovered panic value without fmt (which would pull its
// formatting graph into the wasm).
func panicText(v any) string {
	switch x := v.(type) {
	case error:
		return x.Error()
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		if s, ok := v.(interface{ String() string }); ok {
			return s.String()
		}
		return "panic"
	}
}
