package vcr

import (
	"context"
	"testing"
)

// TestListSnapshots replays GET /v1/servers/{id}/snapshots and asserts the
// snapshot list decodes, including the nullable description and
// exportedSizeInKiB. Read-only, so it is live-refreshable with VCR_RECORD=1; a
// live re-record on a server with no snapshots yields an empty list, which the
// len-guarded shape checks below tolerate.
func TestListSnapshots(t *testing.T) {
	const cassetteName = "TestListSnapshots"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	snaps, err := client.ListSnapshots(context.Background(), id)
	if err != nil {
		t.Fatalf("ListSnapshots() error = %v", err)
	}
	if snaps == nil {
		t.Fatal("ListSnapshots() returned nil slice, want a decoded (possibly empty) list")
	}
	if len(snaps) == 0 {
		return // an empty list is a valid response (e.g. a live re-record)
	}

	first := snaps[0]
	if first.UUID == "" || first.Name == "" || first.State == "" {
		t.Errorf("snaps[0] = %+v, want non-empty UUID, Name, and State", first)
	}
	if first.CreationTime.IsZero() {
		t.Errorf("snaps[0].CreationTime is zero, want a parsed timestamp")
	}
	if len(first.Disks) == 0 {
		t.Errorf("snaps[0].Disks is empty, want at least one disk")
	}
}

// TestListSnapshotsEmpty replays a server with no snapshots and asserts the
// documented empty-slice (not error, not nil) contract. Authored fixture, so
// replay-only (a live server's snapshot count isn't controllable here).
func TestListSnapshotsEmpty(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestListSnapshotsEmpty"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	snaps, err := client.ListSnapshots(context.Background(), id)
	if err != nil {
		t.Fatalf("ListSnapshots() error = %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("ListSnapshots() = %+v, want an empty list", snaps)
	}
}
