package netcup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultPollInterval = 5 * time.Second
	refreshBeforeExpiry = 30 * time.Second
)

// DeviceAuthResponse represents the OAuth 2.0 device authorization response
// (RFC 8628).
type DeviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
}

// TokenResponse represents an OAuth 2.0 token response (RFC 6749).
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	TokenType    string `json:"token_type"`
}

// oidcError is a standard OAuth 2.0 error response.
type oidcError struct {
	Err             string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

func (e *oidcError) Error() string {
	if e.ErrorDescription != "" {
		return e.Err + ": " + e.ErrorDescription
	}
	return e.Err
}

// newOIDCRequest builds a form-encoded POST request against the OIDC endpoint.
func (c *Client) newOIDCRequest(ctx context.Context, path string, form url.Values) (*http.Request, error) {
	u := strings.TrimRight(c.oidcEndpoint, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// doOIDCRequest sends an OIDC request and decodes the JSON response.
func (c *Client) doOIDCRequest(req *http.Request, dst any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("oidc request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return fmt.Errorf("reading oidc response: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		var errResp oidcError
		if json.Unmarshal(body, &errResp) == nil && errResp.Err != "" {
			return &errResp
		}
		return newAPIError(resp)
	}

	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("decoding oidc response: %w", err)
	}
	return nil
}

// RequestDeviceCode initiates the OAuth 2.0 device authorization flow against
// the OIDC endpoint.
func (c *Client) RequestDeviceCode(ctx context.Context) (*DeviceAuthResponse, error) {
	form := url.Values{
		"client_id": {"scp"},
		"scope":     {"offline_access openid"},
	}
	req, err := c.newOIDCRequest(ctx, "/auth/device", form)
	if err != nil {
		return nil, err
	}

	var dar DeviceAuthResponse
	if err := c.doOIDCRequest(req, &dar); err != nil {
		return nil, err
	}
	return &dar, nil
}

// WaitForDeviceAuthorization polls the token endpoint until the user approves
// the device or the flow fails.
func (c *Client) WaitForDeviceAuthorization(ctx context.Context, deviceCode string, interval time.Duration) (*TokenResponse, error) {
	if interval <= 0 {
		interval = defaultPollInterval
	}

	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
		"client_id":   {"scp"},
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}

		req, err := c.newOIDCRequest(ctx, "/token", form)
		if err != nil {
			return nil, err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("token poll failed: %w", err)
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
		_ = resp.Body.Close()

		if resp.StatusCode/100 == 2 {
			var tr TokenResponse
			if err := json.Unmarshal(body, &tr); err != nil {
				return nil, fmt.Errorf("decoding token response: %w", err)
			}
			return &tr, nil
		}

		var oerr oidcError
		if json.Unmarshal(body, &oerr) != nil || oerr.Err == "" {
			return nil, newAPIError(resp)
		}

		switch oerr.Err {
		case "authorization_pending":
		case "slow_down":
			interval += interval / 2
		case "expired_token":
			return nil, fmt.Errorf("device code expired, restart the login flow")
		case "access_denied":
			return nil, fmt.Errorf("authorization denied by user")
		default:
			return nil, &oerr
		}
	}
}

// DeviceLogin runs the complete OAuth 2.0 device authorization flow.
// It prints the verification URI to the provided writer (for the user to
// open in a browser) and then polls for the token.
func (c *Client) DeviceLogin(ctx context.Context, out io.Writer) (*TokenResponse, error) {
	dar, err := c.RequestDeviceCode(ctx)
	if err != nil {
		return nil, err
	}

	if dar.VerificationURIComplete != "" {
		fmt.Fprintln(out, "Open this URL in your browser to approve the device:")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  "+dar.VerificationURIComplete)
	} else {
		fmt.Fprintf(out, "Go to %s and enter the code: %s\n", dar.VerificationURI, dar.UserCode)
	}
	fmt.Fprintln(out)

	interval := time.Duration(dar.Interval) * time.Second
	if interval <= 0 {
		interval = defaultPollInterval
	}

	token, err := c.WaitForDeviceAuthorization(ctx, dar.DeviceCode, interval)
	if err != nil {
		return nil, fmt.Errorf("waiting for device authorization: %w", err)
	}

	fmt.Fprintln(out, "Device authorized successfully.")
	return token, nil
}

// RefreshAccessToken exchanges a refresh token for a new access token (and
// possibly a new refresh token).
func (c *Client) RefreshAccessToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {"scp"},
	}
	req, err := c.newOIDCRequest(ctx, "/token", form)
	if err != nil {
		return nil, err
	}

	var tr TokenResponse
	if err := c.doOIDCRequest(req, &tr); err != nil {
		return nil, err
	}
	return &tr, nil
}
