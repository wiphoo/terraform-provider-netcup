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
	"time"
)

// rdnsConfirmAttempts and rdnsConfirmDelay bound the read-back confirmation
// performed by ConfirmRDNS. netcup applies PTR changes asynchronously, so
// ConfirmRDNS re-reads across this window before giving up. They are package
// variables so tests can shrink the delay.
var (
	rdnsConfirmAttempts = 5
	rdnsConfirmDelay    = 1 * time.Second
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

// canonicalizeIP validates ip and returns its canonical form (RFC 5952 for
// IPv6; IPv4-in-IPv6 addresses are unmapped to dotted-quad) along with the
// address family ("ipv4" or "ipv6") used to route to the correct rDNS
// endpoint. Zone identifiers are rejected since the SCP API has no use for
// them.
func canonicalizeIP(ip string) (canonical, family string, err error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "", "", fmt.Errorf("invalid IP address %q: %w", ip, err)
	}
	if addr.Zone() != "" {
		return "", "", fmt.Errorf("invalid IP address %q: zone identifiers are not supported", ip)
	}

	// Unmap so an IPv4-in-IPv6 address (::ffff:a.b.c.d) is treated as IPv4 and
	// canonicalized to its dotted-quad form, matching the ipv4 route.
	addr = addr.Unmap()

	family = "ipv6"
	if addr.Is4() {
		family = "ipv4"
	}
	return addr.String(), family, nil
}

// GetRDNS calls GET /v1/rdns/{ipv4|ipv6}/{ip} and returns the current reverse
// DNS entry for the IP address.
//
// The IP is validated and canonicalized (RFC 5952 for IPv6) before the API call.
// When no custom PTR is set the API returns {"rdns": null}; the returned
// RdnsEntry has an empty Hostname (no error).
func (c *Client) GetRDNS(ctx context.Context, ip string) (*RdnsEntry, error) {
	canonical, family, err := canonicalizeIP(ip)
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/v1/rdns/%s/%s", family, canonical)
	req, err := c.newRequest(ctx, http.MethodGet, path, "application/json", nil, true)
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
	canonical, family, err := canonicalizeIP(ip)
	if err != nil {
		return nil, err
	}

	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return nil, fmt.Errorf("hostname must not be empty")
	}

	encoded, err := json.Marshal(rdnsSetRequest{IP: canonical, Rdns: hostname})
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/v1/rdns/%s", family)
	req, err := c.newRequest(ctx, http.MethodPost, path, "application/json", bytes.NewReader(encoded), true)
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

// DeleteRDNS calls DELETE /v1/rdns/{ipv4|ipv6}/{ip} to remove the custom
// reverse DNS entry (PTR record) for the given IP address. The address
// reverts to its provider-default PTR.
//
// The IP is validated and canonicalized (RFC 5952 for IPv6) before the API
// call. A 204 No Content response is success; any other status code surfaces
// as an *APIError.
func (c *Client) DeleteRDNS(ctx context.Context, ip string) error {
	canonical, family, err := canonicalizeIP(ip)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/v1/rdns/%s/%s", family, canonical)
	req, err := c.newRequest(ctx, http.MethodDelete, path, "application/json", nil, true)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return newAPIError(resp)
	}
	// Drain any response body so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// ConfirmRDNS re-reads the PTR for ip until it matches expected.Hostname,
// absorbing netcup's asynchronous PTR provisioning delay. It returns the
// confirmed entry on success, or an error if the requested PTR could not be
// confirmed within the retry window (so an unverified set is not reported as
// successful).
//
// The wait between attempts honors ctx: if ctx is done before the next
// attempt, ConfirmRDNS returns ctx.Err() immediately instead of blocking for
// the full delay.
func (c *Client) ConfirmRDNS(ctx context.Context, ip string, expected *RdnsEntry) (*RdnsEntry, error) {
	var lastErr error
	var lastHostname string
	for attempt := 0; attempt < rdnsConfirmAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(rdnsConfirmDelay):
			}
		}
		readBack, err := c.GetRDNS(ctx, ip)
		if err != nil {
			lastErr = err
			continue
		}
		if rdnsHostnamesEqual(readBack.Hostname, expected.Hostname) {
			return readBack, nil
		}
		lastErr = nil
		lastHostname = readBack.Hostname
	}
	if lastErr != nil {
		return nil, fmt.Errorf("rdns set succeeded but could not be confirmed after %d attempts: %w", rdnsConfirmAttempts, lastErr)
	}
	return nil, fmt.Errorf("rdns set succeeded but read-back did not match after %d attempts: set %q, got %q", rdnsConfirmAttempts, expected.Hostname, lastHostname)
}

// rdnsHostnamesEqual reports whether two reverse-DNS hostnames are equivalent.
// PTR values are FQDNs: DNS names are case-insensitive and the API may return a
// canonicalized form with a trailing dot, so both are ignored (along with
// surrounding whitespace) when confirming a read-back.
func rdnsHostnamesEqual(a, b string) bool {
	return normalizeRDNSHostname(a) == normalizeRDNSHostname(b)
}

func normalizeRDNSHostname(h string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
}
