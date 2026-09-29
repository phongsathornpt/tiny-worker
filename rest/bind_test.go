package rest

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tinyworker "github.com/phongsathornpt/tiny-worker"
)

type bindInput struct {
	Name  string   `json:"name" query:"name"`
	Limit int      `json:"limit" query:"limit"`
	Tags  []string `json:"tags" query:"tag"`
	Debug bool     `json:"debug" query:"debug"`
	ID    string   `param:"id"`
	Rate  float64  `query:"rate"`
}

func newReq(method, rawURL string, body []byte) *tinyworker.Request {
	return &tinyworker.Request{
		Method: method,
		URL:    rawURL,
		Path:   tinyworker.ParsePath(rawURL),
		Body:   body,
		Params: nil,
	}
}

func TestBindBodyStrictByDefault(t *testing.T) {
	var in bindInput
	err := Bind(newReq("POST", "https://x/users", []byte(`{"name":"Ada","nickname":"x"}`)), &in)
	if err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
	var be *BindError
	if !errors.As(err, &be) {
		t.Fatalf("error = %T, want *BindError", err)
	}
	if be.Code != CodeInvalidJSON {
		t.Errorf("code = %q, want %q", be.Code, CodeInvalidJSON)
	}
	if be.Field != "nickname" {
		t.Errorf("field = %q, want %q", be.Field, "nickname")
	}
	if !strings.Contains(be.Message, "unknown field") {
		t.Errorf("message = %q, want it to mention unknown field", be.Message)
	}
}

func TestBindLenientWhenAsked(t *testing.T) {
	var in bindInput
	if err := Bind(newReq("POST", "https://x/users", []byte(`{"name":"Ada","nickname":"x"}`)), &in, Strict(false)); err != nil {
		t.Fatalf("lenient bind failed: %v", err)
	}
	if in.Name != "Ada" {
		t.Fatalf("Name = %q, want Ada", in.Name)
	}
}

func TestBindMalformedJSON(t *testing.T) {
	var in bindInput
	err := Bind(newReq("POST", "https://x/users", []byte(`{"name":`)), &in)
	var be *BindError
	if !errors.As(err, &be) || be.Code != CodeInvalidJSON {
		t.Fatalf("error = %v, want invalid_json BindError", err)
	}
	if be.Message == "" {
		t.Error("expected a message describing the malformed body")
	}
}

func TestBindTypeMismatchNamesField(t *testing.T) {
	var in bindInput
	err := Bind(newReq("POST", "https://x/users", []byte(`{"limit":"nope"}`)), &in)
	var be *BindError
	if !errors.As(err, &be) {
		t.Fatalf("error = %T, want *BindError", err)
	}
	if be.Field != "limit" {
		t.Errorf("field = %q, want limit", be.Field)
	}
}

