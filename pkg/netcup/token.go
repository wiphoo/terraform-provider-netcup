package netcup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// TokenSource provides OAuth 2.0 access tokens, refreshing them automatically
// before expiry when a refresh token is available.
type TokenSource interface {
	// Token returns a valid access token, refreshing it if necessary.
	Token(ctx context.Context) (string, error)
}

// NewTokenSource creates a TokenSource that automatically refreshes the access
// token before it expires using the provided refresh token.
//
// The expiry parameter is the absolute time at which the access token expires.
// Refresh happens when the remaining lifetime drops below 30 seconds.
//
// When refreshToken is empty, NewTokenSource returns a source that always
// returns the same access token without attempting to refresh.
func NewTokenSource(client *Client, accessToken, refreshToken string, expiry time.Time) TokenSource {
	if refreshToken == "" {
		return staticTokenSource(accessToken)
	}
	return &refreshingTokenSource{
		client:       client,
		accessToken:  accessToken,
		refreshToken: refreshToken,
		expiry:       expiry,
	}
}

// ParseAccessTokenExpiry extracts the "exp" claim from a JWT access token,
// without verifying its signature (the SCP API is the party that verifies
// the token; this is only used to seed a refresh schedule for TokenSource).
//
// It returns an error when token is not a well-formed JWT (wrong number of
// dot-separated segments, invalid base64url payload, invalid JSON, or a
// missing/zero "exp" claim). Callers should treat a parse error as "unknown
// expiry" and fall back to a zero time.Time, which causes a refreshing
// TokenSource to refresh on first use rather than hard-failing.
func ParseAccessTokenExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("access token is not a JWT: expected 3 dot-separated segments, got %d", len(parts))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("decoding JWT payload: %w", err)
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("decoding JWT claims: %w", err)
	}
	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("JWT has no exp claim")
	}

	return time.Unix(claims.Exp, 0), nil
}

// staticTokenSource always returns the same token.
type staticTokenSource string

func (s staticTokenSource) Token(_ context.Context) (string, error) {
	return string(s), nil
}

// refreshingTokenSource auto-refreshes the access token before expiry.
type refreshingTokenSource struct {
	client       *Client
	accessToken  string
	refreshToken string
	expiry       time.Time
	mu           sync.Mutex
}

func (s *refreshingTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if time.Now().Before(s.expiry.Add(-refreshBeforeExpiry)) {
		return s.accessToken, nil
	}

	resp, err := s.client.RefreshAccessToken(ctx, s.refreshToken)
	if err != nil {
		return "", err
	}

	s.accessToken = resp.AccessToken
	if resp.RefreshToken != "" {
		s.refreshToken = resp.RefreshToken
	}
	if resp.ExpiresIn > 0 {
		s.expiry = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	}

	return s.accessToken, nil
}
