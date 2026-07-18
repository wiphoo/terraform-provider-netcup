package vcr

import (
	"context"
	"testing"
)

// TestGetRescueSystemInactive replays GET /v1/servers/{id}/rescuesystem for a
// server not in rescue: active=false, password=null. Read-only and
// non-disruptive, so it is live-refreshable with VCR_RECORD=1.
func TestGetRescueSystemInactive(t *testing.T) {
	const cassetteName = "TestGetRescueSystemInactive"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	status, err := client.GetRescueSystem(context.Background(), id)
	if err != nil {
		t.Fatalf("GetRescueSystem() error = %v", err)
	}
	if status.Active {
		t.Errorf("status.Active = true, want false")
	}
	if status.Password != nil {
		t.Errorf("status.Password = %v, want nil (no password while inactive)", *status.Password)
	}
}

// TestGetRescueSystemActive replays the active state, where the API surfaces the
// rescue password. The password is the most sensitive field in the v0.3.0
// surface, so the cassette carries only the redacted placeholder; this test
// confirms the field still decodes to a non-nil pointer. Replay-only: recording
// requires the server to actually be in rescue (a reboot).
func TestGetRescueSystemActive(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestGetRescueSystemActive"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	status, err := client.GetRescueSystem(context.Background(), id)
	if err != nil {
		t.Fatalf("GetRescueSystem() error = %v", err)
	}
	if !status.Active {
		t.Errorf("status.Active = false, want true")
	}
	if status.Password == nil {
		t.Fatal("status.Password = nil, want the (redacted) rescue password")
	}
	if *status.Password != "vcr-redacted-password" {
		t.Errorf("status.Password = %q, want the redacted placeholder", *status.Password)
	}
}

// TestEnableRescueSystem replays POST /v1/servers/{id}/rescuesystem returning a
// 202 TaskInfo. Replay-only: enabling reboots the server into the rescue
// environment (and enabling twice is a 400).
func TestEnableRescueSystem(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestEnableRescueSystem"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	task, err := client.EnableRescueSystem(context.Background(), id)
	if err != nil {
		t.Fatalf("EnableRescueSystem() error = %v", err)
	}
	if task == nil || task.UUID == "" {
		t.Fatalf("EnableRescueSystem() task = %+v, want a task with a UUID to poll", task)
	}
}

// TestDisableRescueSystem replays DELETE /v1/servers/{id}/rescuesystem, which
// (unlike the documented bodyless 204) is asynchronous and returns a 202
// TaskInfo — see docs/SCP-API-NOTES.md. Replay-only: disabling reboots the
// server back into its normal OS.
func TestDisableRescueSystem(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestDisableRescueSystem"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	task, err := client.DisableRescueSystem(context.Background(), id)
	if err != nil {
		t.Fatalf("DisableRescueSystem() error = %v", err)
	}
	if task == nil || task.UUID == "" {
		t.Fatalf("DisableRescueSystem() task = %+v, want a task with a UUID to poll", task)
	}
}
