package client

import "strconv"

// RequestOption customises a request before it is executed.
//
// Generated operations take their required inputs — path parameters and the
// request body — as positional arguments, and everything else through options.
// The v7 API attaches a long tail of optional query parameters to most
// endpoints (aid, window, startDate, endDate, max, cursor, expand, ...), and
// threading those through the signature would make every call site unreadable
// and every added parameter a breaking change.
//
//	tests, _, err := svc.GetTests(ctx,
//	    client.WithAccountGroupID("281474976742769"),
//	    client.WithQueryParam("window", "12h"),
//	)
type RequestOption func(*RequestBuilder)

// WithQueryParam sets a single query parameter. Empty values are ignored.
func WithQueryParam(key, value string) RequestOption {
	return func(b *RequestBuilder) { b.SetQueryParam(key, value) }
}

// WithQueryParams sets several query parameters. Empty values are ignored.
func WithQueryParams(params map[string]string) RequestOption {
	return func(b *RequestBuilder) { b.SetQueryParams(params) }
}

// WithQueryParamInt sets an integer query parameter, such as max.
func WithQueryParamInt(key string, value int) RequestOption {
	return func(b *RequestBuilder) { b.SetQueryParam(key, strconv.Itoa(value)) }
}

// WithHeader sets a request header, overriding the generated default.
func WithHeader(key, value string) RequestOption {
	return func(b *RequestBuilder) { b.SetHeader(key, value) }
}

// WithAccountGroupID overrides the client's account group for one call by
// setting the aid query parameter. Use to reach an account group other than the
// one the client was configured with, provided the token owner is assigned to it.
func WithAccountGroupID(accountGroupID string) RequestOption {
	return func(b *RequestBuilder) { b.SetQueryParam("aid", accountGroupID) }
}

// WithCursor sets the pagination cursor on endpoints that support it.
func WithCursor(cursor string) RequestOption {
	return func(b *RequestBuilder) { b.SetQueryParam("cursor", cursor) }
}

// WithMax sets the page size on endpoints that support pagination. Note that
// several collection endpoints — /agents among them — ignore it and return the
// entire collection.
func WithMax(max int) RequestOption {
	return func(b *RequestBuilder) { b.SetQueryParam("max", strconv.Itoa(max)) }
}

// WithWindow sets the window query parameter used by the time-series endpoints,
// expressed as a duration such as "12h" or "7d".
func WithWindow(window string) RequestOption {
	return func(b *RequestBuilder) { b.SetQueryParam("window", window) }
}
