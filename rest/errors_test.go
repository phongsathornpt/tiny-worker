package rest

import (
	"encoding/json"
	"errors"
	"testing"

	tinyworker "github.com/phongsathornpt/tiny-worker"
)

func decodeEnvelope(t *testing.T, res *tinyworker.Response) errorEnvelope {
	t.Helper()
	if res == nil {
		t.Fatal("nil response")
	}
	var env errorEnvelope
	if err := json.Unmarshal(res.Body, &env); err != nil {
		t.Fatalf("body %q is not an envelope: %v", res.Body, err)
	}
	return env
}

func contentType(t *testing.T, res *tinyworker.Response) string {
	t.Helper()
	for _, h := range res.Headers {
		if h.Name == "content-type" {
			return h.Value
		}
	}
	return ""
}

func TestWriteErrorValidationErrors(t *testing.T) {
	res := WriteError(ValidationErrors{
		{Field: "email", Code: "email", Message: "must be a valid email address"},
		{Field: "address.city", Code: "min", Message: "must be at least 2 characters"},
	})
	if res.Status != 422 {
		t.Fatalf("status = %d, want 422", res.Status)
	}
	if ct := contentType(t, res); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	env := decodeEnvelope(t, res)
	if env.Error.Code != CodeValidationFailed {
		t.Errorf("code = %q, want validation_failed", env.Error.Code)
	}
	if len(env.Error.Details) != 2 || env.Error.Details[1].Field != "address.city" {
		t.Fatalf("details = %+v, want both field errors in order", env.Error.Details)
	}
}

func TestWriteErrorBindError(t *testing.T) {
	res := WriteError(&BindError{Code: CodeInvalidJSON, Field: "nickname", Message: "unknown field \"nickname\""})
	if res.Status != 400 {
		t.Fatalf("status = %d, want 400", res.Status)
	}
	env := decodeEnvelope(t, res)
	if env.Error.Code != CodeInvalidJSON {
		t.Errorf("code = %q, want invalid_json", env.Error.Code)
	}
	if len(env.Error.Details) != 1 || env.Error.Details[0].Field != "nickname" {
		t.Fatalf("details = %+v, want the offending field", env.Error.Details)
	}
}

func TestWriteErrorStatusErrorKeepsStatusAndHeaders(t *testing.T) {
	err := tinyworker.MethodNotAllowed("GET", "HEAD")
	res := WriteError(err)
	if res.Status != 405 {
		t.Fatalf("status = %d, want 405", res.Status)
	}
	env := decodeEnvelope(t, res)
	if env.Error.Code != CodeMethodNotAllowed {
		t.Errorf("code = %q, want method_not_allowed", env.Error.Code)
	}
	var allow string
	for _, h := range res.Headers {
		if h.Name == "Allow" {
			allow = h.Value
		}
	}
	if allow == "" {
		t.Fatalf("Allow header was dropped: %+v", res.Headers)
	}
}

func TestWriteErrorNotFoundSentinel(t *testing.T) {
	res := WriteError(tinyworker.ErrNotFound)
	if res.Status != 404 {
		t.Fatalf("status = %d, want 404", res.Status)
	}
	if env := decodeEnvelope(t, res); env.Error.Code != CodeNotFound {
		t.Fatalf("code = %q, want not_found", env.Error.Code)
	}
}

func TestWriteErrorUnknownErrorIsGeneric500(t *testing.T) {
	res := WriteError(errors.New("database exploded: secret host"))
	if res.Status != 500 {
		t.Fatalf("status = %d, want 500", res.Status)
	}
	env := decodeEnvelope(t, res)
	if env.Error.Code != CodeInternalError {
		t.Fatalf("code = %q, want internal_error", env.Error.Code)
	}
	if env.Error.Message != "internal server error" {
		t.Errorf("message = %q, want the generic text (no internals leaked)", env.Error.Message)
	}
}

