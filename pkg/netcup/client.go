// Package netcup is the shared Go SDK for the netcup Server Control Panel (SCP)
// REST API. It is the single place where HTTP communication, authentication
// (OAuth 2.0 device flow and token refresh), and response handling live, and is
// consumed by both the netcupctl CLI and the Terraform provider.
package netcup

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// DefaultAPIEndpoint is the SCP REST API base URL (the /scp-core/api root).
	// The unauthenticated health check is at {DefaultAPIEndpoint}/ping; versioned
	// resources live under {DefaultAPIEndpoint}/v1/...
	DefaultAPIEndpoint = "https://www.servercontrolpanel.de/scp-core/api"

	// DefaultOIDCEndpoint is the SCP OIDC (Keycloak) base URL for the
	// authentication endpoints.
	DefaultOIDCEndpoint = "https://www.servercontrolpanel.de/realms/scp/protocol/openid-connect"

	defaultTimeout = 30 * time.Second
)

// Client talks to the netcup SCP REST API.
//
// Construct it with New. The zero value is not usable.
type Client struct {
	apiEndpoint  string
	oidcEndpoint string
	accessToken  string
	refreshToken string
	httpClient   *http.Client
}

// Option customizes a Client during construction.
type Option func(*Client)

// WithAPIEndpoint overrides the REST API base URL.
func WithAPIEndpoint(endpoint string) Option {
	return func(c *Client) { c.apiEndpoint = endpoint }
}

// WithOIDCEndpoint overrides the OIDC base URL.
func WithOIDCEndpoint(endpoint string) Option {
	return func(c *Client) { c.oidcEndpoint = endpoint }
}

// WithAccessToken sets the Bearer access token used for authenticated calls.
func WithAccessToken(token string) Option {
	return func(c *Client) { c.accessToken = token }
}

// WithRefreshToken sets the refresh token used for transparent token refresh.
func WithRefreshToken(token string) Option {
	return func(c *Client) { c.refreshToken = token }
}

// WithHTTPClient sets a custom *http.Client (for timeouts, transports, tests).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// New creates a Client. Endpoints and the access token default to the public
// SCP values, are then overridden by the NETCUP_API_ENDPOINT,
// NETCUP_OIDC_ENDPOINT, and NETCUP_ACCESS_TOKEN environment variables when set,
// and are finally overridden by any options passed here.
func New(opts ...Option) *Client {
	c := &Client{
		apiEndpoint:  DefaultAPIEndpoint,
		oidcEndpoint: DefaultOIDCEndpoint,
		httpClient:   &http.Client{Timeout: defaultTimeout},
	}

	if v := os.Getenv("NETCUP_API_ENDPOINT"); v != "" {
		c.apiEndpoint = v
	}
	if v := os.Getenv("NETCUP_OIDC_ENDPOINT"); v != "" {
		c.oidcEndpoint = v
	}
	if v := os.Getenv("NETCUP_ACCESS_TOKEN"); v != "" {
		c.accessToken = v
	}
	if v := os.Getenv("NETCUP_REFRESH_TOKEN"); v != "" {
		c.refreshToken = v
	}

	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIEndpoint returns the configured REST API base URL.
func (c *Client) APIEndpoint() string { return c.apiEndpoint }

// OIDCEndpoint returns the configured OIDC base URL.
func (c *Client) OIDCEndpoint() string { return c.oidcEndpoint }

// RefreshToken returns the configured refresh token, if any.
func (c *Client) RefreshToken() string { return c.refreshToken }

// newRequest builds a request against the REST API, attaching the Accept
// header, a Bearer token when one is configured, and a JSON Content-Type when
// a body is supplied.
func (c *Client) newRequest(ctx context.Context, method, path, accept string, body io.Reader) (*http.Request, error) {
	url := strings.TrimRight(c.apiEndpoint, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}
	return req, nil
}

// Ping verifies that the SCP REST API is reachable by calling GET /ping. It does
// not require authentication, but the IP allowlist gate still applies.
func (c *Client) Ping(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodGet, "/ping", "text/plain", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return newAPIError(resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
