package rest

import (
	"encoding/json"
	"errors"
	"testing"

	tinyworker "github.com/phongsathornpt/tiny-worker"
	"github.com/phongsathornpt/tiny-worker/router"
)

// *router.Router must satisfy the REST routing seam.
var _ Router = (*router.Router)(nil)

func newTestRouter() *router.Router {
	r := router.New()
	r.Handle("GET", "/hello", func(*tinyworker.Request) (*tinyworker.Response, error) {
		return &tinyworker.Response{Status: 200, Body: []byte("hello")}, nil
	})
	return r
}

type createUserIn struct {
	Name  string `json:"name" validate:"required,min=2"`
	Email string `json:"email" validate:"required,email"`
}

type userOut struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func TestHandlerBindsValidatesAndRenders(t *testing.T) {
	var seen createUserIn
	h := Handler(func(req *tinyworker.Request, in createUserIn) (userOut, error) {
		seen = in
		return userOut{ID: 7, Name: in.Name, Email: in.Email}, nil
	})

	res, err := h(newReq("POST", "https://x/users", []byte(`{"name":"Ada","email":"ada@example.com"}`)))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.Status != 200 {
		t.Fatalf("status = %d, want 200", res.Status)
	}
	if seen.Name != "Ada" || seen.Email != "ada@example.com" {
		t.Fatalf("handler saw %+v, want the bound values", seen)
	}
	var out userOut
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatalf("body %q: %v", res.Body, err)
	}
	if out.ID != 7 || out.Name != "Ada" {
		t.Fatalf("out = %+v, want the handler's value marshalled", out)
	}
}

func TestHandlerValidationFailureIs422(t *testing.T) {
	h := Handler(func(*tinyworker.Request, createUserIn) (userOut, error) {
		t.Fatal("handler must not run when validation fails")
		return userOut{}, nil
	})
	res, err := h(newReq("POST", "https://x/users", []byte(`{"name":"Ada","email":"nope"}`)))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.Status != 422 {
		t.Fatalf("status = %d, want 422", res.Status)
	}
	env := decodeEnvelope(t, res)
	if env.Error.Code != CodeValidationFailed {
		t.Fatalf("code = %q, want validation_failed", env.Error.Code)
	}
	if len(env.Error.Details) == 0 || env.Error.Details[0].Field != "email" {
		t.Fatalf("details = %+v, want the email field named", env.Error.Details)
	}
}

func TestHandlerMalformedBodyIs400(t *testing.T) {
	h := Handler(func(*tinyworker.Request, createUserIn) (userOut, error) {
		t.Fatal("handler must not run for a malformed body")
		return userOut{}, nil
	})
	res, _ := h(newReq("POST", "https://x/users", []byte(`{"name":`)))
	if res.Status != 400 {
		t.Fatalf("status = %d, want 400", res.Status)
	}
	if env := decodeEnvelope(t, res); env.Error.Code != CodeInvalidJSON {
		t.Fatalf("code = %q, want invalid_json", env.Error.Code)
	}
}

func TestHandlerResultControlsStatus(t *testing.T) {
	h := Handler(func(*tinyworker.Request, createUserIn) (Result, error) {
		return Result{Status: 201, Body: userOut{ID: 1}, Headers: []tinyworker.Header{{Name: "location", Value: "/users/1"}}}, nil
	})
	res, _ := h(newReq("POST", "https://x/users", []byte(`{"name":"Ada","email":"ada@example.com"}`)))
	if res.Status != 201 {
		t.Fatalf("status = %d, want 201", res.Status)
	}
	if ct := contentType(t, res); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var location string
	for _, hh := range res.Headers {
		if hh.Name == "location" {
			location = hh.Value
		}
	}
	if location != "/users/1" {
		t.Errorf("location = %q, want /users/1", location)
	}
}

func TestHandlerResultNoContent(t *testing.T) {
	h := Handler(func(*tinyworker.Request, createUserIn) (Result, error) {
		return Result{Status: 204}, nil
	})
	res, _ := h(newReq("POST", "https://x/users", []byte(`{"name":"Ada","email":"ada@example.com"}`)))
	if res.Status != 204 || len(res.Body) != 0 {
		t.Fatalf("res = %d body=%q, want a bare 204", res.Status, res.Body)
	}
}

func TestHandlerNilPointerOutputIs204(t *testing.T) {
	h := Handler(func(*tinyworker.Request, createUserIn) (*userOut, error) {
		return nil, nil
	})
	res, _ := h(newReq("POST", "https://x/users", []byte(`{"name":"Ada","email":"ada@example.com"}`)))
	if res.Status != 204 {
		t.Fatalf("status = %d, want 204 for a nil pointer result", res.Status)
	}
}

func TestHandlerResponsePassthrough(t *testing.T) {
	h := Handler(func(*tinyworker.Request, createUserIn) (*tinyworker.Response, error) {
		return &tinyworker.Response{Status: 202, Body: []byte("custom")}, nil
	})
	res, _ := h(newReq("POST", "https://x/users", []byte(`{"name":"Ada","email":"ada@example.com"}`)))
	if res.Status != 202 || string(res.Body) != "custom" {
		t.Fatalf("res = %d %q, want the response passed through", res.Status, res.Body)
	}
}