func TestBindQueryCoercion(t *testing.T) {
	in := bindInput{}
	req := newReq("GET", "https://x/users?name=Ada+Lovelace&limit=25&debug=true&rate=1.5&tag=a&tag=b&tag=c%2Cd", nil)
	if err := Bind(req, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.Name != "Ada Lovelace" {
		t.Errorf("Name = %q, want %q (plus decoded as space)", in.Name, "Ada Lovelace")
	}
	if in.Limit != 25 {
		t.Errorf("Limit = %d, want 25", in.Limit)
	}
	if !in.Debug {
		t.Error("Debug = false, want true")
	}
	if in.Rate != 1.5 {
		t.Errorf("Rate = %v, want 1.5", in.Rate)
	}
	want := []string{"a", "b", "c,d"}
	if len(in.Tags) != len(want) {
		t.Fatalf("Tags = %v, want %v", in.Tags, want)
	}
	for i := range want {
		if in.Tags[i] != want[i] {
			t.Fatalf("Tags = %v, want %v", in.Tags, want)
		}
	}
}

func TestBindBadQueryValue(t *testing.T) {
	var in bindInput
	err := Bind(newReq("GET", "https://x/users?limit=abc", nil), &in)
	var be *BindError
	if !errors.As(err, &be) || be.Code != CodeBadRequest {
		t.Fatalf("error = %v, want bad_request BindError", err)
	}
	if be.Field != "limit" {
		t.Errorf("field = %q, want limit", be.Field)
	}
	if !strings.Contains(be.Message, "expected int") {
		t.Errorf("message = %q, want it to name the expected type", be.Message)
	}
}

func TestBindMalformedQuery(t *testing.T) {
	var in bindInput
	err := Bind(newReq("GET", "https://x/users?name=%zz", nil), &in)
	var be *BindError
	if !errors.As(err, &be) || be.Code != CodeBadRequest {
		t.Fatalf("error = %v, want bad_request BindError", err)
	}
}

func TestBindPathParams(t *testing.T) {
	in := bindInput{}
	req := newReq("GET", "https://x/users/42", nil)
	req.Params = []tinyworker.Param{{Name: "id", Value: "42"}}
	if err := Bind(req, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.ID != "42" {
		t.Errorf("ID = %q, want 42", in.ID)
	}
}

func TestBindParamCoercionFailure(t *testing.T) {
	type in struct {
		ID int `param:"id"`
	}
	var dst in
	req := newReq("GET", "https://x/users/abc", nil)
	req.Params = []tinyworker.Param{{Name: "id", Value: "abc"}}
	err := Bind(req, &dst)
	var be *BindError
	if !errors.As(err, &be) || be.Field != "id" {
		t.Fatalf("error = %v, want a param BindError naming id", err)
	}
}

func TestBindAbsentQueryLeavesZeroValue(t *testing.T) {
	in := bindInput{Limit: 7}
	if err := Bind(newReq("GET", "https://x/users", nil), &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.Limit != 7 {
		t.Errorf("Limit = %d, want the pre-set 7 to survive an absent param", in.Limit)
	}
}

func TestBindUnsupportedFieldType(t *testing.T) {
	type nested struct {
		Inner string `query:"inner"`
	}
	type in struct {
		Nested nested `query:"nested"`
	}
	req := newReq("GET", "https://x/things?nested=value", nil)
	var dst in
	err := Bind(req, &dst)
	var be *BindError
	if !errors.As(err, &be) {
		t.Fatalf("error = %v, want a BindError", err)
	}
	if !strings.Contains(be.Message, "cannot bind") {
		t.Errorf("message = %q, want it to explain the unsupported type", be.Message)
	}
}

func TestBindRequiresPointerToStruct(t *testing.T) {
	if err := Bind(newReq("GET", "https://x/y", nil), bindInput{}); err == nil {
		t.Error("expected an error for a non-pointer destination")
	}
	var p *bindInput
	if err := Bind(newReq("GET", "https://x/y", nil), p); err == nil {
		t.Error("expected an error for a nil pointer destination")
	}
}

func TestBindWithCustomCodec(t *testing.T) {
	var in bindInput
	codec := &countingCodec{}
	if err := Bind(newReq("POST", "https://x/users", []byte(`{"name":"Ada"}`)), &in, WithCodec(codec)); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if codec.strictCalls != 1 {
		t.Fatalf("strict decode calls = %d, want 1", codec.strictCalls)
	}
	if in.Name != "Ada" {
		t.Fatalf("Name = %q, want Ada", in.Name)
	}
}

// countingCodec records which decode path the binding took.
type countingCodec struct {
	strictCalls int
	plainCalls  int
}

func (c *countingCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (c *countingCodec) Unmarshal(data []byte, v any) error {
	c.plainCalls++
	return json.Unmarshal(data, v)
}

func (c *countingCodec) UnmarshalStrict(data []byte, v any) error {
	c.strictCalls++
	return json.Unmarshal(data, v)
}

func TestBindValidateCombines(t *testing.T) {
	type in struct {
		Name string `json:"name" validate:"required"`
	}
	var dst in
	req := newReq("POST", "https://x/users", []byte(`{}`))
	err := BindValidate(req, &dst)
	var ve ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v (%T), want ValidationErrors", err, err)
	}
	if len(ve) != 1 || ve[0].Field != "name" || ve[0].Code != "required" {
		t.Fatalf("validation errors = %+v, want one required error on name", ve)
	}
}
