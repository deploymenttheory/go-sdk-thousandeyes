package generatedtest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/client"
	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/config"
	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/thousandeyes_api/endpoint_agents"
)

// The ten /filter endpoints take their criteria as a POST body and page through
// a cursor query parameter.
//
// The envelopes below are SYNTHETIC, and that limits what these tests prove. On
// the tenants available for testing, every filter endpoint returns a single
// empty page with _links as {} — no next link has ever been observed, so the
// assumption that a next href appears in the usual place is taken from the
// specification rather than from the server. See client.PostPaginated.
//
// What these tests do establish is the POST-specific mechanic that GET
// pagination cannot cover: that the request body is replayed on every page. If
// it were dropped after the first request, later pages would be filtered
// differently — or rejected — and the merged result would be wrong.
//
// Confirmed against the live API, and the reason cursor is modelled at all:
// POST /v7/endpoint/agents/filter?cursor=abc returns 400 "Failed to
// deserialize", while ?bogusParam=abc is ignored and returns 200. The server
// parses cursor as a typed value.

// searchFilter is the criteria the /filter endpoints carry in their body. Its
// presence is the point of these tests: the body must survive every page.
func searchFilter() *endpoint_agents.AgentSearchRequest {
	return &endpoint_agents.AgentSearchRequest{
		SearchFilters: &endpoint_agents.AgentSearchFilters{
			ID: []endpoint_agents.EndpointAgentId{"agent-1", "agent-2"},
		},
	}
}

// filterPage builds a page in the shape the filter endpoints return: plain JSON
// rather than HAL, with a total alongside the collection.
func filterPage(t *testing.T, agentID, nextHref string) []byte {
	t.Helper()

	page := map[string]any{
		"agents":      []map[string]any{{"id": agentID}},
		"totalAgents": 2,
		"_links":      map[string]any{},
	}
	if nextHref != "" {
		page["_links"] = map[string]any{"next": map[string]any{"href": nextHref}}
	}

	out, err := json.Marshal(page)
	require.NoError(t, err)
	return out
}

func newEndpointAgents(t *testing.T, srv *httptest.Server) *endpoint_agents.EndpointAgents {
	t.Helper()

	transport, err := client.NewTransport(&config.AuthConfig{
		BearerToken: "test-token",
		APIEndpoint: srv.URL,
	})
	require.NoError(t, err)

	return endpoint_agents.NewEndpointAgents(transport)
}

func TestUnit_PostPagination_ReplaysBodyOnEveryPage(t *testing.T) {
	var requests int32
	var bodies []string

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))

		assert.Equal(t, http.MethodPost, r.Method, "later pages must stay POST, not fall back to GET")

		// The filter endpoints answer as plain JSON, not HAL.
		w.Header().Set("Content-Type", "application/json")

		if atomic.AddInt32(&requests, 1) == 1 {
			_, _ = w.Write(filterPage(t, "agent-1", srv.URL+"/endpoint/agents/filter?cursor=page2"))
			return
		}
		_, _ = w.Write(filterPage(t, "agent-2", ""))
	}))
	defer srv.Close()

	_, _, err := newEndpointAgents(t, srv).FilterEndpointAgents(context.Background(), searchFilter())
	require.NoError(t, err)

	require.Len(t, bodies, 2, "the walk must make a second request")

	// The point of the test: the filter travels with every page. Dropping it
	// after the first would silently change what the later pages return.
	assert.Contains(t, bodies[0], "agent-1", "the filter must reach the first page")
	assert.Equal(t, bodies[0], bodies[1], "the filter body must be replayed verbatim")
}

func TestUnit_PostPagination_StopsWithoutNextLink(t *testing.T) {
	var requests int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		// The single empty page every filter endpoint returns on the tenants
		// available for testing.
		_, _ = w.Write(filterPage(t, "agent-1", ""))
	}))
	defer srv.Close()

	_, _, err := newEndpointAgents(t, srv).FilterEndpointAgents(context.Background(), searchFilter())
	require.NoError(t, err)

	assert.EqualValues(t, int32(1), atomic.LoadInt32(&requests),
		"a response with no next link must cost exactly one request")
}

func TestUnit_PostPagination_RespectsMaxPages(t *testing.T) {
	var requests int32

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(filterPage(t, "agent-1", srv.URL+"/endpoint/agents/filter?cursor=more"))
	}))
	defer srv.Close()

	_, _, err := newEndpointAgents(t, srv).
		FilterEndpointAgents(context.Background(), searchFilter(), client.WithMaxPages(2))
	require.NoError(t, err)

	assert.EqualValues(t, int32(2), atomic.LoadInt32(&requests))
}