func TestHandlerErrorMapping(t *testing.T) {
	h := Handler(func(*tinyworker.Request, createUserIn) (userOut, error) {
		return userOut{}, tinyworker.NotFound("no such user")
	})
	res, _ := h(newReq("POST", "https://x/users", []byte(`{"name":"Ada","email":"ada@example.com"}`)))
	if res.Status != 404 {
		t.Fatalf("status = %d, want 404", res.Status)
	}
	if env := decodeEnvelope(t, res); env.Error.Message != "no such user" {
		t.Fatalf("message = %q, want the StatusError message", env.Error.Message)
	}
}

func TestHandlerUnknownErrorIsGeneric500(t *testing.T) {
	h := Handler(func(*tinyworker.Request, createUserIn) (userOut, error) {
		return userOut{}, errors.New("secret internal detail")
	})
	res, _ := h(newReq("POST", "https://x/users", []byte(`{"name":"Ada","email":"ada@example.com"}`)))
	if res.Status != 500 {
		t.Fatalf("status = %d, want 500", res.Status)
	}
	if env := decodeEnvelope(t, res); env.Error.Message == "secret internal detail" {
		t.Fatal("internal error text leaked to the client")
	}
}

func TestHandlerQueryOnlyInput(t *testing.T) {
	type listIn struct {
		Limit int      `query:"limit" validate:"max=100"`
		Tags  []string `query:"tag"`
	}
	h := Handler(func(*tinyworker.Request, listIn) (Result, error) {
		return Result{Body: []string{"a"}}, nil
	})
	res, _ := h(newReq("GET", "https://x/users?limit=5&tag=a&tag=b", nil))
	if res.Status != 200 {
		t.Fatalf("status = %d, want 200", res.Status)
	}

	res, _ = h(newReq("GET", "https://x/users?limit=1000", nil))
	if res.Status != 422 {
		t.Fatalf("status = %d, want 422 for limit beyond max", res.Status)
	}
}

func TestHandlerNoInput(t *testing.T) {
	h := HandlerNoInput(func(*tinyworker.Request) ([]string, error) {
		return []string{"a", "b"}, nil
	})
	res, err := h(newReq("GET", "https://x/users", nil))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.Status != 200 || string(res.Body) != `["a","b"]` {
		t.Fatalf("res = %d %q", res.Status, res.Body)
	}
}

func TestHandlerNilFuncIs500(t *testing.T) {
	res, _ := Handler[createUserIn, userOut](nil)(newReq("POST", "https://x/users", nil))
	if res.Status != 500 {
		t.Fatalf("status = %d, want 500", res.Status)
	}
}

func TestRouteHelpersRegister(t *testing.T) {
	r := router.New()
	Post(r, "/users", Handler(func(*tinyworker.Request, createUserIn) (userOut, error) {
		return userOut{ID: 1}, nil
	}))
	Get(r, "/users", HandlerNoInput(func(*tinyworker.Request) ([]string, error) {
		return []string{"a"}, nil
	}))
	Delete(r, "/users/:id", HandlerNoInput(func(*tinyworker.Request) (Result, error) {
		return Result{Status: 204}, nil
	}))

	app := tinyworker.New(nil)
	app.UseRouter(r)

	res, _ := app.Serve(newReq("POST", "https://x/users", []byte(`{"name":"Ada","email":"ada@example.com"}`)))
	if res.Status != 200 {
		t.Fatalf("POST /users status = %d, want 200", res.Status)
	}
	res, _ = app.Serve(newReq("DELETE", "https://x/users/1", nil))
	if res.Status != 204 {
		t.Fatalf("DELETE /users/1 status = %d, want 204", res.Status)
	}
}

func TestPrimitives(t *testing.T) {
	res, err := OK(map[string]string{"ok": "yes"})
	if err != nil {
		t.Fatalf("OK: %v", err)
	}
	if res.Status != 200 || string(res.Body) != `{"ok":"yes"}` || contentType(t, res) != "application/json" {
		t.Fatalf("OK = %d %q %q", res.Status, res.Body, contentType(t, res))
	}
	res, err = Created([]int{1})
	if err != nil {
		t.Fatalf("Created: %v", err)
	}
	if res.Status != 201 {
		t.Fatalf("Created status = %d, want 201", res.Status)
	}
	if nc := NoContent(); nc.Status != 204 || len(nc.Body) != 0 {
		t.Fatalf("NoContent = %d %q", nc.Status, nc.Body)
	}
	fail := Fail(404, CodeNotFound, "gone")
	if fail.Status != 404 {
		t.Fatalf("Fail status = %d", fail.Status)
	}
}

func TestHandlerUsesCustomCodec(t *testing.T) {
	codec := &prefixCodec{}
	h := Handler(func(*tinyworker.Request, createUserIn) (userOut, error) {
		return userOut{ID: 1}, nil
	}, WithCodec(codec))
	res, _ := h(newReq("POST", "https://x/users", []byte(`{"name":"Ada","email":"ada@example.com"}`)))
	if res.Body[0] != 'X' {
		t.Fatalf("body = %q, want the custom codec's output", res.Body)
	}
}

// prefixCodec marks its output so tests can see which codec ran.
type prefixCodec struct{}

func (prefixCodec) Marshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	return append([]byte("X"), b...), err
}

func (prefixCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
