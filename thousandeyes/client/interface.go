package client

import (
	"context"

	"go.uber.org/zap"
)

// Client is the interface service implementations depend on.
// The Transport struct in this package satisfies it.
//
// It is deliberately narrower than the go-sdk-jamfpro-v2 equivalent, because
// three of that interface's methods have no counterpart here:
//
//   - RSQLBuilder — RSQL filtering is a Jamf Pro convention. The ThousandEyes
//     API filters through per-endpoint query parameters instead.
//   - InvalidateToken / KeepAliveToken — Jamf Pro issues short-lived bearer
//     tokens that must be refreshed and revoked. A ThousandEyes token is a
//     long-lived credential created by hand in the UI, so the SDK has no token
//     lifecycle to manage.
//   - ServerVersion — Jamf Pro is customer-hosted with per-instance versions,
//     which its API-lifecycle guard needs. ThousandEyes is one hosted service
//     on a single API version.
type Client interface {
	// NewRequest returns a RequestBuilder the service layer uses to construct a
	// complete request — headers, body, query params, result target — before
	// executing it via Get/Post/Put/Patch/Delete/GetBytes/GetPaginated.
	// Auth, retry, throttling and concurrency limiting are applied by the
	// transport at execution time.
	NewRequest(ctx context.Context) *RequestBuilder

	// AccountGroupID returns the account group requests are scoped to, or an
	// empty string when the token's default account group is used. Generated
	// operations send it as the aid query parameter.
	AccountGroupID() string

	// RateLimit returns the organization's quota as last reported by the API
	// through the x-organization-rate-limit-* response headers. Known is false
	// until a response carrying them has been seen.
	RateLimit() RateLimit

	// GetLogger returns the configured zap logger.
	GetLogger() *zap.Logger
}
