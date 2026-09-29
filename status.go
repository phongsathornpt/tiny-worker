package tinyworker

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned when no route matches a request path.
var ErrNotFound = errors.New("tinyworker: route not found")

// ErrMethodNotAllowed is returned when a path exists but not for the request
// method. It is typically mapped to a 405 with an Allow header.
var ErrMethodNotAllowed = errors.New("tinyworker: method not allowed")

// StatusError carries an HTTP status code (and optional headers) alongside an
// error. Returning one from a handler or the router produces a response with
// that status instead of a generic 500.
type StatusError struct {
	Status  int
	Headers []Header
	Err     error
}

func (e *StatusError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("tinyworker: %d: %v", e.Status, e.Err)
	}
	return fmt.Sprintf("tinyworker: status error %d", e.Status)
}

// Unwrap exposes both the human message (if any) and the status-derived
// sentinel, so errors.Is(err, ErrNotFound / ErrMethodNotAllowed) works on
// errors built by NotFound / MethodNotAllowed.
func (e *StatusError) Unwrap() []error {
	var out []error
	if e.Err != nil {
		out = append(out, e.Err)
	}
	switch e.Status {
	case 404:
		out = append(out, ErrNotFound)
	case 405:
		out = append(out, ErrMethodNotAllowed)
	}
	return out
}

// NewStatusError wraps err with an HTTP status code and optional headers.
func NewStatusError(status int, err error, headers ...Header) *StatusError {
	return &StatusError{Status: status, Err: err, Headers: headers}
}

// NotFound builds a 404 StatusError, optionally with a custom message.
func NotFound(message ...string) *StatusError {
	msg := "not found"
	if len(message) > 0 && message[0] != "" {
		msg = message[0]
	}
	return NewStatusError(404, errors.New(msg))
}

// MethodNotAllowed builds a 405 StatusError carrying an Allow header when
// allowed lists at least one method (e.g. "GET, HEAD").
func MethodNotAllowed(allowed ...string) *StatusError {
	err := errors.New("method not allowed")
	if len(allowed) == 0 {
		return NewStatusError(405, err)
	}
	return NewStatusError(405, err, Header{Name: "Allow", Value: joinAllowed(allowed)})
}

// AllowHeader returns the Allow header from a StatusError, if present.
func AllowHeader(err error) (string, bool) {
	var se *StatusError
	if !errors.As(err, &se) {
		return "", false
	}
	for _, h := range se.Headers {
		if h.Name == "Allow" {
			return h.Value, true
		}
	}
	return "", false
}

// IsStatusError reports whether err carries an HTTP status code.
func IsStatusError(err error) bool {
	var se *StatusError
	return errors.As(err, &se)
}

func joinAllowed(methods []string) string {
	out := ""
	for i, m := range methods {
		if i > 0 {
			out += ", "
		}
		out += m
	}
	return out
}
