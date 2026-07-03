package netcup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func oidcHandler(t *testing.T, wantPath, wantGrantType string, statusCode int, body any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %s, want application/x-www-form-urlencoded", ct)
		}
		if wantGrantType != "" {
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if got := r.FormValue("grant_type"); got != wantGrantType {
				t.Errorf("grant_type = %s, want %s", got, wantGrantType)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if body != nil {
			b, _ := json.Marshal(body)
			_, _ = w.Write(b)
		}
	}
}

func TestRequestDeviceCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/device" {
			t.Errorf("path = %s, want /auth/device", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %s", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.FormValue("client_id"); got != "scp" {
			t.Errorf("client_id = %s", got)
		}
		if got := r.FormValue("scope"); got != "offline_access openid" {
			t.Errorf("scope = %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"device_code": "dc123",
			"user_code": "AB-CD-EF",
			"verification_uri": "https://example.com/device",
			"verification_uri_complete": "https://example.com/device?code=AB-CD-EF",
			"interval": 5,
			"expires_in": 600
		}`))
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	dar, err := c.RequestDeviceCode(context.Background())
	if err != nil {
		t.Fatalf("RequestDeviceCode error = %v", err)
	}
	if dar.DeviceCode != "dc123" {
		t.Errorf("DeviceCode = %s", dar.DeviceCode)
	}
	if dar.UserCode != "AB-CD-EF" {
		t.Errorf("UserCode = %s", dar.UserCode)
	}
	if dar.Interval != 5 {
		t.Errorf("Interval = %d", dar.Interval)
	}
}

func TestRequestDeviceCode_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"missing scope"}`))
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	_, err := c.RequestDeviceCode(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid_request") {
		t.Errorf("error = %v, want invalid_request", err)
	}
}

func TestWaitForDeviceAuthorization_Success(t *testing.T) {
	srv := httptest.NewServer(oidcHandler(t, "/token", "urn:ietf:params:oauth:grant-type:device_code", http.StatusOK, map[string]any{
		"access_token":  "at_new",
		"refresh_token": "rt_new",
		"expires_in":    300,
		"token_type":    "Bearer",
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	token, err := c.WaitForDeviceAuthorization(context.Background(), "dc123", time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForDeviceAuthorization error = %v", err)
	}
	if token.AccessToken != "at_new" {
		t.Errorf("AccessToken = %s", token.AccessToken)
	}
	if token.RefreshToken != "rt_new" {
		t.Errorf("RefreshToken = %s", token.RefreshToken)
	}
	if token.ExpiresIn != 300 {
		t.Errorf("ExpiresIn = %d", token.ExpiresIn)
	}
}

func TestWaitForDeviceAuthorization_PendingThenSuccess(t *testing.T) {
	var pollCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if pollCount.Add(1) <= 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"at_last","refresh_token":"rt_last","expires_in":300,"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	token, err := c.WaitForDeviceAuthorization(context.Background(), "dc123", time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForDeviceAuthorization error = %v", err)
	}
	if token.AccessToken != "at_last" {
		t.Errorf("AccessToken = %s", token.AccessToken)
	}
	if n := pollCount.Load(); n != 3 {
		t.Errorf("polled %d times, want 3", n)
	}
}

func TestWaitForDeviceAuthorization_SlowDown(t *testing.T) {
	var first atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !first.Load() {
			first.Store(true)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"slow_down"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	_, err := c.WaitForDeviceAuthorization(context.Background(), "dc123", time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForDeviceAuthorization error = %v", err)
	}
	// After slow_down the interval is incremented by 5 s (RFC 8628), so the
	// second poll must wait at least that long.
}

func TestWaitForDeviceAuthorization_ExpiredToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"expired_token"}`))
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	_, err := c.WaitForDeviceAuthorization(context.Background(), "dc_expired", time.Millisecond)
	if err == nil {
		t.Fatal("expected error for expired_token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error = %v, want expired message", err)
	}
}

func TestWaitForDeviceAuthorization_Denied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"access_denied"}`))
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	_, err := c.WaitForDeviceAuthorization(context.Background(), "dc_denied", time.Millisecond)
	if err == nil {
		t.Fatal("expected error for access_denied")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %v, want denied message", err)
	}
}

