// Package netcup is the shared Go SDK for the netcup Server Control Panel (SCP)
// REST API. It is the single place where HTTP communication, authentication
// (OAuth 2.0 device flow and token refresh), and response handling live, and is
// consumed by both the netcupctl CLI and the Terraform provider.
package netcup

import (
	"context"
	"fmt"
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
	tokenSource  TokenSource
	httpClient   *http.Client

	// pollInterval overrides taskPollInterval for WaitForTask when > 0. It lets
	// a caller that constructs a Client — notably the go-vcr replay tests, in an
	// external package that cannot reach the unexported taskPollInterval package
	// var — shrink the wait between polls so a recorded multi-poll task replays
	// without sleeping for real seconds. Zero means "use the package default".
	pollInterval time.Duration
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

// WithTokenSource attaches a TokenSource that is consulted for the Bearer
// token on every request, taking precedence over the static access token set
// by WithAccessToken. Use this for long-running callers (such as the
// Terraform provider) that need transparent refresh across a token's
// lifetime; short-lived CLI invocations can keep using a static token.
func WithTokenSource(ts TokenSource) Option {
	return func(c *Client) { c.tokenSource = ts }
}

// WithHTTPClient sets a custom *http.Client (for timeouts, transports, tests).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// WithTaskPollInterval overrides how long WaitForTask waits between polls of
// GET /v1/tasks/{uuid}. A value <= 0 is ignored (the package default of 2s
// applies). This is primarily for tests replaying a recorded multi-poll task,
// which would otherwise sleep the full interval between each recorded poll; it
// also lets a caller tune responsiveness against SCP rate limits.
func WithTaskPollInterval(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.pollInterval = d
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
//
// When a TokenSource is configured (WithTokenSource), it is consulted for the
// Bearer token on every call; otherwise the static access token (WithAccessToken)
// is used. When authRequired is true, a TokenSource error is returned to the
// caller rather than sending the request unauthenticated; when false (for
// endpoints that work without authentication, such as Ping), a TokenSource
// error is ignored and the request is sent without a Bearer token instead of
// failing outright.
func (c *Client) newRequest(ctx context.Context, method, path, accept string, body io.Reader, authRequired bool) (*http.Request, error) {
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

	token := c.accessToken
	if c.tokenSource != nil {
		t, err := c.tokenSource.Token(ctx)
		if err != nil {
			if authRequired {
				return nil, fmt.Errorf("getting access token: %w", err)
			}
			t = ""
		}
		token = t
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// Ping verifies that the SCP REST API is reachable by calling GET /ping. It does
// not require authentication, but the IP allowlist gate still applies. A
// TokenSource error is not treated as fatal here (unlike authenticated
// endpoints): Ping still probes connectivity even if the token can't be
// refreshed.
func (c *Client) Ping(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodGet, "/ping", "text/plain", nil, false)
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
