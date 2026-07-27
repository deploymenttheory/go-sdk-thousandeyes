package generatedtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/client"
	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/config"
	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/thousandeyes_api/alerts"
)

// GetAlerts is one of the 37 operations that accept a cursor and so paginate.
// The envelope shape is the live GET /v7/alerts response; the items are
// synthetic because the lab tenant has no alerts. See testdata/README.md.
func loadPage(t *testing.T, name, nextHref string) []byte {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(raw, &envelope))

	links, _ := envelope["_links"].(map[string]any)
	if links == nil {
		links = map[string]any{}
	}
	if nextHref == "" {
		delete(links, "next")
	} else {
		links["next"] = map[string]any{"href": nextHref}
	}
	envelope["_links"] = links

	out, err := json.Marshal(envelope)
	require.NoError(t, err)
	return out
}

// newAlerts wires a service at srv, which stands in for the API.
func newAlerts(t *testing.T, srv *httptest.Server) *alerts.Alerts {
	t.Helper()

	transport, err := client.NewTransport(&config.AuthConfig{
		BearerToken: "test-token",
		APIEndpoint: srv.URL,
	})
	require.NoError(t, err)

	return alerts.NewAlerts(transport)
}

// alertIDs reads the ids out of a merged collection.
func alertIDs(list *alerts.ResourceAlerts) []string {
	out := make([]string, 0, len(list.Alerts))
	for _, a := range list.Alerts {
		if a.ID != nil {
			out = append(out, *a.ID)
		}
	}
	return out
}

func TestUnit_Pagination_FollowsNextAndMergesPages(t *testing.T) {
	var requests int32

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/hal+json")

		if n == 1 {
			// The second page is reached through an absolute href, exactly as
			// the API serves it.
			_, _ = w.Write(loadPage(t, "alerts_page1.json", srv.URL+"/alerts?cursor=page2"))
			return
		}
		_, _ = w.Write(loadPage(t, "alerts_page2.json", ""))
	}))
	defer srv.Close()

	list, resp, err := newAlerts(t, srv).GetAlerts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode())

	// Both pages merged. Before this change the call returned page one and
	// silently dropped the rest.
	require.Len(t, list.Alerts, 2)
	assert.EqualValues(t, int32(2), atomic.LoadInt32(&requests))

	assert.ElementsMatch(t, []string{"alert-1", "alert-2"}, alertIDs(list),
		"both pages must contribute, not just the first")
}

func TestUnit_Pagination_StopsWhenNextLinkIsAbsent(t *testing.T) {
	var requests int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/hal+json")
		_, _ = w.Write(loadPage(t, "alerts_page1.json", ""))
	}))
	defer srv.Close()

	list, _, err := newAlerts(t, srv).GetAlerts(context.Background())
	require.NoError(t, err)

	// A last page carries no next link, so the walk must stop there rather than
	// requesting again.
	assert.Len(t, list.Alerts, 1)
	assert.EqualValues(t, int32(1), atomic.LoadInt32(&requests))
}

func TestUnit_Pagination_WithMaxPagesBoundsTheWalk(t *testing.T) {
	var requests int32

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/hal+json")
		// Always another page: without a bound this would not terminate until
		// the package default of 10,000.
		_, _ = w.Write(loadPage(t, "alerts_page1.json", srv.URL+"/alerts?cursor=more"))
	}))
	defer srv.Close()

	list, _, err := newAlerts(t, srv).GetAlerts(context.Background(), client.WithMaxPages(3))
	require.NoError(t, err, "stopping at the caller's bound is the requested outcome, not a failure")

	assert.Len(t, list.Alerts, 3)
	assert.EqualValues(t, int32(3), atomic.LoadInt32(&requests))
}

func TestUnit_Pagination_SinglePageViaMaxPages(t *testing.T) {
	var requests int32

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/hal+json")
		_, _ = w.Write(loadPage(t, "alerts_page1.json", srv.URL+"/alerts?cursor=more"))
	}))
	defer srv.Close()

	// WithMax sets the page size; WithMaxPages(1) is how a caller asks for one
	// page now that pagination is automatic.
	list, _, err := newAlerts(t, srv).GetAlerts(context.Background(), client.WithMaxPages(1))
	require.NoError(t, err)

	assert.Len(t, list.Alerts, 1)
	assert.EqualValues(t, int32(1), atomic.LoadInt32(&requests))
}

func TestUnit_Pagination_PropagatesPageError(t *testing.T) {
	var requests int32

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&requests, 1) == 1 {
			w.Header().Set("Content-Type", "application/hal+json")
			_, _ = w.Write(loadPage(t, "alerts_page1.json", srv.URL+"/alerts?cursor=page2"))
			return
		}
		// A failure part-way through must surface rather than returning a
		// partial collection as though it were complete.
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, _, err := newAlerts(t, srv).GetAlerts(context.Background())
	require.Error(t, err)
	assert.True(t, client.IsServerError(err), "expected a 5xx APIError, got %v", err)
}
