package netcup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestGetServerSuccess(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers/123" {
			t.Errorf("path = %q, want /v1/servers/123", r.URL.Path)
		}
		if v := r.Header.Get("Accept"); v != "application/json" {
			t.Errorf("Accept = %q, want application/json", v)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id":123,
			"name":"web-01",
			"hostname":"web-01.example.com",
			"disabled":false,
			"template":{"id":10,"name":"VM 2000"},
			"serverLiveInfo":{"state":"running"},
			"architecture":"AMD64",
			"site":{"id":1,"city":"Nuremberg"},
			"ipv4Addresses":[{"id":1,"ip":"192.0.2.10","netmask":"255.255.255.0"}],
			"ipv6Addresses":[{"id":2,"networkPrefix":"2a03:4000:6:b1d::","networkPrefixLength":64}]
		}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	server, err := c.GetServer(context.Background(), 123)
	if err != nil {
		t.Fatalf("GetServer() error = %v", err)
	}
	if server.ID != 123 || server.Name != "web-01" {
		t.Errorf("server = %+v, want ID=123 Name=web-01", server)
	}
	if server.Hostname == nil || *server.Hostname != "web-01.example.com" {
		t.Errorf("Hostname = %v, want web-01.example.com", server.Hostname)
	}
	if server.Template == nil || server.Template.Name != "VM 2000" {
		t.Errorf("Template = %+v, want Name=VM 2000", server.Template)
	}
	if server.ServerLiveInfo == nil || server.ServerLiveInfo.State != "running" {
		t.Errorf("ServerLiveInfo = %+v, want State=running", server.ServerLiveInfo)
	}
	if len(server.IPv4Addresses) != 1 || server.IPv4Addresses[0].IP != "192.0.2.10" {
		t.Errorf("IPv4Addresses = %+v, want one 192.0.2.10", server.IPv4Addresses)
	}
	if len(server.IPv6Addresses) != 1 || server.IPv6Addresses[0].NetworkPrefix != "2a03:4000:6:b1d::" || server.IPv6Addresses[0].NetworkPrefixLength != 64 {
		t.Errorf("IPv6Addresses = %+v, want one 2a03:4000:6:b1d::/64", server.IPv6Addresses)
	}
	if server.Site == nil || server.Site.City != "Nuremberg" {
		t.Errorf("Site = %+v, want City=Nuremberg", server.Site)
	}
	if want := "Bearer tok123"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

// TestGetServerDecodesRealDetailPayload decodes a full, real-shaped response
// captured from GET /v1/servers/{id}. It guards against struct/API type
// mismatches — e.g. `site` is an object, not a string; before that field was
// modeled correctly this test failed with an unmarshal error. The Server struct
// intentionally models a subset of fields, so extra keys (the many
// serverLiveInfo details) must decode without error rather than being rejected.
func TestGetServerDecodesRealDetailPayload(t *testing.T) {
	body, err := os.ReadFile("testdata/server_detail.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	server, err := c.GetServer(context.Background(), 123456)
	if err != nil {
		t.Fatalf("GetServer() error = %v", err)
	}

	if server.Site == nil || server.Site.City != "Nuremberg" || server.Site.ID != 1 {
		t.Errorf("Site = %+v, want {ID:1 City:Nuremberg}", server.Site)
	}
	if server.Architecture == nil || *server.Architecture != "AMD64" {
		t.Errorf("Architecture = %v, want AMD64", server.Architecture)
	}
	if server.ServerLiveInfo == nil || server.ServerLiveInfo.State != "RUNNING" {
		t.Errorf("ServerLiveInfo = %+v, want State=RUNNING", server.ServerLiveInfo)
	}
	if server.Template == nil || server.Template.Name != "VPS Lite 1 G12s" {
		t.Errorf("Template = %+v, want Name=VPS Lite 1 G12s", server.Template)
	}
	if len(server.IPv4Addresses) != 1 || server.IPv4Addresses[0].IP != "192.0.2.10" ||
		server.IPv4Addresses[0].Gateway == nil || *server.IPv4Addresses[0].Gateway != "192.0.2.1" ||
		server.IPv4Addresses[0].Broadcast == nil || *server.IPv4Addresses[0].Broadcast != "192.0.2.255" {
		t.Errorf("IPv4Addresses = %+v, want one 192.0.2.10 with gateway/broadcast", server.IPv4Addresses)
	}
	if len(server.IPv6Addresses) != 1 || server.IPv6Addresses[0].NetworkPrefix != "2a03:4000:2:8f7::" ||
		server.IPv6Addresses[0].NetworkPrefixLength != 64 {
		t.Errorf("IPv6Addresses = %+v, want one 2a03:4000:2:8f7::/64", server.IPv6Addresses)
	}
}

func TestGetServerNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"server not found"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.GetServer(context.Background(), 999)
	if err == nil {
		t.Fatal("GetServer() error = nil, want error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
}

func TestGetServerHandlesNullLiveInfoAndAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":5,"name":"db-01","disabled":true,"serverLiveInfo":null}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	server, err := c.GetServer(context.Background(), 5)
	if err != nil {
		t.Fatalf("GetServer() error = %v", err)
	}
	if server.ServerLiveInfo != nil {
		t.Errorf("ServerLiveInfo = %+v, want nil", server.ServerLiveInfo)
	}
	if len(server.IPv4Addresses) != 0 || len(server.IPv6Addresses) != 0 {
		t.Errorf("addresses = v4:%+v v6:%+v, want both empty", server.IPv4Addresses, server.IPv6Addresses)
	}
	if !server.Disabled {
		t.Error("Disabled = false, want true")
	}
}
