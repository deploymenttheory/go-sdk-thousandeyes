package mocks_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/client"
	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/mocks"
	bgp "github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/thousandeyes_api/bgp_tests"
	agents "github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/thousandeyes_api/cloud_and_enterprise_agents"
)

// The fixtures are real responses captured from the lab tenant with curl; see
// testdata. Requests travel the real transport, so these exercise the shipped
// auth, retry and error-parsing code rather than a stand-in.

func TestUnit_Mocks_ServesFixture(t *testing.T) {
	srv := mocks.New(t)
	srv.Register(t, "GET", "/agents", 200, "agents_list_200.json")

	list, resp, err := agents.NewCloudAndEnterpriseAgents(srv.Client(t)).
		GetAgents(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 200, resp.StatusCode())
	assert.Len(t, list.Agents, 3)
	assert.Equal(t, 1, srv.RequestCount())
}

func TestUnit_Mocks_RecordsWhatTheSDKSent(t *testing.T) {
	srv := mocks.New(t)
	srv.Register(t, "GET", "/agents", 200, "agents_list_200.json")

	_, _, err := agents.NewCloudAndEnterpriseAgents(srv.Client(t)).
		GetAgents(context.Background(), client.WithAccountGroupID("12345"))
	require.NoError(t, err)

	sent := srv.Requests()
	require.Len(t, sent, 1)
	assert.Equal(t, "GET", sent[0].Method)
	assert.Equal(t, "/agents", sent[0].Path)
	// The account group reaches the wire as the aid query parameter.
	assert.Contains(t, sent[0].Query, "aid=12345")
}

func TestUnit_Mocks_ErrorFixtureBecomesAPIError(t *testing.T) {
	srv := mocks.New(t)
	// A captured RFC 7807 problem document, exactly as the API serves it.
	srv.Register(t, "POST", "/tests/bgp", 400, "error_400_problem.json")

	_, _, err := bgp.NewBGPTests(srv.Client(t)).
		CreateBgpTest(context.Background(), &bgp.BgpTestRequest{})
	require.Error(t, err)

	assert.True(t, client.IsBadRequest(err))
	assert.Contains(t, err.Error(), "errors in your request")
}

func TestUnit_Mocks_UnregisteredCallIsLoud(t *testing.T) {
	srv := mocks.New(t)

	// Nothing registered: the harness must say so rather than leaving a
	// confusing decode failure downstream.
	_, _, err := agents.NewCloudAndEnterpriseAgents(srv.Client(t)).
		GetAgents(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no mock registered")
}

func TestUnit_Mocks_ReportsRateLimitHeaders(t *testing.T) {
	srv := mocks.New(t)
	srv.Register(t, "GET", "/agents", 200, "agents_list_200.json")

	transport := srv.Client(t)
	_, _, err := agents.NewCloudAndEnterpriseAgents(transport).GetAgents(context.Background())
	require.NoError(t, err)

	// The harness reports the quota because the live API does, and the
	// transport paces against it.
	quota := transport.RateLimit()
	assert.True(t, quota.Known)
	assert.Equal(t, 240, quota.Limit)
	assert.Equal(t, 239, quota.Remaining)
}
