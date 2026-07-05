package vcr

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

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

	// Replace go-vcr's default exact-URL matcher: redactInteraction rewrites
	// the IP embedded in an rDNS request URL before the cassette is saved, so
	// a replay-mode caller building its request from the real IP would
	// otherwise never match the committed, already-redacted entry. See
	// matchInteraction's doc comment (redact.go).
	rec.SetMatcher(matchInteraction)

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
	// see redact.go. Registered as a *save* filter (AddSaveFilter), not a
	// regular one (AddFilter): go-vcr's RoundTrip builds the *http.Response it
	// hands back to the code under test from the same *cassette.Interaction a
	// regular AddFilter mutates, before that response is returned — so a
	// regular filter would make record-mode tests observe the fake redacted
	// IP/hostname instead of the real live SCP response (breaking, e.g., a
	// test that reads a server's real IP and then calls the rDNS endpoint
	// with it). AddSaveFilter runs later, in Stop()/Save(), strictly after
	// every live round trip in this recording session has already returned
	// its real response to the caller, so only the persisted cassette is
	// rewritten. Replay mode never calls Save(), so this never runs there.
	rec.AddSaveFilter(redactInteraction)

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
