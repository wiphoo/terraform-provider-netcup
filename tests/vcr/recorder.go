package vcr

import (
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
		netcup.WithHTTPClient(&http.Client{Transport: rec}),
		netcup.WithAccessToken(token),
	)
}
