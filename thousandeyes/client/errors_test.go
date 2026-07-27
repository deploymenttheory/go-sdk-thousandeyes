package client

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// The bodies below are verbatim from the live API. The v7 service answers with
// three unrelated error shapes depending on what went wrong, plus an empty body
// on 404, and each is exercised here so a regression in one parser cannot hide
// behind another.
func TestUnit_Errors_Parse_ProblemJSON(t *testing.T) {
	// Observed: POST /v7/tests/bgp with an invalid body.
	body := []byte(`{"type":"about:blank","title":"There were some errors in your request, ` +
		`please correct them before trying again. Error in field prefix.prefix : Prefix is empty.",` +
		`"status":400,"instance":"/v7/tests/bgp"}`)

	err := ParseErrorResponse(body, http.StatusBadRequest, "400 Bad Request", "POST", "/tests/bgp", zap.NewNop())

	apiErr, ok := asAPIError(err)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Contains(t, apiErr.Message, "Prefix is empty")
	// about:blank means "no specific type", so it must not surface as a code.
	assert.Empty(t, apiErr.Code)
	assert.True(t, IsBadRequest(err))
}

func TestUnit_Errors_Parse_ProblemJSONWithType(t *testing.T) {
	body := []byte(`{"type":"https://example.com/errors/quota","title":"Quota exceeded","detail":"Try later","status":429}`)

	err := ParseErrorResponse(body, http.StatusTooManyRequests, "429 Too Many Requests", "GET", "/tests", zap.NewNop())

	apiErr, _ := asAPIError(err)
	assert.Equal(t, "https://example.com/errors/quota", apiErr.Code)
	assert.Equal(t, "Quota exceeded", apiErr.Message)
	assert.Equal(t, "Try later", apiErr.Detail)
	// Detail is appended to the message rather than lost.
	assert.Contains(t, err.Error(), "Try later")
	assert.True(t, IsRateLimited(err))
}

func TestUnit_Errors_Parse_OAuthError(t *testing.T) {
	// Observed: a syntactically valid but unrecognised bearer token.
	body := []byte(`{"error":"invalid_token","error_description":"Invalid access token"}`)

	err := ParseErrorResponse(body, http.StatusUnauthorized, "401 Unauthorized", "GET", "/tests", zap.NewNop())

	apiErr, _ := asAPIError(err)
	assert.Equal(t, "invalid_token", apiErr.Code)
	assert.Equal(t, "Invalid access token", apiErr.Message)
	assert.True(t, IsUnauthorized(err))
}

func TestUnit_Errors_Parse_LegacyError(t *testing.T) {
	// Observed: no Authorization header at all. A different shape from the
	// invalid-token case above, despite the same status.
	body := []byte("{\"errorMessage\":\"401 Not Authorized\\nPlease ensure you are using the correct authentication token.\"}")

	err := ParseErrorResponse(body, http.StatusUnauthorized, "401 Unauthorized", "GET", "/tests", zap.NewNop())

	apiErr, _ := asAPIError(err)
	assert.Contains(t, apiErr.Message, "401 Not Authorized")
	assert.Empty(t, apiErr.Code)
	assert.True(t, IsUnauthorized(err))
}

func TestUnit_Errors_Parse_EmptyBody(t *testing.T) {
	// Observed: 404 returns no body whatsoever, so a description has to be
	// supplied or the error reads as blank.
	err := ParseErrorResponse(nil, http.StatusNotFound, "404 Not Found", "GET", "/tests/bgp/1", zap.NewNop())

	apiErr, _ := asAPIError(err)
	assert.NotEmpty(t, apiErr.Message)
	assert.Contains(t, apiErr.Message, "not found")
	assert.True(t, IsNotFound(err))
}

func TestUnit_Errors_Parse_UnrecognisedBody(t *testing.T) {
	err := ParseErrorResponse([]byte("upstream failure"), http.StatusBadGateway, "502 Bad Gateway", "GET", "/tests", zap.NewNop())

	apiErr, _ := asAPIError(err)
	assert.Equal(t, "upstream failure", apiErr.Message)
	assert.True(t, IsServerError(err))
}

func TestUnit_Errors_Parse_NilLogger(t *testing.T) {
	// Callers outside the transport may not have a logger to hand.
	assert.NotPanics(t, func() {
		_ = ParseErrorResponse(nil, http.StatusNotFound, "404", "GET", "/x", nil)
	})
}

func TestUnit_Errors_Predicates_IgnoreOtherErrors(t *testing.T) {
	plain := assert.AnError
	assert.False(t, IsNotFound(plain))
	assert.False(t, IsUnauthorized(plain))
	assert.False(t, IsForbidden(plain))
	assert.False(t, IsBadRequest(plain))
	assert.False(t, IsRateLimited(plain))
	assert.False(t, IsServerError(plain))
}

func TestUnit_Errors_Error_Format(t *testing.T) {
	e := &APIError{
		Code:       "invalid_token",
		Message:    "Invalid access token",
		StatusCode: 401,
		Status:     "401 Unauthorized",
		Method:     "GET",
		Endpoint:   "/tests",
	}
	assert.Equal(t,
		"ThousandEyes API error (401 401 Unauthorized) [invalid_token] at GET /tests: Invalid access token",
		e.Error())

	e.Code = ""
	assert.Equal(t,
		"ThousandEyes API error (401 401 Unauthorized) at GET /tests: Invalid access token",
		e.Error())
}
