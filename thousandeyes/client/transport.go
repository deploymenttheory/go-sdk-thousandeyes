package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/config"
	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/constants"
	"go.uber.org/zap"
	"resty.dev/v3"
)

// Transport is the HTTP transport layer for the ThousandEyes API.
// It wraps a resty.Client with bearer token auth, idempotent-only retries with
// exponential backoff, rate-limit-aware throttling, optional concurrency
// limiting, and structured logging.
type Transport struct {
	client        *resty.Client
	logger        *zap.Logger
	authConfig    *config.AuthConfig
	BaseURL       string
	globalHeaders map[string]string
	userAgent     string

	// Optional throttles — nil / zero means disabled.
	sem                *semaphore
	requestDelay       time.Duration
	totalRetryDuration time.Duration

	// responseTracker measures per-request latency and derives an adaptive
	// inter-request delay when the server begins responding slowly.
	responseTracker *responseTimeTracker

	// rateLimiter records the organization quota the API reports on every
	// response and derives a pacing delay from it.
	rateLimiter *rateLimitTracker
}

// GetHTTPClient returns the underlying resty client for advanced use.
func (t *Transport) GetHTTPClient() *resty.Client {
	return t.client
}

// GetLogger returns the configured logger.
func (t *Transport) GetLogger() *zap.Logger {
	return t.logger
}

