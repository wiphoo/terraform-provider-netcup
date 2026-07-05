package vcr

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/dnaeon/go-vcr/cassette"
	"github.com/dnaeon/go-vcr/recorder"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// clientTimeout mirrors netcup.New's own default HTTP client timeout
// (pkg/netcup/client.go's unexported defaultTimeout, so it can't be imported
// directly). NewClient must set this explicitly: WithHTTPClient below
// replaces netcup.New's default *http.Client (which carries this timeout)
// entirely, and an http.Client with a zero Timeout can hang on a stalled
// live SCP call in record mode until go test's own (much longer) timeout.
const clientTimeout = 30 * time.Second

// NewClient constructs a *netcup.Client wired to a go-vcr recorder for the
// named cassette (relative to testdata/cassettes/). When VCR_RECORD=1 the
// recorder proxies to live SCP and records the interactions; otherwise it
// replays the cassette. PII is scrubbed before the cassette is written
// (record mode only — replay mode is read-only): see redact.go for what gets
// substituted and CONTRIBUTING.md's "Redaction" section for the full table.
//
// The cassette name should match the test function name, e.g. "TestListServers".
func NewClient(t *testing.T, cassetteName string) *netcup.Client {
	t.Helper()

	mode := recorder.ModeReplaying
	if os.Getenv("VCR_RECORD") == "1" {
		mode = recorder.ModeRecording
	}

	rec, err := recorder.NewAsMode("testdata/cassettes/"+cassetteName, mode, nil)
	if err != nil {
		t.Fatalf("go-vcr recorder: %v", err)
	}

	// go-vcr v1.2.0's NewAsMode silently switches to ModeRecording when the
	// cassette file is missing, even though ModeReplaying was requested.
	// Left unchecked, a missing or typo'd cassette would issue a live SCP
	// request using the fake replay token instead of failing locally,
	// breaking the no-network PR-CI guarantee. Fail fast instead, before
	// registering Stop as a cleanup (which would otherwise persist a bogus
	// cassette from that live attempt).
	if err := checkCassetteFound(mode, rec.Mode(), cassetteName); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		// In record mode this is what actually persists the regenerated
		// cassette; a discarded error here would let make acc-record report
		// success after live API calls while silently leaving the cassette
		// file stale or missing (e.g. a read-only checkout or permission
		// error). In replay mode Stop is a no-op that always returns nil.
		if err := rec.Stop(); err != nil {
			t.Errorf("go-vcr: saving cassette %q: %v", cassetteName, err)
		}
	})

	// Scrub PII before the cassette is written: the Authorization header, plus
	// body/URL fields (IPs, hostnames, nicknames, PTRs, userId, OIDC tokens) —
	// see redact.go. The filter runs only in record mode; replay mode is
	// read-only.
	rec.AddFilter(func(i *cassette.Interaction) error {
		delete(i.Request.Headers, "Authorization")
		i.URL = redactURL(i.URL)
		i.Request.Body = redactRequestBody(i.Request.Headers.Get("Content-Type"), i.Request.Body)
		i.Response.Body = redactResponseBody(i.Response.Body)
		return nil
	})

	token := "vcr-replay-fake-token"
	if mode == recorder.ModeRecording {
		token = os.Getenv("NETCUP_ACCESS_TOKEN")
		if token == "" {
			t.Fatal("VCR_RECORD=1 requires NETCUP_ACCESS_TOKEN")
		}
	}

	return netcup.New(
		// Pinned explicitly rather than left to default resolution: netcup.New
		// falls back to the NETCUP_API_ENDPOINT env var when set, which would
		// change the request URL and break DefaultMatcher's method+URL match
		// against whatever endpoint the cassette was actually recorded from.
		netcup.WithAPIEndpoint(netcup.DefaultAPIEndpoint),
		netcup.WithHTTPClient(&http.Client{Transport: rec, Timeout: clientTimeout}),
		netcup.WithAccessToken(token),
	)
}

// checkCassetteFound returns a non-nil error when requestedMode asked for
// ModeReplaying but actualMode came back as something else — the signal that
// go-vcr v1.2.0 silently fell back to recording because the cassette file
// doesn't exist. A caller in the record path (requestedMode ==
// ModeRecording) is always fine, regardless of actualMode.
func checkCassetteFound(requestedMode, actualMode recorder.Mode, cassetteName string) error {
	if requestedMode != recorder.ModeReplaying || actualMode == recorder.ModeReplaying {
		return nil
	}
	return fmt.Errorf(
		"cassette %q not found (testdata/cassettes/%s.yaml): go-vcr would silently record a live interaction instead of replaying it; commit the cassette, or run with VCR_RECORD=1 to create it",
		cassetteName, cassetteName,
	)
}
