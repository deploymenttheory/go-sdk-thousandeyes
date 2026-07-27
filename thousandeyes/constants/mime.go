// Package constants holds MIME types and endpoint paths for the ThousandEyes API.
package constants

// MIME types used by the ThousandEyes v7 API.
//
// The API is HAL-flavoured: successful responses are served as
// application/hal+json, which is why HALJSON rather than ApplicationJSON is the
// default Accept header on generated read operations. Request bodies are plain
// application/json. Errors on 4xx validation failures arrive as
// application/problem+json (RFC 7807).
//
// Verified against the live API:
//
//	GET  /v7/tests      -> content-type: application/hal+json;charset=UTF-8
//	POST /v7/tests/bgp  -> content-type: application/problem+json;charset=UTF-8 (400)
const (
	// HALJSON is the content type of successful ThousandEyes responses.
	HALJSON = "application/hal+json"

	// ApplicationJSON is the content type for request bodies, and for the
	// minority of responses that are not HAL.
	ApplicationJSON = "application/json"

	// ProblemJSON is the content type of RFC 7807 error responses.
	ProblemJSON = "application/problem+json"

	// TextCSV is used by the reporting endpoints that support CSV output.
	TextCSV = "text/csv"

	// OctetStream is used for binary downloads.
	OctetStream = "application/octet-stream"
)

// Accept is the value generated operations send on requests that expect a
// response body. Both types are listed because a minority of endpoints answer
// with plain JSON rather than HAL.
const Accept = HALJSON + ", " + ApplicationJSON

// XML types. The v7 API is JSON-only, but the transport's content-type
// sniffing is shared with the response helpers, which handle both.
const (
	ApplicationXML = "application/xml"
	TextXML        = "text/xml"
)
