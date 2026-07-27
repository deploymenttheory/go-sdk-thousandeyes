package client

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// headers builds a response header set carrying the organization quota, in the
// form the live API emits it.
func headers(limit, remaining string, reset time.Time) http.Header {
	h := http.Header{}
	if limit != "" {
		h.Set(HeaderRateLimitLimit, limit)
	}
	if remaining != "" {
		h.Set(HeaderRateLimitRemaining, remaining)
	}
	if !reset.IsZero() {
		h.Set(HeaderRateLimitReset, strconvItoa(reset.Unix()))
	}
	return h
}

func strconvItoa(v int64) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	var buf []byte
	for v > 0 {
		buf = append([]byte{digits[v%10]}, buf...)
		v /= 10
	}
	return string(buf)
}

func TestUnit_RateLimit_Observe_PlentyRemaining(t *testing.T) {
	now := time.Unix(1785153000, 0)
	tracker := newRateLimitTracker()

	// The live API answered 240/239 on a first call; well above the reserve, so
	// no pacing should be applied.
	delay := tracker.observe(headers("240", "239", now.Add(time.Minute)), now)

	assert.Zero(t, delay)
	snap := tracker.snapshot()
	assert.True(t, snap.Known)
	assert.Equal(t, 240, snap.Limit)
	assert.Equal(t, 239, snap.Remaining)
}

func TestUnit_RateLimit_Observe_PacesNearExhaustion(t *testing.T) {
	now := time.Unix(1785153000, 0)
	tracker := newRateLimitTracker()

	// 10 of 240 left with 60s to go: the budget is spread across the window
	// rather than spent immediately.
	delay := tracker.observe(headers("240", "10", now.Add(60*time.Second)), now)

	assert.Equal(t, 6*time.Second, delay)
}

func TestUnit_RateLimit_Observe_Exhausted(t *testing.T) {
	now := time.Unix(1785153000, 0)
	tracker := newRateLimitTracker()

	// Nothing left: wait out the remainder of the window.
	delay := tracker.observe(headers("240", "0", now.Add(30*time.Second)), now)

	assert.Equal(t, 30*time.Second, delay)
}

func TestUnit_RateLimit_Observe_ResetInThePast(t *testing.T) {
	now := time.Unix(1785153000, 0)
	tracker := newRateLimitTracker()

	// The window has already rolled over, so there is nothing to wait for.
	delay := tracker.observe(headers("240", "0", now.Add(-time.Second)), now)

	assert.Zero(t, delay)
}

func TestUnit_RateLimit_Observe_NoHeaders(t *testing.T) {
	tracker := newRateLimitTracker()

	// Endpoints that report no quota must not be paced, and must leave the
	// snapshot unknown so callers can tell the difference.
	delay := tracker.observe(http.Header{}, time.Now())

	assert.Zero(t, delay)
	assert.False(t, tracker.snapshot().Known)
}

func TestUnit_RateLimit_Observe_MalformedHeaders(t *testing.T) {
	tracker := newRateLimitTracker()

	delay := tracker.observe(headers("not-a-number", "10", time.Time{}), time.Now())

	assert.Zero(t, delay)
	assert.False(t, tracker.snapshot().Known)
}

func TestUnit_RateLimit_Observe_NoResetHeader(t *testing.T) {
	tracker := newRateLimitTracker()

	// Without a reset time there is no window to pace across, even when the
	// budget is low. The quota is still recorded.
	delay := tracker.observe(headers("240", "1", time.Time{}), time.Now())

	assert.Zero(t, delay)
	assert.True(t, tracker.snapshot().Known)
}

func TestUnit_ResponseTime_Record_Baseline(t *testing.T) {
	tracker := newResponseTimeTracker()

	// The first sample establishes the baseline and cannot be slow relative to it.
	assert.Zero(t, tracker.record(100*time.Millisecond))
	// Steady responses stay unthrottled.
	assert.Zero(t, tracker.record(100*time.Millisecond))
}

func TestUnit_ResponseTime_Record_SlowdownPauses(t *testing.T) {
	tracker := newResponseTimeTracker()
	tracker.record(10 * time.Millisecond)

	// An order-of-magnitude slowdown is evidence of server pressure.
	delay := tracker.record(2 * time.Second)

	assert.Positive(t, delay)
	assert.LessOrEqual(t, delay, adaptiveDelayMax)
}

func TestUnit_ResponseTime_Record_CapsDelay(t *testing.T) {
	tracker := newResponseTimeTracker()
	tracker.record(time.Millisecond)

	// A single pathological response must not stall the client indefinitely.
	assert.Equal(t, adaptiveDelayMax, tracker.record(time.Hour))
}
