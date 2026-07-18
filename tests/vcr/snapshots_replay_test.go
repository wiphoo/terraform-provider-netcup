package vcr

import (
	"context"
	"testing"
)

// TestListSnapshots replays GET /v1/servers/{id}/snapshots and asserts the
// snapshot list decodes, including the nullable description and
// exportedSizeInKiB.
//
// Replay-only: although the call is read-only, SnapshotMinimal.uuid is a live
// resource identifier with no save-filter redaction rule (and, being an opaque
// UUID with no distinctive shape, no feasible scrub guard — unlike the marked
// synthetic ranges used for IPs/MACs), so a VCR_RECORD refresh against a server
// with snapshots would commit real snapshot UUIDs. Keeping it replay-only
// avoids that; the fixture is authored from the documented SnapshotMinimal
// schema. See CONTRIBUTING.md and PR #88.
func TestListSnapshots(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestListSnapshots"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	snaps, err := client.ListSnapshots(context.Background(), id)
	if err != nil {
		t.Fatalf("ListSnapshots() error = %v", err)
	}
	if len(snaps) == 0 {
		t.Fatal("ListSnapshots() returned no snapshots, want the recorded fixtures")
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
