// Package templating models the ThousandEyes Templates API's substitution
// syntax.
//
// The Templates API lets almost any field carry either a concrete value or a
// Handlebars expression to be resolved when the template is applied. In the
// OpenAPI specification this appears as an anyOf over the concrete schema and
// HandlebarsExpression — 147 such sites, all but one belonging to Templates.
// That is a substitution overlay rather than genuine polymorphism, so one
// generic type covers every site instead of a generated union per field.
package templating

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
)

// handlebarsPattern matches a Handlebars expression: a string that is entirely
// one {{...}} substitution, optionally surrounded by whitespace.
//
// The match is deliberately strict. A field whose value merely contains braces
// is a literal string, and treating it as an expression would silently change
// what gets sent to the API.
var handlebarsPattern = regexp.MustCompile(`^\s*\{\{[^{}]*\}\}\s*$`)

// TemplatedValue holds either a concrete value or a Handlebars expression that
// resolves to one.
//
// Exactly one of the two is set after a successful decode. A zero value encodes
// as JSON null, which is how an absent optional field is represented.
type TemplatedValue[T any] struct {
	// Value is the concrete value, when the field carries one.
	Value *T
	// Expression is the Handlebars source, including its braces, when the field
	// defers to template substitution.
	Expression *string
}

// NewValue returns a TemplatedValue holding a concrete value.
func NewValue[T any](v T) TemplatedValue[T] {
	return TemplatedValue[T]{Value: &v}
}

// NewExpression returns a TemplatedValue holding a Handlebars expression, for
// example "{{agent.name}}".
func NewExpression[T any](expression string) TemplatedValue[T] {
	return TemplatedValue[T]{Expression: &expression}
}

// IsExpression reports whether the field defers to template substitution.
func (t TemplatedValue[T]) IsExpression() bool { return t.Expression != nil }

// IsValue reports whether the field carries a concrete value.
func (t TemplatedValue[T]) IsValue() bool { return t.Value != nil }

// Get returns the concrete value and whether it is present. It is false for an
// unresolved expression, which by definition has no value yet.
func (t TemplatedValue[T]) Get() (T, bool) {
	if t.Value == nil {
		var zero T
		return zero, false
	}
	return *t.Value, true
}

// String renders the field for display: the expression as written, or the
// concrete value's JSON encoding.
func (t TemplatedValue[T]) String() string {
	if t.Expression != nil {
		return *t.Expression
	}
	if t.Value == nil {
		return ""
	}
	encoded, err := json.Marshal(t.Value)
	if err != nil {
		return fmt.Sprintf("%v", *t.Value)
	}
	return string(encoded)
}

// UnmarshalJSON decodes either form.
//
// A JSON string that is entirely a {{...}} expression becomes Expression;
// everything else, including a string that merely contains braces, decodes into
// Value as the concrete type.
func (t *TemplatedValue[T]) UnmarshalJSON(data []byte) error {
	t.Value = nil
	t.Expression = nil

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	// Only a JSON string can be an expression, so the check is cheap and
	// cannot misfire on an object or number.
	if trimmed[0] == '"' {
		var candidate string
		if err := json.Unmarshal(trimmed, &candidate); err == nil && handlebarsPattern.MatchString(candidate) {
			t.Expression = &candidate
			return nil
		}
	}

	var value T
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return fmt.Errorf("decoding templated value: %w", err)
	}
	t.Value = &value
	return nil
}

// MarshalJSON encodes whichever form is set, or null when neither is.
func (t TemplatedValue[T]) MarshalJSON() ([]byte, error) {
	switch {
	case t.Expression != nil:
		encoded, err := json.Marshal(*t.Expression)
		if err != nil {
			return nil, fmt.Errorf("encoding templated expression: %w", err)
		}
		return encoded, nil
	case t.Value != nil:
		encoded, err := json.Marshal(t.Value)
		if err != nil {
			return nil, fmt.Errorf("encoding templated value: %w", err)
		}
		return encoded, nil
	default:
		return []byte("null"), nil
	}
}
