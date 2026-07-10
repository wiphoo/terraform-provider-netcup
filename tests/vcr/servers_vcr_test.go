package vcr

import (
	"context"
	"net/http"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// testNonexistentServerID is a known-nonexistent server ID for
// TestGetServer_404.yaml. It is a fixed sentinel (never a real account
// server), so unlike the server IDs derived by ServerIDForTest it needs no
// re-record sync.
const testNonexistentServerID = int32(999999999)

func TestListServers(t *testing.T) {
	client := NewClient(t, "TestListServers")
	servers, err := client.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if len(servers) == 0 {
		t.Fatal("ListServers() returned empty list, want at least 1 server")
	}
	s := servers[0]
	if s.ID == 0 {
		t.Error("servers[0].ID = 0, want non-zero")
	}
	if s.Name == "" {
		t.Error("servers[0].Name is empty")
	}
	if s.Hostname == nil || *s.Hostname == "" {
		t.Error("servers[0].Hostname is nil or empty")
	}
	if s.Template == nil || s.Template.Name == "" {
		t.Error("servers[0].Template.Name is empty")
	}
}

func TestGetServer_200(t *testing.T) {
	client := NewClient(t, "TestGetServer_200")
	server, err := client.GetServer(context.Background(), ServerIDForTest(t, "TestGetServer_200"))
	if err != nil {
		t.Fatalf("GetServer() error = %v", err)
	}
	if server.Hostname == nil || *server.Hostname == "" {
		t.Error("Hostname is nil or empty")
	}
	if server.ServerLiveInfo == nil || server.ServerLiveInfo.State == "" {
		t.Error("ServerLiveInfo.State is empty")
	}
	if server.Template == nil || server.Template.Name == "" {
		t.Error("Template.Name is empty")
	}
	if len(server.IPv4Addresses) == 0 {
		t.Error("IPv4Addresses is empty")
	}
	if len(server.IPv6Addresses) == 0 {
		t.Error("IPv6Addresses is empty")
	}
}

func TestGetServer_404(t *testing.T) {
	client := NewClient(t, "TestGetServer_404")
	_, err := client.GetServer(context.Background(), testNonexistentServerID)
	if err == nil {
		t.Fatal("GetServer() error = nil, want *netcup.APIError")
	}
	apiErr, ok := err.(*netcup.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
}
