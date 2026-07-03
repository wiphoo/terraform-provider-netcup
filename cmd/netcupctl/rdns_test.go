package main

import (
	"bytes"
	"encoding/json"
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
		if want := "/v1/rdns/ipv6/2001:db8:6:b1d::1"; r.URL.Path != want {
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
	if err := rdnsGet([]string{"2001:0DB8:0006:0B1D::1"}, &buf); err != nil {
		t.Fatalf("rdnsGet() error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "2001:db8:6:b1d::1") {
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

// rdnsSetGetHandler serves both the POST (set) and GET (read-back) endpoints
// for an IP, recording the last hostname set so the GET can echo it back.
type rdnsSetGetHandler struct {
	t        *testing.T
	ip       string
	hostname string
	postSeen bool
	getSeen  bool
}

func (h *rdnsSetGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/rdns/ipv4":
		h.postSeen = true
		var body struct {
			IP   string `json:"ip"`
			Rdns string `json:"rdns"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			h.t.Errorf("decode set body: %v", err)
		}
		if body.IP != h.ip {
			h.t.Errorf("set body IP = %q, want %q", body.IP, h.ip)
		}
		h.hostname = body.Rdns
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/rdns/ipv4/"+h.ip:
		h.getSeen = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"` + h.hostname + `"}`))
	default:
		h.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestRDNSSet_TextOutput_IPv4(t *testing.T) {
	h := &rdnsSetGetHandler{t: t, ip: "203.0.113.10", hostname: ""}
	srv := httptest.NewServer(h)
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := rdnsSet([]string{"203.0.113.10", "server.example.com"}, &buf); err != nil {
		t.Fatalf("rdnsSet() error = %v", err)
	}
	if !h.postSeen {
		t.Error("POST (set) was not called")
	}
	if !h.getSeen {
		t.Error("GET (read-back) was not called")
	}
	out := buf.String()
	if !strings.Contains(out, "203.0.113.10") {
		t.Errorf("output missing IP: %q", out)
	}
	if !strings.Contains(out, "server.example.com") {
		t.Errorf("output missing hostname: %q", out)
	}
}

func TestRDNSSet_JSONOutput(t *testing.T) {
	h := &rdnsSetGetHandler{t: t, ip: "203.0.113.10", hostname: ""}
	srv := httptest.NewServer(h)
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	for _, args := range [][]string{
		{"--json", "203.0.113.10", "server.example.com"},
		{"203.0.113.10", "server.example.com", "--json"},
	} {
		var buf bytes.Buffer
		if err := rdnsSet(args, &buf); err != nil {
			t.Fatalf("rdnsSet(%v) error = %v", args, err)
		}
		out := strings.TrimSpace(buf.String())
		if !strings.HasPrefix(out, "{") || !strings.Contains(out, `"ip":`) {
			t.Errorf("rdnsSet(%v) JSON output unexpected: %q", args, out)
		}
	}
}

func TestRDNSSet_ReadBackConfirmationOrder(t *testing.T) {
	var order []string
	hostname := "server.example.com"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, r.Method)
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"rdns":"` + hostname + `"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := rdnsSet([]string{"203.0.113.10", hostname}, &buf); err != nil {
		t.Fatalf("rdnsSet() error = %v", err)
	}
	if len(order) != 2 {
		t.Fatalf("expected 2 API calls, got %d: %v", len(order), order)
	}
	if order[0] != http.MethodPost {
		t.Errorf("first call = %s, want POST (set)", order[0])
	}
	if order[1] != http.MethodGet {
		t.Errorf("second call = %s, want GET (read-back)", order[1])
	}
}

func TestRDNSSet_MissingArgs(t *testing.T) {
	var buf bytes.Buffer
	err := rdnsSet(nil, &buf)
	if err == nil {
		t.Fatal("rdnsSet() error = nil, want error for missing args")
	}
	if !strings.Contains(err.Error(), "IP address and a hostname") {
		t.Errorf("error should mention missing IP/hostname, got: %v", err)
	}

	err = rdnsSet([]string{"203.0.113.10"}, &buf)
	if err == nil {
		t.Fatal("rdnsSet() error = nil, want error for missing hostname")
	}
	if !strings.Contains(err.Error(), "IP address and a hostname") {
		t.Errorf("error should mention missing hostname, got: %v", err)
	}
}

func TestRDNSSet_TooManyArgs(t *testing.T) {
	var buf bytes.Buffer
	err := rdnsSet([]string{"203.0.113.10", "server.example.com", "extra"}, &buf)
	if err == nil {
		t.Fatal("rdnsSet() error = nil, want error for three args")
	}
	if !strings.Contains(err.Error(), "IP address and a hostname") {
		t.Errorf("error should reject extra args, got: %v", err)
	}
}

func TestRDNSSet_EmptyHostname(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("API should not be called for an empty hostname")
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	err := rdnsSet([]string{"203.0.113.10", "   "}, &buf)
	if err == nil {
		t.Fatal("rdnsSet() error = nil, want error for empty hostname")
	}
	if !strings.Contains(err.Error(), "hostname") {
		t.Errorf("error should mention hostname, got: %v", err)
	}
}

func TestRDNSSet_InvalidIP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("API should not be called for invalid IP")
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	err := rdnsSet([]string{"not-an-ip", "server.example.com"}, &buf)
	if err == nil {
		t.Fatal("rdnsSet() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid IP address") {
		t.Errorf("error should mention invalid IP, got: %v", err)
	}
}

func TestRDNSSet_SetFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"validation error"}`))
			return
		}
		t.Errorf("unexpected GET after failed POST: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	err := rdnsSet([]string{"203.0.113.10", "server.example.com"}, &buf)
	if err == nil {
		t.Fatal("rdnsSet() error = nil, want error for failed POST")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("error should mention 422, got: %v", err)
	}
}

// fastRDNSConfirm shrinks the read-back retry delay so tests that exhaust the
// retry window don't sleep for seconds. It restores the default afterward.
func fastRDNSConfirm(t *testing.T) {
	t.Helper()
	old := rdnsConfirmDelay
	rdnsConfirmDelay = 0
	t.Cleanup(func() { rdnsConfirmDelay = old })
}

func TestRDNSSet_ReadBackFails(t *testing.T) {
	// If read-back never succeeds, the set is not verifiably in effect, so the
	// command must exit non-zero after exhausting its retries.
	fastRDNSConfirm(t)
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			gets++
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"rdns entry not found"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	err := rdnsSet([]string{"203.0.113.10", "server.example.com"}, &buf)
	if err == nil {
		t.Fatal("rdnsSet() error = nil, want error when read-back never confirms")
	}
	if !strings.Contains(err.Error(), "could not be confirmed") {
		t.Errorf("error should mention confirmation failure, got: %v", err)
	}
	if gets != rdnsConfirmAttempts {
		t.Errorf("read-back attempts = %d, want %d (should retry)", gets, rdnsConfirmAttempts)
	}
}

func TestRDNSSet_ReadBackMismatch(t *testing.T) {
	// A persistently different read-back value means the requested PTR is not in
	// effect, so the command exits non-zero after retrying.
	fastRDNSConfirm(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			// Read-back returns a different hostname than what was set.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"rdns":"other.example.com"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	err := rdnsSet([]string{"203.0.113.10", "server.example.com"}, &buf)
	if err == nil {
		t.Fatal("rdnsSet() error = nil, want mismatch error")
	}
	if !strings.Contains(err.Error(), "did not match") {
		t.Errorf("error should mention read-back mismatch, got: %v", err)
	}
	if !strings.Contains(err.Error(), "server.example.com") || !strings.Contains(err.Error(), "other.example.com") {
		t.Errorf("error should name both hostnames, got: %v", err)
	}
}

// TestRDNSSet_ReadBackEventuallyConsistent proves the retry loop absorbs
// netcup's asynchronous provisioning: the first read-back returns the old
// (null) value, a later one returns the requested hostname, and the command
// succeeds.
func TestRDNSSet_ReadBackEventuallyConsistent(t *testing.T) {
	fastRDNSConfirm(t)
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			gets++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if gets < 3 {
				// Not provisioned yet.
				_, _ = w.Write([]byte(`{"rdns":null}`))
				return
			}
			_, _ = w.Write([]byte(`{"rdns":"server.example.com"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := rdnsSet([]string{"203.0.113.10", "server.example.com"}, &buf); err != nil {
		t.Fatalf("rdnsSet() error = %v, want success once read-back becomes consistent", err)
	}
	if gets != 3 {
		t.Errorf("read-back attempts = %d, want 3 (stops once confirmed)", gets)
	}
	if out := buf.String(); !strings.Contains(out, "server.example.com") {
		t.Errorf("output should show the confirmed value, got: %q", out)
	}
}

// TestRDNSSet_ReadBackNormalized covers the common case where the API stores a
// PTR and echoes it back canonicalized (lowercased, trailing dot). This must
// be treated as a match, not a spurious mismatch.
func TestRDNSSet_ReadBackNormalized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// Canonicalized: lowercased and with a trailing FQDN dot.
			_, _ = w.Write([]byte(`{"rdns":"server.example.com."}`))
		}
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := rdnsSet([]string{"203.0.113.10", "Server.Example.COM"}, &buf); err != nil {
		t.Fatalf("rdnsSet() error = %v, want success for normalized read-back", err)
	}
	out := buf.String()
	if !strings.Contains(out, "server.example.com.") {
		t.Errorf("output should show the read-back value, got: %q", out)
	}
}

func TestRDNSHostnamesEqual(t *testing.T) {
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
		if got := rdnsHostnamesEqual(c.a, c.b); got != c.want {
			t.Errorf("rdnsHostnamesEqual(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
