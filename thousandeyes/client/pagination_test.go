package client

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The envelope below is the live shape of GET /v7/tests: the payload array is
// named after the resource rather than sitting under a fixed "results" key, and
// paging is a cursor followed through _links.next.
const testsEnvelope = `{
  "tests": [{"testId":"1"},{"testId":"2"}],
  "_links": {"self": {"href": "https://api.thousandeyes.com/v7/tests?aid=1"}}
}`

func TestUnit_Pagination_ExtractCollection_Success(t *testing.T) {
	collection, err := extractCollection([]byte(testsEnvelope), "tests")
	require.NoError(t, err)

	var items []map[string]any
	require.NoError(t, json.Unmarshal(collection, &items))
	assert.Len(t, items, 2)
}

func TestUnit_Pagination_ExtractCollection_MissingKey(t *testing.T) {
	// An endpoint with nothing to return may omit the array entirely, which is
	// an empty page rather than a failure.
	collection, err := extractCollection([]byte(testsEnvelope), "agents")
	require.NoError(t, err)
	assert.Nil(t, collection)
}

func TestUnit_Pagination_ExtractCollection_EmptyKeyReturnsWholeBody(t *testing.T) {
	collection, err := extractCollection([]byte(testsEnvelope), "")
	require.NoError(t, err)
	assert.JSONEq(t, testsEnvelope, string(collection))
}

func TestUnit_Pagination_ExtractCollection_InvalidJSON(t *testing.T) {
	_, err := extractCollection([]byte("{not json"), "tests")
	assert.ErrorContains(t, err, "decoding response envelope")
}

func TestUnit_Pagination_Envelope_NoNextLink(t *testing.T) {
	// The last page — and every response from the endpoints that do not
	// paginate at all — carries a self link but no next.
	var envelope halPage
	require.NoError(t, json.Unmarshal([]byte(testsEnvelope), &envelope))

	assert.Nil(t, envelope.Links.Next)
	require.NotNil(t, envelope.Links.Self)
	assert.Contains(t, envelope.Links.Self.Href, "/v7/tests")
}

func TestUnit_Pagination_Envelope_NextLink(t *testing.T) {
	body := `{"tests":[],"_links":{"next":{"href":"https://api.thousandeyes.com/v7/tests?cursor=abc"}}}`

	var envelope halPage
	require.NoError(t, json.Unmarshal([]byte(body), &envelope))

	require.NotNil(t, envelope.Links.Next)
	// The href is absolute and already carries the cursor, so it is followed
	// verbatim rather than having query parameters reapplied to it.
	assert.Equal(t, "https://api.thousandeyes.com/v7/tests?cursor=abc", envelope.Links.Next.Href)
}

func TestUnit_Pagination_Envelope_AbsentLinks(t *testing.T) {
	// A plain JSON response with no HAL envelope must not be treated as paged.
	var envelope halPage
	require.NoError(t, json.Unmarshal([]byte(`{"tests":[]}`), &envelope))
	assert.Nil(t, envelope.Links.Next)
}
