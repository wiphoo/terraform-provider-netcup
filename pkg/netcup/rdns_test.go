package netcup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fastRDNSConfirm shrinks the read-back retry delay so tests that exhaust the
// retry window don't sleep for seconds. It restores the default afterward.
func fastRDNSConfirm(t *testing.T) {
	t.Helper()
	old := rdnsConfirmDelay
	rdnsConfirmDelay = 0
	t.Cleanup(func() { rdnsConfirmDelay = old })
}

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

func TestDeleteRDNSSuccess_IPv4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := http.MethodDelete; r.Method != got {
			t.Errorf("method = %q, want %q", r.Method, got)
		}
		if want := "/v1/rdns/ipv4/203.0.113.10"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	if err := c.DeleteRDNS(context.Background(), "203.0.113.10"); err != nil {
		t.Fatalf("DeleteRDNS() error = %v", err)
	}
}

func TestDeleteRDNSSuccess_IPv6(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// IPv6 address should be canonicalized (RFC 5952) in the path.
		if want := "/v1/rdns/ipv6/2001:db8:6:b1d::1"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	// Use expanded/non-canonical form; the SDK must canonicalize it.
	err := c.DeleteRDNS(context.Background(), "2001:0DB8:0006:0B1D:0000:0000:0000:0001")
	if err != nil {
		t.Fatalf("DeleteRDNS() error = %v", err)
	}
}

func TestDeleteRDNS_IPv4MappedIsUnmapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An IPv4-in-IPv6 address must route to the ipv4 endpoint with its
		// dotted-quad form, not /v1/rdns/ipv4/::ffff:203.0.113.10.
		if want := "/v1/rdns/ipv4/203.0.113.10"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	if err := c.DeleteRDNS(context.Background(), "::ffff:203.0.113.10"); err != nil {
		t.Fatalf("DeleteRDNS() error = %v", err)
	}
}

func TestDeleteRDNS_ZoneRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("API should not be called for a zoned address")
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	err := c.DeleteRDNS(context.Background(), "fe80::1%eth0")
	if err == nil {
		t.Fatal("DeleteRDNS() error = nil, want error for zoned address")
	}
	if !strings.Contains(err.Error(), "zone") {
		t.Errorf("error = %v, want mention of zone identifiers", err)
	}
}

func TestDeleteRDNS_InvalidIP(t *testing.T) {
	c := New(WithAccessToken("tok123"))
	err := c.DeleteRDNS(context.Background(), "not-an-ip")
	if err == nil {
		t.Fatal("DeleteRDNS() error = nil, want error for invalid IP")
	}
	if !strings.Contains(err.Error(), "invalid IP address") {
		t.Errorf("error = %v, want mention of invalid IP address", err)
	}
}

