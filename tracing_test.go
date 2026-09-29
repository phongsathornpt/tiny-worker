package tinyworker

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, raw string) TraceContext {
	t.Helper()
	tc, ok := parseTraceparent(raw)
	if !ok {
		t.Fatalf("parseTraceparent(%q) failed, want ok", raw)
	}
	return tc
}

func TestParseTraceparentValid(t *testing.T) {
	const raw = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	tc := mustParse(t, raw)

	if tc.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("traceID = %q", tc.TraceID)
	}
	if tc.ParentSpanID() != "00f067aa0ba902b7" {
		t.Fatalf("parentSpanID = %q", tc.ParentSpanID())
	}
	if tc.Flags != "01" {
		t.Fatalf("flags = %q", tc.Flags)
	}
	if !tc.Sample() {
		t.Fatal("flags 01 must sample")
	}
}

func TestParseTraceparentInvalid(t *testing.T) {
	cases := []string{
		"", // missing
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",           // too short
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01- Extra", // extension suffix
		"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",        // version ff
		"00-BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",         // uppercase trace id
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00F067AA0BA902B7-01",        // uppercase parent id
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",        // all-zero trace id
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",        // all-zero parent id
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-ff",        // flags ff forbidden
		"0g-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",        // non-hex version
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",           // truncated flags
	}
	for _, raw := range cases {
		if _, ok := parseTraceparent(raw); ok {
			t.Errorf("parseTraceparent(%q) = ok, want invalid", raw)
		}
	}
}

func TestParseTraceparentFlags00(t *testing.T) {
	tc := mustParse(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00")
	if tc.Sample() {
		t.Fatal("flags 00 must not sample")
	}
	if tc.ParentSpanID() == "" {
		t.Fatal("parent span must survive regardless of sampling")
	}
}

func TestTraceMiddlewareContinuesTrace(t *testing.T) {
	const incoming = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	var handlerTC TraceContext
	var handlerOK bool
	app := New(func(req *Request) (*Response, error) {
		handlerTC, handlerOK = Trace(req)
		return &Response{Status: 200}, nil
	})
	app.Use(Tracing())

	res, err := app.Serve(&Request{
		Method: "GET", Path: "/",
		Headers: []Header{{Name: "Traceparent", Value: incoming}}, // mixed case on purpose
	})
	if err != nil {
		t.Fatal(err)
	}
	if !handlerOK {
		t.Fatal("handler must see a trace context")
	}
	if handlerTC.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id changed: %q", handlerTC.TraceID)
	}
	if handlerTC.SpanID == "00f067aa0ba902b7" || len(handlerTC.SpanID) != 16 || !isLowerHex(handlerTC.SpanID) {
		t.Fatalf("span id not regenerated: %q", handlerTC.SpanID)
	}
	if handlerTC.Flags != "01" {
		t.Fatalf("flags not propagated: %q", handlerTC.Flags)
	}

	// Responses never carry a traceparent header (that is a request header);
	// downstream propagation uses TraceContext.String for outbound calls.
	if got := responseHeaderValue(res, "traceparent"); got != "" {
		t.Fatalf("response must not carry traceparent, got %q", got)
	}
	downstream := handlerTC.String()
	wantPrefix := "00-4bf92f3577b34da6a3ce929d0e0e4736-" + handlerTC.SpanID + "-"
	if !strings.HasPrefix(downstream, wantPrefix) {
		t.Fatalf("outbound traceparent = %q, want prefix %q", downstream, wantPrefix)
	}

	// traceresponse echoes the caller's parent and our span.
	tr := responseHeaderValue(res, "traceresponse")
	if tr != "00-00f067aa0ba902b7-"+handlerTC.SpanID+"-01" {
		t.Fatalf("traceresponse = %q", tr)
	}
}

func TestTraceMiddlewareRootContext(t *testing.T) {
	var handlerTC TraceContext
	app := New(func(req *Request) (*Response, error) {
		handlerTC, _ = Trace(req)
		return &Response{Status: 200}, nil
	})
	app.Use(Tracing())

	res, err := app.Serve(&Request{Method: "GET", Path: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(handlerTC.TraceID) != 32 || !isLowerHex(handlerTC.TraceID) || allZero(handlerTC.TraceID) {
		t.Fatalf("generated trace id invalid: %q", handlerTC.TraceID)
	}
	if len(handlerTC.SpanID) != 16 || !isLowerHex(handlerTC.SpanID) {
		t.Fatalf("generated span id invalid: %q", handlerTC.SpanID)
	}
	if handlerTC.ParentSpanID() != "" {
		t.Fatalf("root context must have no parent: %q", handlerTC.ParentSpanID())
	}
	if handlerTC.Flags != "01" {
		t.Fatalf("root flags = %q, want 01", handlerTC.Flags)
	}
	if got := responseHeaderValue(res, "traceparent"); got != "" {
		t.Fatalf("response must not carry traceparent, got %q", got)
	}
	if got := responseHeaderValue(res, "traceresponse"); got != handlerTC.String() {
		t.Fatalf("traceresponse = %q, want root traceparent", got)
	}
}

func TestTraceMiddlewareInvalidHeaderStartsRoot(t *testing.T) {
	var handlerTC TraceContext
	app := New(func(req *Request) (*Response, error) {
		handlerTC, _ = Trace(req)
		return &Response{Status: 200}, nil
	})
	app.Use(Tracing())

	if _, err := app.Serve(&Request{
		Method: "GET", Path: "/",
		Headers: []Header{{Name: "traceparent", Value: "garbage"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(handlerTC.TraceID) != 32 {
		t.Fatalf("invalid header must start a root trace, got %q", handlerTC.TraceID)
	}
}

func TestTraceresponseOnError(t *testing.T) {
	app := New(func(*Request) (*Response, error) {
		return nil, NotFound("gone")
	})
	app.Use(Tracing())

	res, err := app.Serve(&Request{Method: "GET", Path: "/x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if res == nil || res.Status != 404 {
		t.Fatalf("res = %#v, want rebuilt 404", res)
	}
	if got := responseHeaderValue(res, "traceresponse"); got == "" {
		t.Fatal("traceresponse must be attached to error responses too")
	}
}

func TestRandomHexUniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := randomHex(16)
		if len(id) != 32 {
			t.Fatalf("len = %d", len(id))
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id: %q", id)
		}
		seen[id] = struct{}{}
	}
}
