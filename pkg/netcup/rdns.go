package netcup

import (
	"bytes"
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

// rdnsSetRequest is the JSON body sent to POST /v1/rdns/{ipv4|ipv6}.
type rdnsSetRequest struct {
	IP   string `json:"ip"`
	Rdns string `json:"rdns"`
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
	if addr.Zone() != "" {
		return nil, fmt.Errorf("invalid IP address %q: zone identifiers are not supported", ip)
	}

	// Unmap so an IPv4-in-IPv6 address (::ffff:a.b.c.d) is treated as IPv4 and
	// canonicalized to its dotted-quad form, matching the ipv4 route.
	addr = addr.Unmap()
	canonical := addr.String()

	family := "ipv6"
	if addr.Is4() {
		family = "ipv4"
	}

	path := fmt.Sprintf("/v1/rdns/%s/%s", family, canonical)
	req, err := c.newRequest(ctx, http.MethodGet, path, "application/json", nil)
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

// SetRDNS calls POST /v1/rdns/{ipv4|ipv6} to set the reverse DNS hostname
// (PTR record) for the given IP address. The operation is an upsert: it
// creates or replaces the PTR as needed.
//
// The IP is validated and canonicalized (RFC 5952 for IPv6) before the API
// call. The hostname is trimmed of surrounding whitespace; an empty hostname
// is rejected (use DeleteRDNS to remove a PTR).
//
// The returned RdnsEntry echoes the canonical IP and the hostname that was
// sent; it does not reflect a fresh read-back. Call GetRDNS to confirm the
// value remotely.
func (c *Client) SetRDNS(ctx context.Context, ip, hostname string) (*RdnsEntry, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil, fmt.Errorf("invalid IP address %q: %w", ip, err)
	}
	if addr.Zone() != "" {
		return nil, fmt.Errorf("invalid IP address %q: zone identifiers are not supported", ip)
	}

	addr = addr.Unmap()
	canonical := addr.String()

	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return nil, fmt.Errorf("hostname must not be empty")
	}

	family := "ipv6"
	if addr.Is4() {
		family = "ipv4"
	}

	encoded, err := json.Marshal(rdnsSetRequest{IP: canonical, Rdns: hostname})
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/v1/rdns/%s", family)
	req, err := c.newRequest(ctx, http.MethodPost, path, "application/json", bytes.NewReader(encoded))
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
	// Drain any response body so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)

	return &RdnsEntry{
		IP:       canonical,
		Hostname: hostname,
	}, nil
}
