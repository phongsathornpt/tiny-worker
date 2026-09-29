package rest

import (
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Validatable is implemented by input types that validate themselves. It is
// checked after tag rules run, so both can be combined.
//
// Return ValidationErrors to attach field paths, or any other error for a
// single non-field problem.
type Validatable interface {
	Validate() error
}

// Rule validates one field. args is the text after "=" in the tag ("" when the
// rule takes no argument). Return nil when the value is acceptable.
type Rule func(field string, value reflect.Value, args string) *FieldError

// rules holds the built-in and registered rules. Deliberately small: the
// built-ins cover the common REST checks without dragging regexp (+65 KB
// gzip) or net/mail (+55 KB) into the wasm.
var rules = map[string]Rule{
	"required": ruleRequired,
	"len":      ruleLen,
	"min":      ruleMin,
	"max":      ruleMax,
	"oneof":    ruleOneOf,
	"email":    ruleEmail,
}

// RegisterRule adds or replaces a validation rule by tag name.
func RegisterRule(name string, r Rule) {
	if name == "" || r == nil {
		return
	}
	rules[name] = r
}

// RuleNames lists the registered rule names in sorted order.
func RuleNames() []string {
	names := make([]string, 0, len(rules))
	for name := range rules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Validate runs `validate` tags and Validatable over v, returning
// ValidationErrors (rendered as 422) or nil.
//
// Field names are JSON dot paths (address.city, tags[0]), matching what the
// client sent. Nested structs and struct elements of slices/arrays are walked
// recursively; maps are not.
//
// Shape rules (len/min/max, and email) skip unset values — an empty string,
// a zero number, an empty slice — so they describe ranges for values that are
// present. Combine with required when presence matters:
// `validate:"required,min=18"` rejects both a missing and a zero age.
func Validate(v any) error {
	if v == nil {
		return nil
	}
	var errs ValidationErrors
	validateValue(reflect.ValueOf(v), "", &errs)
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func validateValue(rv reflect.Value, path string, errs *ValidationErrors) {
	rv = indirect(rv)
	if !rv.IsValid() || rv.Kind() != reflect.Struct {
		return
	}
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.PkgPath != "" {
			continue
		}
		fv := rv.Field(i)
		fp := joinPath(path, fieldPath(f))
		if tag := f.Tag.Get("validate"); tag != "" {
			runRules(fp, fv, tag, errs)
		}
		validateNested(fv, fp, errs)
	}
	if v, ok := asValidatable(rv); ok {
		if err := v.Validate(); err != nil {
			appendCustom(err, path, errs)
		}
	}
}

func validateNested(fv reflect.Value, path string, errs *ValidationErrors) {
	vv := indirect(fv)
	if !vv.IsValid() {
		return
	}
	switch vv.Kind() {
	case reflect.Struct:
		validateValue(vv, path, errs)
	case reflect.Slice, reflect.Array:
		for i := 0; i < vv.Len(); i++ {
			validateValue(vv.Index(i), indexPath(path, i), errs)
		}
	}
}

func runRules(path string, fv reflect.Value, tag string, errs *ValidationErrors) {
	for _, spec := range strings.Split(tag, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		name, args := spec, ""
		if i := strings.IndexByte(spec, '='); i >= 0 {
			name, args = spec[:i], spec[i+1:]
		}
		rule, ok := rules[name]
		if !ok {
			*errs = append(*errs, FieldError{
				Field:   path,
				Code:    "invalid_rule",
				Message: "unknown validation rule " + quote(name),
			})
			continue
		}
		if fe := rule(path, fv, args); fe != nil {
			*errs = append(*errs, *fe)
		}
	}
}

// appendCustom folds a Validatable error into the field errors.
func appendCustom(err error, path string, errs *ValidationErrors) {
	var ve ValidationErrors
	if errors.As(err, &ve) {
		for _, fe := range ve {
			if fe.Code == "" {
				fe.Code = CodeValidationFailed
			}
			if fe.Field == "" {
				fe.Field = path
			}
			*errs = append(*errs, fe)
		}
		return
	}
	*errs = append(*errs, FieldError{Field: path, Code: CodeValidationFailed, Message: err.Error()})
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func indexPath(path string, i int) string {
	return path + "[" + strconv.Itoa(i) + "]"
}

// indirect dereferences pointers and interfaces; the zero Value means "absent".
func indirect(v reflect.Value) reflect.Value {
	for i := 0; i < 8 && v.IsValid(); i++ {
		switch v.Kind() {
		case reflect.Ptr, reflect.Interface:
			if v.IsNil() {
				return reflect.Value{}
			}
			v = v.Elem()
		default:
			return v
		}
	}
	return v
}

func asValidatable(rv reflect.Value) (Validatable, bool) {
	if rv.CanAddr() {
		if v, ok := rv.Addr().Interface().(Validatable); ok {
			return v, true
		}
	}
	if rv.CanInterface() {
		if v, ok := rv.Interface().(Validatable); ok {
			return v, true
		}
	}
	return nil, false
}

// --- built-in rules -------------------------------------------------------

func ruleRequired(field string, v reflect.Value, _ string) *FieldError {
	if isZeroish(v) {
		return &FieldError{Field: field, Code: "required", Message: "is required"}
	}
	return nil
}

func isZeroish(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	}
	vv := indirect(v)
	if !vv.IsValid() {
		return true
	}
	switch vv.Kind() {
	case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
		return vv.Len() == 0
	case reflect.Bool:
		return !vv.Bool()
	default:
		return vv.IsZero()
	}
}

func ruleLen(field string, v reflect.Value, args string) *FieldError {
	n, ok := parseArgInt(args)
	if !ok {
		return invalidRuleArg(field, "len", args)
	}
	vv := indirect(v)
	if !vv.IsValid() || isZeroish(vv) {
		return nil // unset values are the required rule's business
	}
	got, unit, ok := measure(vv)
	if !ok {
		return invalidRuleType(field, "len", v)
	}
	if got != n {
		return &FieldError{
			Field:   field,
			Code:    "len",
			Message: "must have exactly " + strconv.Itoa(n) + " " + unit,
		}
	}
	return nil
}

func ruleMin(field string, v reflect.Value, args string) *FieldError {
	n, ok := parseArgInt(args)
	if !ok {
		return invalidRuleArg(field, "min", args)
	}
	vv := indirect(v)
	if !vv.IsValid() || isZeroish(vv) {
		return nil // unset values are the required rule's business
	}
	if isNumeric(vv.Kind()) {
		if numericValue(vv) < float64(n) {
			return &FieldError{Field: field, Code: "min", Message: "must be at least " + strconv.Itoa(n)}
		}
		return nil
	}
	got, unit, ok := measure(vv)
	if !ok {
		return invalidRuleType(field, "min", v)
	}
	if got < n {
		return &FieldError{
			Field:   field,
			Code:    "min",
			Message: "must be at least " + strconv.Itoa(n) + " " + unit,
		}
	}
	return nil
}

func ruleMax(field string, v reflect.Value, args string) *FieldError {
	n, ok := parseArgInt(args)
	if !ok {
		return invalidRuleArg(field, "max", args)
	}
	vv := indirect(v)
	if !vv.IsValid() || isZeroish(vv) {
		return nil // unset values are the required rule's business
	}
	if isNumeric(vv.Kind()) {
		if numericValue(vv) > float64(n) {
			return &FieldError{Field: field, Code: "max", Message: "must be at most " + strconv.Itoa(n)}
		}
		return nil
	}
	got, unit, ok := measure(vv)
	if !ok {
		return invalidRuleType(field, "max", v)
	}
	if got > n {
		return &FieldError{
			Field:   field,
			Code:    "max",
			Message: "must be at most " + strconv.Itoa(n) + " " + unit,
		}
	}
	return nil
}

func ruleOneOf(field string, v reflect.Value, args string) *FieldError {
	vv := indirect(v)
	if !vv.IsValid() {
		return nil
	}
	if vv.Kind() != reflect.String {
		return invalidRuleType(field, "oneof", v)
	}
	s := vv.String()
	opts := strings.Fields(args)
	for _, o := range opts {
		if s == o {
			return nil
		}
	}
	return &FieldError{
		Field:   field,
		Code:    "oneof",
		Message: "must be one of " + quoteList(opts),
	}
}

func ruleEmail(field string, v reflect.Value, _ string) *FieldError {
	vv := indirect(v)
	if !vv.IsValid() {
		return nil
	}
	if vv.Kind() != reflect.String {
		return invalidRuleType(field, "email", v)
	}
	// An empty value is the required rule's business, not email's.
	if s := vv.String(); s != "" && isEmail(s) {
		return nil
	}
	return &FieldError{Field: field, Code: "email", Message: "must be a valid email address"}
}

// --- helpers --------------------------------------------------------------

func parseArgInt(args string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(args))
	if err != nil {
		return 0, false
	}
	return n, true
}

// measure returns the length of a string (in runes), slice, array, or map,
// along with the unit word used in messages.
func measure(v reflect.Value) (int, string, bool) {
	if !v.IsValid() {
		return 0, "", false
	}
	switch v.Kind() {
	case reflect.String:
		return utf8.RuneCountInString(v.String()), "characters", true
	case reflect.Slice, reflect.Array, reflect.Map:
		return v.Len(), "items", true
	}
	return 0, "", false
}

func isNumeric(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func numericValue(v reflect.Value) float64 {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint())
	default:
		return v.Float()
	}
}

func invalidRuleArg(field, rule, args string) *FieldError {
	return &FieldError{
		Field:   field,
		Code:    "invalid_rule",
		Message: "rule " + quote(rule) + " needs a numeric argument, got " + quote(args),
	}
}

func invalidRuleType(field, rule string, v reflect.Value) *FieldError {
	kind := "value"
	if v.IsValid() {
		kind = v.Kind().String()
	}
	return &FieldError{
		Field:   field,
		Code:    "invalid_rule",
		Message: "rule " + quote(rule) + " does not support " + kind,
	}
}

func quoteList(items []string) string {
	if len(items) == 0 {
		return "the allowed values"
	}
	var b strings.Builder
	for i, item := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quote(item))
	}
	return b.String()
}
