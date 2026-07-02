package netcup

import (
	"context"
	"encoding/json"
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

// ListServers calls GET /v1/servers and returns the account's servers. An empty
// slice is a valid response (no error).
func (c *Client) ListServers(ctx context.Context) ([]ServerListMinimal, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/v1/servers", "application/json")
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
