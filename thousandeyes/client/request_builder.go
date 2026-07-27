package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"resty.dev/v3"
)

// MultipartProgressCallback is called during multipart uploads to report progress.
type MultipartProgressCallback func(fieldName string, fileName string, bytesWritten int64, totalBytes int64)

// requestExecutor is the execution backend for a RequestBuilder.
// Transport implements it directly; tests supply a mock via NewMockRequestBuilder.
type requestExecutor interface {
	execute(req *resty.Request, method, path string, result any) (*resty.Response, error)
	executeGetBytes(req *resty.Request, path string) (*resty.Response, []byte, error)
	executePaginated(req *resty.Request, method, path string, collectionKey string, maxPages int, mergePage func([]byte) error) (*resty.Response, error)
}

// RequestBuilder constructs a single API request. Following the same pattern
// as the AWS SDK, the service layer (serialization) owns the full request
// shape — headers, body, query params, result target — before handing the
// completed request to the executor (transport) which handles auth, retry,
// concurrency limiting, and throttling.
//
// Usage:
//
//	resp, err := s.client.NewRequest(ctx).
//	    SetHeader("Accept", constants.ApplicationJSON).
//	    SetHeader("Content-Type", constants.ApplicationJSON).
//	    SetBody(payload).
//	    SetResult(&result).
//	    Post(constants.EndpointFoo)
type RequestBuilder struct {
	req      *resty.Request
	executor requestExecutor
	result   any
	// maxPages bounds a paginated walk. Zero means the package default.
	maxPages int
}

// SetMaxPages bounds how many pages a paginated request will fetch.
//
// Generated read operations paginate to exhaustion by default, which is what
// makes them correct. This is how a caller asks for less: SetMaxPages(1)
// fetches a single page. Values below zero are ignored.
func (b *RequestBuilder) SetMaxPages(pages int) *RequestBuilder {
	if pages > 0 {
		b.maxPages = pages
	}
	return b
}

// SetHeader sets a request-level header. Empty values are ignored.
func (b *RequestBuilder) SetHeader(key, value string) *RequestBuilder {
	if value != "" {
		b.req.SetHeader(key, value)
	}
	return b
}

// SetQueryParam adds a URL query parameter. Empty values are ignored.
func (b *RequestBuilder) SetQueryParam(key, value string) *RequestBuilder {
	if value != "" {
		b.req.SetQueryParam(key, value)
	}
	return b
}

// SetQueryParams adds multiple URL query parameters in bulk. Empty values are ignored.
func (b *RequestBuilder) SetQueryParams(params map[string]string) *RequestBuilder {
	for k, v := range params {
		if v != "" {
			b.req.SetQueryParam(k, v)
		}
	}
	return b
}

// SetBody sets the request body. Nil is ignored.
func (b *RequestBuilder) SetBody(body any) *RequestBuilder {
	if body != nil {
		b.req.SetBody(body)
	}
	return b
}

// SetResult sets the target for JSON unmarshaling of a successful response.
func (b *RequestBuilder) SetResult(result any) *RequestBuilder {
	b.result = result
	b.req.SetResult(result)
	return b
}

// SetMultipartFile configures the request for a multipart file upload.
// Execute with Post after setting any additional form fields or headers.
// Content-Type is managed automatically by resty.
func (b *RequestBuilder) SetMultipartFile(fileField, fileName string, fileReader io.Reader, fileSize int64, callback MultipartProgressCallback) *RequestBuilder {
	if fileReader != nil && fileName != "" && fileField != "" {
		field := &resty.MultipartField{
			Name:        fileField,
			FileName:    fileName,
			ContentType: "application/octet-stream",
			Reader:      fileReader,
			FileSize:    fileSize,
		}
		if callback != nil {
			field.ProgressCallback = func(p resty.MultipartFieldProgress) {
				callback(p.Name, p.FileName, p.Written, p.FileSize)
			}
		}
		b.req.SetMultipartFields(field)
	}
	return b
}

// SetMultipartFormData adds additional form fields to a multipart request.
func (b *RequestBuilder) SetMultipartFormData(formFields map[string]string) *RequestBuilder {
	if len(formFields) > 0 {
		b.req.SetMultipartFormData(formFields)
	}
	return b
}

// Get executes the request as GET against path.
func (b *RequestBuilder) Get(path string) (*resty.Response, error) {
	return b.executor.execute(b.req, "GET", path, b.result)
}

// Post executes the request as POST against path.
func (b *RequestBuilder) Post(path string) (*resty.Response, error) {
	return b.executor.execute(b.req, "POST", path, b.result)
}

// Put executes the request as PUT against path.
func (b *RequestBuilder) Put(path string) (*resty.Response, error) {
	return b.executor.execute(b.req, "PUT", path, b.result)
}

// Patch executes the request as PATCH against path.
func (b *RequestBuilder) Patch(path string) (*resty.Response, error) {
	return b.executor.execute(b.req, "PATCH", path, b.result)
}

// Delete executes the request as DELETE against path.
func (b *RequestBuilder) Delete(path string) (*resty.Response, error) {
	return b.executor.execute(b.req, "DELETE", path, b.result)
}

// GetBytes executes a GET request and returns raw response bytes without JSON
// unmarshaling. Use for binary responses such as certificates, icons, or exports.
func (b *RequestBuilder) GetBytes(path string) (*resty.Response, []byte, error) {
	return b.executor.executeGetBytes(b.req, path)
}

