package netcup

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetRescueSystemActive(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"active":true,"password":"s3cret-rescue"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	status, err := c.GetRescueSystem(context.Background(), 123)
	if err != nil {
		t.Fatalf("GetRescueSystem() error = %v", err)
	}
	if !status.Active {
		t.Errorf("Active = false, want true")
	}
	if status.Password == nil || *status.Password != "s3cret-rescue" {
		t.Errorf("Password = %v, want s3cret-rescue", status.Password)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/servers/123/rescuesystem" {
		t.Errorf("path = %q, want /v1/servers/123/rescuesystem", gotPath)
	}
}

func TestGetRescueSystemInactive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"active":false,"password":null}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	status, err := c.GetRescueSystem(context.Background(), 5)
	if err != nil {
		t.Fatalf("GetRescueSystem() error = %v", err)
	}
	if status.Active {
		t.Errorf("Active = true, want false")
	}
	if status.Password != nil {
		t.Errorf("Password = %v, want nil when inactive", *status.Password)
	}
}

func TestGetRescueSystemAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"Server not found."}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	status, err := c.GetRescueSystem(context.Background(), 999)
	if err == nil {
		t.Fatal("GetRescueSystem() error = nil, want error")
	}
	if status != nil {
		t.Errorf("status = %+v, want nil on error", status)
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

func TestEnableRescueSystem202ReturnsTask(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = strings.TrimSpace(string(b))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"task-enable","name":"ActivateRescueSystem","state":"PENDING"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.EnableRescueSystem(context.Background(), 123)
	if err != nil {
		t.Fatalf("EnableRescueSystem() error = %v", err)
	}
	if task == nil || task.UUID != "task-enable" || task.State != TaskStatePending {
		t.Fatalf("task = %+v, want UUID=task-enable State=PENDING", task)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/servers/123/rescuesystem" {
		t.Errorf("path = %q, want /v1/servers/123/rescuesystem", gotPath)
	}
	if gotBody != "" {
		t.Errorf("body = %q, want empty (POST takes no request body)", gotBody)
	}
}

func TestEnableRescueSystemAlreadyActive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"BAD_REQUEST","message":"Rescue system currently active."}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.EnableRescueSystem(context.Background(), 5)
	if err == nil {
		t.Fatal("EnableRescueSystem() error = nil, want error")
	}
	if task != nil {
		t.Errorf("task = %+v, want nil on error", task)
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
}

func TestDisableRescueSystem202ReturnsTask(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = strings.TrimSpace(string(b))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"task-disable","name":"DeactivateRescueSystem","state":"RUNNING"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.DisableRescueSystem(context.Background(), 42)
	if err != nil {
		t.Fatalf("DisableRescueSystem() error = %v", err)
	}
	if task == nil || task.UUID != "task-disable" || task.State != TaskStateRunning {
		t.Fatalf("task = %+v, want UUID=task-disable State=RUNNING", task)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/v1/servers/42/rescuesystem" {
		t.Errorf("path = %q, want /v1/servers/42/rescuesystem", gotPath)
	}
	if gotBody != "" {
		t.Errorf("body = %q, want empty (DELETE takes no request body)", gotBody)
	}
}

func TestDisableRescueSystemAlreadyInactive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"BAD_REQUEST","message":"Rescue system currently deactivated."}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.DisableRescueSystem(context.Background(), 7)
	if err == nil {
		t.Fatal("DisableRescueSystem() error = nil, want error")
	}
	if task != nil {
		t.Errorf("task = %+v, want nil on error", task)
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
}
