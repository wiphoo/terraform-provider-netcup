package vcr

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// snapshotNameForTest is the synthetic snapshot name used across the v0.7.0
// snapshot cassettes. It's authored in the same server-<hex> shape the
// save-time redactor would produce from a real name (redact.go's
// fakeServerName), so the fixtures read exactly like a live `make acc-record`
// capture with names scrubbed — and, being committed as the redacted value,
// it round-trips through the method+URL-only matcher unchanged (the snapshot
// name sits in the delete/restore request URL path).
const snapshotNameForTest = "server-0a0b0c0d"

// TestCreateSnapshot202 replays POST /v1/servers/{id}/snapshots returning the
// async 202 TaskInfo that CreateSnapshot hands back for WaitForTask polling.
// Replay-only: a live recording would create a real snapshot on the
// maintainer's server (see skipInRecordMode).
func TestCreateSnapshot202(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestCreateSnapshot202"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	desc := "vcr-redacted-description"
	disk := "vda"
	task, err := client.CreateSnapshot(context.Background(), id, netcup.ServerSnapshotCreate{
		Name:        snapshotNameForTest,
		Description: &desc,
		DiskName:    &disk,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}
	if task == nil || task.UUID == "" {
		t.Fatalf("CreateSnapshot() task = %+v, want a task with a UUID", task)
	}
	if task.State != netcup.TaskStatePending {
		t.Errorf("task.State = %q, want %q", task.State, netcup.TaskStatePending)
	}
}

// TestCreateSnapshot202AndWait replays the async create, then the
// GET /v1/tasks/{uuid} polls WaitForTask issues, transitioning RUNNING ->
// FINISHED. The UUID flows from the 202 body into WaitForTask, so no
// cassette-derived constant is needed; WithTaskPollInterval collapses the
// between-poll wait. Replay-only (a live recording would create a real
// snapshot).
func TestCreateSnapshot202AndWait(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestCreateSnapshot202AndWait"
	client := NewClient(t, cassetteName, netcup.WithTaskPollInterval(time.Millisecond))
	id := ServerIDForTest(t, cassetteName)

	desc := "vcr-redacted-description"
	disk := "vda"
	task, err := client.CreateSnapshot(context.Background(), id, netcup.ServerSnapshotCreate{
		Name:        snapshotNameForTest,
		Description: &desc,
		DiskName:    &disk,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}
	if task == nil || task.UUID == "" {
		t.Fatalf("CreateSnapshot() task = %+v, want a task with a UUID", task)
	}

	ctx, cancel := context.WithTimeout(context.Background(), replayWaitTimeout)
	defer cancel()
	final, err := client.WaitForTask(ctx, task.UUID)
	if err != nil {
		t.Fatalf("WaitForTask() error = %v", err)
	}
	if final.State != netcup.TaskStateFinished {
		t.Errorf("final.State = %q, want %q", final.State, netcup.TaskStateFinished)
	}
	if final.TaskProgress == nil || final.TaskProgress.ProgressInPercent != 100 {
		t.Errorf("final.TaskProgress = %+v, want ProgressInPercent 100", final.TaskProgress)
	}
}

// TestCreateSnapshotAPIError replays a 400 rejection (an online snapshot the
// server won't accept on UEFI) and asserts CreateSnapshot surfaces it as
// *netcup.APIError carrying the status code, with no task returned. Authored
// from the documented error shape, so replay-only.
func TestCreateSnapshotAPIError(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestCreateSnapshotAPIError"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	task, err := client.CreateSnapshot(context.Background(), id, netcup.ServerSnapshotCreate{
		Name:           snapshotNameForTest,
		OnlineSnapshot: true,
	})
	if task != nil {
		t.Errorf("task = %+v, want nil on error", task)
	}
	var apiErr *netcup.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("CreateSnapshot() error = %v, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
}

// TestDeleteSnapshot202 replays DELETE /v1/servers/{id}/snapshots/{name}
// returning the async 202 TaskInfo that DeleteSnapshot hands back for
// WaitForTask polling. Replay-only: a live recording would irrecoverably
// delete a real snapshot (see skipInRecordMode).
func TestDeleteSnapshot202(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestDeleteSnapshot202"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	task, err := client.DeleteSnapshot(context.Background(), id, snapshotNameForTest)
	if err != nil {
		t.Fatalf("DeleteSnapshot() error = %v", err)
	}
	if task == nil || task.UUID == "" {
		t.Fatalf("DeleteSnapshot() task = %+v, want a task with a UUID", task)
	}
	if task.State != netcup.TaskStatePending {
		t.Errorf("task.State = %q, want %q", task.State, netcup.TaskStatePending)
	}
}

// TestDeleteSnapshotAPIError replays a 404 rejection (a snapshot that doesn't
// exist) and asserts DeleteSnapshot surfaces it as *netcup.APIError carrying
// the status code, with no task returned. Authored from the documented error
// shape, so replay-only.
func TestDeleteSnapshotAPIError(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestDeleteSnapshotAPIError"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	task, err := client.DeleteSnapshot(context.Background(), id, snapshotNameForTest)
	if task != nil {
		t.Errorf("task = %+v, want nil on error", task)
	}
	var apiErr *netcup.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("DeleteSnapshot() error = %v, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

// TestRestoreSnapshot202 replays POST /v1/servers/{id}/snapshots/{name}/revert
// returning the async 202 TaskInfo that RestoreSnapshot hands back for
// WaitForTask polling. Replay-only: a live recording would revert the
// maintainer's server disks and reboot it (see skipInRecordMode).
func TestRestoreSnapshot202(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestRestoreSnapshot202"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	task, err := client.RestoreSnapshot(context.Background(), id, snapshotNameForTest)
	if err != nil {
		t.Fatalf("RestoreSnapshot() error = %v", err)
	}
	if task == nil || task.UUID == "" {
		t.Fatalf("RestoreSnapshot() task = %+v, want a task with a UUID", task)
	}
	if task.State != netcup.TaskStatePending {
		t.Errorf("task.State = %q, want %q", task.State, netcup.TaskStatePending)
	}
}

// TestRestoreSnapshot202AndWait replays the async restore, then the
// GET /v1/tasks/{uuid} polls WaitForTask issues, transitioning RUNNING ->
// FINISHED. The UUID flows from the 202 body into WaitForTask, so no
// cassette-derived constant is needed; WithTaskPollInterval collapses the
// between-poll wait. Replay-only (a live recording would revert the
// maintainer's server).
func TestRestoreSnapshot202AndWait(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestRestoreSnapshot202AndWait"
	client := NewClient(t, cassetteName, netcup.WithTaskPollInterval(time.Millisecond))
	id := ServerIDForTest(t, cassetteName)

	task, err := client.RestoreSnapshot(context.Background(), id, snapshotNameForTest)
	if err != nil {
		t.Fatalf("RestoreSnapshot() error = %v", err)
	}
	if task == nil || task.UUID == "" {
		t.Fatalf("RestoreSnapshot() task = %+v, want a task with a UUID", task)
	}

	ctx, cancel := context.WithTimeout(context.Background(), replayWaitTimeout)
	defer cancel()
	final, err := client.WaitForTask(ctx, task.UUID)
	if err != nil {
		t.Fatalf("WaitForTask() error = %v", err)
	}
	if final.State != netcup.TaskStateFinished {
		t.Errorf("final.State = %q, want %q", final.State, netcup.TaskStateFinished)
	}
	if final.TaskProgress == nil || final.TaskProgress.ProgressInPercent != 100 {
		t.Errorf("final.TaskProgress = %+v, want ProgressInPercent 100", final.TaskProgress)
	}
}

// TestRestoreSnapshotAPIError replays a 404 rejection (a snapshot that doesn't
// exist) and asserts RestoreSnapshot surfaces it as *netcup.APIError carrying
// the status code, with no task returned. Authored from the documented error
// shape, so replay-only.
func TestRestoreSnapshotAPIError(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestRestoreSnapshotAPIError"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	task, err := client.RestoreSnapshot(context.Background(), id, snapshotNameForTest)
	if task != nil {
		t.Errorf("task = %+v, want nil on error", task)
	}
	var apiErr *netcup.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("RestoreSnapshot() error = %v, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}
