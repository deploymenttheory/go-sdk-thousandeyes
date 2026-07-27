// Package generatedtest exercises the behaviour of generated code.
//
// It lives outside thousandeyes/thousandeyes_api because the generator clears
// that tree on every run, which would delete any test placed alongside the
// generated files.
package generatedtest

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tests "github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/thousandeyes_api/agent_to_server_endpoint_scheduled_tests"
)

// The payloads below mirror the live API, where agent selection is sent flat on
// the request and returned nested under agentSelectorConfig.

func TestUnit_Union_Unmarshal_SelectsVariant(t *testing.T) {
	const payload = `{"agentSelectorType":"specific-agents","agents":["1","2"],"maxMachines":5}`

	var u tests.EndpointAgentSelectorConfig
	require.NoError(t, json.Unmarshal([]byte(payload), &u))

	assert.Equal(t, tests.EndpointAgentSelectorConfigTypeSpecificAgents, u.AgentSelectorType)
	assert.True(t, u.AgentSelectorType.IsKnown())

	variant, ok := u.GetSpecificAgents()
	require.True(t, ok)
	assert.EqualValues(t, []string{"1", "2"}, variant.Agents)

	// Only the selected variant is populated.
	assert.True(t, u.IsSpecificAgents())
	assert.False(t, u.IsAllAgents())
	assert.Nil(t, u.AllAgents)
	assert.Nil(t, u.AgentTags)

	_, ok = u.GetAllAgents()
	assert.False(t, ok)
}

func TestUnit_Union_Constructor_SetsTagAndVariant(t *testing.T) {
	// The constructor exists so the tag and the variant cannot disagree.
	u := tests.NewEndpointAgentSelectorConfigFromAllAgents(
		tests.EndpointAllAgentsSelectorConfig{},
	)

	assert.Equal(t, tests.EndpointAgentSelectorConfigTypeAllAgents, u.AgentSelectorType)
	assert.True(t, u.IsAllAgents())
	require.NotNil(t, u.AllAgents)

	encoded, err := json.Marshal(u)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"agentSelectorType":"all-agents"`)
}

func TestUnit_Union_Marshal_RoundTrip(t *testing.T) {
	const payload = `{"agentSelectorType":"specific-agents","agents":["1","2"],"maxMachines":5}`

	var u tests.EndpointAgentSelectorConfig
	require.NoError(t, json.Unmarshal([]byte(payload), &u))

	encoded, err := json.Marshal(u)
	require.NoError(t, err)

	var back tests.EndpointAgentSelectorConfig
	require.NoError(t, json.Unmarshal(encoded, &back))

	variant, ok := back.GetSpecificAgents()
	require.True(t, ok, "round trip lost the variant")
	assert.EqualValues(t, []string{"1", "2"}, variant.Agents)
}

func TestUnit_Union_Unmarshal_UnknownDiscriminatorIsNotAnError(t *testing.T) {
	// The API adds variants without a major version change. Failing the whole
	// response because one field is new would be worse than leaving it
	// unpopulated, so the payload is retained instead.
	const payload = `{"agentSelectorType":"agent-groups-v2","groups":["a"]}`

	var u tests.EndpointAgentSelectorConfig
	require.NoError(t, json.Unmarshal([]byte(payload), &u))

	assert.EqualValues(t, "agent-groups-v2", u.AgentSelectorType)
	assert.False(t, u.AgentSelectorType.IsKnown())
	assert.NotEmpty(t, u.Raw)
	assert.Nil(t, u.SpecificAgents)

	// An unknown variant still round-trips, because Raw is the fallback.
	encoded, err := json.Marshal(u)
	require.NoError(t, err)
	assert.JSONEq(t, payload, string(encoded))
}

func TestUnit_Union_Marshal_MismatchedTagIsAnError(t *testing.T) {
	// This is the hole the pass closed. Previously a tag naming a variant that
	// was not set fell back to Raw or null, silently sending the server
	// something the caller never asked for.
	u := tests.EndpointAgentSelectorConfig{
		AgentSelectorType: tests.EndpointAgentSelectorConfigTypeSpecificAgents,
		// SpecificAgents deliberately left nil.
	}

	_, err := json.Marshal(u)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SpecificAgents is not set")
}

func TestUnit_Union_Marshal_ZeroValueIsNull(t *testing.T) {
	encoded, err := json.Marshal(tests.EndpointAgentSelectorConfig{})
	require.NoError(t, err)
	assert.Equal(t, "null", string(encoded))
}

func TestUnit_Union_DiscriminatorEnum_IsOpen(t *testing.T) {
	// The synthesised discriminator enum follows the same open contract as
	// every other generated enumeration.
	known := tests.EndpointAgentSelectorConfigTypeAllAgents
	assert.True(t, known.IsKnown())
	assert.Equal(t, "all-agents", known.String())

	var future tests.EndpointAgentSelectorConfigType = "agent-groups-v2"
	assert.False(t, future.IsKnown())
	assert.Equal(t, "EndpointAgentSelectorConfigType(agent-groups-v2)", future.String())

	assert.NotEmpty(t, tests.EndpointAgentSelectorConfigTypeValues())
}
