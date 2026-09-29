package rest

import (
	"testing"

	tinyworker "github.com/phongsathornpt/tiny-worker"
)

type benchInput struct {
	Name  string   `json:"name" query:"name" validate:"required,min=2,max=64"`
	Email string   `json:"email" query:"email" validate:"required,email"`
	Age   int      `json:"age" query:"age" validate:"min=0,max=150"`
	Tags  []string `json:"tags,omitempty" query:"tag"`
}

var benchBody = []byte(`{"name":"Ada Lovelace","email":"ada@example.com","age":36,"tags":["a","b"]}`)

func BenchmarkBindBody(b *testing.B) {
	req := newReq("POST", "https://bench.test/users", benchBody)
	b.ReportAllocs()
	for b.Loop() {
		var in benchInput
		if err := Bind(req, &in); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBindQueryOnly(b *testing.B) {
	req := newReq("GET", "https://bench.test/users?name=Ada&limit=25&tag=a&tag=b", nil)
	b.ReportAllocs()
	for b.Loop() {
		var in benchInput
		if err := Bind(req, &in); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidate(b *testing.B) {
	in := benchInput{Name: "Ada Lovelace", Email: "ada@example.com", Age: 36, Tags: []string{"a", "b"}}
	b.ReportAllocs()
	for b.Loop() {
		if err := Validate(&in); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEmailRule(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if !isEmail("ada.lovelace@example.co.uk") {
			b.Fatal("expected valid")
		}
	}
}

func BenchmarkErrorResponse(b *testing.B) {
	err := ValidationErrors{
		{Field: "email", Code: "email", Message: "must be a valid email address"},
		{Field: "name", Code: "required", Message: "is required"},
	}
	b.ReportAllocs()
	for b.Loop() {
		if res := WriteError(err); res.Status != 422 {
			b.Fatalf("status = %d", res.Status)
		}
	}
}

func BenchmarkTypedHandlerHappyPath(b *testing.B) {
	h := Handler(func(*tinyworker.Request, benchInput) (userOut, error) {
		return userOut{ID: 1, Name: "Ada"}, nil
	})
	req := newReq("POST", "https://bench.test/users", benchBody)
	b.ReportAllocs()
	for b.Loop() {
		res, err := h(req)
		if err != nil || res.Status != 200 {
			b.Fatalf("res=%v err=%v", res, err)
		}
	}
}
