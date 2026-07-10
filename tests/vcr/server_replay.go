package vcr

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/dnaeon/go-vcr/cassette"
	"gopkg.in/yaml.v2"
)

// serverURLPattern matches the numeric server ID embedded in a
// /v1/servers/{id} request URL (the server *detail* endpoint). The server
// *list* endpoint (/v1/servers, no trailing ID) deliberately does not match.
var serverURLPattern = regexp.MustCompile(`/v1/servers/([0-9]+)`)

// ServerIDForTest returns the live NETCUP_TEST_SERVER_ID in record mode. In
// replay mode it derives the server ID from the cassette being replayed (the
// numeric segment of a /v1/servers/{id} request URL), so cassettes regenerated
// via `make acc-record` with any real test server ID remain immediately
// replayable with no manual constant to keep in sync — the same contract
// RDNSIPForTest provides for rDNS IPs.
func ServerIDForTest(t *testing.T, cassetteName string) int32 {
	t.Helper()
	if os.Getenv("VCR_RECORD") == "1" {
		v := os.Getenv("NETCUP_TEST_SERVER_ID")
		if v == "" {
			t.Fatal("VCR_RECORD=1 requires NETCUP_TEST_SERVER_ID")
		}
		id, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			t.Fatalf("NETCUP_TEST_SERVER_ID: %v", err)
		}
		return int32(id)
	}

	id, err := serverIDFromCassetteFile(filepath.Join("testdata", "cassettes", cassetteName+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func serverIDFromCassetteFile(path string) (int32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read server cassette %q: %w", path, err)
	}

	var c cassette.Cassette
	if err := yaml.Unmarshal(data, &c); err != nil {
		return 0, fmt.Errorf("parse server cassette %q: %w", path, err)
	}
	for _, ia := range c.Interactions {
		if ia == nil {
			continue
		}
		if m := serverURLPattern.FindStringSubmatch(ia.URL); m != nil {
			id, err := strconv.ParseInt(m[1], 10, 32)
			if err != nil {
				return 0, fmt.Errorf("server cassette %q has a non-int32 server ID %q: %w", path, m[1], err)
			}
			return int32(id), nil
		}
	}
	return 0, fmt.Errorf("server cassette %q does not contain a /v1/servers/{id} request URL", path)
}
