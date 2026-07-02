package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRDNSGet_TextOutput_IPv4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rdns/ipv4/203.0.113.10" {
			t.Errorf("path = %q, want /v1/rdns/ipv4/203.0.113.10", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"server.example.com"}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := rdnsGet([]string{"203.0.113.10"}, &buf); err != nil {
		t.Fatalf("rdnsGet() error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "203.0.113.10") {
		t.Errorf("output missing IP: %q", out)
	}
	if !strings.Contains(out, "server.example.com") {
		t.Errorf("output missing hostname: %q", out)
	}
}

func TestRDNSGet_TextOutput_IPv6(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// IPv6 must be canonicalized.
		if want := "/v1/rdns/ipv6/2a03:4000:6:b1d::1"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"ipv6.example.com"}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := rdnsGet([]string{"2A03:4000:0006:0B1D::1"}, &buf); err != nil {
		t.Fatalf("rdnsGet() error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "2a03:4000:6:b1d::1") {
		t.Errorf("output missing canonicalized IP: %q", out)
	}
	if !strings.Contains(out, "ipv6.example.com") {
		t.Errorf("output missing hostname: %q", out)
	}
}

func TestRDNSGet_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"server.example.com"}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	for _, args := range [][]string{{"--json", "203.0.113.10"}, {"203.0.113.10", "--json"}} {
		var buf bytes.Buffer
		if err := rdnsGet(args, &buf); err != nil {
			t.Fatalf("rdnsGet(%v) error = %v", args, err)
		}
		out := strings.TrimSpace(buf.String())
		if !strings.HasPrefix(out, "{") || !strings.Contains(out, `"ip":`) {
			t.Errorf("rdnsGet(%v) JSON output unexpected: %q", args, out)
		}
	}
}

func TestRDNSGet_NoPTR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":null}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := rdnsGet([]string{"203.0.113.10"}, &buf); err != nil {
		t.Fatalf("rdnsGet() error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "<none>") {
		t.Errorf("output should show '<none>' for null rdns: %q", out)
	}
}

func TestRDNSGet_MissingIP(t *testing.T) {
	var buf bytes.Buffer
	err := rdnsGet(nil, &buf)
	if err == nil {
		t.Fatal("rdnsGet() error = nil, want error for missing IP")
	}
	if !strings.Contains(err.Error(), "IP address") {
		t.Errorf("error should mention missing IP, got: %v", err)
	}
}

func TestRDNSGet_TooManyArgs(t *testing.T) {
	var buf bytes.Buffer
	err := rdnsGet([]string{"203.0.113.10", "203.0.113.11"}, &buf)
	if err == nil {
		t.Fatal("rdnsGet() error = nil, want error for two IPs")
	}
	if !strings.Contains(err.Error(), "single IP address") {
		t.Errorf("error should reject multiple IPs, got: %v", err)
	}
}

func TestRDNSGet_TextOutputIsTabAligned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"server.example.com"}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := rdnsGet([]string{"203.0.113.10"}, &buf); err != nil {
		t.Fatalf("rdnsGet() error = %v", err)
	}
	out := buf.String()
	// tabwriter flushes aligned columns using spaces; no raw tab should remain.
	if strings.Contains(out, "\t") {
		t.Errorf("output should be tab-aligned (no raw tabs), got: %q", out)
	}
	if !strings.Contains(out, "server.example.com") {
		t.Errorf("output missing hostname: %q", out)
	}
}

func TestRDNSGet_InvalidIP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("API should not be called for invalid IPs")
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	err := rdnsGet([]string{"not-an-ip"}, &buf)
	if err == nil {
		t.Fatal("rdnsGet() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid IP address") {
		t.Errorf("error should mention invalid IP, got: %v", err)
	}
}

func TestRDNSGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"rdns entry not found"}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	err := rdnsGet([]string{"203.0.113.10"}, &buf)
	if err == nil {
		t.Fatal("rdnsGet() error = nil, want 404 error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404, got: %v", err)
	}
}
