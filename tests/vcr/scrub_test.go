package vcr

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/dnaeon/go-vcr/cassette"
	"gopkg.in/yaml.v2"
)

// jwtShapePattern matches an "eyJ..." base64url segment followed by two more
// dot-separated segments — the shape of a JWT (header.payload.signature),
// regardless of where it appears in a body.
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

// tokenFieldJSONPattern matches a JSON access_token/refresh_token field.
var tokenFieldJSONPattern = regexp.MustCompile(`"(access_token|refresh_token)"\s*:\s*"([^"]*)"`)

var (
	_, fakeIPv4CIDR, _ = net.ParseCIDR("203.0.113.0/24")
	_, fakeIPv6CIDR, _ = net.ParseCIDR("2001:db8::/32")
)

// TestCassettesAreScrubbed is an independent guard: it scans every committed
// cassette for anything that looks like it slipped through unredacted. It
// deliberately doesn't share code with the redaction hook (redact.go) it's
// checking — no shared JSON-walking logic, no calling the hook to compute an
// "expected" value to diff against — it re-derives "is this OK" from first
// principles (RFC 5737/3849 ranges, JWT shape, the synthetic userId/token
// placeholder constants) instead. It does parse the cassette with the same
// go-vcr cassette.Cassette struct the recorder itself uses, so it inspects
// exactly the fields go-vcr actually persists (e.g. Request.Form, a second,
// independent copy of form-encoded fields alongside Request.Body) rather
// than guessing at the on-disk YAML shape via regex. Runs in PR CI: no
// credentials, no network, no dependency on VCR_RECORD.
func TestCassettesAreScrubbed(t *testing.T) {
	var matches []string
	for _, pattern := range []string{
		"testdata/cassettes/*.yaml",
		"../../internal/provider/testdata/cassettes/*.yaml",
	} {
		m, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		matches = append(matches, m...)
	}
	if len(matches) == 0 {
		t.Fatal("no cassette files found — glob patterns matched zero files")
	}

	for _, path := range matches {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			var c cassette.Cassette
			if err := yaml.Unmarshal(data, &c); err != nil {
				t.Fatalf("parse cassette YAML: %v", err)
			}

			for _, ia := range c.Interactions {
				checkNoAuthorizationHeader(t, ia)
				checkFormValuesAreSynthetic(t, ia)
				for _, body := range []string{ia.Request.Body, ia.Response.Body} {
					checkNoJWTShape(t, body)
					checkIPv4sInRange(t, body)
					checkIPv6sInRange(t, body)
					checkUserIDsAreSynthetic(t, body)
					checkTokenFieldsAreSynthetic(t, body)
				}
				checkIPv4sInRange(t, ia.URL)
				checkIPv6sInRange(t, ia.URL)
			}
		})
	}
}

func checkNoAuthorizationHeader(t *testing.T, ia *cassette.Interaction) {
	t.Helper()
	if _, ok := ia.Request.Headers["Authorization"]; ok {
		t.Errorf("unscrubbed Authorization header on request to %s", ia.URL)
	}
}

// checkFormValuesAreSynthetic covers cassette.Interaction.Request.Form —
// go-vcr's recorder parses every request with http.Request.ParseForm and
// stores the result here, a second, independent copy of any form-encoded
// token alongside Request.Body's serialized string.
func checkFormValuesAreSynthetic(t *testing.T, ia *cassette.Interaction) {
	t.Helper()
	for k, values := range ia.Form {
		if !tokenLikeKeys[k] {
			continue
		}
		for _, v := range values {
			if v != "" && v != redactedTokenPlaceholder {
				t.Errorf("found a non-synthetic %s in request.form: %q", k, truncate(v, 20))
			}
		}
	}
}

func checkNoJWTShape(t *testing.T, body string) {
	t.Helper()
	for _, m := range jwtShapePattern.FindAllString(body, -1) {
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

func checkUserIDsAreSynthetic(t *testing.T, body string) {
	t.Helper()
	for _, m := range userIDFieldPattern.FindAllStringSubmatch(body, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n != fakeUserIDValue {
			t.Errorf("found a non-synthetic userId: %s (want %d)", m[0], fakeUserIDValue)
		}
	}
}

// checkTokenFieldsAreSynthetic catches an access_token/refresh_token leak
// directly in a body string — needed alongside checkFormValuesAreSynthetic
// (which only sees Request.Form) because: (a) OIDC responses carry the
// token in a JSON Response.Body, not a form, and (b) a real token isn't
// necessarily JWT-shaped, so checkNoJWTShape alone can't be relied on to
// catch an opaque one.
func checkTokenFieldsAreSynthetic(t *testing.T, body string) {
	t.Helper()
	for _, m := range tokenFieldJSONPattern.FindAllStringSubmatch(body, -1) {
		key, val := m[1], m[2]
		if val != "" && val != redactedTokenPlaceholder {
			t.Errorf("found a non-synthetic %s in JSON body: %q", key, truncate(val, 20))
		}
	}
	if values, err := url.ParseQuery(body); err == nil {
		for k, vs := range values {
			if !tokenLikeKeys[k] {
				continue
			}
			for _, v := range vs {
				if v != "" && v != redactedTokenPlaceholder {
					t.Errorf("found a non-synthetic %s in form body: %q", k, truncate(v, 20))
				}
			}
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
