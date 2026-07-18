package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerSnapshots_TableOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers/123/snapshots" {
			t.Errorf("path = %q, want /v1/servers/123/snapshots", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"uuid":"a1","name":"nightly","description":null,"disks":["sda"],
			 "creationTime":"2026-07-16T13:34:04Z","state":"RUNNING","online":true,"exported":false},
			{"uuid":"b2","name":"exported-snap","description":null,"disks":[],
			 "creationTime":"2026-07-10T09:00:00Z","state":"STOPPED","online":false,"exported":true,"exportedSizeInKiB":204800}
		]`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := serverSnapshots([]string{"123"}, &buf); err != nil {
		t.Fatalf("serverSnapshots() error = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "CREATED", "STATE", "ONLINE", "EXPORTED", "nightly", "exported-snap", "2026-07-16T13:34:04Z", "RUNNING", "true", "false"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
}

func TestServerSnapshots_NoSnapshots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := serverSnapshots([]string{"123"}, &buf); err != nil {
		t.Fatalf("serverSnapshots() error = %v", err)
	}
	if !strings.Contains(buf.String(), "No snapshots found.") {
		t.Errorf("output = %q, want 'No snapshots found.'", buf.String())
	}
}

func TestServerSnapshots_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"uuid":"a1","name":"nightly","disks":[],"creationTime":"2026-07-16T13:34:04Z","state":"RUNNING","online":true,"exported":false}]`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	// Flag placed both before and after the positional ID must work, since the
	// documented form is `server snapshots <id> [--json]`.
	for _, args := range [][]string{{"--json", "123"}, {"123", "--json"}} {
		var buf bytes.Buffer
		if err := serverSnapshots(args, &buf); err != nil {
			t.Fatalf("serverSnapshots(%v) error = %v", args, err)
		}
		out := strings.TrimSpace(buf.String())
		if !strings.HasPrefix(out, "[") || !strings.HasSuffix(out, "]") {
			t.Errorf("serverSnapshots(%v) JSON output should be an array, got: %q", args, out)
		}
		if !strings.Contains(out, `"name": "nightly"`) {
			t.Errorf("serverSnapshots(%v) JSON output missing snapshot data: %q", args, out)
		}
	}
}

func TestServerSnapshots_MissingID(t *testing.T) {
	var buf bytes.Buffer
	err := serverSnapshots(nil, &buf)
	if err == nil {
		t.Fatal("serverSnapshots() error = nil, want error for missing ID")
	}
	if !strings.Contains(err.Error(), "server ID") {
		t.Errorf("error should mention missing server ID, got: %v", err)
	}
}

func TestServerSnapshots_InvalidID(t *testing.T) {
	var buf bytes.Buffer
	err := serverSnapshots([]string{"not-a-number"}, &buf)
	if err == nil {
		t.Fatal("serverSnapshots() error = nil, want error for non-integer ID")
	}
	if !strings.Contains(err.Error(), "invalid server ID") {
		t.Errorf("error should mention invalid server ID, got: %v", err)
	}
}

func TestServerSnapshots_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"server not found"}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	err := serverSnapshots([]string{"999"}, &buf)
	if err == nil {
		t.Fatal("serverSnapshots() error = nil, want not-found error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404, got: %v", err)
	}
}
