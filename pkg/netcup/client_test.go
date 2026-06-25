package netcup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPingSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			t.Errorf("path = %q, want /ping", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL))
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestPingSendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if want := "Bearer tok123"; gotAuth != want {
		t.Fatalf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestPingMapsErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL))
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping() error = nil, want error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusUnauthorized)
	}
}

func TestDefaultAPIEndpointIsAPIRoot(t *testing.T) {
	// The health check lives at {root}/ping. The default must be the
	// /scp-core/api root, not the versioned /v1 base — otherwise Ping hits an
	// authenticated path and fails with 401.
	const want = "https://www.servercontrolpanel.de/scp-core/api"
	if DefaultAPIEndpoint != want {
		t.Fatalf("DefaultAPIEndpoint = %q, want %q", DefaultAPIEndpoint, want)
	}
}

func TestNewDefaultsAndEnvAndOptionPrecedence(t *testing.T) {
	// Defaults when nothing is set.
	if got := New().APIEndpoint(); got != DefaultAPIEndpoint {
		t.Fatalf("default APIEndpoint = %q, want %q", got, DefaultAPIEndpoint)
	}

	// Environment overrides the default.
	t.Setenv("NETCUP_API_ENDPOINT", "https://env.example/api")
	if got := New().APIEndpoint(); got != "https://env.example/api" {
		t.Fatalf("env APIEndpoint = %q, want env value", got)
	}

	// An explicit option overrides the environment.
	if got := New(WithAPIEndpoint("https://opt.example")).APIEndpoint(); got != "https://opt.example" {
		t.Fatalf("option APIEndpoint = %q, want option value", got)
	}
}
