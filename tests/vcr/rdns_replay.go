package vcr

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/dnaeon/go-vcr/cassette"
	"gopkg.in/yaml.v2"
)

// RDNSIPForTest returns the live NETCUP_TEST_IP in record mode. In replay
// mode it derives the redacted rDNS IP from the cassette being replayed, so
// cassettes regenerated with any real test IP remain immediately replayable.
func RDNSIPForTest(t *testing.T, cassetteName string) string {
	t.Helper()
	if os.Getenv("VCR_RECORD") == "1" {
		ip := os.Getenv("NETCUP_TEST_IP")
		if ip == "" {
			t.Fatal("VCR_RECORD=1 requires NETCUP_TEST_IP")
		}
		return ip
	}

	ip, err := rdnsIPFromCassetteFile(filepath.Join("testdata", "cassettes", cassetteName+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return ip
}

func rdnsIPFromCassetteFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read rDNS cassette %q: %w", path, err)
	}

	var c cassette.Cassette
	if err := yaml.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("parse rDNS cassette %q: %w", path, err)
	}
	for _, ia := range c.Interactions {
		if ip, ok := rdnsIPFromInteraction(ia); ok {
			return ip, nil
		}
	}
	return "", fmt.Errorf("rDNS cassette %q does not contain a redacted IPv4 handle", path)
}

func rdnsIPFromInteraction(ia *cassette.Interaction) (string, bool) {
	if ia == nil {
		return "", false
	}
	if ip, ok := rdnsIPFromURL(ia.Request.URL); ok {
		return ip, true
	}
	if ip, ok := rdnsIPFromJSONBody(ia.Request.Body); ok {
		return ip, true
	}
	return rdnsIPFromJSONBody(ia.Response.Body)
}

func rdnsIPFromURL(rawURL string) (string, bool) {
	m := rdnsURLPattern.FindStringSubmatch(rawURL)
	if m == nil {
		return "", false
	}
	return validIPv4(m[3])
}

func rdnsIPFromJSONBody(body string) (string, bool) {
	if body == "" {
		return "", false
	}
	var fields struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		return "", false
	}
	return validIPv4(fields.IP)
}

func validIPv4(s string) (string, bool) {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return "", false
	}
	return s, true
}