// GetPaginated transparently fetches all pages of a cursor-paginated endpoint,
// calling mergePage with the raw JSON of each page's collection array.
//
// collectionKey names the array within the HAL envelope — "tests", "agents",
// "labels" and so on. ThousandEyes names it after the resource rather than
// using a fixed "results" key, so the caller supplies it.
//
// Query parameters already set on the builder are sent with the first request.
// Later pages follow the absolute href in _links.next, which already carries
// the cursor and the original query.
func (b *RequestBuilder) GetPaginated(
	path string,
	collectionKey string,
	mergePage func([]byte) error,
) (*resty.Response, error) {
	return b.executor.executePaginated(b.req, "GET", path, collectionKey, b.maxPages, mergePage)
}

// PostPaginated is GetPaginated for collections filtered through a request body.
// The body set on the builder is replayed on every page.
//
// The ten /filter endpoints take their criteria as a POST body — too complex for
// a query string — and page through a cursor query parameter, the same shape
// used by Elasticsearch _search and similar search APIs.
//
// UNVERIFIED against a live tenant. What is confirmed by testing
// POST /v7/endpoint/agents/filter:
//
//   - cursor is a recognised, typed parameter: ?cursor=abc returns
//     400 "Failed to deserialize", whereas an unknown parameter such as
//     ?bogusParam=abc is silently ignored and returns 200.
//   - the response envelope differs from the HAL collections: content-type is
//     application/json, and the body carries a totalAgents count alongside the
//     collection and _links.
//
// What is not confirmed:
//
//   - no _links.next has ever been observed from a filter endpoint, because the
//     tenants available for testing return a single empty page. _links came back
//     as {} — without even a self link, which the HAL collections always carry.
//     That the next href appears in the same position for these endpoints is an
//     inference from the specification, not an observation.
//
// The walk itself is the same code path as GetPaginated, so a next link in the
// expected place will be followed. Until a tenant with enough data to force a
// second page is available, treat multi-page behaviour here as untested.
func (b *RequestBuilder) PostPaginated(
	path string,
	collectionKey string,
	mergePage func([]byte) error,
) (*resty.Response, error) {
	return b.executor.executePaginated(b.req, "POST", path, collectionKey, b.maxPages, mergePage)
}

// mockRequestExecutor backs a RequestBuilder in tests, routing execution
// through a caller-supplied dispatch function instead of a real Transport.
type mockRequestExecutor struct {
	fn              func(method, path string, result any) (*resty.Response, error)
	queryParamStore *map[string]string
}

func (m *mockRequestExecutor) execute(req *resty.Request, method, path string, result any) (*resty.Response, error) {
	m.captureQueryParams(req)
	return m.fn(method, path, result)
}

func (m *mockRequestExecutor) executeGetBytes(req *resty.Request, path string) (*resty.Response, []byte, error) {
	m.captureQueryParams(req)
	resp, err := m.fn("GET", path, nil)
	if err != nil {
		return resp, nil, err
	}
	return resp, resp.Bytes(), nil
}

func (m *mockRequestExecutor) executePaginated(req *resty.Request, method, path string, collectionKey string, maxPages int, mergePage func([]byte) error) (*resty.Response, error) {
	m.captureQueryParams(req)
	resp, err := m.fn("GET", path, nil)
	if err != nil {
		return resp, err
	}
	body := resp.Bytes()
	if mergePage != nil && len(body) > 0 {
		var pageResp struct {
			Results json.RawMessage `json:"results"`
		}
		if json.Unmarshal(body, &pageResp) == nil && len(pageResp.Results) > 0 {
			if err := mergePage(pageResp.Results); err != nil {
				return resp, fmt.Errorf("mergePage failed: %w", err)
			}
		} else {
			if err := mergePage(body); err != nil {
				return resp, fmt.Errorf("mergePage failed: %w", err)
			}
		}
	}
	return resp, nil
}

func (m *mockRequestExecutor) captureQueryParams(req *resty.Request) {
	if m.queryParamStore != nil && req != nil {
		params := make(map[string]string)
		for k, v := range req.QueryParams {
			if len(v) > 0 {
				params[k] = v[0]
			}
		}
		if len(params) > 0 {
			*m.queryParamStore = params
		}
	}
}

// NewMockRequestBuilder returns a RequestBuilder suitable for unit tests.
// The fn callback receives the HTTP method, path, and result pointer and
// returns a pre-programmed response — identical to how existing mock
// dispatch functions work.
func NewMockRequestBuilder(ctx context.Context, fn func(method, path string, result any) (*resty.Response, error)) *RequestBuilder {
	return &RequestBuilder{
		req:      resty.New().R().SetContext(ctx),
		executor: &mockRequestExecutor{fn: fn, queryParamStore: nil},
	}
}

// NewMockRequestBuilderWithQueryCapture returns a RequestBuilder suitable for unit tests
// that also captures query parameters into the provided map pointer.
func NewMockRequestBuilderWithQueryCapture(ctx context.Context, fn func(method, path string, result any) (*resty.Response, error), queryStore *map[string]string) *RequestBuilder {
	return &RequestBuilder{
		req:      resty.New().R().SetContext(ctx),
		executor: &mockRequestExecutor{fn: fn, queryParamStore: queryStore},
	}
}
