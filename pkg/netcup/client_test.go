package netcup

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestListServersSuccess(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers" {
			t.Errorf("path = %q, want /v1/servers", r.URL.Path)
		}
		if v := r.Header.Get("Accept"); v != "application/json" {
			t.Errorf("Accept = %q, want application/json", v)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":1,"name":"srv1","hostname":"srv1.example","disabled":false,"template":{"id":10,"name":"VM 1000"}}]`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	servers, err := c.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("len(servers) = %d, want 1", len(servers))
	}
	if servers[0].ID != 1 || servers[0].Name != "srv1" {
		t.Errorf("server = %+v, want ID=1 Name=srv1", servers[0])
	}
	if servers[0].Hostname == nil || *servers[0].Hostname != "srv1.example" {
		t.Errorf("Hostname = %v, want %q", servers[0].Hostname, "srv1.example")
	}
	if servers[0].Template == nil || servers[0].Template.Name != "VM 1000" {
		t.Errorf("Template = %+v, want Name=VM 1000", servers[0].Template)
	}
	if want := "Bearer tok123"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestListServersEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	servers, err := c.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("len(servers) = %d, want 0", len(servers))
	}
}

func TestListServersMapsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`invalid token`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("bad"))
	_, err := c.ListServers(context.Background())
	if err == nil {
		t.Fatal("ListServers() error = nil, want error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusUnauthorized)
	}
}

func TestListServersHandlesNullFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":2,"name":"srv2","disabled":true}]`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	servers, err := c.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("len(servers) = %d, want 1", len(servers))
	}
	if servers[0].Hostname != nil {
		t.Errorf("Hostname = %v, want nil", servers[0].Hostname)
	}
	if servers[0].Template != nil {
		t.Errorf("Template = %+v, want nil", servers[0].Template)
	}
	if !servers[0].Disabled {
		t.Error("Disabled = false, want true")
	}
}

func TestPingUsesTokenSourceOverStaticToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("static-tok"), WithTokenSource(staticTokenSource("source-tok")))
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if want := "Bearer source-tok"; gotAuth != want {
		t.Fatalf("Authorization = %q, want %q (TokenSource should take precedence)", gotAuth, want)
	}
}

type erroringTokenSource struct{}

func (erroringTokenSource) Token(_ context.Context) (string, error) {
	return "", fmt.Errorf("token source unavailable")
}

// TestPingIgnoresTokenSourceError proves that Ping (documented as not
// requiring authentication) still probes connectivity even when the
// TokenSource can't produce a token: it must not fail with a token-refresh
// error, and it must send the request without a Bearer header instead of
// aborting outright.
func TestPingIgnoresTokenSourceError(t *testing.T) {
	var gotAuth string
	var authHeaderSet bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, authHeaderSet = r.Header["Authorization"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithTokenSource(erroringTokenSource{}))
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v, want no error (Ping should tolerate a token source failure)", err)
	}
	if authHeaderSet {
		t.Errorf("Authorization header = %q, want no header when the token source errors", gotAuth)
	}
}

// TestGetRDNSSurfacesTokenSourceError proves that authenticated endpoints
// (unlike Ping) still fail cleanly when the TokenSource errors, rather than
// silently sending an unauthenticated request.
func TestGetRDNSSurfacesTokenSourceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("API should not be called when the token source errors for an authenticated endpoint")
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithTokenSource(erroringTokenSource{}))
	_, err := c.GetRDNS(context.Background(), "203.0.113.10")
	if err == nil {
		t.Fatal("GetRDNS() error = nil, want error from token source")
	}
	if !strings.Contains(err.Error(), "token source unavailable") {
		t.Errorf("error = %v, want to mention the token source failure", err)
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
	// Ensure a hermetic environment: the runner (or the documented user setup)
	// may export these, which would otherwise shadow the defaults below.
	t.Setenv("NETCUP_API_ENDPOINT", "")
	t.Setenv("NETCUP_OIDC_ENDPOINT", "")
	t.Setenv("NETCUP_ACCESS_TOKEN", "")

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
