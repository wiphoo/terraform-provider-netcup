package vcr

import (
	"context"
	"testing"

	"github.com/dnaeon/go-vcr/recorder"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

func TestRecorderReplay(t *testing.T) {
	client := NewClient(t, "TestRecorderReplay")
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v, want nil", err)
	}
	if got := client.APIEndpoint(); got != netcup.DefaultAPIEndpoint {
		t.Errorf("APIEndpoint() = %q, want %q", got, netcup.DefaultAPIEndpoint)
	}
}

// TestCheckCassetteFound covers the go-vcr v1.2.0 quirk where NewAsMode
// silently downgrades ModeReplaying to ModeRecording when the cassette file
// is missing: NewClient must treat that as an error rather than let a
// missing/typo'd cassette silently issue a live SCP request (see the
// NewClient doc comment).
func TestCheckCassetteFound(t *testing.T) {
	tests := []struct {
		name          string
		requestedMode recorder.Mode
		actualMode    recorder.Mode
		wantErr       bool
	}{
		{"replay honored", recorder.ModeReplaying, recorder.ModeReplaying, false},
		{"replay silently downgraded to recording (cassette missing)", recorder.ModeReplaying, recorder.ModeRecording, true},
		{"recording explicitly requested", recorder.ModeRecording, recorder.ModeRecording, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkCassetteFound(tc.requestedMode, tc.actualMode, "SomeCassette")
			if (err != nil) != tc.wantErr {
				t.Errorf("checkCassetteFound(%v, %v) error = %v, wantErr %v", tc.requestedMode, tc.actualMode, err, tc.wantErr)
			}
		})
	}
}
