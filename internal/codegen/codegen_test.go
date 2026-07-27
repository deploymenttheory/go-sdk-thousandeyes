package codegen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_Codegen_GoIdentifier_StripsAPINamespace(t *testing.T) {
	// The unified specification merges 27 per-domain documents, each keeping
	// its own component namespace. Inside a package that already belongs to
	// that domain the prefix is noise.
	assert.Equal(t, "BgpTest", goIdentifier("Tests_API_BgpTest"))
	assert.Equal(t, "Link", goIdentifier("Alerts_API_Link"))
	assert.Equal(t, "PaginationLinks", goIdentifier("Endpoint_Test_Results_API_PaginationLinks"))

	// Names without the namespace are left alone beyond casing.
	assert.Equal(t, "GetBgpTests", goIdentifier("getBgpTests"))
	assert.Equal(t, "AgentToServer", goIdentifier("agent-to-server"))
}

func TestUnit_Codegen_GoIdentifier_EdgeCases(t *testing.T) {
	assert.Equal(t, "Value", goIdentifier(""))
	// An identifier cannot start with a digit.
	assert.Equal(t, "N2fa", goIdentifier("2fa"))
}

func TestUnit_Codegen_PackageName_FromTag(t *testing.T) {
	assert.Equal(t, "bgp_tests", packageName("BGP Tests"))
	assert.Equal(t, "agent_to_server_endpoint_scheduled_tests", packageName("Agent to Server Endpoint Scheduled Tests"))
	assert.Equal(t, "cloud_and_enterprise_agents", packageName("Cloud and Enterprise Agents"))
	assert.Equal(t, "misc", packageName(""))
}

func TestUnit_Codegen_SafeParamName_AvoidsKeywords(t *testing.T) {
	// The specification names a path parameter "type", which is a Go keyword
	// and produced un-compilable output before this guard.
	assert.Equal(t, "typeParam", safeParamName("type"))
	assert.Equal(t, "rangeParam", safeParamName("range"))
	// Locals the generated body already uses would be shadowed.
	assert.Equal(t, "requestParam", safeParamName("request"))
	assert.Equal(t, "ctxParam", safeParamName("ctx"))
	// Ordinary names pass through untouched.
	assert.Equal(t, "testId", safeParamName("testId"))
}

func TestUnit_Codegen_GoFieldName_Initialisms(t *testing.T) {
	assert.Equal(t, "TestID", goFieldName("testId"))
	assert.Equal(t, "AID", goFieldName("aid"))
	assert.Equal(t, "TestName", goFieldName("testName"))
}

func TestUnit_Codegen_EndpointExpression_NoParams(t *testing.T) {
	assert.Equal(t, `"/tests/bgp"`, endpointExpression("/tests/bgp", nil))
}

func TestUnit_Codegen_EndpointExpression_WithParams(t *testing.T) {
	params := []Param{{Name: "testId", GoName: "testId"}}
	assert.Equal(t,
		`fmt.Sprintf("/tests/bgp/%s", testId)`,
		endpointExpression("/tests/bgp/{testId}", params))
}

func TestUnit_Codegen_EndpointExpression_KeywordParam(t *testing.T) {
	// The interpolated argument must use the same escaped name as the signature.
	params := []Param{{Name: "type", GoName: "typeParam"}}
	assert.Equal(t,
		`fmt.Sprintf("/things/%s", typeParam)`,
		endpointExpression("/things/{type}", params))
}

