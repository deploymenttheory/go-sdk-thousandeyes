package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"resty.dev/v3"
)

// defaultMaxPages bounds a pagination loop so a server that keeps returning a
// next link cannot spin forever. A caller can lower it per request with
// RequestBuilder.SetMaxPages.
const defaultMaxPages = 10_000

// halPage is the envelope ThousandEyes returns from collection endpoints.
//
// It differs from the Jamf Pro shape in two ways that matter here. The payload
// array is named after the resource — "tests", "agents", "labels" — rather than
// a fixed "results" key, so the collection key is supplied by the caller. And
// paging is by opaque cursor followed through _links.next rather than by
// page number, so there is no total count to compute an end from: the last page
// is the one with no next link.
//
// Verified against the live API — GET /v7/tests returns:
//
//	{"tests":[...],"_links":{"self":{"href":"https://api.thousandeyes.com/v7/tests?aid=..."}}}
type halPage struct {
	Links struct {
		Next *struct {
			Href string `json:"href"`
		} `json:"next"`
		Self *struct {
			Href string `json:"href"`
		} `json:"self"`
	} `json:"_links"`
}

// executePaginated implements requestExecutor for Transport.
//
// It fetches every page of a cursor-paginated endpoint, calling mergePage with
// the raw JSON of each page's collection array. Query parameters already set on
// req are sent with the first request; subsequent requests follow the absolute
// href in _links.next, which already carries the cursor and the original
// filters, so they must not be re-applied.
//
// The verb is carried across pages because a collection may be filtered through
// a POST body rather than the query string; that body is replayed on each page.
//
// Endpoints that do not paginate simply return no next link, and the loop ends
// after a single request. That is the common case: only a minority of the v7
// collection endpoints support cursors at all, and some — /agents among them —
// return the entire collection and ignore paging parameters entirely.
func (t *Transport) executePaginated(
	req *resty.Request,
	method string,
	path string,
	collectionKey string,
	maxPages int,
	mergePage func([]byte) error,
) (*resty.Response, error) {
	if method == "" {
		method = "GET"
	}
	if maxPages <= 0 {
		maxPages = defaultMaxPages
	}

	// The service has set filters and Accept on req; carry both onto each page.
	baseParams := make(map[string]string)
	for k, vs := range req.QueryParams {
		if len(vs) > 0 {
			baseParams[k] = vs[0]
		}
	}
	// A POST collection filters through its body, which every page repeats.
	body := req.Body

	templateHeaders := make(map[string]string)
	for k, vs := range req.Header {
		if len(vs) > 0 {
			templateHeaders[k] = vs[0]
		}
	}

	ctx := req.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	var (
		lastResp *resty.Response
		nextURL  string
	)

	for page := 0; page < maxPages; page++ {
		pageReq := t.client.R().
			SetContext(ctx).
			SetResponseBodyUnlimitedReads(true)
		if body != nil {
			pageReq.SetBody(body)
		}
		for k, v := range templateHeaders {
			if v != "" {
				pageReq.SetHeader(k, v)
			}
		}

		target := path
		if page == 0 {
			for k, v := range baseParams {
				if v != "" {
					pageReq.SetQueryParam(k, v)
				}
			}
		} else {
			// The next href is absolute and already carries the cursor and the
			// original query, so it is used verbatim.
			target = nextURL
		}

		resp, err := t.executeRequest(pageReq, method, target)
		lastResp = resp
		if err != nil {
			return lastResp, err
		}

		body := resp.Bytes()

		collection, err := extractCollection(body, collectionKey)
		if err != nil {
			return lastResp, err
		}
		if len(collection) > 0 {
			if err := mergePage(collection); err != nil {
				return lastResp, fmt.Errorf("merge page: %w", err)
			}
		}

		var envelope halPage
		if err := json.Unmarshal(body, &envelope); err != nil {
			// A response that does not carry the HAL envelope cannot be paged
			// beyond the first request; the collection has already been merged.
			return lastResp, nil
		}
		if envelope.Links.Next == nil || envelope.Links.Next.Href == "" {
			return lastResp, nil
		}
		if _, err := url.Parse(envelope.Links.Next.Href); err != nil {
			return lastResp, fmt.Errorf("parsing next link %q: %w", envelope.Links.Next.Href, err)
		}
		nextURL = envelope.Links.Next.Href
	}

	// Stopping at the caller's bound is the requested outcome, not a failure;
	// only the runaway default indicates something is wrong.
	if maxPages < defaultMaxPages {
		return lastResp, nil
	}
	return lastResp, fmt.Errorf("pagination exceeded %d pages for %s", maxPages, path)
}

// extractCollection pulls the named array out of a HAL envelope. A missing key
// yields no items rather than an error, since an endpoint with nothing to
// return may omit the array entirely.
func extractCollection(body []byte, collectionKey string) (json.RawMessage, error) {
	if collectionKey == "" {
		return body, nil
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decoding response envelope: %w", err)
	}

	collection, ok := envelope[collectionKey]
	if !ok {
		return nil, nil
	}
	return collection, nil
}
