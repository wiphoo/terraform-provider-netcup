package netcup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSetPowerStatePreDispatchTokenErrorIsMarked proves that when the configured
// TokenSource cannot produce a token, SetPowerState fails in newRequest BEFORE the
// PATCH is dispatched, and the returned error is marked with ErrPreDispatch so
// callers can classify it as a definitive, safe-to-retry failure via errors.Is.
// The original token error must remain in the chain.
func TestSetPowerStatePreDispatchTokenErrorIsMarked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("PATCH must not be dispatched when the token source fails")
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithTokenSource(erroringTokenSource{}))
	_, err := c.SetPowerState(context.Background(), 42, PowerOff, "")
	if err == nil {
		t.Fatal("SetPowerState() error = nil, want a pre-dispatch token error")
	}
	if !errors.Is(err, ErrPreDispatch) {
		t.Errorf("errors.Is(err, ErrPreDispatch) = false, want true; err = %v", err)
	}
	if !strings.Contains(err.Error(), "token source unavailable") {
		t.Errorf("error = %v, want the original token failure preserved in the chain", err)
	}
}

func TestSetPowerState202ReturnsTask(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotContentType, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = strings.TrimSpace(string(b))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"task-abc","name":"SetServerState","state":"PENDING"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.SetPowerState(context.Background(), 123, PowerOff, "")
	if err != nil {
		t.Fatalf("SetPowerState() error = %v", err)
	}
	if task == nil || task.UUID != "task-abc" || task.State != TaskStatePending {
		t.Fatalf("task = %+v, want UUID=task-abc State=PENDING", task)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/v1/servers/123" {
		t.Errorf("path = %q, want /v1/servers/123", gotPath)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want empty (no stateOption)", gotQuery)
	}
	if gotContentType != "application/merge-patch+json" {
		t.Errorf("Content-Type = %q, want application/merge-patch+json", gotContentType)
	}
	if gotBody != `{"state":"OFF"}` {
		t.Errorf("body = %q, want {\"state\":\"OFF\"}", gotBody)
	}
}

func TestSetPowerStateAppendsStateOption(t *testing.T) {
	var gotQuery, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		gotBody = strings.TrimSpace(string(b))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"task-xyz","state":"RUNNING"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.SetPowerState(context.Background(), 7, PowerOn, "RESET")
	if err != nil {
		t.Fatalf("SetPowerState() error = %v", err)
	}
	if task == nil || task.UUID != "task-xyz" {
		t.Fatalf("task = %+v, want UUID=task-xyz", task)
	}
	if gotQuery != "stateOption=RESET" {
		t.Errorf("query = %q, want stateOption=RESET", gotQuery)
	}
	if gotBody != `{"state":"ON"}` {
		t.Errorf("body = %q, want {\"state\":\"ON\"}", gotBody)
	}
}

func TestSetPowerState200ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 200 with no body: applied synchronously, no task to track.
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.SetPowerState(context.Background(), 42, PowerOn, "")
	if err != nil {
		t.Fatalf("SetPowerState() error = %v", err)
	}
	if task != nil {
		t.Errorf("task = %+v, want nil for a 200 response", task)
	}
}

func TestSetPowerStateAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"MAINTENANCE","message":"Node is in maintenance mode."}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.SetPowerState(context.Background(), 9, PowerSuspended, "")
	if err == nil {
		t.Fatal("SetPowerState() error = nil, want error")
	}
	if task != nil {
		t.Errorf("task = %+v, want nil on error", task)
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want 503", apiErr.StatusCode)
	}
}

// TestSetPowerStateSuspendBody guards the SUSPENDED value and confirms no
// stateOption is sent for it.
func TestSetPowerStateSuspendBody(t *testing.T) {
	var gotBody, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = strings.TrimSpace(string(b))
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(TaskInfo{UUID: "t1", State: TaskStatePending})
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	if _, err := c.SetPowerState(context.Background(), 1, PowerSuspended, ""); err != nil {
		t.Fatalf("SetPowerState() error = %v", err)
	}
	if gotBody != `{"state":"SUSPENDED"}` {
		t.Errorf("body = %q, want {\"state\":\"SUSPENDED\"}", gotBody)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want empty", gotQuery)
	}
}
