package provider

import (
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/dnaeon/go-vcr/cassette"
	"github.com/dnaeon/go-vcr/recorder"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

func newVCRClient(t *testing.T, cassetteName string) *netcup.Client {
	t.Helper()

	mode := recorder.ModeReplaying
	if os.Getenv("VCR_RECORD") == "1" {
		mode = recorder.ModeRecording
	}

	rec, err := recorder.NewAsMode("testdata/cassettes/"+cassetteName, mode, nil)
	if err != nil {
		t.Fatalf("go-vcr recorder: %v", err)
	}

	if err := checkCassetteFound(mode, rec.Mode(), cassetteName); err != nil {
		t.Fatal(err)
	}

	rec.AddSaveFilter(vcrRedactInteraction)

	t.Cleanup(func() {
		if err := rec.Stop(); err != nil {
			t.Errorf("go-vcr: saving cassette %q: %v", cassetteName, err)
		}
	})

	token := "vcr-replay-fake-token"
	if mode == recorder.ModeRecording {
		token = os.Getenv("NETCUP_ACCESS_TOKEN")
		if token == "" {
			t.Fatal("VCR_RECORD=1 requires NETCUP_ACCESS_TOKEN")
		}
	}

	return netcup.New(
		netcup.WithAPIEndpoint(netcup.DefaultAPIEndpoint),
		netcup.WithHTTPClient(&http.Client{Transport: rec}),
		netcup.WithAccessToken(token),
	)
}

func checkCassetteFound(requestedMode, actualMode recorder.Mode, cassetteName string) error {
	if requestedMode != recorder.ModeReplaying || actualMode == recorder.ModeReplaying {
		return nil
	}
	return fmt.Errorf(
		"cassette %q not found (testdata/cassettes/%s.yaml): go-vcr would silently record a live interaction instead of replaying it; commit the cassette, or run with VCR_RECORD=1 to create it",
		cassetteName, cassetteName,
	)
}

func vcrRedactInteraction(i *cassette.Interaction) error {
	delete(i.Request.Headers, "Authorization")
	return nil
}
