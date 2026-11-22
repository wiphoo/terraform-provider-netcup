package netcup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNew_EndpointValidation(t *testing.T) {
	t.Setenv("NETCUP_API_ENDPOINT", "")
	t.Setenv("NETCUP_OIDC_ENDPOINT", "")
	tests := []struct {
		name    string
		opts    []Option
		wantErr string
	}{
		{"defaults", nil, ""},
		{"loopback http", []Option{WithAPIEndpoint("http://127.0.0.1:8080"), WithOIDCEndpoint("http://localhost:1/x")}, ""},
		{"ipv6 loopback", []Option{WithAPIEndpoint("http://[::1]:8080")}, ""},
		{"plain http api", []Option{WithAPIEndpoint("http://example.com/api")}, "must use https"},
		{"plain http oidc", []Option{WithOIDCEndpoint("http://example.com/oidc")}, "must use https"},
		{"userinfo", []Option{WithAPIEndpoint("https://u:p@example.com")}, "userinfo"},
		{"no host", []Option{WithAPIEndpoint("https:///path")}, "no host"},
		{"bad scheme", []Option{WithAPIEndpoint("ftp://127.0.0.1")}, "must use https"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(tt.opts...)
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "p@") {
				t.Errorf("error leaks userinfo: %v", err)
			}
			// No request may be dispatched with an invalid endpoint.
			if _, rerr := c.newRequest(context.Background(), http.MethodGet, "/x", "", nil, false); !errors.Is(rerr, ErrPreDispatch) {
				t.Errorf("newRequest() error = %v, want ErrPreDispatch", rerr)
			}
			if _, rerr := c.newOIDCRequest(context.Background(), "/token", nil); rerr == nil {
				t.Error("newOIDCRequest() error = nil, want endpoint error")
			}
		})
	}
}

func TestDecodeJSON_LimitsBody(t *testing.T) {
	var v map[string]string
	if err := decodeJSON(strings.NewReader(`{"a":"b"}`), &v); err != nil || v["a"] != "b" {
		t.Fatalf("decodeJSON small = %v, %v", v, err)
	}
	big := `{"a":"` + strings.Repeat("x", maxResponseBody+10) + `"}`
	if err := decodeJSON(strings.NewReader(big), &v); err == nil {
		t.Fatal("decodeJSON oversized body: error = nil, want truncation error")
	}
}

func TestSnapshots_DotSegmentNameRejected(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	c := New(WithAPIEndpoint(srv.URL))
	for _, name := range []string{".", ".."} {
		if _, err := c.DeleteSnapshot(context.Background(), 1, name); !errors.Is(err, ErrPreDispatch) {
			t.Errorf("DeleteSnapshot(%q) error = %v, want ErrPreDispatch", name, err)
		}
		if _, err := c.RestoreSnapshot(context.Background(), 1, name); !errors.Is(err, ErrPreDispatch) {
			t.Errorf("RestoreSnapshot(%q) error = %v, want ErrPreDispatch", name, err)
		}
	}
	if called {
		t.Error("request dispatched for dot-segment name")
	}
}

func refreshServer(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(WithOIDCEndpoint(srv.URL), WithAPIEndpoint(srv.URL))
}

func TestRefreshingTokenSource_EmptyAccessToken(t *testing.T) {
	c := refreshServer(t, `{"access_token":"","expires_in":300}`)
	ts := NewTokenSource(c, "old", "rt", time.Time{})
	if _, err := ts.Token(context.Background()); err == nil {
		t.Fatal("Token() error = nil, want error for empty access_token")
	}
}

func TestRefreshingTokenSource_NoExpiresIn(t *testing.T) {
	// JWT exp in the past: must refresh again next call rather than trust it.
	jwt := fakeJWT(`{"exp":1}`)
	c := refreshServer(t, `{"access_token":"`+jwt+`"}`)
	ts := NewTokenSource(c, "old", "rt", time.Time{}).(*refreshingTokenSource)
	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !ts.expiry.Equal(time.Unix(1, 0)) {
		t.Errorf("expiry = %v, want JWT exp", ts.expiry)
	}

	// Not a JWT: short default TTL, not zero/forever.
	c = refreshServer(t, `{"access_token":"opaque"}`)
	ts = NewTokenSource(c, "old", "rt", time.Time{}).(*refreshingTokenSource)
	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(ts.expiry); d <= 0 || d > defaultRefreshedTokenTTL {
		t.Errorf("expiry in %v, want within (0, %v]", d, defaultRefreshedTokenTTL)
	}
}