func TestWriteErrorServerStatusHidesDetail(t *testing.T) {
	res := WriteError(tinyworker.NewStatusError(503, errors.New("upstream pool exhausted")))
	if env := decodeEnvelope(t, res); env.Error.Message != "internal server error" {
		t.Fatalf("message = %q, want generic server text", env.Error.Message)
	}
}

func TestWriteErrorClientStatusKeepsMessage(t *testing.T) {
	res := WriteError(tinyworker.NotFound("user 7 does not exist"))
	env := decodeEnvelope(t, res)
	if env.Error.Message != "user 7 does not exist" {
		t.Fatalf("message = %q, want the StatusError message", env.Error.Message)
	}
}

func TestEnvelopeDetailsOmittedWhenEmpty(t *testing.T) {
	res := WriteError(tinyworker.ErrNotFound)
	if got := string(res.Body); got == "" || contains(got, "details") {
		t.Fatalf("body = %q, want no details key for a fieldless error", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestErrorsMiddlewareEnvelopesRoutingMisses(t *testing.T) {
	r := newTestRouter()
	app := tinyworker.New(nil)
	app.UseRouter(r)
	app.Use(Errors())

	res, err := app.Serve(newReq("GET", "https://x/nope", nil))
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if res.Status != 404 {
		t.Fatalf("status = %d, want 404", res.Status)
	}
	if env := decodeEnvelope(t, res); env.Error.Code != CodeNotFound {
		t.Fatalf("code = %q, want not_found", env.Error.Code)
	}

	res, err = app.Serve(newReq("DELETE", "https://x/hello", nil))
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if res.Status != 405 {
		t.Fatalf("status = %d, want 405", res.Status)
	}
	if env := decodeEnvelope(t, res); env.Error.Code != CodeMethodNotAllowed {
		t.Fatalf("code = %q, want method_not_allowed", env.Error.Code)
	}
}

func TestErrorsMiddlewareLeavesSuccessAlone(t *testing.T) {
	app := tinyworker.New(func(*tinyworker.Request) (*tinyworker.Response, error) {
		return &tinyworker.Response{Status: 200, Headers: []tinyworker.Header{{Name: "content-type", Value: "text/plain"}}, Body: []byte("hello")}, nil
	})
	app.Use(Errors())
	res, err := app.Serve(newReq("GET", "https://x/hello", nil))
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if string(res.Body) != "hello" {
		t.Fatalf("body = %q, want it untouched", res.Body)
	}
}

func TestErrorsMiddlewareRecoversPanics(t *testing.T) {
	app := tinyworker.New(func(*tinyworker.Request) (*tinyworker.Response, error) {
		panic("boom")
	})
	app.Use(Errors())
	res, err := app.Serve(newReq("GET", "https://x/boom", nil))
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if res.Status != 500 {
		t.Fatalf("status = %d, want 500", res.Status)
	}
	if env := decodeEnvelope(t, res); env.Error.Code != CodeInternalError {
		t.Fatalf("code = %q, want internal_error", env.Error.Code)
	}
}

func TestErrorsMiddlewareHandlesNilResponse(t *testing.T) {
	app := tinyworker.New(func(*tinyworker.Request) (*tinyworker.Response, error) {
		return nil, nil
	})
	app.Use(Errors())
	res, err := app.Serve(newReq("GET", "https://x/nil", nil))
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if res.Status != 500 {
		t.Fatalf("status = %d, want 500", res.Status)
	}
}

func TestCodeVocabularyIsCovered(t *testing.T) {
	for status, want := range map[int]string{
		400: CodeBadRequest,
		401: CodeUnauthorized,
		403: CodeForbidden,
		404: CodeNotFound,
		405: CodeMethodNotAllowed,
		413: CodePayloadTooLarge,
		422: CodeValidationFailed,
		500: CodeInternalError,
		503: CodeInternalError,
	} {
		if got := codeForStatus(status); got != want {
			t.Errorf("codeForStatus(%d) = %q, want %q", status, got, want)
		}
	}
}