// NewTransport creates and fully configures a Jamf Pro API transport.
//
// Behaviour applied at construction time (resty native where possible):
//   - Bearer token authentication with automatic refresh
//   - Idempotent-only retry (GET/PUT/DELETE) with exponential backoff
//   - Sticky-session cookie jar (handles jpro-ingress, APBALANCEID, JSESSIONID)
//   - Deprecation header warning logged on every response
//   - Adaptive inter-request delay derived from response-time EMA tracking
//
// Jamf Pro does not emit rate-limit HTTP headers. Throttling is inferred
// from observed response times per Jamf scalability best practices.
func NewTransport(authConfig *config.AuthConfig, opts ...ClientOption) (*Transport, error) {
	if authConfig == nil {
		return nil, fmt.Errorf("auth config is required")
	}

	// Collect all caller-supplied options into a settings struct.
	// Zero values signal "use the built-in default".
	settings := &TransportSettings{
		GlobalHeaders: make(map[string]string),
	}
	for _, opt := range opts {
		if err := opt(settings); err != nil {
			return nil, fmt.Errorf("failed to apply client option: %w", err)
		}
	}

	// Logger: caller-supplied or production default.
	logger := settings.Logger
	if logger == nil {
		var err error
		logger, err = zap.NewProduction()
		if err != nil {
			return nil, fmt.Errorf("failed to create logger: %w", err)
		}
	}

	// BaseURL: option overrides authConfig.InstanceDomain.
	baseURL := settings.BaseURL
	if baseURL == "" {
		baseURL = authConfig.Endpoint()
	}
	if baseURL == "" {
		return nil, fmt.Errorf("instance domain is required")
	}
	baseURL = trimTrailingSlash(baseURL)

	// UserAgent: option overrides SDK default.
	userAgent := settings.UserAgent
	if userAgent == "" {
		userAgent = fmt.Sprintf("%s/%s", UserAgentBase, constants.Version)
	}
	// Timeouts/retries: option value if non-zero, else SDK default.
	timeout := settings.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	retryCount := settings.RetryCount
	if retryCount == 0 {
		retryCount = MaxRetries
	}
	retryWait := settings.RetryWaitTime
	if retryWait == 0 {
		retryWait = RetryWaitTime
	}
	retryMaxWait := settings.RetryMaxWaitTime
	if retryMaxWait == 0 {
		retryMaxWait = RetryMaxWaitTime
	}

	// Resty creates a cookie jar by default, which enables sticky sessions automatically.
	// Jamf Cloud sets jpro-ingress / APBALANCEID / JSESSIONID in Set-Cookie
	// headers; resty resends them on subsequent requests without extra logic.
	// See: https://developer.jamf.com/jamf-pro/docs/sticky-sessions-for-jamf-cloud
	restyClient := resty.New()
	restyClient.SetBaseURL(baseURL)
	restyClient.SetTimeout(timeout)
	restyClient.SetRetryCount(retryCount)
	restyClient.SetRetryWaitTime(retryWait)
	restyClient.SetRetryMaxWaitTime(retryMaxWait)
	restyClient.SetHeader("User-Agent", userAgent)

	// Only retry idempotent methods on transient server errors.
	// Resty's built-in exponential backoff handles the wait between retries.
	// See: https://developer.jamf.com/jamf-pro/docs/jamf-pro-api-scalability-best-practices
	restyClient.AddRetryConditions(retryCondition)

	if settings.Debug {
		restyClient.SetDebug(true)
	}

	// TLS: InsecureSkipVerify takes precedence over a custom TLSClientConfig.
	if settings.InsecureSkipVerify {
		restyClient.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec
	} else if settings.TLSClientConfig != nil {
		restyClient.SetTLSClientConfig(settings.TLSClientConfig)
	}

	if settings.ProxyURL != "" {
		restyClient.SetProxy(settings.ProxyURL)
	}
	if settings.HTTPTransport != nil {
		restyClient.SetTransport(settings.HTTPTransport)
	}
	for k, v := range settings.GlobalHeaders {
		restyClient.SetHeader(k, v)
	}

	// Build optional concurrency semaphore.
	var sem *semaphore
	if settings.MaxConcurrentRequests > 0 {
		sem = newSemaphore(settings.MaxConcurrentRequests)
	}

	transport := &Transport{
		client:             restyClient,
		logger:             logger,
		authConfig:         authConfig,
		BaseURL:            baseURL,
		globalHeaders:      settings.GlobalHeaders,
		userAgent:          userAgent,
		responseTracker:    newResponseTimeTracker(),
		rateLimiter:        newRateLimitTracker(),
		sem:                sem,
		requestDelay:       settings.MandatoryRequestDelay,
		totalRetryDuration: settings.TotalRetryDuration,
	}

	// Log deprecated endpoint warnings and cookie usage via resty response middleware.
	restyClient.AddResponseMiddleware(func(_ *resty.Client, r *resty.Response) error {
		if dep := r.Header().Get("Deprecation"); dep != "" {
			transport.logger.Warn("ThousandEyes API endpoint is deprecated",
				zap.String("endpoint", r.Request.URL),
				zap.String("deprecation", dep),
				zap.String("sunset", r.Header().Get("Sunset")),
			)
		}

		if r.Request != nil && r.Request.Header != nil {
			cookieHeader := r.Request.Header.Get("Cookie")
			transport.logger.Info("Request cookie status",
				zap.String("method", r.Request.Method),
				zap.String("path", r.Request.URL),
				zap.String("cookie_sent", cookieHeader),
				zap.Bool("has_cookie", cookieHeader != ""),
			)
		}

		return nil
	})

	// A ThousandEyes token is a long-lived credential presented as-is, so
	// authentication is a single header rather than a token exchange.
	restyClient.SetAuthToken(authConfig.BearerToken)

	// Apply OpenTelemetry instrumentation (always enabled, uses global providers).
	// If no global providers are configured, this is a no-op.
	// This must happen AFTER construction is complete.
	transport.applyOpenTelemetry()

	logger.Info("ThousandEyes API transport created",
		zap.String("base_url", transport.BaseURL),
		zap.String("account_group_id", authConfig.AccountGroupID),
	)
	return transport, nil
}

// trimTrailingSlash removes trailing slashes from a string.
// This is used to ensure that the base URL is correctly formatted.
func trimTrailingSlash(s string) string {
	if len(s) > 0 && s[len(s)-1] == '/' {
		return s[:len(s)-1]
	}
	return s
}

// NewRequest returns a RequestBuilder for this transport. The service layer
// uses it to construct the full request — headers, body, query params, result
// target — before calling Get/Post/Put/Patch/Delete to execute it. Auth,
// retry, concurrency limiting, and throttling are applied by the transport.
func (t *Transport) NewRequest(ctx context.Context) *RequestBuilder {
	return &RequestBuilder{
		req:      t.client.R().SetContext(ctx).SetResponseBodyUnlimitedReads(true),
		executor: t,
	}
}

// execute implements requestExecutor for Transport.
func (t *Transport) execute(req *resty.Request, method, path string, _ any) (*resty.Response, error) {
	return t.executeRequest(req, method, path)
}

