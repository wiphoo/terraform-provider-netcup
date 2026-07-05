package vcr

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// fakeUserIDValue is the single synthetic userId every real userId is
// rewritten to, regardless of the real value. Unlike IPs/hostnames (which
// can legitimately differ per server/PTR within one cassette), a recorded
// account only ever has one userId, so there is nothing to distinguish and
// no need to hash it.
const fakeUserIDValue = 10001

// redactedTokenPlaceholder replaces every access_token/refresh_token value.
// It deliberately isn't JWT-shaped (no dots, doesn't start with "eyJ") so it
// can never trip TestCassettesAreScrubbed's own JWT-shape detector.
const redactedTokenPlaceholder = "vcr-redacted-token"

// fakeHostnameDomain is the fixed domain every hostname/nickname/PTR is
// rewritten under.
const fakeHostnameDomain = "example.com"

// fakeIPv4Prefix is RFC 5737's TEST-NET-3 (203.0.113.0/24).
var fakeIPv4Prefix = [3]byte{203, 0, 113}

// fakeIPv6Prefix is RFC 3849's documentation range (2001:db8::/32).
var fakeIPv6Prefix = [4]byte{0x20, 0x01, 0x0d, 0xb8}

// hashBytes is the sole source of "randomness" for every fake* function:
// same real input always produces the same digest, so re-recording the same
// account yields identical cassettes (order-independent, no shared state).
func hashBytes(s string) [32]byte {
	return sha256.Sum256([]byte(s))
}

// fakeIPv4 deterministically maps a real IPv4 address into RFC 5737's
// 203.0.113.0/24 documentation range. Non-IPv4 input is returned unchanged
// (pure functions don't panic on unexpected shapes).
func fakeIPv4(real string) string {
	ip := net.ParseIP(real)
	if ip == nil || ip.To4() == nil {
		return real
	}
	h := hashBytes("ipv4:" + real)
	return fmt.Sprintf("%d.%d.%d.%d", fakeIPv4Prefix[0], fakeIPv4Prefix[1], fakeIPv4Prefix[2], h[0])
}

// fakeIPv6 deterministically maps a real IPv6 address (or bare network
// prefix such as "2a03:4000:2:8f7::") into RFC 3849's 2001:db8::/32
// documentation range. Non-IPv6 input is returned unchanged.
func fakeIPv6(real string) string {
	addr, err := netip.ParseAddr(real)
	if err != nil {
		return real
	}
	addr = addr.Unmap()
	if !addr.Is6() {
		return real
	}
	h := hashBytes("ipv6:" + real)
	b := addr.As16()
	copy(b[0:4], fakeIPv6Prefix[:])
	copy(b[4:16], h[:12])
	return netip.AddrFrom16(b).String()
}

// fakeHostname deterministically maps a real hostname/nickname/PTR value
// into a synthetic FQDN under fakeHostnameDomain. An empty string (no
// nickname set / no custom PTR) is meaningful state, not PII, and is passed
// through unchanged.
func fakeHostname(real string) string {
	if real == "" {
		return real
	}
	h := hashBytes("hostname:" + real)
	return fmt.Sprintf("host-%x.%s", h[:4], fakeHostnameDomain)
}

// isIPv4Netmask reports whether ip is a syntactically valid subnet mask (a
// contiguous run of 1 bits followed by 0 bits, e.g. 255.255.255.0) rather
// than a routable address. Netmasks are structurally IPv4-shaped but never
// identify an account — there are only 33 possible values and they're
// common to every IPv4 network — so they're preserved as-is instead of
// routed into the RFC 5737 range, and TestCassettesAreScrubbed's guard
// allowlists them for the same reason.
func isIPv4Netmask(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	v := uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3])
	ones := 0
	for ones < 32 && v&(1<<(31-ones)) != 0 {
		ones++
	}
	var mask uint32
	if ones < 32 {
		mask = ^uint32(0) << (32 - ones)
	} else {
		mask = ^uint32(0)
	}
	return v == mask
}

// ipLikeKeys are JSON object keys holding an IPv4 or IPv6 address, rewritten
// wherever they appear regardless of nesting depth. The address family is
// detected from the value itself (net.ParseIP), not the key name, since
// "gateway" is reused by both ipv4Addresses[] and ipv6Addresses[]. A value
// can be a bare address string, a CIDR string (address + "/" + prefix
// length, e.g. serverLiveInfo.interfaces[].ipv6NetworkPrefixes), or an array
// of either — serverLiveInfo.interfaces[] (not modeled by any Go struct;
// ServerInfo only decodes "state") carries real addresses in exactly this
// shape, so the key list has to cover it even though it's absent from the
// substitution table's Server-level field names.
var ipLikeKeys = map[string]bool{
	"ip": true, "gateway": true, "broadcast": true, "networkPrefix": true,
	"ipv4Addresses": true, "ipv6Addresses": true,
	"ipv6LinkLocalAddresses": true, "ipv6NetworkPrefixes": true,
}

// hostnameLikeKeys are JSON object keys holding a hostname/nickname/PTR
// value.
var hostnameLikeKeys = map[string]bool{
	"hostname": true, "nickname": true, "rdns": true,
}

// tokenLikeKeys are JSON object keys (and form field names) holding an OIDC
// access or refresh token value.
var tokenLikeKeys = map[string]bool{
	"access_token": true, "refresh_token": true,
}

// redactBody rewrites known-sensitive fields in a recorded request/response
// body. It tolerates bodies that are neither JSON nor form-encoded (e.g. the
// hand-authored TestRecorderReplay fixture's plain-text "OK") by returning
// them unchanged.
func redactResponseBody(body string) string {
	if redacted, ok := redactJSONBody(body); ok {
		return redacted
	}
	return body
}

