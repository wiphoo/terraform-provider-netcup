package netcup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ServerListMinimal is the per-server projection returned by GET /v1/servers.
// It contains no IP addresses, power state, or product detail beyond the
// template name. Use GET /v1/servers/{serverId} for richer fields.
type ServerListMinimal struct {
	ID       int32                  `json:"id"`
	Name     string                 `json:"name"`
	Hostname *string                `json:"hostname"`
	Nickname *string                `json:"nickname"`
	Disabled bool                   `json:"disabled"`
	Template *ServerTemplateMinimal `json:"template"`
}

// ServerTemplateMinimal is the template object embedded in ServerListMinimal.
type ServerTemplateMinimal struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
}

// Server is the detail projection returned by GET /v1/servers/{serverId}. Unlike
// ServerListMinimal it carries power state (ServerLiveInfo) and IP addresses.
// ServerLiveInfo and the address slices may be nil/empty depending on the server.
type Server struct {
	ID       int32                  `json:"id"`
	Name     string                 `json:"name"`
	Hostname *string                `json:"hostname"`
	Nickname *string                `json:"nickname"`
	Disabled bool                   `json:"disabled"`
	Template *ServerTemplateMinimal `json:"template"`

	// ServerLiveInfo holds the live power state; nil when unavailable.
	ServerLiveInfo *ServerInfo `json:"serverLiveInfo"`

	IPv4Addresses []IPv4AddressMinimal `json:"ipv4Addresses"`
	IPv6Addresses []IPv6AddressMinimal `json:"ipv6Addresses"`

	Architecture *string     `json:"architecture"`
	Site         *ServerSite `json:"site"`
}

// ServerSite is the datacenter location embedded in Server.
type ServerSite struct {
	ID   int32  `json:"id"`
	City string `json:"city"`
}

// ServerInfo is the live status object embedded in Server. State is the power
// state (e.g. "running", "stopped").
type ServerInfo struct {
	State string `json:"state"`
}

// IPv4AddressMinimal is one entry of Server.IPv4Addresses.
type IPv4AddressMinimal struct {
	ID        int32   `json:"id"`
	IP        string  `json:"ip"`
	Netmask   string  `json:"netmask"`
	Gateway   *string `json:"gateway"`
	Broadcast *string `json:"broadcast"`
}

// IPv6AddressMinimal is one entry of Server.IPv6Addresses. An address is
// NetworkPrefix/NetworkPrefixLength (e.g. 2001:db8:6:b1d::/64).
type IPv6AddressMinimal struct {
	ID                  int32   `json:"id"`
	NetworkPrefix       string  `json:"networkPrefix"`
	NetworkPrefixLength int32   `json:"networkPrefixLength"`
	Gateway             *string `json:"gateway"`
}

// GetServer calls GET /v1/servers/{id} and returns the server's detail. An
// unknown ID surfaces as an *APIError with StatusCode 404.
func (c *Client) GetServer(ctx context.Context, id int32) (*Server, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/v1/servers/%d", id), "application/json", nil, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return nil, newAPIError(resp)
	}

	var server Server
	if err := json.NewDecoder(resp.Body).Decode(&server); err != nil {
		return nil, err
	}
	// Drain any trailing bytes so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)
	return &server, nil
}

// ListServers calls GET /v1/servers and returns the account's servers. An empty
// slice is a valid response (no error).
func (c *Client) ListServers(ctx context.Context) ([]ServerListMinimal, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/v1/servers", "application/json", nil, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return nil, newAPIError(resp)
	}

	var servers []ServerListMinimal
	if err := json.NewDecoder(resp.Body).Decode(&servers); err != nil {
		return nil, err
	}
	// Drain any trailing bytes so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)
	return servers, nil
}