// executeGetBytes implements requestExecutor for Transport.
// Returns raw response bytes without JSON unmarshaling, going through the
// full executeRequest path for retry, throttling, and concurrency limiting.
func (t *Transport) executeGetBytes(req *resty.Request, path string) (*resty.Response, []byte, error) {
	resp, err := t.executeRequest(req, "GET", path)
	if err != nil {
		return resp, nil, err
	}
	return resp, resp.Bytes(), nil
}

// executeRequest is the central request executor used by all HTTP verb methods.
// It applies the concurrency semaphore, total-retry deadline, mandatory
// per-request delay, and adaptive response-time throttling.
func (t *Transport) executeRequest(req *resty.Request, method, path string) (*resty.Response, error) {
	ctx := req.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Wrap in a deadline for the total allowed retry window if configured and
	// the caller has not already set a more restrictive deadline.
	if t.totalRetryDuration > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, t.totalRetryDuration)
			defer cancel()
			req.SetContext(ctx)
		}
	}

	// Acquire concurrency slot — blocks until available or context cancelled.
	if t.sem != nil {
		if err := t.sem.acquire(ctx); err != nil {
			return nil, fmt.Errorf("concurrency limit: %w", err)
		}
		defer t.sem.release()
	}

	t.logger.Debug("Executing API request", zap.String("method", method), zap.String("path", path))

	resp, execErr := req.Execute(method, path)

	if execErr != nil {
		t.logger.Error("Request failed",
			zap.String("method", method),
			zap.String("path", path),
			zap.Error(execErr),
		)
		return resp, fmt.Errorf("request failed: %w", execErr)
	}

	if err := t.validateResponse(resp, method, path); err != nil {
		return resp, err
	}

	if resp.IsStatusFailure() {
		return resp, ParseErrorResponse(
			[]byte(resp.String()),
			resp.StatusCode(),
			resp.Status(),
			method,
			path,
			t.logger,
		)
	}

	duration := resp.Duration()

	var stickySessionCookie string
	var allCookies []string
	if resp.RawResponse != nil {
		for _, cookie := range resp.Cookies() {
			allCookies = append(allCookies, cookie.Name)
			if cookie.Name == "jpro-ingress" || cookie.Name == "APBALANCEID" || cookie.Name == "JSESSIONID" {
				stickySessionCookie = fmt.Sprintf("%s=%s", cookie.Name, cookie.Value)
			}
		}
	}

	t.logger.Info("Request completed",
		zap.String("method", method),
		zap.String("path", path),
		zap.Int("status_code", resp.StatusCode()),
		zap.Duration("duration", duration),
		zap.String("sticky_session_cookie", stickySessionCookie),
		zap.Strings("all_response_cookies", allCookies),
	)

	// Mandatory fixed delay (user-configured for bulk operations).
	if t.requestDelay > 0 {
		time.Sleep(t.requestDelay)
	}

	// Primary throttle: the API reports the organization's quota on every
	// response, so pace against the budget it gives us rather than inferring
	// pressure from latency. Only bites once the budget is nearly spent.
	if resp != nil {
		if pace := t.rateLimiter.observe(resp.Header(), time.Now()); pace > 0 {
			limit := t.rateLimiter.snapshot()
			t.logger.Debug("Rate limit pacing applied",
				zap.Int("limit", limit.Limit),
				zap.Int("remaining", limit.Remaining),
				zap.Time("reset", limit.Reset),
				zap.Duration("delay", pace),
			)
			time.Sleep(pace)
			return resp, nil
		}
	}

	// Secondary throttle, for endpoints that report no quota headers: when the
	// server responds more slowly than its own EMA baseline, pause
	// proportionally before the next request.
	if adaptive := t.responseTracker.record(duration); adaptive > 0 {
		t.logger.Debug("Adaptive delay applied due to elevated response time",
			zap.Duration("response_time", duration),
			zap.Duration("adaptive_delay", adaptive),
		)
		time.Sleep(adaptive)
	}

	return resp, nil
}

// AccountGroupID returns the account group requests are scoped to, or an empty
// string when the token's default account group is used.
func (t *Transport) AccountGroupID() string {
	if t.authConfig == nil {
		return ""
	}
	return t.authConfig.AccountGroupID
}

// RateLimit returns the organization quota as last reported by the API.
func (t *Transport) RateLimit() RateLimit {
	return t.rateLimiter.snapshot()
}
