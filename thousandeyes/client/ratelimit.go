package client

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Rate limit response headers emitted by the ThousandEyes API.
//
// Verified against the live API — GET /v7/tests returns:
//
//	x-organization-rate-limit-limit:     240
//	x-organization-rate-limit-remaining: 239
//	x-organization-rate-limit-reset:     1785153000   (unix seconds)
//
// This is the significant departure from go-sdk-jamfpro-v2, whose throttling is
// inferred purely from response latency because Jamf Pro emits no rate-limit
// headers at all. Here the server tells us the budget directly, so the primary
// signal is the headers; the latency tracker is retained only as a secondary
// signal for endpoints that omit them.
const (
	HeaderRateLimitLimit     = "x-organization-rate-limit-limit"
	HeaderRateLimitRemaining = "x-organization-rate-limit-remaining"
	HeaderRateLimitReset     = "x-organization-rate-limit-reset"
)

// rateLimitReserve is the fraction of the budget below which requests start
// being paced. Above it the client runs at full speed; below it the remaining
// budget is spread across the time left in the window.
const rateLimitReserve = 0.1

// RateLimit is a snapshot of the organization's quota as last reported.
type RateLimit struct {
	// Limit is the total number of requests permitted in the window.
	Limit int
	// Remaining is how many are left.
	Remaining int
	// Reset is when the window rolls over.
	Reset time.Time
	// Known is false until a response carrying the headers has been seen.
	Known bool
}

// rateLimitTracker records the quota reported by the server and derives how
// long to wait before issuing the next request.
type rateLimitTracker struct {
	mu    sync.Mutex
	state RateLimit
}

func newRateLimitTracker() *rateLimitTracker { return &rateLimitTracker{} }

// observe records the rate-limit headers from a response and returns the delay
// to apply before the next request.
//
// While plenty of budget remains the delay is zero. Once the remaining budget
// falls below rateLimitReserve of the limit, the time left in the window is
// divided across the remaining requests so the budget lasts until reset rather
// than being exhausted early and earning a 429.
func (r *rateLimitTracker) observe(header http.Header, now time.Time) time.Duration {
	limit, okLimit := atoiHeader(header, HeaderRateLimitLimit)
	remaining, okRemaining := atoiHeader(header, HeaderRateLimitRemaining)
	resetUnix, okReset := atoiHeader(header, HeaderRateLimitReset)

	if !okLimit || !okRemaining {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.state = RateLimit{Limit: limit, Remaining: remaining, Known: true}
	if okReset {
		r.state.Reset = time.Unix(int64(resetUnix), 0)
	}

	if limit <= 0 || remaining < 0 {
		return 0
	}
	if float64(remaining) > float64(limit)*rateLimitReserve {
		return 0
	}

	// Budget is nearly spent. If the window is about to roll over there is
	// nothing useful to do but wait it out; otherwise pace what is left.
	if !okReset {
		return 0
	}
	untilReset := r.state.Reset.Sub(now)
	if untilReset <= 0 {
		return 0
	}
	if remaining == 0 {
		return untilReset
	}
	return untilReset / time.Duration(remaining)
}

// snapshot returns the last observed quota.
func (r *rateLimitTracker) snapshot() RateLimit {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func atoiHeader(header http.Header, key string) (int, bool) {
	raw := header.Get(key)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return v, true
}

// responseTimeTracker computes an exponential moving average of response
// durations and signals when the caller should pause.
//
// This is the secondary signal. It carries over from go-sdk-jamfpro-v2 for
// endpoints that return no rate-limit headers, where observed latency is the
// only available evidence that the server is under pressure.
type responseTimeTracker struct {
	mu          sync.Mutex
	ema         time.Duration
	alpha       float64 // EMA smoothing factor: 0 < alpha < 1
	initialized bool
}

// newResponseTimeTracker returns a tracker using alpha=0.2, which weights the
// most recent sample at 20%: stable, but still reacting to a sustained
// slowdown within a few samples.
func newResponseTimeTracker() *responseTimeTracker {
	return &responseTimeTracker{alpha: 0.2}
}

// record adds a response duration sample and returns the adaptive delay to
// sleep before the next request.
//
// When a response takes more than twice the current EMA the server is under
// measurable pressure, and the excess over the EMA is returned as the pause,
// capped at adaptiveDelayMax so a single slow response cannot stall the client.
func (r *responseTimeTracker) record(d time.Duration) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.initialized {
		r.ema = d
		r.initialized = true
		return 0
	}

	// EMA update: ema = alpha*current + (1-alpha)*previous
	r.ema = time.Duration(float64(d)*r.alpha + float64(r.ema)*(1-r.alpha))

	if d <= 2*r.ema {
		return 0
	}

	excess := d - r.ema
	if excess > adaptiveDelayMax {
		return adaptiveDelayMax
	}
	return excess
}
