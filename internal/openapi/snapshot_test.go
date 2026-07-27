package openapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_Snapshot_DirName_Success(t *testing.T) {
	at := time.UnixMilli(1783012345678)

	assert.Equal(t, "7.0.97-t1783012345678", SnapshotDirName("7.0.97", at))
	assert.Equal(t, "unknown-t1783012345678", SnapshotDirName("", at))
}

func writeSnapshot(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, SpecFileName), []byte(body), 0o644))
	return dir
}

func TestUnit_Snapshot_Latest_NoneYet(t *testing.T) {
	// A first run is the normal state, not a failure, so it is reported through a
	// sentinel the caller can match rather than a bare nil.
	t.Run("missing directory", func(t *testing.T) {
		latest, err := LatestSnapshot(filepath.Join(t.TempDir(), "absent"))
		require.ErrorIs(t, err, ErrNoSnapshot)
		assert.Nil(t, latest)
	})

	t.Run("empty directory", func(t *testing.T) {
		latest, err := LatestSnapshot(t.TempDir())
		require.ErrorIs(t, err, ErrNoSnapshot)
		assert.Nil(t, latest)
	})

	t.Run("no usable snapshot", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "notasnapshot"), 0o755))

		latest, err := LatestSnapshot(root)
		require.ErrorIs(t, err, ErrNoSnapshot)
		assert.Nil(t, latest)
	})
}

func TestUnit_Snapshot_Latest_OrdersByEncodedTimestamp(t *testing.T) {
	root := t.TempDir()

	// Written newest-first so that any accidental reliance on filesystem
	// modification time or directory order would pick the wrong one.
	writeSnapshot(t, root, "7.0.99-t3000000000000", "newest")
	writeSnapshot(t, root, "7.0.97-t1000000000000", "oldest")
	writeSnapshot(t, root, "7.0.98-t2000000000000", "middle")

	latest, err := LatestSnapshot(root)
	require.NoError(t, err)
	require.NotNil(t, latest)

	assert.Equal(t, "7.0.99", latest.Version)
	assert.Equal(t, time.UnixMilli(3000000000000), latest.Timestamp)
}

func TestUnit_Snapshot_Latest_SkipsUnusable(t *testing.T) {
	root := t.TempDir()

	writeSnapshot(t, root, "7.0.97-t1000000000000", "valid")
	// Not a snapshot directory at all.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "notasnapshot"), 0o755))
	// Correctly named but has no spec file, so it cannot serve as a baseline.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "7.0.98-t2000000000000"), 0o755))
	// A stray file must not be mistaken for a directory.
	require.NoError(t, os.WriteFile(filepath.Join(root, "7.0.99-t3000000000000"), []byte("x"), 0o644))

	latest, err := LatestSnapshot(root)
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, "7.0.97", latest.Version)
}

func TestUnit_Snapshot_Write_RoundTrip(t *testing.T) {
	root := t.TempDir()
	raw := readFixture(t, "spec_after.yaml")

	spec, err := ParseSpec(UnifiedSpecPath, raw)
	require.NoError(t, err)

	at := time.UnixMilli(1783012345678)
	meta := Metadata{
		Version:             spec.Version,
		SHA256:              spec.SHA256,
		SourcePath:          UnifiedSpecPath,
		SourceURL:           DefaultBaseURL + "/" + UnifiedSpecPath,
		FetchedAt:           at,
		PathCount:           spec.PathCount(),
		OperationCount:      len(spec.Operations),
		CatalogueEntryCount: 27,
		CatalogueEntries:    []string{UnifiedSpecPath},
	}

	dir, err := Write(root, spec, meta, at)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "7.0.97-t1783012345678"), dir)

	// The stored document must be a faithful copy, not a re-serialisation.
	stored, err := os.ReadFile(filepath.Join(dir, SpecFileName))
	require.NoError(t, err)
	assert.Equal(t, raw, stored)

	encoded, err := os.ReadFile(filepath.Join(dir, MetadataFileName))
	require.NoError(t, err)
	var decoded Metadata
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, "7.0.97", decoded.Version)
	assert.Equal(t, spec.SHA256, decoded.SHA256)
	assert.Equal(t, 27, decoded.CatalogueEntryCount)

	latest, err := LatestSnapshot(root)
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, spec.SHA256, latest.SHA256)

	reloaded, err := latest.Load(UnifiedSpecPath)
	require.NoError(t, err)
	assert.Equal(t, spec.SHA256, reloaded.SHA256)
	assert.Equal(t, spec.OperationKeys(), reloaded.OperationKeys())
}

func TestUnit_Snapshot_Write_CreatesRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "openapi-specs")

	spec, err := ParseSpec(UnifiedSpecPath, readFixture(t, "spec_before.yaml"))
	require.NoError(t, err)

	dir, err := Write(root, spec, Metadata{}, time.UnixMilli(1))
	require.NoError(t, err)
	assert.DirExists(t, dir)
}
