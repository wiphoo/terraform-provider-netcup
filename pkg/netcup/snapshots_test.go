package netcup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListSnapshotsSuccess(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers/123/snapshots" {
			t.Errorf("path = %q, want /v1/servers/123/snapshots", r.URL.Path)
		}
		if v := r.Header.Get("Accept"); v != "application/json" {
			t.Errorf("Accept = %q, want application/json", v)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{
				"uuid":"a1b2",
				"name":"nightly-backup",
				"description":"before upgrade",
				"disks":["sda","sdb"],
				"creationTime":"2026-07-16T13:34:04Z",
				"state":"RUNNING",
				"online":true,
				"exported":false,
				"exportedSizeInKiB":null
			},
			{
				"uuid":"c3d4",
				"name":"exported-snap",
				"description":null,
				"disks":[],
				"creationTime":"2026-07-10T09:00:00Z",
				"state":"STOPPED",
				"online":false,
				"exported":true,
				"exportedSizeInKiB":204800
			}
		]`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	snapshots, err := c.ListSnapshots(context.Background(), 123)
	if err != nil {
		t.Fatalf("ListSnapshots() error = %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("len(snapshots) = %d, want 2", len(snapshots))
	}

	first := snapshots[0]
	if first.UUID != "a1b2" || first.Name != "nightly-backup" {
		t.Errorf("first = %+v, want UUID=a1b2 Name=nightly-backup", first)
	}
	if first.Description == nil || *first.Description != "before upgrade" {
		t.Errorf("Description = %v, want 'before upgrade'", first.Description)
	}
	if len(first.Disks) != 2 || first.Disks[0] != "sda" || first.Disks[1] != "sdb" {
		t.Errorf("Disks = %+v, want [sda sdb]", first.Disks)
	}
	if want := time.Date(2026, 7, 16, 13, 34, 4, 0, time.UTC); !first.CreationTime.Equal(want) {
		t.Errorf("CreationTime = %v, want %v", first.CreationTime, want)
	}
	if first.State != "RUNNING" || !first.Online || first.Exported {
		t.Errorf("first state/online/exported = %q/%t/%t, want RUNNING/true/false", first.State, first.Online, first.Exported)
	}
	if first.ExportedSizeInKiB != nil {
		t.Errorf("ExportedSizeInKiB = %v, want nil", first.ExportedSizeInKiB)
	}

	second := snapshots[1]
	if second.Description != nil {
		t.Errorf("second Description = %v, want nil", second.Description)
	}
	if second.ExportedSizeInKiB == nil || *second.ExportedSizeInKiB != 204800 {
		t.Errorf("ExportedSizeInKiB = %v, want 204800", second.ExportedSizeInKiB)
	}
	if !second.Exported {
		t.Error("second Exported = false, want true")
	}

	if want := "Bearer tok123"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestListSnapshotsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	snapshots, err := c.ListSnapshots(context.Background(), 123)
	if err != nil {
		t.Fatalf("ListSnapshots() error = %v, want nil (empty list is valid)", err)
	}
	if len(snapshots) != 0 {
		t.Errorf("len(snapshots) = %d, want 0", len(snapshots))
	}
}

func TestListSnapshotsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"server not found"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.ListSnapshots(context.Background(), 999)
	if err == nil {
		t.Fatal("ListSnapshots() error = nil, want error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
}

func TestCreateSnapshotSuccess(t *testing.T) {
	var gotAuth string
	var gotBody serverSnapshotCreateCapture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/servers/123/snapshots" {
			t.Errorf("path = %q, want /v1/servers/123/snapshots", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"task-9","state":"PENDING"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	desc := "before upgrade"
	disk := "sda"
	task, err := c.CreateSnapshot(context.Background(), 123, ServerSnapshotCreate{
		Name:        "nightly",
		Description: &desc,
		DiskName:    &disk,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}
	if task == nil || task.UUID != "task-9" || task.State != TaskStatePending {
		t.Errorf("task = %+v, want UUID=task-9 State=PENDING", task)
	}
	if gotBody.Name != "nightly" {
		t.Errorf("body name = %q, want nightly", gotBody.Name)
	}
	if gotBody.Description == nil || *gotBody.Description != "before upgrade" {
		t.Errorf("body description = %v, want 'before upgrade'", gotBody.Description)
	}
	if gotBody.DiskName == nil || *gotBody.DiskName != "sda" {
		t.Errorf("body diskName = %v, want sda", gotBody.DiskName)
	}
	if gotBody.OnlineSnapshot {
		t.Error("body onlineSnapshot = true, want false")
	}
	if want := "Bearer tok123"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestCreateSnapshotOnlineFlag(t *testing.T) {
	var gotBody serverSnapshotCreateCapture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"task-9","state":"PENDING"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	if _, err := c.CreateSnapshot(context.Background(), 123, ServerSnapshotCreate{Name: "live", OnlineSnapshot: true}); err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}
	if !gotBody.OnlineSnapshot {
		t.Error("body onlineSnapshot = false, want true")
	}
	if gotBody.DiskName != nil {
		t.Errorf("body diskName = %v, want nil for an online snapshot", gotBody.DiskName)
	}
}

func TestCreateSnapshotNameRequired(t *testing.T) {
	c := New()
	_, err := c.CreateSnapshot(context.Background(), 123, ServerSnapshotCreate{})
	if err == nil {
		t.Fatal("CreateSnapshot() error = nil, want ErrPreDispatch")
	}
	if !errors.Is(err, ErrPreDispatch) {
		t.Errorf("error = %v, want wrapped ErrPreDispatch", err)
	}
}

func TestCreateSnapshotAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"server.snapshot.create.error.online.uefi","message":"online snapshot not possible"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.CreateSnapshot(context.Background(), 123, ServerSnapshotCreate{Name: "live", OnlineSnapshot: true})
	if err == nil {
		t.Fatal("CreateSnapshot() error = nil, want API error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusBadRequest)
	}
}

func TestDeleteSnapshotSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.URL.Path != "/v1/servers/123/snapshots/nightly" {
			t.Errorf("path = %q, want /v1/servers/123/snapshots/nightly", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"task-9","state":"PENDING"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.DeleteSnapshot(context.Background(), 123, "nightly")
	if err != nil {
		t.Fatalf("DeleteSnapshot() error = %v", err)
	}
	if task == nil || task.UUID != "task-9" {
		t.Errorf("task = %+v, want UUID=task-9", task)
	}
}

func TestDeleteSnapshotNameEscaped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/servers/123/snapshots/my%20snap%2F1" {
			t.Errorf("escaped path = %q, want /v1/servers/123/snapshots/my%%20snap%%2F1", r.URL.EscapedPath())
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"task-9","state":"PENDING"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	if _, err := c.DeleteSnapshot(context.Background(), 123, "my snap/1"); err != nil {
		t.Fatalf("DeleteSnapshot() error = %v", err)
	}
}

func TestDeleteSnapshotNameRequired(t *testing.T) {
	c := New()
	_, err := c.DeleteSnapshot(context.Background(), 123, "")
	if err == nil {
		t.Fatal("DeleteSnapshot() error = nil, want ErrPreDispatch")
	}
	if !errors.Is(err, ErrPreDispatch) {
		t.Errorf("error = %v, want wrapped ErrPreDispatch", err)
	}
}

func TestDeleteSnapshotAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"snapshot not found"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.DeleteSnapshot(context.Background(), 123, "nightly")
	if err == nil {
		t.Fatal("DeleteSnapshot() error = nil, want API error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
}

func TestRestoreSnapshotSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/servers/123/snapshots/nightly/revert" {
			t.Errorf("path = %q, want /v1/servers/123/snapshots/nightly/revert", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"task-9","state":"PENDING"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	task, err := c.RestoreSnapshot(context.Background(), 123, "nightly")
	if err != nil {
		t.Fatalf("RestoreSnapshot() error = %v", err)
	}
	if task == nil || task.UUID != "task-9" {
		t.Errorf("task = %+v, want UUID=task-9", task)
	}
}

func TestRestoreSnapshotNameRequired(t *testing.T) {
	c := New()
	_, err := c.RestoreSnapshot(context.Background(), 123, "")
	if err == nil {
		t.Fatal("RestoreSnapshot() error = nil, want ErrPreDispatch")
	}
	if !errors.Is(err, ErrPreDispatch) {
		t.Errorf("error = %v, want wrapped ErrPreDispatch", err)
	}
}

func TestRestoreSnapshotAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"snapshot not found"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.RestoreSnapshot(context.Background(), 123, "nightly")
	if err == nil {
		t.Fatal("RestoreSnapshot() error = nil, want API error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
}

// serverSnapshotCreateCapture is a minimal decode target for the create request
// body, declared locally to keep the test independent of the SDK's json tags
// shape.
type serverSnapshotCreateCapture struct {
	Name           string  `json:"name"`
	Description    *string `json:"description"`
	DiskName       *string `json:"diskName"`
	OnlineSnapshot bool    `json:"onlineSnapshot"`
}
