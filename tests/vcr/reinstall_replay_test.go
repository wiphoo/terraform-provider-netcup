package vcr

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// TestReinstallServer202AndWait replays POST /v1/servers/{id}/image returning a
// 202 TaskInfo, then the GET /v1/tasks/{uuid} polls WaitForTask issues,
// transitioning RUNNING -> FINISHED. The UUID flows from the 202 body into
// WaitForTask, so no cassette-derived constant is needed; WithTaskPollInterval
// collapses the between-poll wait so the recorded transition replays without a
// real-time sleep. Replay-only: a live recording would perform a DESTRUCTIVE OS
// reinstall on the maintainer's server (see skipInRecordMode), exactly why the
// power/task cassettes are replay-only too.
func TestReinstallServer202AndWait(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestReinstallServer202AndWait"
	client := NewClient(t, cassetteName, netcup.WithTaskPollInterval(time.Millisecond))
	id := ServerIDForTest(t, cassetteName)

	flavour := int32(42)
	task, err := client.ReinstallServer(context.Background(), id, netcup.ServerImageSetup{
		ImageFlavourID: &flavour,
	})
	if err != nil {
		t.Fatalf("ReinstallServer() error = %v", err)
	}
	if task == nil || task.UUID == "" {
		t.Fatalf("ReinstallServer() task = %+v, want a task with a UUID", task)
	}
	if task.State != netcup.TaskStatePending {
		t.Errorf("task.State = %q, want %q", task.State, netcup.TaskStatePending)
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
		t.Error("final.Steps is empty, want the recorded wipe/install sub-steps")
	}
}

// TestReinstallServerCustomScript replays a reinstall carrying a full install
// body — image flavour, hostname, an additional user + password, and a
// customScript bootstrap — and asserts the 202 TaskInfo decodes. The committed
// request body shows the sensitive fields (additionalUserUsername/Password,
// customScript) already in their redacted form; go-vcr matches on method+URL,
// not body (see matchInteraction), so the SDK still marshals the real values
// passed here while the persisted cassette stays free of secrets. Replay-only
// (destructive).
func TestReinstallServerCustomScript(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestReinstallServerCustomScript"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	flavour := int32(42)
	hostname := "web-01.example.com"
	user := "deploy"
	password := "s3cr3t-never-committed"
	script := "#!/bin/sh\necho provisioned > /etc/motd\n"
	task, err := client.ReinstallServer(context.Background(), id, netcup.ServerImageSetup{
		ImageFlavourID:         &flavour,
		Hostname:               &hostname,
		AdditionalUserUsername: &user,
		AdditionalUserPassword: &password,
		CustomScript:           &script,
	})
	if err != nil {
		t.Fatalf("ReinstallServer() error = %v", err)
	}
	if task == nil || task.State != netcup.TaskStatePending {
		t.Fatalf("task = %+v, want a PENDING task", task)
	}
}

// TestReinstallServerAPIError replays a 422 rejection (an image flavour the
// server won't accept) and asserts ReinstallServer surfaces it as
// *netcup.APIError carrying the status code, with no task returned. Authored
// from the documented ValidationError shape, so replay-only.
func TestReinstallServerAPIError(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestReinstallServerAPIError"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	flavour := int32(999999)
	task, err := client.ReinstallServer(context.Background(), id, netcup.ServerImageSetup{
		ImageFlavourID: &flavour,
	})
	if task != nil {
		t.Errorf("task = %+v, want nil on error", task)
	}
	var apiErr *netcup.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("ReinstallServer() error = %v, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("StatusCode = %d, want 422", apiErr.StatusCode)
	}
}
