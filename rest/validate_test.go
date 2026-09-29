package rest

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type address struct {
	City string `json:"city" validate:"required,min=2,max=64"`
	Zip  string `json:"zip" validate:"len=4"`
}

type profile struct {
	Name    string    `json:"name" validate:"required,min=2,max=8"`
	Email   string    `json:"email" validate:"required,email"`
	Age     int       `json:"age" validate:"min=0,max=150"`
	Role    string    `json:"role" validate:"oneof=admin user"`
	Tags    []string  `json:"tags" validate:"required"`
	Address address   `json:"address"`
	Friends []address `json:"friends"`
}

func validateErrs(t *testing.T, v any) ValidationErrors {
	t.Helper()
	err := Validate(v)
	if err == nil {
		t.Fatalf("Validate(%+v) = nil, want errors", v)
	}
	var ve ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("error = %T, want ValidationErrors", err)
	}
	return ve
}

func findField(ve ValidationErrors, field string) *FieldError {
	for i := range ve {
		if ve[i].Field == field {
			return &ve[i]
		}
	}
	return nil
}

func TestValidateAllRulesPass(t *testing.T) {
	p := profile{
		Name:    "Ada",
		Email:   "ada@example.com",
		Age:     36,
		Role:    "admin",
		Tags:    []string{"x"},
		Address: address{City: "Oslo", Zip: "0150"},
	}
	if err := Validate(&p); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}

func TestValidateReportsEachFailure(t *testing.T) {
	p := profile{Email: "nope", Role: "root", Age: 200, Address: address{City: "A", Zip: "42"}}
	ve := validateErrs(t, &p)

	for field, code := range map[string]string{
		"name":         "required",
		"email":        "email",
		"role":         "oneof",
		"age":          "max",
		"tags":         "required",
		"address.city": "min",
		"address.zip":  "len",
	} {
		fe := findField(ve, field)
		if fe == nil {
			t.Errorf("missing error for %s in %+v", field, ve)
			continue
		}
		if fe.Code != code {
			t.Errorf("%s code = %q, want %q", field, fe.Code, code)
		}
		if fe.Message == "" {
			t.Errorf("%s has an empty message", field)
		}
	}
}

func TestValidateSliceElementsUseIndexPaths(t *testing.T) {
	p := profile{
		Name:    "Ada",
		Email:   "ada@example.com",
		Age:     1,
		Role:    "user",
		Tags:    []string{"x"},
		Address: address{City: "Oslo", Zip: "0150"},
		Friends: []address{{City: "Oslo", Zip: "0150"}, {Zip: "0150"}},
	}
	ve := validateErrs(t, &p)
	fe := findField(ve, "friends[1].city")
	if fe == nil {
		t.Fatalf("expected friends[1].city error, got %+v", ve)
	}
	if fe.Code != "required" {
		t.Errorf("code = %q, want required", fe.Code)
	}
	if findField(ve, "friends[0].city") != nil {
		t.Errorf("unexpected error for a valid element: %+v", ve)
	}
}

func TestValidateMinMaxOnStringsCountsRunes(t *testing.T) {
	type in struct {
		Name string `validate:"min=3"`
	}
	if err := Validate(&in{Name: "日本語"}); err != nil {
		t.Fatalf("three runes should satisfy min=3: %v", err)
	}
	ve := validateErrs(t, &in{Name: "ab"})
	if !strings.Contains(ve[0].Message, "characters") {
		t.Errorf("message = %q, want a character-count message", ve[0].Message)
	}
}

func TestValidateMinMaxOnSlicesCountsItems(t *testing.T) {
	type in struct {
		Tags []string `validate:"min=2,max=3"`
	}
	if err := Validate(&in{Tags: []string{"a", "b"}}); err != nil {
		t.Fatalf("two items should satisfy min=2: %v", err)
	}
	ve := validateErrs(t, &in{Tags: []string{"a"}})
	if !strings.Contains(ve[0].Message, "items") {
		t.Errorf("message = %q, want an item-count message", ve[0].Message)
	}
}

