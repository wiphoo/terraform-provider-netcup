package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerImages_TableOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers/123/imageflavours" {
			t.Errorf("path = %q, want /v1/servers/123/imageflavours", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"ubuntu-2404","alias":"Ubuntu 24.04","text":"Ubuntu 24.04 LTS","image":{"id":10,"name":"Ubuntu"}},
			{"id":2,"name":"debian-12","alias":"Debian 12","text":"Debian 12","image":null}
		]`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := serverImages([]string{"123"}, &buf); err != nil {
		t.Fatalf("serverImages() error = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"ID", "NAME", "ALIAS", "IMAGE", "ubuntu-2404", "Ubuntu 24.04", "Ubuntu", "debian-12"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
}

func TestServerImages_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := serverImages([]string{"123"}, &buf); err != nil {
		t.Fatalf("serverImages() error = %v", err)
	}
	if !strings.Contains(buf.String(), "No image flavours found.") {
		t.Errorf("output = %q, want 'No image flavours found.'", buf.String())
	}
}

func TestServerImages_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":1,"name":"ubuntu-2404","alias":"Ubuntu 24.04","text":"Ubuntu 24.04 LTS","image":{"id":10,"name":"Ubuntu"}}]`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	// Flag placed both before and after the positional ID must work, since the
	// documented form is `server images <id> [--json]`.
	for _, args := range [][]string{{"--json", "1"}, {"1", "--json"}} {
		var buf bytes.Buffer
		if err := serverImages(args, &buf); err != nil {
			t.Fatalf("serverImages(%v) error = %v", args, err)
		}
		out := strings.TrimSpace(buf.String())
		if !strings.HasPrefix(out, "[") || !strings.HasSuffix(out, "]") {
			t.Errorf("serverImages(%v) JSON output should be an array, got: %q", args, out)
		}
		if !strings.Contains(out, `"name": "ubuntu-2404"`) || !strings.Contains(out, `"text": "Ubuntu 24.04 LTS"`) {
			t.Errorf("serverImages(%v) JSON output missing flavour data: %q", args, out)
		}
	}
}

func TestServerImages_MissingID(t *testing.T) {
	var buf bytes.Buffer
	err := serverImages(nil, &buf)
	if err == nil {
		t.Fatal("serverImages() error = nil, want error for missing ID")
	}
	if !strings.Contains(err.Error(), "server ID") {
		t.Errorf("error should mention missing server ID, got: %v", err)
	}
}

func TestServerImages_InvalidID(t *testing.T) {
	var buf bytes.Buffer
	err := serverImages([]string{"not-a-number"}, &buf)
	if err == nil {
		t.Fatal("serverImages() error = nil, want error for non-integer ID")
	}
	if !strings.Contains(err.Error(), "invalid server ID") {
		t.Errorf("error should mention invalid server ID, got: %v", err)
	}
}

func TestServerImages_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"server not found"}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	err := serverImages([]string{"999"}, &buf)
	if err == nil {
		t.Fatal("serverImages() error = nil, want not-found error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404, got: %v", err)
	}
}
