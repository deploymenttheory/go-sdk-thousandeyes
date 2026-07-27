package openapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return raw
}

func TestUnit_Catalogue_Parse_Success(t *testing.T) {
	entries, err := ParseCatalogue(readFixture(t, "config.json"))
	require.NoError(t, err)

	// The duplicate tests.yaml entry must collapse, and index.html must be ignored.
	require.Len(t, entries, 3)

	assert.Equal(t, []string{
		"reference/account-management/admin.yaml",
		"reference/tests/tests.yaml",
		"reference/unified-oas/api.yaml",
	}, Titles(entries))

	admin := entries[0]
	assert.Equal(t, "Administrative API", admin.Title)
	assert.Equal(t, "oas3", admin.Type)
}

func TestUnit_Catalogue_Parse_Errors(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		_, err := ParseCatalogue([]byte("{not json"))
		assert.ErrorContains(t, err, "decoding json")
	})

	t.Run("no yaml entries", func(t *testing.T) {
		_, err := ParseCatalogue([]byte(`{"items":[{"content":"index.html"}]}`))
		assert.ErrorContains(t, err, "format may have changed")
	})
}

func TestUnit_Catalogue_EntryURL_Success(t *testing.T) {
	e := CatalogueEntry{Content: "reference/tests/tests.yaml"}

	// Trailing slashes on the base and leading slashes on the content must not
	// produce a doubled separator.
	assert.Equal(t, "https://example.com/docs/reference/tests/tests.yaml", e.URL("https://example.com/docs"))
	assert.Equal(t, "https://example.com/docs/reference/tests/tests.yaml", e.URL("https://example.com/docs/"))

	withSlash := CatalogueEntry{Content: "/reference/tests/tests.yaml"}
	assert.Equal(t, "https://example.com/docs/reference/tests/tests.yaml", withSlash.URL("https://example.com/docs/"))
}

func TestUnit_Catalogue_Find_Success(t *testing.T) {
	entries, err := ParseCatalogue(readFixture(t, "config.json"))
	require.NoError(t, err)

	found, ok := Find(entries, UnifiedSpecPath)
	require.True(t, ok)
	assert.Equal(t, "ThousandEyes API", found.Title)

	_, ok = Find(entries, "reference/nope.yaml")
	assert.False(t, ok)
}

func TestUnit_Catalogue_Fetch_Success(t *testing.T) {
	fixture := readFixture(t, "config.json")

	var requested string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	entries, err := FetchCatalogue(context.Background(), srv.Client(), srv.URL)
	require.NoError(t, err)

	assert.Equal(t, "/config.json", requested)
	assert.Len(t, entries, 3)
}

func TestUnit_Catalogue_Fetch_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchCatalogue(context.Background(), srv.Client(), srv.URL)
	assert.ErrorContains(t, err, "404")
}
