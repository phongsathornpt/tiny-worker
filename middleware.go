package tinyworker

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// Recover converts panics into a 500 StatusError instead of letting them
// escape the handler. The bridge also recovers, but recovering here keeps the
// Go runtime healthy with a proper error value for outer middleware (e.g.
// LogRequests) to observe.
//
// This only fires where the Go runtime can unwind: stock Go
// (GOOS=js GOARCH=wasm) builds and the native/browser test suites. TinyGo does
// not unwind for panics in any -panic strategy, so in production builds a
// panic traps and worker.js's rebuild-and-retry turns it into a 500 instead.
// Keep Recover in the chain: it is what makes panic behavior correct on the
// runtimes that support recovering.
func Recover() Middleware {
	return func(next Handler) Handler {
		return func(req *Request) (res *Response, err error) {
			defer func() {
				if r := recover(); r != nil {
					println("tiny-worker: recovered panic:", panicValue(r))
					err = NewStatusError(500, errors.New("internal server error"))
					res = nil
				}
			}()
			return next(req)
		}
	}
}

// LoggerFunc receives one line per request. Returns for log are ignored;
// log to whatever sink you have (println reaches worker console logs).
type LoggerFunc func(method, path string, status int, ms int64)

// LogRequests logs method, path, status, and duration for every request,
// including router misses (404/405) and recovered panics (500).
func LogRequests(log LoggerFunc) Middleware {
	return func(next Handler) Handler {
		return func(req *Request) (*Response, error) {
			start := time.Now()
			res, err := next(req)
			status := 200
			switch {
			case err != nil:
				status = statusOf(err, 500)
			case res != nil:
				status = res.Status
			}
			log(req.Method, req.Path, status, time.Since(start).Milliseconds())
			return res, err
		}
	}
}

// CORSOptions configures the CORS middleware. Empty AllowOrigins defaults to
// "*". AllowHeaders/AllowMethods are only echoed on preflight responses.
type CORSOptions struct {
	AllowOrigins []string // exact origins; ["*"] allows any
	AllowMethods []string // preflight only; default echoes the request method
	AllowHeaders []string // preflight only
	MaxAge       string   // preflight only, e.g. "86400"
}

// CORS adds cross-origin headers to every response and answers OPTIONS
// preflights before routing, so un-routed OPTIONS requests still succeed.
func CORS(opts CORSOptions) Middleware {
	origins := opts.AllowOrigins
	if len(origins) == 0 {
		origins = []string{"*"}
	}
	return func(next Handler) Handler {
		return func(req *Request) (*Response, error) {
			origin := headerValue(req, "Origin")
			allowed := originAllowed(origins, origin)

			if req.Method == "OPTIONS" && isPreflight(req) {
				if !allowed {
					return nil, NewStatusError(403, errors.New("cors: origin not allowed"))
				}
				res := &Response{Status: 204}
				setHeader(res, "Access-Control-Allow-Origin", matchOrigin(origins, origin))
				setHeader(res, "Access-Control-Allow-Methods", joinList(opts.AllowMethods, "*"))
				if len(opts.AllowHeaders) > 0 {
					setHeader(res, "Access-Control-Allow-Headers", joinList(opts.AllowHeaders, ""))
				}
				if opts.MaxAge != "" {
					setHeader(res, "Access-Control-Max-Age", opts.MaxAge)
				}
				return res, nil
			}

			res, err := next(req)
			if res == nil {
				// Attach CORS headers even to error responses by rebuilding.
				if err == nil {
					return res, err
				}
				res = &Response{Status: statusOf(err, 500)}
			}
			if allowed {
				setHeader(res, "Access-Control-Allow-Origin", matchOrigin(origins, origin))
			}
			return res, err
		}
	}
}

// panicValue renders a recovered panic value without fmt (see status.go's
// note on keeping the formatting machinery out of the wasm). Mirrors
// fmt.Sprint for the shapes panic values take; anything else logs "panic".
func panicValue(v any) string {
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

func isPreflight(req *Request) bool {
	return headerValue(req, "Access-Control-Request-Method") != ""
}

func originAllowed(origins []string, origin string) bool {
	if origin == "" {
		return true
	}
	for _, o := range origins {
		if o == "*" || strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

func matchOrigin(origins []string, origin string) string {
	for _, o := range origins {
		if o != "*" && strings.EqualFold(o, origin) {
			return o // echo the exact origin for credentialed requests
		}
	}
	return "*"
}

func joinList(list []string, fallback string) string {
	if len(list) == 0 {
		return fallback
	}
	return strings.Join(list, ", ")
}

func headerValue(req *Request, name string) string {
	for _, h := range req.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// responseHeaderValue reads a header from a Response (case-insensitive).
func responseHeaderValue(res *Response, name string) string {
	for _, h := range res.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func setHeader(res *Response, name, value string) {
	for i := range res.Headers {
		if strings.EqualFold(res.Headers[i].Name, name) {
			res.Headers[i].Value = value
			return
		}
	}
	res.Headers = append(res.Headers, Header{Name: name, Value: value})
}

// statusOf infers the HTTP status an error will produce, matching the
// bridge's errorResponse mapping: StatusError carries its own code, bare
// sentinels map to their canonical status, anything else is `fallback`.
func statusOf(err error, fallback int) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return 404
	case errors.Is(err, ErrMethodNotAllowed):
		return 405
	}
	return fallback
}
