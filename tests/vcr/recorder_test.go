package vcr

import (
	"context"
	"testing"

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
