package vcr

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// jwtShapePattern matches an "eyJ..." base64url segment followed by two more
// dot-separated segments — the shape of a JWT (header.payload.signature),
// regardless of where it appears (Authorization header, OIDC response body).
var jwtShapePattern = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}`)

// ipv4LikePattern and ipv6LikePattern are deliberately loose: candidates are
// validated with net.ParseIP below, so a loose regex only risks harmless
// false candidates (which ParseIP then rejects), never false negatives from
// being too strict.
var (
	ipv4LikePattern = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	ipv6LikePattern = regexp.MustCompile(`[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{0,4}){2,7}`)
)

// userIDFieldPattern matches a JSON "userId" field with a bare numeric
// value, e.g. `"userId":10001` or `"userId": 10001`.
var userIDFieldPattern = regexp.MustCompile(`"userId"\s*:\s*(-?[0-9]+)`)

// authorizationHeaderLinePattern matches an "Authorization:" YAML header
// entry — the AddFilter hook in recorder.go deletes this key entirely, so a
// properly scrubbed cassette should never contain this pattern at all.
var authorizationHeaderLinePattern = regexp.MustCompile(`(?i)^\s*Authorization:`)

var (
	_, fakeIPv4CIDR, _ = net.ParseCIDR("203.0.113.0/24")
	_, fakeIPv6CIDR, _ = net.ParseCIDR("2001:db8::/32")
)

// TestCassettesAreScrubbed is an independent guard: it scans every committed
// cassette's raw bytes for anything that looks like it slipped through
// unredacted. It deliberately doesn't share code with the redaction hook
// (redact.go) it's checking — it re-derives "is this OK" from first
// principles (RFC 5737/3849 ranges, JWT shape, the synthetic userId
// constant) rather than asserting the hook's own output, so a bug in the
// hook's key list can't also blind this test. Runs in PR CI: no
// credentials, no network, no dependency on VCR_RECORD.
func TestCassettesAreScrubbed(t *testing.T) {
	matches, err := filepath.Glob("testdata/cassettes/*.yaml")
	if err != nil {
		t.Fatalf("glob cassettes: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no cassette files found under testdata/cassettes/ — glob pattern is likely wrong")
	}

	for _, path := range matches {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			text := string(data)

			checkNoAuthorizationHeader(t, text)
			checkNoJWTShape(t, text)
			checkIPv4sInRange(t, text)
			checkIPv6sInRange(t, text)
			checkUserIDsAreSynthetic(t, text)
		})
	}
}

func checkNoAuthorizationHeader(t *testing.T, text string) {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if authorizationHeaderLinePattern.MatchString(line) {
			t.Errorf("unscrubbed Authorization header: %q", strings.TrimSpace(line))
		}
	}
}

func checkNoJWTShape(t *testing.T, text string) {
	t.Helper()
	for _, m := range jwtShapePattern.FindAllString(text, -1) {
		t.Errorf("found a JWT-shaped string (Bearer eyJ...): %q", truncate(m, 40))
	}
}

func checkIPv4sInRange(t *testing.T, text string) {
	t.Helper()
	for _, m := range ipv4LikePattern.FindAllString(text, -1) {
		ip := net.ParseIP(m)
		if ip == nil || ip.To4() == nil {
			continue
		}
		if isIPv4Netmask(ip) {
			continue // netmasks (255.255.255.0, ...) are structurally IPv4 but not PII
		}
		if !fakeIPv4CIDR.Contains(ip) {
			t.Errorf("found an IPv4 address outside RFC 5737 (203.0.113.0/24): %s", m)
		}
	}
}

func checkIPv6sInRange(t *testing.T, text string) {
	t.Helper()
	for _, m := range ipv6LikePattern.FindAllString(text, -1) {
		ip := net.ParseIP(m)
		if ip == nil || ip.To4() != nil {
			continue // not a valid/complete IPv6 literal (e.g. a MAC address, a truncated match)
		}
		if !fakeIPv6CIDR.Contains(ip) {
			t.Errorf("found an IPv6 address outside RFC 3849 (2001:db8::/32): %s", m)
		}
	}
}

func checkUserIDsAreSynthetic(t *testing.T, text string) {
	t.Helper()
	for _, m := range userIDFieldPattern.FindAllStringSubmatch(text, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n != fakeUserIDValue {
			t.Errorf("found a non-synthetic userId: %s (want %d)", m[0], fakeUserIDValue)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
