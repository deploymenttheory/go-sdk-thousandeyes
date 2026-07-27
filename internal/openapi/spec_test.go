package openapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_Spec_Parse_Success(t *testing.T) {
	raw := readFixture(t, "spec_before.yaml")

	spec, err := ParseSpec("reference/unified-oas/api.yaml", raw)
	require.NoError(t, err)

	assert.Equal(t, "7.0.96", spec.Version)
	assert.Equal(t, "reference/unified-oas/api.yaml", spec.Source)

	sum := sha256.Sum256(raw)
	assert.Equal(t, hex.EncodeToString(sum[:]), spec.SHA256)
	assert.Equal(t, raw, spec.Raw, "raw bytes must be preserved verbatim")

	assert.Equal(t, []string{
		"DELETE /tests/{id}",
		"GET /endpoint/tests/scheduled-tests",
		"GET /legacy/labels",
		"GET /tests",
		"GET /tests/{id}",
	}, spec.OperationKeys())

	assert.Equal(t, 4, spec.PathCount())
}

func TestUnit_Spec_Parse_IgnoresNonOperationKeys(t *testing.T) {
	// "parameters" sits alongside the methods in a path item but is not an
	// operation; counting it would inflate every diff.
	spec, err := ParseSpec("x.yaml", readFixture(t, "spec_before.yaml"))
	require.NoError(t, err)

	for _, key := range spec.OperationKeys() {
		assert.NotContains(t, key, "PARAMETERS")
	}
}

func TestUnit_Spec_Parse_InvalidYAML(t *testing.T) {
	_, err := ParseSpec("x.yaml", []byte("\tnot: [valid"))
	assert.ErrorContains(t, err, "decoding yaml")
}

func TestUnit_Spec_Parse_MissingVersion(t *testing.T) {
	// A document without info.version must still parse; the version is only used
	// for naming, and failing the run over it would be worse than an odd name.
	spec, err := ParseSpec("x.yaml", []byte("openapi: 3.0.1\npaths: {}\n"))
	require.NoError(t, err)
	assert.Empty(t, spec.Version)
	assert.Empty(t, spec.Operations)
}

func TestUnit_Spec_Fetch_Success(t *testing.T) {
	fixture := readFixture(t, "spec_before.yaml")

	var requested string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	spec, err := FetchSpec(context.Background(), srv.Client(), srv.URL, UnifiedSpecPath)
	require.NoError(t, err)

	assert.Equal(t, "/"+UnifiedSpecPath, requested)
	assert.Equal(t, "7.0.96", spec.Version)
}

func TestUnit_Spec_Fetch_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := FetchSpec(context.Background(), srv.Client(), srv.URL, UnifiedSpecPath)
	assert.ErrorContains(t, err, "500")
}
