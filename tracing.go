package tinyworker

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// TraceContext carries W3C Trace Context data through a request. Extracted
// from (or generated for) the incoming traceparent header, it can be read by
// middleware and handlers and rendered as the downstream traceparent.
type TraceContext struct {
	TraceID string // 32 lowercase hex chars
	SpanID  string // 16 lowercase hex chars, unique per hop
	Flags   string // 2 lowercase hex chars, propagated unchanged

	parentSpanID string // caller's span ID, empty for root contexts
}

// ParentSpanID is the span ID of the caller, if the request carried one.
func (tc TraceContext) ParentSpanID() string { return tc.parentSpanID }

// Sampled reports whether the sampled flag (bit 0) is set.
func (tc TraceContext) Sample() bool {
	v, err := strconv.ParseUint(tc.Flags, 16, 8)
	return err == nil && v&0x01 != 0
}

// traceCtxKey stores the request's TraceContext.
var traceCtx = Value[TraceContext]("trace.context")

// Trace of req returns the request's trace context. The Tracing middleware
// always installs one; with (TraceContext, false) nothing was installed.
func Trace(req *Request) (TraceContext, bool) { return traceCtx.Get(req) }

// Tracing returns middleware implementing W3C Trace Context propagation:
//
//   - a valid traceparent (any known version) is continued: trace-id kept,
//     a fresh span-id generated, flags propagated;
//   - an invalid or missing traceparent starts a new root context;
//   - the context is stored in request-scoped values (tinyworker.Trace);
//   - the response echoes the caller's version with our span in
//     traceresponse, or the freshly generated root traceparent.
func Tracing() Middleware {
	return func(next Handler) Handler {
		return func(req *Request) (*Response, error) {
			tc, root := extractTraceContext(req)
			traceCtx.Set(req, tc)

			res, err := next(req)
			if res == nil {
				if err == nil {
					return res, err
				}
				res = &Response{Status: statusOf(err, 500)}
			}
			if root {
				setHeader(res, "traceresponse", tc.String())
			} else {
				setHeader(res, "traceresponse", "00-"+tc.ParentSpanID()+"-"+tc.SpanID+"-"+tc.Flags)
			}
			return res, err
		}
	}
}

func extractTraceContext(req *Request) (TraceContext, bool) {
	raw := headerValue(req, "traceparent")
	if raw != "" {
		if tc, ok := parseTraceparent(raw); ok {
			tc.SpanID = randomHex(8)
			return tc, false
		}
	}
	return TraceContext{
		TraceID: randomHex(16),
		SpanID:  randomHex(8),
		Flags:   "01",
	}, true
}

// parseTraceparent validates a traceparent header per the W3C grammar:
// VERSION(2 hex) '-' TRACEID(32 hex) '-' PARENTID(16 hex) '-' FLAGS(2 hex),
// lowercase, version != ff, no all-zero ids, no trailing extensions
// accepted (spec conformance for version 00).
func parseTraceparent(raw string) (TraceContext, bool) {
	var tc TraceContext
	raw = strings.TrimSpace(raw)
	if len(raw) != 55 || raw[2] != '-' || raw[35] != '-' || raw[52] != '-' {
		return tc, false
	}
	version, traceID, parentID, flags := raw[0:2], raw[3:35], raw[36:52], raw[53:55]
	if version == "ff" || flags == "ff" {
		return tc, false
	}
	for _, part := range [4]string{version, traceID, parentID, flags} {
		if !isLowerHex(part) {
			return tc, false
		}
	}
	if allZero(traceID) || allZero(parentID) {
		return tc, false
	}
	tc.TraceID = traceID
	tc.parentSpanID = parentID
	tc.Flags = flags
	return tc, true
}

// String renders the downstream traceparent: our span is the new parent.
func (tc TraceContext) String() string {
	return "00-" + tc.TraceID + "-" + tc.SpanID + "-" + tc.Flags
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func allZero(s string) bool { return strings.Trim(s, "0") == "" }

func timeNowUnixNano() int64 { return time.Now().UnixNano() }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand never fails on the supported runtimes; fall back to a
		// time-derived value rather than panicking inside the pipeline.
		now := uint64(timeNowUnixNano())
		for i := 0; i < n && i < 8; i++ {
			b[i] = byte(now >> (i * 8))
		}
		for i := 8; i < n; i++ {
			b[i] = byte(now ^ uint64(i)<<3)
		}
	}
	return hex.EncodeToString(b)
}