func TestValidateUnsetValuesSkipShapeRules(t *testing.T) {
	type in struct {
		Age  int      `validate:"min=18"`
		Bio  string   `validate:"max=10"`
		Tags []string `validate:"min=2"`
	}
	if err := Validate(&in{}); err != nil {
		t.Fatalf("unset values should not trip shape rules: %v", err)
	}
	// Present-but-invalid values still fail.
	if err := Validate(&in{Age: 17}); err == nil {
		t.Fatal("expected an error for a present value below min")
	}
}

func TestValidateRequiredCatchesZeroValues(t *testing.T) {
	type in struct {
		Age int `validate:"required,min=18"`
	}
	ve := validateErrs(t, &in{})
	if ve[0].Code != "required" {
		t.Fatalf("code = %q, want required", ve[0].Code)
	}
}

func TestValidateUnknownRuleIsReportedNotPanicked(t *testing.T) {
	type in struct {
		Name string `validate:"requried"`
	}
	ve := validateErrs(t, &in{Name: "x"})
	if ve[0].Code != "invalid_rule" {
		t.Fatalf("code = %q, want invalid_rule", ve[0].Code)
	}
}

func TestValidateNonNumericArgument(t *testing.T) {
	type in struct {
		Name string `validate:"min=many"`
	}
	ve := validateErrs(t, &in{Name: "abcdef"})
	if ve[0].Code != "invalid_rule" {
		t.Fatalf("code = %q, want invalid_rule", ve[0].Code)
	}
}

func TestValidateRuleTypeMismatch(t *testing.T) {
	type in struct {
		Count int `validate:"email"`
	}
	ve := validateErrs(t, &in{Count: 3})
	if ve[0].Code != "invalid_rule" {
		t.Fatalf("code = %q, want invalid_rule for email on an int", ve[0].Code)
	}
}

func TestValidatePointerFields(t *testing.T) {
	type in struct {
		Name *string `validate:"required"`
		City *string `json:"city" validate:"min=2"`
	}
	empty := ""
	oslo := "Oslo"
	ve := validateErrs(t, &in{})
	if findField(ve, "Name") == nil {
		t.Fatalf("expected a required error for the nil pointer, got %+v", ve)
	}
	if err := Validate(&in{Name: &empty, City: &oslo}); err != nil {
		t.Fatalf("non-nil pointers should validate: %v", err)
	}
}

func TestValidateCustomValidatable(t *testing.T) {
	type in struct {
		Code string `json:"code"`
	}
	var got in
	err := Validate(&customInput{Code: "abc"})
	_ = got
	var ve ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v (%T), want ValidationErrors", err, err)
	}
	if findField(ve, "code") == nil {
		t.Fatalf("expected field errors from Validate(), got %+v", ve)
	}
}

type customInput struct {
	Code string
}

func (c *customInput) Validate() error {
	if c.Code == "ok" {
		return nil
	}
	return ValidationErrors{{Field: "code", Code: "custom", Message: "must be ok"}}
}

func TestValidateCustomPlainError(t *testing.T) {
	err := Validate(&plainCustom{})
	var ve ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v, want ValidationErrors", err)
	}
	if ve[0].Code != CodeValidationFailed || ve[0].Message != "no good" {
		t.Fatalf("got %+v, want a validation_failed detail", ve[0])
	}
}

type plainCustom struct{}

func (plainCustom) Validate() error { return errors.New("no good") }

func TestRegisterRuleAndRuleNames(t *testing.T) {
	RegisterRule("even", func(field string, v reflect.Value, _ string) *FieldError {
		if v.Kind() == reflect.Int && v.Int()%2 != 0 {
			return &FieldError{Field: field, Code: "even", Message: "must be even"}
		}
		return nil
	})
	defer func() {
		delete(rules, "even")
	}()

	type in struct {
		N int `validate:"even"`
	}
	if err := Validate(&in{N: 2}); err != nil {
		t.Fatalf("even 2 should pass: %v", err)
	}
	ve := validateErrs(t, &in{N: 3})
	if ve[0].Code != "even" {
		t.Fatalf("code = %q, want even", ve[0].Code)
	}

	names := RuleNames()
	found := false
	for _, n := range names {
		if n == "even" {
			found = true
		}
	}
	if !found {
		t.Fatalf("RuleNames() = %v, want it to include the registered rule", names)
	}
}
