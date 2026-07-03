package netcup

import (
	"context"
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
		if want := "/v1/rdns/ipv6/2a03:4000:6:b1d::1"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"ipv6.example.com"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	// Use expanded/non-canonical form; the SDK must canonicalize it.
	entry, err := c.GetRDNS(context.Background(), "2A03:4000:0006:0B1D:0000:0000:0000:0001")
	if err != nil {
		t.Fatalf("GetRDNS() error = %v", err)
	}
	if entry.IP != "2a03:4000:6:b1d::1" {
		t.Errorf("IP = %q, want %q", entry.IP, "2a03:4000:6:b1d::1")
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