func TestDeleteRDNS_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"rdns entry not found"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	err := c.DeleteRDNS(context.Background(), "203.0.113.10")
	if err == nil {
		t.Fatal("DeleteRDNS() error = nil, want error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
}

func TestDeleteRDNS_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"validation error"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	err := c.DeleteRDNS(context.Background(), "203.0.113.10")
	if err == nil {
		t.Fatal("DeleteRDNS() error = nil, want error")
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

func TestConfirmRDNS_ImmediateMatch(t *testing.T) {
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"server.example.com"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	entry, err := c.ConfirmRDNS(context.Background(), "203.0.113.10", &RdnsEntry{IP: "203.0.113.10", Hostname: "server.example.com"})
	if err != nil {
		t.Fatalf("ConfirmRDNS() error = %v", err)
	}
	if entry.Hostname != "server.example.com" {
		t.Errorf("Hostname = %q, want %q", entry.Hostname, "server.example.com")
	}
	if gets != 1 {
		t.Errorf("GET calls = %d, want 1 (no retry needed)", gets)
	}
}

func TestConfirmRDNS_ReadBackFails(t *testing.T) {
	// If read-back never succeeds, the set is not verifiably in effect, so
	// ConfirmRDNS must return an error after exhausting its retries.
	fastRDNSConfirm(t)
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets++
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"rdns entry not found"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.ConfirmRDNS(context.Background(), "203.0.113.10", &RdnsEntry{IP: "203.0.113.10", Hostname: "server.example.com"})
	if err == nil {
		t.Fatal("ConfirmRDNS() error = nil, want error when read-back never confirms")
	}
	if !strings.Contains(err.Error(), "could not be confirmed") {
		t.Errorf("error should mention confirmation failure, got: %v", err)
	}
	if gets != rdnsConfirmAttempts {
		t.Errorf("read-back attempts = %d, want %d (should retry)", gets, rdnsConfirmAttempts)
	}
}

func TestConfirmRDNS_ReadBackMismatch(t *testing.T) {
	// A persistently different read-back value means the requested PTR is not
	// in effect, so ConfirmRDNS returns an error after retrying.
	fastRDNSConfirm(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"other.example.com"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.ConfirmRDNS(context.Background(), "203.0.113.10", &RdnsEntry{IP: "203.0.113.10", Hostname: "server.example.com"})
	if err == nil {
		t.Fatal("ConfirmRDNS() error = nil, want mismatch error")
	}
	if !strings.Contains(err.Error(), "did not match") {
		t.Errorf("error should mention read-back mismatch, got: %v", err)
	}
	if !strings.Contains(err.Error(), "server.example.com") || !strings.Contains(err.Error(), "other.example.com") {
		t.Errorf("error should name both hostnames, got: %v", err)
	}
}

// TestConfirmRDNS_ReadBackEventuallyConsistent proves the retry loop absorbs
// netcup's asynchronous provisioning: the first read-back returns the old
// (null) value, a later one returns the requested hostname, and ConfirmRDNS
// succeeds.
func TestConfirmRDNS_ReadBackEventuallyConsistent(t *testing.T) {
	fastRDNSConfirm(t)
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if gets < 3 {
			// Not provisioned yet.
			_, _ = w.Write([]byte(`{"rdns":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"rdns":"server.example.com"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	entry, err := c.ConfirmRDNS(context.Background(), "203.0.113.10", &RdnsEntry{IP: "203.0.113.10", Hostname: "server.example.com"})
	if err != nil {
		t.Fatalf("ConfirmRDNS() error = %v, want success once read-back becomes consistent", err)
	}
	if gets != 3 {
		t.Errorf("read-back attempts = %d, want 3 (stops once confirmed)", gets)
	}
	if entry.Hostname != "server.example.com" {
		t.Errorf("Hostname = %q, want confirmed value", entry.Hostname)
	}
}

// TestConfirmRDNS_ReadBackNormalized covers the common case where the API
// stores a PTR and echoes it back canonicalized (lowercased, trailing dot).
// This must be treated as a match, not a spurious mismatch.
func TestConfirmRDNS_ReadBackNormalized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Canonicalized: lowercased and with a trailing FQDN dot.
		_, _ = w.Write([]byte(`{"rdns":"server.example.com."}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	entry, err := c.ConfirmRDNS(context.Background(), "203.0.113.10", &RdnsEntry{IP: "203.0.113.10", Hostname: "Server.Example.COM"})
	if err != nil {
		t.Fatalf("ConfirmRDNS() error = %v, want success for normalized read-back", err)
	}
	if entry.Hostname != "server.example.com." {
		t.Errorf("Hostname = %q, want read-back value %q", entry.Hostname, "server.example.com.")
	}
}

// TestConfirmRDNS_ContextCancellation proves that a canceled context stops the
// retry loop promptly instead of blocking for the full rdnsConfirmDelay: the
// server always returns a mismatch, forcing a retry wait, but the context is
// canceled before that wait would elapse.
func TestConfirmRDNS_ContextCancellation(t *testing.T) {
	old := rdnsConfirmDelay
	rdnsConfirmDelay = 1 * time.Hour
	t.Cleanup(func() { rdnsConfirmDelay = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"other.example.com"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))

	done := make(chan struct{})
	var err error
	go func() {
		_, err = c.ConfirmRDNS(ctx, "203.0.113.10", &RdnsEntry{IP: "203.0.113.10", Hostname: "server.example.com"})
		close(done)
	}()

	// Let the first attempt run, then cancel before the (1h) retry delay would
	// ever elapse.
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ConfirmRDNS() did not return promptly after context cancellation")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error = %v, want context canceled", err)
	}
}

func TestEqualRDNSHostnames(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"server.example.com", "server.example.com.", true},
		{"Server.Example.COM", "server.example.com", true},
		{"  server.example.com  ", "server.example.com.", true},
		{"server.example.com", "other.example.com", false},
		{"", "", true},
	}
	for _, c := range cases {
		if got := EqualRDNSHostnames(c.a, c.b); got != c.want {
			t.Errorf("EqualRDNSHostnames(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCanonicalizeIP(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr string // substring expected in the error, "" means no error
	}{
		{name: "ipv4", in: "203.0.113.10", want: "203.0.113.10"},
		{name: "ipv6 uppercase compressed", in: "2001:0DB8:0000:0000:0000:0000:0000:0001", want: "2001:db8::1"},
		{name: "ipv4-in-ipv6 unmapped", in: "::ffff:203.0.113.10", want: "203.0.113.10"},
		{name: "zone rejected", in: "fe80::1%eth0", wantErr: "zone"},
		{name: "invalid", in: "not-an-ip", wantErr: "invalid IP address"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CanonicalizeIP(c.in)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("CanonicalizeIP(%q) error = nil, want error mentioning %q", c.in, c.wantErr)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("CanonicalizeIP(%q) error = %v, want mention of %q", c.in, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CanonicalizeIP(%q) error = %v, want nil", c.in, err)
			}
			if got != c.want {
				t.Errorf("CanonicalizeIP(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeRDNSHostname(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"server.example.com", "server.example.com"},
		{"Server.Example.COM.", "server.example.com"},
		{"  server.example.com.  ", "server.example.com"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeRDNSHostname(c.in); got != c.want {
			t.Errorf("NormalizeRDNSHostname(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
