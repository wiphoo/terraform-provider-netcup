package vcr

import (
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/dnaeon/go-vcr/cassette"
	"github.com/dnaeon/go-vcr/recorder"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// NewClient constructs a *netcup.Client wired to a go-vcr recorder for the
// named cassette (relative to testdata/cassettes/). When VCR_RECORD=1 the
// recorder proxies to live SCP and records the interactions; otherwise it
// replays the cassette. Authorization request headers are scrubbed before the
// cassette is written (record mode only — replay mode is read-only).
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

	t.Cleanup(func() { _ = rec.Stop() })

	// Scrub auth headers before the cassette is written. The filter runs only
	// in record mode; replay mode is read-only.
	rec.AddFilter(func(i *cassette.Interaction) error {
		delete(i.Request.Headers, "Authorization")
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
		netcup.WithHTTPClient(&http.Client{Transport: rec}),
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
