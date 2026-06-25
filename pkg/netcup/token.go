package netcup

import (
	"context"
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
