package netcup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// taskPollInterval bounds how often WaitForTask re-reads a task's state while
// waiting for it to reach a terminal state. netcup executes power and rescue
// operations asynchronously (the mutating call returns 202 with a TaskInfo),
// so WaitForTask polls across this interval until the task finishes or the
// caller's context is done. It is a package variable so tests can shrink it.
var taskPollInterval = 2 * time.Second

// TaskState is the lifecycle state of an asynchronous SCP task.
type TaskState string

// Task states returned by the SCP REST API. FINISHED is the only success
// terminal; ERROR, CANCELED, and ROLLBACK are failure terminals. PENDING,
// RUNNING, and WAITING_FOR_CANCEL are non-terminal (WaitForTask keeps polling).
const (
	TaskStatePending          TaskState = "PENDING"
	TaskStateRunning          TaskState = "RUNNING"
	TaskStateFinished         TaskState = "FINISHED"
	TaskStateError            TaskState = "ERROR"
	TaskStateWaitingForCancel TaskState = "WAITING_FOR_CANCEL"
	TaskStateCanceled         TaskState = "CANCELED"
	TaskStateRollback         TaskState = "ROLLBACK"
)

// IsTerminal reports whether the state is a terminal one (the task will not
// transition further): FINISHED, ERROR, CANCELED, or ROLLBACK.
func (s TaskState) IsTerminal() bool {
	switch s {
	case TaskStateFinished, TaskStateError, TaskStateCanceled, TaskStateRollback:
		return true
	default:
		return false
	}
}

// TaskInfo is the response from the SCP task endpoints (GET /v1/tasks/{uuid}),
// and is also the 202 body returned by asynchronous mutating operations such
// as power state changes and rescue-system activation.
type TaskInfo struct {
	UUID  string    `json:"uuid"`
	Name  string    `json:"name"`
	State TaskState `json:"state"`

	// StartedAt and FinishedAt are nil until the task starts/finishes.
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`

	// TaskProgress carries progress hints while the task runs; nil when absent.
	TaskProgress *TaskProgress `json:"taskProgress"`

	// Message is a human-readable status/error detail; nil when absent.
	Message *string `json:"message"`

	// ResponseError carries the structured error (code + message) for a failed
	// task; nil when absent. The API may populate this instead of the top-level
	// Message, so failureMessage falls back to it.
	ResponseError *ResponseError `json:"responseError"`

	// OnRollback is true when the task is rolling back a failed operation.
	OnRollback bool `json:"onRollback"`
}

// TaskProgress is the optional progress hint embedded in TaskInfo.
type TaskProgress struct {
	ExpectedFinishedAt *time.Time `json:"expectedFinishedAt"`
	ProgressInPercent  float64    `json:"progressInPercent"`
}

// ResponseError is the structured error object the SCP API returns for failed
// tasks (and other error responses): a machine-readable code and a message.
type ResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// failureMessage returns the best available human-readable detail for a failed
// task, preferring the top-level Message and falling back to ResponseError
// (which the API may populate instead). It returns an empty string when neither
// carries a detail.
func (t *TaskInfo) failureMessage() string {
	if t.Message != nil && *t.Message != "" {
		return *t.Message
	}
	if t.ResponseError != nil {
		switch {
		case t.ResponseError.Code != "" && t.ResponseError.Message != "":
			return t.ResponseError.Code + ": " + t.ResponseError.Message
		case t.ResponseError.Message != "":
			return t.ResponseError.Message
		case t.ResponseError.Code != "":
			return t.ResponseError.Code
		}
	}
	return ""
}

// TaskError is returned by WaitForTask when a task reaches a failure terminal
// state (ERROR, CANCELED, or ROLLBACK). Callers can type-assert it to inspect
// the failing State and any Message the API supplied.
type TaskError struct {
	UUID    string
	State   TaskState
	Message string
}

// Error implements the error interface.
func (e *TaskError) Error() string {
	msg := fmt.Sprintf("task %s ended in state %s", e.UUID, e.State)
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// GetTask calls GET /v1/tasks/{uuid} and returns the task's current state. An
// unknown UUID surfaces as an *APIError with StatusCode 404.
func (c *Client) GetTask(ctx context.Context, uuid string) (*TaskInfo, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/v1/tasks/%s", url.PathEscape(uuid)), "application/json", nil, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return nil, newAPIError(resp)
	}

	var task TaskInfo
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, err
	}
	// Drain any trailing bytes so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)
	return &task, nil
}

// WaitForTask polls GET /v1/tasks/{uuid} until the task reaches a terminal
// state or ctx is done. It returns the final TaskInfo on FINISHED; a
// *TaskError on ERROR/CANCELED/ROLLBACK (carrying the task's Message); and
// ctx.Err() if the context is canceled or its deadline passes first.
//
// The overall wait is bounded by the caller's context, not a fixed attempt
// count: netcup's power and rescue tasks can take minutes, so the timeout
// belongs to the caller (e.g. the CLI's --wait flag). Between reads it waits
// taskPollInterval, honoring ctx so cancellation is observed promptly.
//
// Transient GetTask errors (network blips, 5xx, 429) are retried until ctx
// expires; the last such error is wrapped into the returned ctx error if the
// context ends first. Permanent client errors (a 4xx other than 429, such as a
// 404 for a bad UUID or 401/403 for an invalid token/IP) will never succeed on
// retry, so they are returned immediately and unwrapped, letting callers
// recover the *APIError with errors.As instead of waiting out the window.
func (c *Client) WaitForTask(ctx context.Context, uuid string) (*TaskInfo, error) {
	var lastErr error
	for {
		task, err := c.GetTask(ctx, uuid)
		if err != nil {
			// A canceled context is authoritative; stop and report it.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Permanent client errors won't recover on retry — return the
			// actionable error now rather than after the wait window.
			if isPermanentTaskPollError(err) {
				return nil, err
			}
			lastErr = err
		} else if task.State.IsTerminal() {
			if task.State == TaskStateFinished {
				return task, nil
			}
			return nil, &TaskError{UUID: uuid, State: task.State, Message: task.failureMessage()}
		} else {
			lastErr = nil
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("waiting for task %s: %w (last poll error: %v)", uuid, ctx.Err(), lastErr)
			}
			return nil, ctx.Err()
		case <-time.After(taskPollInterval):
		}
	}
}

// isPermanentTaskPollError reports whether a GetTask error is a permanent
// client error that will never succeed on retry. A 4xx is permanent except for
// the ones that are inherently retryable: 408 Request Timeout, 425 Too Early,
// and 429 Too Many Requests. Network errors and 5xx are also treated as
// transient (they return false here).
func isPermanentTaskPollError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return false
	default:
		return true
	}
}
