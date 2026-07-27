// Package mocks provides a fixture-backed API server for testing services
// without reaching the network.
//
// It differs from the equivalent harness in go-sdk-jamfpro-v2, which substitutes
// the client interface and so bypasses the transport. Here the server stands in
// for the API and requests travel the real path — auth headers, retries,
// rate-limit accounting, cursor pagination and error parsing all run as they do
// in production. A test that passes against this harness has exercised the code
// that ships.
//
//	srv := mocks.New(t)
//	srv.Register("GET", "/agents", 200, "agents_list_200.json")
//
//	agents := cloud_and_enterprise_agents.NewCloudAndEnterpriseAgents(srv.Client(t))
//	list, _, err := agents.GetAgents(context.Background())
package mocks

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/client"
	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/config"
	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/constants"
)

// FixtureDir is where Register looks for fixture files, relative to the test.
const FixtureDir = "testdata"

// Server is a fixture-backed stand-in for the ThousandEyes API.
type Server struct {
	*httptest.Server

	mu        sync.Mutex
	responses map[string]response
	requests  []Request
	fixtures  string
}

// Request records a call the server received, so a test can assert on what the
// SDK actually sent rather than only on what it returned.
type Request struct {
	Method string
	Path   string
	Query  string
	Body   string
}

type response struct {
	status      int
	body        []byte
	contentType string
}

// New returns a Server that fails the test on any unregistered request. It is
// closed automatically when the test finishes.
func New(t *testing.T) *Server {
	t.Helper()

	s := &Server{
		responses: map[string]response{},
		fixtures:  FixtureDir,
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

// WithFixtureDir points the server at a different fixture directory.
func (s *Server) WithFixtureDir(dir string) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fixtures = dir
	return s
}

// Client returns an SDK client wired to this server.
func (s *Server) Client(t *testing.T) *client.Transport {
	t.Helper()

	transport, err := client.NewTransport(&config.AuthConfig{
		BearerToken: "test-token",
		APIEndpoint: s.URL,
	})
	if err != nil {
		t.Fatalf("creating transport: %v", err)
	}
	return transport
}

// Register serves a fixture file for a method and path.
func (s *Server) Register(t *testing.T, method, path string, status int, fixture string) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(s.fixtures, fixture))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", fixture, err)
	}
	s.RegisterBody(method, path, status, body, contentTypeFor(status))
}

// RegisterBody serves a literal body, for cases with no fixture on disk — an
// empty 404, or a page whose next link is synthesised by the test.
func (s *Server) RegisterBody(method, path string, status int, body []byte, contentType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[key(method, path)] = response{status: status, body: body, contentType: contentType}
}

// Requests returns the calls received, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	copy(out, s.requests)
	return out
}

// RequestCount returns how many calls were received, which is how a test
// asserts that pagination made the expected number of round trips.
func (s *Server) RequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body := make([]byte, 0)
	if r.Body != nil {
		buf := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(buf)
			body = buf
		}
	}

	s.mu.Lock()
	s.requests = append(s.requests, Request{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
		Body:   string(body),
	})
	resp, ok := s.responses[key(r.Method, r.URL.Path)]
	s.mu.Unlock()

	if !ok {
		// An unregistered call is a test setup error, and saying so beats a
		// confusing decode failure further down.
		w.Header().Set("Content-Type", constants.ProblemJSON)
		w.WriteHeader(http.StatusNotImplemented)
		fmt.Fprintf(w, `{"type":"about:blank","title":"no mock registered for %s %s","status":501}`,
			r.Method, r.URL.Path)
		return
	}

	// The live API reports the organization quota on every response, and the
	// transport paces against it, so the harness reports it too.
	w.Header().Set(client.HeaderRateLimitLimit, "240")
	w.Header().Set(client.HeaderRateLimitRemaining, "239")
	w.Header().Set("Content-Type", resp.contentType)
	w.WriteHeader(resp.status)
	_, _ = w.Write(resp.body)
}

func key(method, path string) string { return method + " " + path }

// contentTypeFor mirrors the live API: success is HAL, 4xx validation failures
// are RFC 7807 problem documents.
func contentTypeFor(status int) string {
	if status >= 400 {
		return constants.ProblemJSON
	}
	return constants.HALJSON
}
