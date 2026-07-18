package vcr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// replayWaitTimeout bounds WaitForTask in the replay tests. If a cassette stops
// matching (exhausted, or an SDK request change), go-vcr returns
// ErrInteractionNotFound; WaitForTask sees that as a non-*APIError and treats it
// as transient, retrying until its context ends. With an unbounded context that
// would hang CI until the global `go test` timeout instead of failing promptly,
// so these waits get a short deadline — the normal path completes in well under
// a millisecond (WithTaskPollInterval shrinks the poll gap), so this only ever
// bites a genuinely broken cassette.
const replayWaitTimeout = 30 * time.Second

// TestWaitForTaskFinished replays a power change (202 TaskInfo) followed by the
// GET /v1/tasks/{uuid} polls it produces, transitioning RUNNING -> FINISHED.
// The UUID flows from the 202 body into WaitForTask, so no cassette-derived
// constant is needed. WithTaskPollInterval collapses the between-poll wait so
// the recorded multi-poll transition replays without real-time sleeps.
// Replay-only: recording would reboot the maintainer's server.
func TestWaitForTaskFinished(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestWaitForTaskFinished"
	client := NewClient(t, cassetteName, netcup.WithTaskPollInterval(time.Millisecond))
	id := ServerIDForTest(t, cassetteName)

	task, err := client.SetPowerState(context.Background(), id, netcup.PowerOn, "POWERCYCLE")
	if err != nil {
		t.Fatalf("SetPowerState() error = %v", err)
	}
	if task == nil || task.UUID == "" {
		t.Fatalf("SetPowerState() task = %+v, want a task with a UUID", task)
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
	if len(final.Steps) == 0 {
		t.Error("final.Steps is empty, want the recorded sub-steps")
	}
}

// TestWaitForTaskError replays a task that reaches the ERROR terminal state and
// asserts WaitForTask surfaces a *TaskError carrying the responseError detail.
// Authored from the documented ERROR shape (a real task ERROR can't be forced
// on demand), so it is replay-only.
func TestWaitForTaskError(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestWaitForTaskError"
	client := NewClient(t, cassetteName, netcup.WithTaskPollInterval(time.Millisecond))
	uuid := taskUUIDFromCassette(t, cassetteName)

	ctx, cancel := context.WithTimeout(context.Background(), replayWaitTimeout)
	defer cancel()
	_, err := client.WaitForTask(ctx, uuid)
	var taskErr *netcup.TaskError
	if !errors.As(err, &taskErr) {
		t.Fatalf("WaitForTask() error = %v, want *netcup.TaskError", err)
	}
	if taskErr.State != netcup.TaskStateError {
		t.Errorf("taskErr.State = %q, want %q", taskErr.State, netcup.TaskStateError)
	}
	if want := "POWER_ON_FAILED: host is out of capacity"; taskErr.Message != want {
		t.Errorf("taskErr.Message = %q, want %q", taskErr.Message, want)
	}
}