func TestUnit_Codegen_ResolveNameCollisions_RenamesModel(t *testing.T) {
	// "Account Groups" yields a service AccountGroups, and the schema
	// Administrative_API_AccountGroups reduces to the same identifier. Both
	// land in one package, so the model is renamed.
	models := []Model{
		{Name: "AccountGroups", Definition: "struct {\n\tItems []AccountGroups `json:\"items\"`\n}"},
		{Name: "AccountGroup", Definition: "struct{}"},
	}
	operations := []Operation{{
		ResultType: "AccountGroups",
		ReturnSig:  "*AccountGroups, *resty.Response, error",
		BodyType:   "*AccountGroups",
		ParamSig:   ", request *AccountGroups",
	}}

	out := resolveNameCollisions("AccountGroups", models, &operations)

	names := []string{out[0].Name, out[1].Name}
	assert.Contains(t, names, "ResourceAccountGroups")
	assert.NotContains(t, names, "AccountGroups")

	// References must be rewritten everywhere, or the package will not compile.
	assert.Contains(t, out[0].Definition, "[]ResourceAccountGroups")
	assert.Equal(t, "ResourceAccountGroups", operations[0].ResultType)
	assert.Equal(t, "*ResourceAccountGroups, *resty.Response, error", operations[0].ReturnSig)
	assert.Equal(t, "*ResourceAccountGroups", operations[0].BodyType)

	// The near-miss singular name must survive untouched.
	assert.Contains(t, names, "AccountGroup")
}

func TestUnit_Codegen_ResolveNameCollisions_NoCollision(t *testing.T) {
	models := []Model{{Name: "BgpTest", Definition: "struct{}"}}
	operations := []Operation{{ResultType: "BgpTest"}}

	out := resolveNameCollisions("BGPTests", models, &operations)

	assert.Equal(t, "BgpTest", out[0].Name)
	assert.Equal(t, "BgpTest", operations[0].ResultType)
}

func TestUnit_Codegen_ReplaceTypeRef_WholeWordsOnly(t *testing.T) {
	// A substring match would corrupt neighbouring type names.
	assert.Equal(t, "[]Foo", replaceTypeRef("[]Bar", "Bar", "Foo"))
	assert.Equal(t, "BarBaz", replaceTypeRef("BarBaz", "Bar", "Foo"))
	assert.Equal(t, "*Foo", replaceTypeRef("*Bar", "Bar", "Foo"))
}

func TestUnit_Codegen_FirstSentence_Truncates(t *testing.T) {
	assert.Equal(t, "One.", firstSentence("One. Two. Three."))
	assert.Equal(t, "No terminator here", firstSentence("No terminator here"))
	assert.Equal(t, "Collapsed whitespace.", firstSentence("Collapsed\n  whitespace. More."))
}

func TestUnit_Codegen_CommentBlock_Wraps(t *testing.T) {
	out := commentBlock("Name ", strings.Repeat("word ", 40))

	for _, line := range strings.Split(out, "\n") {
		assert.True(t, strings.HasPrefix(line, "// "), "every line must be a comment: %q", line)
		assert.LessOrEqual(t, len(line), 82, "line too long: %q", line)
	}
	assert.True(t, strings.HasPrefix(out, "// Name "))
}

func TestUnit_Codegen_Resolve_AllComponentSections(t *testing.T) {
	// Parameters are $ref'd into components/parameters, not components/schemas.
	// Resolving only schemas silently dropped every path parameter.
	spec := &Spec{Components: map[string]map[string]any{
		"schemas":    {"Tests_API_Test": map[string]any{"type": "object"}},
		"parameters": {"Tests_API_TestIdPath": map[string]any{"name": "testId", "in": "path"}},
	}}

	param, name, ok := spec.Resolve("#/components/parameters/Tests_API_TestIdPath")
	require.True(t, ok)
	assert.Equal(t, "Tests_API_TestIdPath", name)
	assert.Equal(t, "testId", param["name"])

	_, _, ok = spec.Resolve("#/components/schemas/Tests_API_Test")
	assert.True(t, ok)

	_, _, ok = spec.Resolve("https://example.com/remote.yaml#/Foo")
	assert.False(t, ok)

	_, _, ok = spec.Resolve("#/components/schemas/Missing")
	assert.False(t, ok)
}

func TestUnit_Codegen_TypeOf_NullableUnion(t *testing.T) {
	// OpenAPI 3.1 style ["string","null"] must resolve to the concrete type.
	assert.Equal(t, "string", typeOf(map[string]any{"type": []any{"string", "null"}}))
	assert.Equal(t, "object", typeOf(map[string]any{"properties": map[string]any{}}))
	assert.Equal(t, "", typeOf(map[string]any{}))
}
