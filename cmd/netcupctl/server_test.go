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
