package templating

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_TemplatedValue_Unmarshal_ConcreteValue(t *testing.T) {
	var v TemplatedValue[int]
	require.NoError(t, json.Unmarshal([]byte(`300`), &v))

	assert.True(t, v.IsValue())
	assert.False(t, v.IsExpression())

	got, ok := v.Get()
	require.True(t, ok)
	assert.Equal(t, 300, got)
}

func TestUnit_TemplatedValue_Unmarshal_Expression(t *testing.T) {
	var v TemplatedValue[string]
	require.NoError(t, json.Unmarshal([]byte(`"{{agent.name}}"`), &v))

	assert.True(t, v.IsExpression())
	assert.False(t, v.IsValue())
	assert.Equal(t, "{{agent.name}}", *v.Expression)

	// An unresolved expression has no value yet.
	_, ok := v.Get()
	assert.False(t, ok)
}

func TestUnit_TemplatedValue_Unmarshal_LiteralStringIsNotAnExpression(t *testing.T) {
	// A string that merely contains braces is a literal. Treating it as an
	// expression would silently change what gets sent to the API.
	for _, literal := range []string{
		`"prefix {{name}} suffix"`,
		`"{{unclosed"`,
		`"{{a}}{{b}}"`,
		`"plain"`,
	} {
		var v TemplatedValue[string]
		require.NoError(t, json.Unmarshal([]byte(literal), &v), literal)
		assert.True(t, v.IsValue(), "expected a literal value for %s", literal)
		assert.False(t, v.IsExpression(), "expected no expression for %s", literal)
	}
}

func TestUnit_TemplatedValue_Unmarshal_SurroundingWhitespace(t *testing.T) {
	var v TemplatedValue[string]
	require.NoError(t, json.Unmarshal([]byte(`"  {{agent.name}}  "`), &v))
	assert.True(t, v.IsExpression())
}

func TestUnit_TemplatedValue_Unmarshal_Object(t *testing.T) {
	type agent struct {
		Name string `json:"name"`
	}

	var v TemplatedValue[agent]
	require.NoError(t, json.Unmarshal([]byte(`{"name":"London"}`), &v))

	got, ok := v.Get()
	require.True(t, ok)
	assert.Equal(t, "London", got.Name)
}

func TestUnit_TemplatedValue_Unmarshal_Null(t *testing.T) {
	var v TemplatedValue[int]
	require.NoError(t, json.Unmarshal([]byte(`null`), &v))
	assert.False(t, v.IsValue())
	assert.False(t, v.IsExpression())
}

func TestUnit_TemplatedValue_Unmarshal_TypeMismatch(t *testing.T) {
	var v TemplatedValue[int]
	err := json.Unmarshal([]byte(`{"not":"an int"}`), &v)
	assert.ErrorContains(t, err, "decoding templated value")
}

func TestUnit_TemplatedValue_Marshal_RoundTrip(t *testing.T) {
	t.Run("value", func(t *testing.T) {
		out, err := json.Marshal(NewValue(300))
		require.NoError(t, err)
		assert.JSONEq(t, `300`, string(out))
	})

	t.Run("expression", func(t *testing.T) {
		out, err := json.Marshal(NewExpression[int]("{{interval}}"))
		require.NoError(t, err)
		assert.JSONEq(t, `"{{interval}}"`, string(out))
	})

	t.Run("zero value encodes as null", func(t *testing.T) {
		out, err := json.Marshal(TemplatedValue[int]{})
		require.NoError(t, err)
		assert.Equal(t, "null", string(out))
	})
}

func TestUnit_TemplatedValue_Marshal_SurvivesRoundTrip(t *testing.T) {
	original := NewExpression[string]("{{agent.name}}")

	encoded, err := json.Marshal(original)
	require.NoError(t, err)

	var back TemplatedValue[string]
	require.NoError(t, json.Unmarshal(encoded, &back))

	assert.True(t, back.IsExpression())
	assert.Equal(t, *original.Expression, *back.Expression)
}

func TestUnit_TemplatedValue_String(t *testing.T) {
	assert.Equal(t, "{{agent.name}}", NewExpression[string]("{{agent.name}}").String())
	assert.Equal(t, "300", NewValue(300).String())
	assert.Empty(t, TemplatedValue[int]{}.String())
}
