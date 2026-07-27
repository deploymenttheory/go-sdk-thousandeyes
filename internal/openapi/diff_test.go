package openapi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func specPair(t *testing.T) (before, after *Spec) {
	t.Helper()
	var err error
	before, err = ParseSpec(UnifiedSpecPath, readFixture(t, "spec_before.yaml"))
	require.NoError(t, err)
	after, err = ParseSpec(UnifiedSpecPath, readFixture(t, "spec_after.yaml"))
	require.NoError(t, err)
	return before, after
}

func TestUnit_Diff_Compare_Success(t *testing.T) {
	before, after := specPair(t)

	d := Compare(before, after, Titles(nil), nil)

	assert.False(t, d.FirstSnapshot)
	assert.Equal(t, "7.0.96", d.PreviousVersion)
	assert.Equal(t, "7.0.97", d.CurrentVersion)
	assert.True(t, d.VersionChanged())
	assert.True(t, d.Changed())

	assert.Equal(t, []string{
		"PATCH /endpoint/tests/scheduled-tests/{id}",
		"POST /endpoint/tests/scheduled-tests",
	}, d.AddedOperations)

	assert.Equal(t, []string{"GET /legacy/labels"}, d.RemovedOperations)

	// The summary of GET /tests changed, which must register as modified rather
	// than as an add plus a remove.
	assert.Equal(t, []string{"GET /tests"}, d.ModifiedOperations)
}

func TestUnit_Diff_Compare_FirstSnapshot(t *testing.T) {
	_, after := specPair(t)

	d := Compare(nil, after, nil, []string{"reference/unified-oas/api.yaml"})

	assert.True(t, d.FirstSnapshot)
	assert.True(t, d.Changed())
	assert.Empty(t, d.PreviousVersion)
	assert.Len(t, d.AddedOperations, len(after.Operations))
	assert.Empty(t, d.RemovedOperations)

	// With no previous catalogue there is nothing to compare, so entries must not
	// all be reported as newly appeared.
	assert.Empty(t, d.AddedCatalogueEntries)
}

func TestUnit_Diff_Compare_Identical(t *testing.T) {
	before, _ := specPair(t)
	same, err := ParseSpec(UnifiedSpecPath, readFixture(t, "spec_before.yaml"))
	require.NoError(t, err)

	d := Compare(before, same, []string{"a.yaml"}, []string{"a.yaml"})

	assert.False(t, d.Changed())
	assert.False(t, d.VersionChanged())
	assert.Empty(t, d.AddedOperations)
	assert.Empty(t, d.RemovedOperations)
	assert.Empty(t, d.ModifiedOperations)
}

func TestUnit_Diff_Compare_CatalogueChanges(t *testing.T) {
	before, after := specPair(t)

	d := Compare(before, after,
		[]string{"a.yaml", "gone.yaml"},
		[]string{"a.yaml", "new.yaml"},
	)

	assert.Equal(t, []string{"new.yaml"}, d.AddedCatalogueEntries)
	assert.Equal(t, []string{"gone.yaml"}, d.RemovedCatalogueEntries)
}

func TestUnit_Diff_Title_Success(t *testing.T) {
	assert.Contains(t, Diff{FirstSnapshot: true, CurrentVersion: "7.0.97"}.Title(), "add ThousandEyes API specs for 7.0.97")
	assert.Contains(t, Diff{PreviousVersion: "7.0.96", CurrentVersion: "7.0.97"}.Title(), "update ThousandEyes API specs to 7.0.97")
	assert.Contains(t, Diff{PreviousVersion: "7.0.97", CurrentVersion: "7.0.97"}.Title(), "refresh")
}

func TestUnit_Diff_Body_Success(t *testing.T) {
	before, after := specPair(t)
	d := Compare(before, after, nil, nil)

	body := d.Body("https://example.com/api.yaml", after.PathCount(), len(after.Operations), 27)

	assert.Contains(t, body, "Version `7.0.96` → `7.0.97`")
	assert.Contains(t, body, "https://example.com/api.yaml")
	assert.Contains(t, body, d.CurrentSHA256)
	assert.Contains(t, body, "Operations added (2)")
	assert.Contains(t, body, "`POST /endpoint/tests/scheduled-tests`")
	assert.Contains(t, body, "Operations removed (1)")
	assert.Contains(t, body, "Operations modified (1)")
	assert.Contains(t, body, "| Catalogue entries | 27 |")
}

func TestUnit_Diff_Body_ContentOnlyChange(t *testing.T) {
	// Same operations either side, different checksum: the body must say so
	// rather than rendering a set of empty sections.
	d := Diff{
		PreviousVersion: "7.0.97",
		CurrentVersion:  "7.0.97",
		PreviousSHA256:  "aaa",
		CurrentSHA256:   "bbb",
	}

	body := d.Body("https://example.com/api.yaml", 10, 20, 27)

	assert.Contains(t, body, "republished with different content")
	assert.Contains(t, body, "No operation-level changes detected")
}

func TestUnit_Diff_Body_TruncatesLongLists(t *testing.T) {
	many := make([]string, maxListed+10)
	for i := range many {
		many[i] = "GET /path"
	}
	d := Diff{CurrentVersion: "7.0.97", PreviousVersion: "7.0.96", AddedOperations: many}

	body := d.Body("u", 1, 1, 1)

	assert.Contains(t, body, "and 10 more")
	assert.Equal(t, maxListed+1, strings.Count(body, "\n- "))
}

func TestUnit_Diff_Summary_Success(t *testing.T) {
	before, after := specPair(t)
	assert.Equal(t, "7.0.97: +2/-1/~1 operations", Compare(before, after, nil, nil).Summary())
	assert.Contains(t, Compare(nil, after, nil, nil).Summary(), "first snapshot at 7.0.97")
}
