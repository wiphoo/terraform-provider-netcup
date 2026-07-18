package netcup

import (
	"context"
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
