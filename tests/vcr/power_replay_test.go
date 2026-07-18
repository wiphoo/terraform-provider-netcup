package vcr

import (
	"context"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// TestSetPowerState records PATCH /v1/servers/{id}?stateOption=POWERCYCLE and
// asserts the async 202 path returns the TaskInfo the caller polls with
// WaitForTask. Replay-only: a live recording would reboot the maintainer's
// server (see skipInRecordMode).
func TestSetPowerState(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestSetPowerState"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	task, err := client.SetPowerState(context.Background(), id, netcup.PowerOn, "POWERCYCLE")
	if err != nil {
		t.Fatalf("SetPowerState() error = %v", err)
	}
	if task == nil {
		t.Fatal("SetPowerState() returned nil task for a 202 response")
	}
	if task.UUID == "" {
		t.Error("task.UUID is empty, want the async task handle")
	}
	if task.State != netcup.TaskStatePending {
		t.Errorf("task.State = %q, want %q", task.State, netcup.TaskStatePending)
	}
}
