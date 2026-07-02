package netcup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
)

// RdnsEntry is the response from the SCP reverse DNS endpoints.
type RdnsEntry struct {
	// IP is the canonical form of the requested IP address (RFC 5952 for IPv6).
	IP string `json:"ip"`
	// Hostname is the current reverse-DNS hostname (the PTR value). An empty
	// string means no custom PTR is set (the address uses its provider-default
	// reverse DNS).
	Hostname string `json:"hostname"`
}

// rdnsResponse is the raw JSON shape returned by GET /v1/rdns/{ipv4|ipv6}/{ip}.
type rdnsResponse struct {
	Rdns *string `json:"rdns"`
}

// GetRDNS calls GET /v1/rdns/{ipv4|ipv6}/{ip} and returns the current reverse
// DNS entry for the IP address.
//
// The IP is validated and canonicalized (RFC 5952 for IPv6) before the API call.
// When no custom PTR is set the API returns {"rdns": null}; the returned
// RdnsEntry has an empty Hostname (no error).
func (c *Client) GetRDNS(ctx context.Context, ip string) (*RdnsEntry, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil, fmt.Errorf("invalid IP address %q: %w", ip, err)
	}

	canonical := addr.String()

	family := "ipv6"
	if addr.Is4() || addr.Is4In6() {
		family = "ipv4"
	}

	path := fmt.Sprintf("/v1/rdns/%s/%s", family, canonical)
	req, err := c.newRequest(ctx, http.MethodGet, path, "application/json")
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

	var raw rdnsResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	// Drain any trailing bytes so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)

	hostname := ""
	if raw.Rdns != nil {
		hostname = strings.TrimSpace(*raw.Rdns)
	}

	return &RdnsEntry{
		IP:       canonical,
		Hostname: hostname,
	}, nil
}
