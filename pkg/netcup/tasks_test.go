package netcup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fastTaskPoll shrinks the task poll interval so tests that poll across several
// attempts don't sleep for seconds. It restores the default afterward.
func fastTaskPoll(t *testing.T) {
	t.Helper()
	old := taskPollInterval
	taskPollInterval = 0
	t.Cleanup(func() { taskPollInterval = old })
}

func TestGetTaskSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/v1/tasks/abc-123"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		if v := r.Header.Get("Authorization"); v != "Bearer tok123" {
			t.Errorf("Authorization = %q, want Bearer tok123", v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"uuid": "abc-123",
			"name": "SetServerState",
			"state": "RUNNING",
			"startedAt": "2026-07-17T10:00:00Z",
			"taskProgress": {"progressInPercent": 42.5},
			"onRollback": false
		}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.GetTask(context.Background(), "abc-123")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if task.UUID != "abc-123" {
		t.Errorf("UUID = %q, want %q", task.UUID, "abc-123")
	}
	if task.State != TaskStateRunning {
		t.Errorf("State = %q, want %q", task.State, TaskStateRunning)
	}
	if task.StartedAt == nil || task.StartedAt.IsZero() {
		t.Errorf("StartedAt = %v, want a parsed time", task.StartedAt)
	}
	if task.TaskProgress == nil || task.TaskProgress.ProgressInPercent != 42.5 {
		t.Errorf("TaskProgress = %+v, want ProgressInPercent 42.5", task.TaskProgress)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"task not found"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.GetTask(context.Background(), "missing")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("GetTask() error = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

func TestWaitForTaskSuccessAfterPolls(t *testing.T) {
	fastTaskPoll(t)

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		state := "RUNNING"
		if n >= 3 {
			state = "FINISHED"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"uuid":"t","state":%q}`, state)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.WaitForTask(context.Background(), "t")
	if err != nil {
		t.Fatalf("WaitForTask() error = %v", err)
	}
	if task.State != TaskStateFinished {
		t.Errorf("State = %q, want FINISHED", task.State)
	}
	if calls.Load() < 3 {
		t.Errorf("calls = %d, want >= 3 (should have polled until FINISHED)", calls.Load())
	}
}

func TestWaitForTaskFailureStates(t *testing.T) {
	fastTaskPoll(t)

	for _, state := range []TaskState{TaskStateError, TaskStateCanceled, TaskStateRollback} {
		t.Run(string(state), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"uuid":"t","state":%q,"message":"boom"}`, state)
			}))
			defer srv.Close()

			c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
			_, err := c.WaitForTask(context.Background(), "t")
			var taskErr *TaskError
			if !errors.As(err, &taskErr) {
				t.Fatalf("WaitForTask() error = %v, want *TaskError", err)
			}
			if taskErr.State != state {
				t.Errorf("State = %q, want %q", taskErr.State, state)
			}
			if taskErr.Message != "boom" {
				t.Errorf("Message = %q, want %q", taskErr.Message, "boom")
			}
		})
	}
}

func TestWaitForTaskResponseErrorFallback(t *testing.T) {
	fastTaskPoll(t)

	// Top-level message is empty; the detail lives in responseError. The
	// resulting TaskError must surface the responseError code + message.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"uuid": "t",
			"state": "ERROR",
			"message": null,
			"responseError": {"code": "POWER_ON_FAILED", "message": "host is out of capacity"}
		}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.WaitForTask(context.Background(), "t")
	var taskErr *TaskError
	if !errors.As(err, &taskErr) {
		t.Fatalf("WaitForTask() error = %v, want *TaskError", err)
	}
	if want := "POWER_ON_FAILED: host is out of capacity"; taskErr.Message != want {
		t.Errorf("Message = %q, want %q", taskErr.Message, want)
	}
}

func TestWaitForTaskTransientErrorThenSuccess(t *testing.T) {
	fastTaskPoll(t)

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			// A transient server error on the first poll must be retried.
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uuid":"t","state":"FINISHED"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.WaitForTask(context.Background(), "t")
	if err != nil {
		t.Fatalf("WaitForTask() error = %v, want success after retry", err)
	}
	if task.State != TaskStateFinished {
		t.Errorf("State = %q, want FINISHED", task.State)
	}
}

func TestWaitForTaskRetriesRetryable4xx(t *testing.T) {
	fastTaskPoll(t)

	// 408 Request Timeout is a 4xx but inherently retryable: WaitForTask must
	// retry it rather than surface it immediately like a permanent 404.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusRequestTimeout)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uuid":"t","state":"FINISHED"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.WaitForTask(ctx, "t")
	if err != nil {
		t.Fatalf("WaitForTask() error = %v, want success after retrying 408", err)
	}
	if task.State != TaskStateFinished {
		t.Errorf("State = %q, want FINISHED", task.State)
	}
	if n := calls.Load(); n < 2 {
		t.Errorf("calls = %d, want >= 2 (408 should be retried)", n)
	}
}

func TestWaitForTaskPermanentErrorReturnsImmediately(t *testing.T) {
	fastTaskPoll(t)

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		// A bad UUID (or bad token/IP) is a permanent 4xx: it must not be
		// retried until the wait window expires.
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"task not found"}`))
	}))
	defer srv.Close()

	// A generous deadline: a correct implementation returns well before it, so
	// a DeadlineExceeded here would signal a regression (retrying forever).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.WaitForTask(ctx, "missing")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("WaitForTask() error = %v, want *APIError returned immediately", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("server calls = %d, want 1 (permanent error should not be retried)", n)
	}
}

func TestWaitForTaskContextDeadline(t *testing.T) {
	// A non-terminal task that never finishes: WaitForTask must give up when
	// the caller's context deadline passes.
	old := taskPollInterval
	taskPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { taskPollInterval = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uuid":"t","state":"RUNNING"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.WaitForTask(ctx, "t")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForTask() error = %v, want context.DeadlineExceeded", err)
	}
}
