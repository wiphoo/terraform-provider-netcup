package netcup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReinstallServer202ReturnsTask(t *testing.T) {
	var gotMethod, gotPath, gotContentType string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"task-reinstall","name":"ServerImageSetup","state":"PENDING"}`))
	}))
	defer srv.Close()

	flavour := int32(42)
	script := "#!/bin/sh\necho hi"
	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.ReinstallServer(context.Background(), 123, ServerImageSetup{
		ImageFlavourID: &flavour,
		CustomScript:   &script,
	})
	if err != nil {
		t.Fatalf("ReinstallServer() error = %v", err)
	}
	if task == nil || task.UUID != "task-reinstall" || task.State != TaskStatePending {
		t.Fatalf("task = %+v, want UUID=task-reinstall State=PENDING", task)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/servers/123/image" {
		t.Errorf("path = %q, want /v1/servers/123/image", gotPath)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}

	// Body must carry only the set fields — nothing else should be marshalled.
	if len(gotBody) != 2 {
		t.Errorf("body keys = %v, want exactly imageFlavourId and customScript", gotBody)
	}
	if v, ok := gotBody["imageFlavourId"].(float64); !ok || int32(v) != 42 {
		t.Errorf("imageFlavourId = %v, want 42", gotBody["imageFlavourId"])
	}
	if v, ok := gotBody["customScript"].(string); !ok || v != script {
		t.Errorf("customScript = %v, want %q", gotBody["customScript"], script)
	}
	for _, k := range []string{"diskName", "hostname", "locale", "timezone", "sshKeyIds", "emailToExecutingUser"} {
		if _, present := gotBody[k]; present {
			t.Errorf("unset field %q was sent in body %v", k, gotBody)
		}
	}
}

func TestReinstallServerRequiresImageFlavourID(t *testing.T) {
	c := New(WithAccessToken("tok123"))
	task, err := c.ReinstallServer(context.Background(), 9, ServerImageSetup{})
	if err == nil {
		t.Fatal("ReinstallServer() error = nil, want error")
	}
	if task != nil {
		t.Errorf("task = %+v, want nil on error", task)
	}
	if !errors.Is(err, ErrPreDispatch) {
		t.Errorf("errors.Is(err, ErrPreDispatch) = false, want true; err = %v", err)
	}
}

func TestReinstallServerInvalidIDAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"server not found"}`))
	}))
	defer srv.Close()

	flavour := int32(42)
	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.ReinstallServer(context.Background(), 999999, ServerImageSetup{ImageFlavourID: &flavour})
	if err == nil {
		t.Fatal("ReinstallServer() error = nil, want error")
	}
	if task != nil {
		t.Errorf("task = %+v, want nil on error", task)
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

// TestReinstallServerEmptySetupSendsEmptyObject confirms an all-nil setup
// marshals to {} — no fields leak into the request body.
func TestReinstallServerEmptySetupRejectedBeforeDispatch(t *testing.T) {
	c := New(WithAccessToken("tok123"))
	_, err := c.ReinstallServer(context.Background(), 1, ServerImageSetup{})
	if err == nil {
		t.Fatal("ReinstallServer() error = nil, want pre-dispatch error")
	}
	if !errors.Is(err, ErrPreDispatch) {
		t.Errorf("errors.Is(err, ErrPreDispatch) = false, want true; err = %v", err)
	}
}
