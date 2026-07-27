// Package config holds the authentication and connection settings for the
// ThousandEyes API client.
package config

import (
	"errors"
	"os"
	"strings"
)

// DefaultAPIEndpoint is the ThousandEyes v7 API base URL. v7 is the only
// version this SDK supports.
const DefaultAPIEndpoint = "https://api.thousandeyes.com/v7"

var (
	// ErrMissingBearerToken is returned when no token has been supplied.
	ErrMissingBearerToken = errors.New("bearer token is required")
)

// AuthConfig holds the credentials and connection settings for the API.
//
// ThousandEyes has no client-credentials or service-account identity type: the
// v7 API accepts exactly one credential, an OAuth2 bearer token belonging to a
// user, created from Account Settings > Users and Roles > Profile. There is
// consequently no token exchange, refresh or revocation flow in this SDK — the
// token is presented as-is on every request.
type AuthConfig struct {
	// BearerToken is the ThousandEyes user API token. Required.
	BearerToken string

	// AccountGroupID scopes requests to an account group. Optional; when set it
	// is sent as the aid query parameter on every request, and when empty the
	// API falls back to the token owner's default account group.
	AccountGroupID string

	// APIEndpoint overrides the API base URL. Defaults to DefaultAPIEndpoint.
	APIEndpoint string

	// HideSensitiveData suppresses the bearer token in log output. Enable in
	// production so tokens do not reach log files.
	HideSensitiveData bool
}

// Validate checks that the configuration is usable.
func (a *AuthConfig) Validate() error {
	if strings.TrimSpace(a.BearerToken) == "" {
		return ErrMissingBearerToken
	}
	return nil
}

// Endpoint returns the configured API base URL, or the default.
func (a *AuthConfig) Endpoint() string {
	if e := strings.TrimSpace(a.APIEndpoint); e != "" {
		return strings.TrimSuffix(e, "/")
	}
	return DefaultAPIEndpoint
}

// AuthConfigFromEnv builds an AuthConfig from the environment. The variable
// names match those the official ThousandEyes tooling reads, so one exported
// environment serves this SDK, the Terraform provider and the CLI alike.
//
//	TE_TOKEN         bearer token (required)
//	TE_AID           account group ID (optional)
//	TE_API_ENDPOINT  API base URL (optional)
func AuthConfigFromEnv() *AuthConfig {
	return &AuthConfig{
		BearerToken:    os.Getenv("TE_TOKEN"),
		AccountGroupID: os.Getenv("TE_AID"),
		APIEndpoint:    os.Getenv("TE_API_ENDPOINT"),
	}
}