// redactRequestBody is like redactResponseBody but also handles
// application/x-www-form-urlencoded request bodies (the OIDC device/token
// endpoints), gated on contentType so a plain-text body is never
// misinterpreted as an (almost always "successfully parseable") form body.
func redactRequestBody(contentType, body string) string {
	if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		return redactFormBody(body)
	}
	if redacted, ok := redactJSONBody(body); ok {
		return redacted
	}
	return body
}

// redactJSONBody parses body as generic JSON, recursively rewrites known
// sensitive fields, and re-marshals it. ok is false when body doesn't parse
// as JSON at all, in which case body should be left untouched by the caller.
func redactJSONBody(body string) (result string, ok bool) {
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return "", false
	}

	redacted := redactValue(v)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(redacted); err != nil {
		return "", false
	}
	return strings.TrimRight(buf.String(), "\n"), true
}

// redactValue recursively walks parsed JSON, applying redactField to every
// object member. Recursing into the child before redacting it means
// redactField only ever needs to handle the child's own scalar value.
func redactValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, child := range val {
			out[k] = redactField(k, redactValue(child))
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, child := range val {
			out[i] = redactValue(child)
		}
		return out
	default:
		return val
	}
}

// redactField applies the substitution for key, if key is sensitive, to an
// already-recursed child value. Fields not in any of the known key sets
// (id, name, template.*, site.*, disabled, state, architecture, netmask,
// ...) pass through unchanged.
func redactField(key string, val interface{}) interface{} {
	switch {
	case ipLikeKeys[key]:
		return redactIPValue(val)
	case hostnameLikeKeys[key]:
		s, ok := val.(string)
		if !ok || s == "" {
			return val
		}
		return fakeHostname(s)
	case tokenLikeKeys[key]:
		s, ok := val.(string)
		if !ok || s == "" {
			return val
		}
		return redactedTokenPlaceholder
	case key == "userId":
		return fakeUserIDValue
	default:
		return val
	}
}

// redactIPValue redacts an ipLikeKeys value, which can be a bare address
// string, a CIDR string, or an array of either. Array elements that aren't
// strings (e.g. Server.IPv4Addresses[]'s {id, ip, netmask, ...} objects,
// which reuse the "ipv4Addresses" key name) are left as-is: they were
// already redacted by their own inner keys during the recursive walk.
func redactIPValue(val interface{}) interface{} {
	switch v := val.(type) {
	case string:
		if v == "" {
			return val
		}
		return redactIPOrCIDRString(v)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, elem := range v {
			if s, ok := elem.(string); ok && s != "" {
				out[i] = redactIPOrCIDRString(s)
			} else {
				out[i] = elem
			}
		}
		return out
	default:
		return val
	}
}

// redactIPOrCIDRString redacts the address portion of s, which may be a
// bare address ("2a03:4000:2:8f7::") or a CIDR ("2a03:4000:2:8f7::/64") —
// serverLiveInfo.interfaces[].ipv6NetworkPrefixes uses the latter shape. The
// prefix length suffix, if present, is preserved unchanged: it's a subnet
// size, not an identifying value.
func redactIPOrCIDRString(s string) string {
	if addr, prefixLen, found := strings.Cut(s, "/"); found {
		return redactIPString(addr) + "/" + prefixLen
	}
	return redactIPString(s)
}

// redactIPString maps a real IPv4 or IPv6 address string into its
// corresponding RFC 5737 / RFC 3849 documentation range. A value that
// doesn't parse as an IP is returned unchanged (pure functions don't panic
// on unexpected input).
func redactIPString(s string) string {
	ip := net.ParseIP(s)
	if ip == nil {
		return s
	}
	if ip.To4() != nil {
		return fakeIPv4(s)
	}
	return fakeIPv6(s)
}

// redactFormBody rewrites tokenLikeKeys values in an
// application/x-www-form-urlencoded body (the OIDC device/token endpoints'
// request bodies, which are form- not JSON-encoded).
func redactFormBody(body string) string {
	values, err := url.ParseQuery(body)
	if err != nil {
		return body
	}
	redactFormValues(values)
	return values.Encode()
}

// redactFormValues rewrites tokenLikeKeys values in place. go-vcr's recorder
// parses every request with http.Request.ParseForm and stores the result as
// cassette.Interaction.Request.Form — a second, independent copy of any
// form-encoded token alongside Request.Body's serialized string — so this
// must be applied to both, not just the body.
func redactFormValues(values url.Values) {
	for k := range values {
		if tokenLikeKeys[k] {
			values.Set(k, redactedTokenPlaceholder)
		}
	}
}

// rdnsURLPattern matches the rDNS endpoints' URL path, which embeds the
// real IP address directly (e.g. /v1/rdns/ipv4/203.0.113.5): the only
// production request URL shape carrying an address outside a JSON/form
// body.
var rdnsURLPattern = regexp.MustCompile(`^(.*/v1/rdns/(ipv4|ipv6)/)([^/?]+)(.*)$`)

// redactURL rewrites the IP embedded in an rDNS endpoint URL. URLs that
// don't match the rDNS path shape (e.g. /v1/servers/{id}, whose id is
// preserved as-is) are returned unchanged.
func redactURL(rawURL string) string {
	m := rdnsURLPattern.FindStringSubmatch(rawURL)
	if m == nil {
		return rawURL
	}
	prefix, family, ip, suffix := m[1], m[2], m[3], m[4]
	var fake string
	if family == "ipv4" {
		fake = fakeIPv4(ip)
	} else {
		fake = fakeIPv6(ip)
	}
	return prefix + fake + suffix
}
