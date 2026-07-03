package netcup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetRDNSSuccess_IPv4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/v1/rdns/ipv4/203.0.113.10"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		if v := r.Header.Get("Accept"); v != "application/json" {
			t.Errorf("Accept = %q, want application/json", v)
		}
		if v := r.Header.Get("Authorization"); v != "Bearer tok123" {
			t.Errorf("Authorization = %q, want Bearer tok123", v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"server.example.com"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	entry, err := c.GetRDNS(context.Background(), "203.0.113.10")
	if err != nil {
		t.Fatalf("GetRDNS() error = %v", err)
	}
	if entry.IP != "203.0.113.10" {
		t.Errorf("IP = %q, want %q", entry.IP, "203.0.113.10")
	}
	if entry.Hostname != "server.example.com" {
		t.Errorf("Hostname = %q, want %q", entry.Hostname, "server.example.com")
	}
}

func TestGetRDNSSuccess_IPv6(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// IPv6 address should be canonicalized (RFC 5952) in the path.
		if want := "/v1/rdns/ipv6/2001:db8:6:b1d::1"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"ipv6.example.com"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	// Use expanded/non-canonical form; the SDK must canonicalize it.
	entry, err := c.GetRDNS(context.Background(), "2001:0DB8:0006:0B1D:0000:0000:0000:0001")
	if err != nil {
		t.Fatalf("GetRDNS() error = %v", err)
	}
	if entry.IP != "2001:db8:6:b1d::1" {
		t.Errorf("IP = %q, want %q", entry.IP, "2001:db8:6:b1d::1")
	}
	if entry.Hostname != "ipv6.example.com" {
		t.Errorf("Hostname = %q, want %q", entry.Hostname, "ipv6.example.com")
	}
}

func TestGetRDNS_HostnameNull(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":null}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	entry, err := c.GetRDNS(context.Background(), "203.0.113.10")
	if err != nil {
		t.Fatalf("GetRDNS() error = %v", err)
	}
	if entry.Hostname != "" {
		t.Errorf("Hostname = %q, want empty string for null rdns", entry.Hostname)
	}
	if entry.IP != "203.0.113.10" {
		t.Errorf("IP = %q, want %q", entry.IP, "203.0.113.10")
	}
}

func TestGetRDNS_IPv4MappedIsUnmapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An IPv4-in-IPv6 address must route to the ipv4 endpoint with its
		// dotted-quad form, not /v1/rdns/ipv4/::ffff:203.0.113.10.
		if want := "/v1/rdns/ipv4/203.0.113.10"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"mapped.example.com"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	entry, err := c.GetRDNS(context.Background(), "::ffff:203.0.113.10")
	if err != nil {
		t.Fatalf("GetRDNS() error = %v", err)
	}
	if entry.IP != "203.0.113.10" {
		t.Errorf("IP = %q, want %q", entry.IP, "203.0.113.10")
	}
}

func TestGetRDNS_ZoneRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("API should not be called for a zoned address")
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.GetRDNS(context.Background(), "fe80::1%eth0")
	if err == nil {
		t.Fatal("GetRDNS() error = nil, want error for zoned address")
	}
	if !strings.Contains(err.Error(), "zone") {
		t.Errorf("error = %v, want mention of zone identifiers", err)
	}
}

func TestGetRDNS_InvalidIP(t *testing.T) {
	c := New(WithAccessToken("tok123"))
	_, err := c.GetRDNS(context.Background(), "not-an-ip")
	if err == nil {
		t.Fatal("GetRDNS() error = nil, want error for invalid IP")
	}
	if !strings.Contains(err.Error(), "invalid IP address") {
		t.Errorf("error = %v, want mention of invalid IP address", err)
	}
}