func TestRefreshAccessToken(t *testing.T) {
	srv := httptest.NewServer(oidcHandler(t, "/token", "refresh_token", http.StatusOK, map[string]any{
		"access_token":  "at_refreshed",
		"refresh_token": "rt_refreshed",
		"expires_in":    300,
		"token_type":    "Bearer",
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	token, err := c.RefreshAccessToken(context.Background(), "rt_old")
	if err != nil {
		t.Fatalf("RefreshAccessToken error = %v", err)
	}
	if token.AccessToken != "at_refreshed" {
		t.Errorf("AccessToken = %s", token.AccessToken)
	}
	if token.RefreshToken != "rt_refreshed" {
		t.Errorf("RefreshToken = %s", token.RefreshToken)
	}
}

func TestRefreshAccessToken_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"token expired"}`))
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	_, err := c.RefreshAccessToken(context.Background(), "rt_expired")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error = %v, want invalid_grant", err)
	}
}

func TestDeviceLogin_PrintsURLAndReturnsToken(t *testing.T) {
	var buf strings.Builder
	var polled atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/device":
			_, _ = w.Write([]byte(`{
				"device_code":"dc123","user_code":"XY-ZZ-00",
				"verification_uri":"https://ex.com/device",
				"verification_uri_complete":"https://ex.com/device?code=XY-ZZ-00",
				"interval":1,"expires_in":600
			}`))
		case "/token":
			if polled.Load() {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":300,"token_type":"Bearer"}`))
				return
			}
			polled.Store(true)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
		}
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	token, err := c.DeviceLogin(context.Background(), &buf)
	if err != nil {
		t.Fatalf("DeviceLogin error = %v", err)
	}
	if !strings.Contains(buf.String(), "https://ex.com/device?code=XY-ZZ-00") {
		t.Errorf("output = %q, want verification URL", buf.String())
	}
	if token.AccessToken != "at" {
		t.Errorf("AccessToken = %s", token.AccessToken)
	}
}

func TestTokenSource_Static_NoRefreshToken(t *testing.T) {
	c := New()
	ts := NewTokenSource(c, "tok_fixed", "", time.Time{})
	tok, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok != "tok_fixed" {
		t.Errorf("Token() = %s", tok)
	}
}

func TestTokenSource_ReturnsCachedBeforeExpiry(t *testing.T) {
	var refreshCalled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalled.Store(true)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"new","expires_in":300,"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	expiry := time.Now().Add(5 * time.Minute)
	ts := NewTokenSource(c, "cached_tok", "rt_valid", expiry)

	tok, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok != "cached_tok" {
		t.Errorf("Token() = %s, want cached_tok", tok)
	}
	if refreshCalled.Load() {
		t.Error("Token() called refresh on a non-expired token")
	}
}

func TestTokenSource_RefreshesOnExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"refreshed_tok","expires_in":300,"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	expiry := time.Now().Add(-time.Hour) // already expired
	ts := NewTokenSource(c, "old_tok", "rt_valid", expiry)

	tok, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok != "refreshed_tok" {
		t.Errorf("Token() = %s, want refreshed_tok", tok)
	}
}

func TestTokenSource_RefreshesAndUpdatesRefreshToken(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Each call returns a different refresh token (rotation).
		fmt.Fprintf(w, `{"access_token":"at_%d","refresh_token":"rt_%d","expires_in":1,"token_type":"Bearer"}`, callCount.Load(), callCount.Load())
	}))
	defer srv.Close()

	c := New(WithOIDCEndpoint(srv.URL))
	expiry := time.Now().Add(-time.Hour) // expired
	ts := NewTokenSource(c, "old_tok", "rt_valid", expiry)

	tok1, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("first Token() error = %v", err)
	}
	if tok1 != "at_1" {
		t.Errorf("first Token() = %s", tok1)
	}

	// Wait for the 1-second token to expire, then call again.
	time.Sleep(1100 * time.Millisecond)

	tok2, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("second Token() error = %v", err)
	}
	if tok2 != "at_2" {
		t.Errorf("second Token() = %s, want at_2", tok2)
	}
}

func TestNewWithEnvRefreshToken(t *testing.T) {
	t.Setenv("NETCUP_API_ENDPOINT", "")
	t.Setenv("NETCUP_OIDC_ENDPOINT", "")
	t.Setenv("NETCUP_ACCESS_TOKEN", "")
	t.Setenv("NETCUP_REFRESH_TOKEN", "rt_from_env")

	c := New()
	if c.RefreshToken() != "rt_from_env" {
		t.Errorf("RefreshToken() = %s", c.RefreshToken())
	}
}

func TestDeviceLogin_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/auth/device" {
			_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"XX","verification_uri":"https://ex.com/device","interval":5,"expires_in":600}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := New(WithOIDCEndpoint(srv.URL))
	var buf strings.Builder
	_, err := c.DeviceLogin(ctx, &buf)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestNewRequestDoesNotLeakRefreshToken(t *testing.T) {
	c := New(WithAccessToken("my_token"), WithRefreshToken("my_refresh"))
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		if authHeader != "Bearer my_token" {
			t.Errorf("Authorization = %s", authHeader)
		}
		if strings.Contains(authHeader, "my_refresh") {
			t.Error("refresh token leaked into Authorization header")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, err := c.newRequest(context.Background(), http.MethodGet, "/ping", "text/plain", nil)
	if err != nil {
		t.Fatalf("newRequest error = %v", err)
	}
	req.URL = &url.URL{Scheme: "http", Host: srv.Listener.Addr().String(), Path: "/ping"}
	_, _ = c.httpClient.Do(req)
}
