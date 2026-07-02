package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerList_NoServers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := serverList(nil, &buf); err != nil {
		t.Fatalf("serverList() error = %v", err)
	}
	if !strings.Contains(buf.String(), "No servers found.") {
		t.Errorf("output = %q, want 'No servers found.'", buf.String())
	}
}

func TestServerList_TableOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"web-01","hostname":"web-01.example.com","disabled":false,"template":{"id":10,"name":"VM 2000"}},
			{"id":2,"name":"db-01","disabled":true}
		]`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := serverList(nil, &buf); err != nil {
		t.Fatalf("serverList() error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "web-01") {
		t.Errorf("output missing server name: %q", out)
	}
	if !strings.Contains(out, "VM 2000") {
		t.Errorf("output missing product: %q", out)
	}
	if !strings.Contains(out, "Disabled") {
		t.Errorf("output missing Disabled status: %q", out)
	}
	if !strings.Contains(out, "Enabled") {
		t.Errorf("output missing Enabled status: %q", out)
	}
}

func TestServerList_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":1,"name":"srv1","hostname":"srv1.example","disabled":false,"template":{"id":10,"name":"VM 1000"}}]`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := serverList([]string{"--json"}, &buf); err != nil {
		t.Fatalf("serverList() error = %v", err)
	}
	out := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(out, "[") || !strings.HasSuffix(out, "]") {
		t.Errorf("JSON output should be an array, got: %q", out)
	}
	if !strings.Contains(out, `"name": "srv1"`) {
		t.Errorf("JSON output missing server data: %q", out)
	}
}

func TestServerList_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`invalid token`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "bad-token")

	var buf bytes.Buffer
	err := serverList(nil, &buf)
	if err == nil {
		t.Fatal("serverList() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention status code, got: %v", err)
	}
}

func TestServerGet_TableOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers/123" {
			t.Errorf("path = %q, want /v1/servers/123", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id":123,"name":"web-01","hostname":"web-01.example.com","disabled":false,
			"template":{"id":10,"name":"VM 2000"},
			"serverLiveInfo":{"state":"running"},
			"ipv4Addresses":[{"id":1,"ip":"192.0.2.10","netmask":"255.255.255.0"}],
			"ipv6Addresses":[{"id":2,"networkPrefix":"2a03:4000:6:b1d::","networkPrefixLength":64}]
		}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := serverGet([]string{"123"}, &buf); err != nil {
		t.Fatalf("serverGet() error = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"web-01", "VM 2000", "running", "192.0.2.10", "2a03:4000:6:b1d::/64"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
}

func TestServerGet_UnknownStatusAndNoAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":5,"name":"db-01","disabled":true,"serverLiveInfo":null}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := serverGet([]string{"5"}, &buf); err != nil {
		t.Fatalf("serverGet() error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "unknown") {
		t.Errorf("output should show 'unknown' status: %q", out)
	}
	if !strings.Contains(out, "Disabled") {
		t.Errorf("output should show Disabled admin state: %q", out)
	}
	// No addresses render as "-".
	if !strings.Contains(out, "IPv4:") || !strings.Contains(out, "-") {
		t.Errorf("output should show placeholder for missing addresses: %q", out)
	}
}

func TestServerGet_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":7,"name":"srv7","disabled":false}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	// Flag placed both before and after the positional ID must work, since the
	// documented form is `server get <id> [--json]`.
	for _, args := range [][]string{{"--json", "7"}, {"7", "--json"}} {
		var buf bytes.Buffer
		if err := serverGet(args, &buf); err != nil {
			t.Fatalf("serverGet(%v) error = %v", args, err)
		}
		out := strings.TrimSpace(buf.String())
		if !strings.HasPrefix(out, "{") || !strings.Contains(out, `"name": "srv7"`) {
			t.Errorf("serverGet(%v) JSON output unexpected: %q", args, out)
		}
	}
}

func TestServerGet_MissingID(t *testing.T) {
	var buf bytes.Buffer
	err := serverGet(nil, &buf)
	if err == nil {
		t.Fatal("serverGet() error = nil, want error for missing ID")
	}
	if !strings.Contains(err.Error(), "server ID") {
		t.Errorf("error should mention missing server ID, got: %v", err)
	}
}

func TestServerGet_InvalidID(t *testing.T) {
	var buf bytes.Buffer
	err := serverGet([]string{"not-a-number"}, &buf)
	if err == nil {
		t.Fatal("serverGet() error = nil, want error for non-integer ID")
	}
	if !strings.Contains(err.Error(), "invalid server ID") {
		t.Errorf("error should mention invalid server ID, got: %v", err)
	}
}

func TestServerGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"server not found"}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	err := serverGet([]string{"999"}, &buf)
	if err == nil {
		t.Fatal("serverGet() error = nil, want not-found error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404, got: %v", err)
	}
}