func TestGetRDNS_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"rdns entry not found"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.GetRDNS(context.Background(), "203.0.113.10")
	if err == nil {
		t.Fatal("GetRDNS() error = nil, want error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
}

func TestGetRDNS_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"validation error"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.GetRDNS(context.Background(), "203.0.113.10")
	if err == nil {
		t.Fatal("GetRDNS() error = nil, want error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusUnprocessableEntity)
	}
}

func TestSetRDNSSuccess_IPv4(t *testing.T) {
	var gotBody rdnsSetRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := http.MethodPost; r.Method != got {
			t.Errorf("method = %q, want %q", r.Method, got)
		}
		if want := "/v1/rdns/ipv4"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		if v := r.Header.Get("Accept"); v != "application/json" {
			t.Errorf("Accept = %q, want application/json", v)
		}
		if v := r.Header.Get("Content-Type"); v != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", v)
		}
		if v := r.Header.Get("Authorization"); v != "Bearer tok123" {
			t.Errorf("Authorization = %q, want Bearer tok123", v)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	entry, err := c.SetRDNS(context.Background(), "203.0.113.10", "server.example.com")
	if err != nil {
		t.Fatalf("SetRDNS() error = %v", err)
	}
	if gotBody.IP != "203.0.113.10" {
		t.Errorf("request IP = %q, want %q", gotBody.IP, "203.0.113.10")
	}
	if gotBody.Rdns != "server.example.com" {
		t.Errorf("request Rdns = %q, want %q", gotBody.Rdns, "server.example.com")
	}
	if entry.IP != "203.0.113.10" {
		t.Errorf("entry IP = %q, want %q", entry.IP, "203.0.113.10")
	}
	if entry.Hostname != "server.example.com" {
		t.Errorf("entry Hostname = %q, want %q", entry.Hostname, "server.example.com")
	}
}

func TestSetRDNSSuccess_IPv6(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/v1/rdns/ipv6"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		var body rdnsSetRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		// IPv6 must be canonicalized (RFC 5952) in the body.
		if want := "2001:db8:6:b1d::1"; body.IP != want {
			t.Errorf("request IP = %q, want %q", body.IP, want)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	// Use a non-canonical form; the SDK must canonicalize it.
	entry, err := c.SetRDNS(context.Background(), "2001:0DB8:0006:0B1D::1", "ipv6.example.com")
	if err != nil {
		t.Fatalf("SetRDNS() error = %v", err)
	}
	if entry.IP != "2001:db8:6:b1d::1" {
		t.Errorf("entry IP = %q, want %q", entry.IP, "2001:db8:6:b1d::1")
	}
}

func TestSetRDNS_IPv4MappedIsUnmapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/v1/rdns/ipv4"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		var body rdnsSetRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if want := "203.0.113.10"; body.IP != want {
			t.Errorf("request IP = %q, want %q (must be unmapped to dotted-quad)", body.IP, want)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	if _, err := c.SetRDNS(context.Background(), "::ffff:203.0.113.10", "mapped.example.com"); err != nil {
		t.Fatalf("SetRDNS() error = %v", err)
	}
}

func TestSetRDNS_TrimsHostname(t *testing.T) {
	var gotBody rdnsSetRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	entry, err := c.SetRDNS(context.Background(), "203.0.113.10", "  server.example.com  ")
	if err != nil {
		t.Fatalf("SetRDNS() error = %v", err)
	}
	if gotBody.Rdns != "server.example.com" {
		t.Errorf("request Rdns = %q, want trimmed %q", gotBody.Rdns, "server.example.com")
	}
	if entry.Hostname != "server.example.com" {
		t.Errorf("entry Hostname = %q, want trimmed %q", entry.Hostname, "server.example.com")
	}
}

func TestSetRDNS_NoContentResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A 204 No Content response is a valid upsert success.
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	if _, err := c.SetRDNS(context.Background(), "203.0.113.10", "server.example.com"); err != nil {
		t.Fatalf("SetRDNS() error = %v for 204 response", err)
	}
}

func TestSetRDNS_EmptyHostnameRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("API should not be called for an empty hostname")
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	for _, h := range []string{"", "   ", "\t\n"} {
		_, err := c.SetRDNS(context.Background(), "203.0.113.10", h)
		if err == nil {
			t.Errorf("SetRDNS() error = nil for hostname %q, want error", h)
		}
		if !strings.Contains(err.Error(), "hostname") {
			t.Errorf("error = %v, want mention of hostname", err)
		}
	}
}

func TestSetRDNS_InvalidIP(t *testing.T) {
	c := New(WithAccessToken("tok123"))
	_, err := c.SetRDNS(context.Background(), "not-an-ip", "server.example.com")
	if err == nil {
		t.Fatal("SetRDNS() error = nil, want error for invalid IP")
	}
	if !strings.Contains(err.Error(), "invalid IP address") {
		t.Errorf("error = %v, want mention of invalid IP address", err)
	}
}

func TestSetRDNS_ZoneRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("API should not be called for a zoned address")
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.SetRDNS(context.Background(), "fe80::1%eth0", "server.example.com")
	if err == nil {
		t.Fatal("SetRDNS() error = nil, want error for zoned address")
	}
	if !strings.Contains(err.Error(), "zone") {
		t.Errorf("error = %v, want mention of zone identifiers", err)
	}
}

func TestSetRDNS_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"validation error"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.SetRDNS(context.Background(), "203.0.113.10", "server.example.com")
	if err == nil {
		t.Fatal("SetRDNS() error = nil, want error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusUnprocessableEntity)
	}
}

func TestSetRDNS_UnauthorizedHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.SetRDNS(context.Background(), "203.0.113.10", "server.example.com")
	if err == nil {
		t.Fatal("SetRDNS() error = nil, want error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusUnauthorized)
	}
	if !strings.Contains(apiErr.Error(), "IP is allowed") {
		t.Errorf("error = %v, want IP allowlist hint for 401", apiErr)
	}
}
