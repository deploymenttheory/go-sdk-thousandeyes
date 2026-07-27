package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// APIError represents an error response from the ThousandEyes API.
type APIError struct {
	// Code carries the machine-readable discriminator when the response
	// provides one: the RFC 7807 "type", or the OAuth "error" field.
	Code string
	// Message is the human-readable explanation.
	Message string
	// Detail is the RFC 7807 "detail" member, when present.
	Detail string
	// StatusCode is the HTTP status code.
	StatusCode int
	// Status is the HTTP status line.
	Status string
	// Endpoint is the request path.
	Endpoint string
	// Method is the HTTP method.
	Method string
}

// Error implements the error interface.
func (e *APIError) Error() string {
	msg := e.Message
	if e.Detail != "" && e.Detail != e.Message {
		msg = strings.TrimSuffix(msg, ".") + ": " + e.Detail
	}
	if e.Code != "" {
		return fmt.Sprintf("ThousandEyes API error (%d %s) [%s] at %s %s: %s",
			e.StatusCode, e.Status, e.Code, e.Method, e.Endpoint, msg)
	}
	return fmt.Sprintf("ThousandEyes API error (%d %s) at %s %s: %s",
		e.StatusCode, e.Status, e.Method, e.Endpoint, msg)
}

// problemDetails is the RFC 7807 body the API returns for validation failures,
// served as application/problem+json. Verified against the live API:
//
//	POST /v7/tests/bgp with an invalid body ->
//	{"type":"about:blank","title":"There were some errors in your request...",
//	 "status":400,"instance":"/v7/tests/bgp"}
type problemDetails struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

// oauthError is the body returned when a bearer token is present but invalid.
// Verified against the live API:
//
//	{"error":"invalid_token","error_description":"Invalid access token"}
type oauthError struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// legacyError is the body returned when no credentials are supplied at all.
// Verified against the live API:
//
//	{"errorMessage":"401 Not Authorized\nPlease ensure you are using the correct
//	authentication token."}
type legacyError struct {
	ErrorMessage string `json:"errorMessage"`
}

// ParseErrorResponse turns an error response into an *APIError.
//
// The API does not use a single error shape. Three distinct ones have been
// observed on the live service and are tried in turn, with a final fallback for
// an empty body — which is what a 404 returns.
func ParseErrorResponse(
	body []byte,
	statusCode int,
	status, method, endpoint string,
	logger *zap.Logger,
) error {
	apiError := &APIError{
		StatusCode: statusCode,
		Status:     status,
		Endpoint:   endpoint,
		Method:     method,
	}

	switch {
	case parseProblem(body, apiError):
	case parseOAuth(body, apiError):
	case parseLegacy(body, apiError):
	default:
		apiError.Message = strings.TrimSpace(string(body))
		if apiError.Message == "" {
			// A 404 in particular answers with no body at all.
			apiError.Message = defaultMessageForStatus(statusCode)
		}
	}

	if logger != nil {
		logger.Error("API error response",
			zap.Int("status_code", statusCode),
			zap.String("method", method),
			zap.String("endpoint", endpoint),
			zap.String("code", apiError.Code),
			zap.String("message", apiError.Message))
	}

	return apiError
}

func parseProblem(body []byte, target *APIError) bool {
	var p problemDetails
	if err := json.Unmarshal(body, &p); err != nil || p.Title == "" {
		return false
	}
	target.Message = p.Title
	target.Detail = p.Detail
	// "about:blank" is RFC 7807's way of saying "no specific type", so it
	// carries nothing worth surfacing as a code.
	if p.Type != "" && p.Type != "about:blank" {
		target.Code = p.Type
	}
	return true
}

func parseOAuth(body []byte, target *APIError) bool {
	var o oauthError
	if err := json.Unmarshal(body, &o); err != nil || o.Error == "" {
		return false
	}
	target.Code = o.Error
	target.Message = o.Description
	if target.Message == "" {
		target.Message = o.Error
	}
	return true
}

func parseLegacy(body []byte, target *APIError) bool {
	var l legacyError
	if err := json.Unmarshal(body, &l); err != nil || l.ErrorMessage == "" {
		return false
	}
	target.Message = strings.TrimSpace(l.ErrorMessage)
	return true
}

func defaultMessageForStatus(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return "The request could not be understood by the server due to malformed syntax."
	case http.StatusUnauthorized:
		return "The request lacks valid authentication credentials for the target resource."
	case http.StatusForbidden:
		return "The server understood the request but refuses to authorize it. " +
			"The token may lack the required permission."
	case http.StatusNotFound:
		return "The server has not found anything matching the request URI."
	case http.StatusConflict:
		return "The request could not be completed due to a conflict with the current state of the resource."
	case http.StatusUnprocessableEntity:
		return "The request is syntactically correct but contains a field with an unacceptable value."
	case http.StatusTooManyRequests:
		return "The organization rate limit has been exceeded."
	case http.StatusInternalServerError:
		return "The server encountered an unexpected condition."
	case http.StatusServiceUnavailable:
		return "The server is temporarily unable to handle the request."
	default:
		return "Unknown error"
	}
}

// asAPIError extracts an *APIError from err, if it is one.
func asAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// IsNotFound reports whether err is an APIError with HTTP 404.
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsUnauthorized reports whether err is an APIError with HTTP 401, meaning the
// token is missing, malformed or expired.
func IsUnauthorized(err error) bool { return hasStatus(err, http.StatusUnauthorized) }

// IsForbidden reports whether err is an APIError with HTTP 403, meaning the
// token is valid but the account lacks the permission, or the requested account
// group is not one the token owner is assigned to.
func IsForbidden(err error) bool { return hasStatus(err, http.StatusForbidden) }

// IsBadRequest reports whether err is an APIError with HTTP 400.
func IsBadRequest(err error) bool { return hasStatus(err, http.StatusBadRequest) }

// IsRateLimited reports whether err is an APIError with HTTP 429. The
// organization's limit and reset time are carried on the
// x-organization-rate-limit-* response headers.
func IsRateLimited(err error) bool { return hasStatus(err, http.StatusTooManyRequests) }

// IsServerError reports whether err is an APIError with a 5xx status.
func IsServerError(err error) bool {
	apiErr, ok := asAPIError(err)
	return ok && apiErr.StatusCode >= http.StatusInternalServerError && apiErr.StatusCode < 600
}

func hasStatus(err error, status int) bool {
	apiErr, ok := asAPIError(err)
	return ok && apiErr.StatusCode == status
}
