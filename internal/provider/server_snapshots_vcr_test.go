package provider

import (
	"context"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestServerSnapshotsDataSource(t *testing.T) {
	const cassetteName = "TestServerSnapshotsDataSource"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	ds, schemaResp := configureServerSnapshotsDataSource(t, client)

	serverID := strconv.FormatInt(int64(vcrServerIDForTest(t, cassetteName)), 10)
	req := readRequest(t, schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, serverID),
	})

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema
	ds.Read(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serverSnapshotsDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if len(state.Snapshots) == 0 {
		t.Fatal("Snapshots is empty, want at least 1")
	}

	for _, s := range state.Snapshots {
		if s.UUID.ValueString() == "" {
			t.Error("snapshot has empty UUID")
		}
		if s.Name.ValueString() == "" {
			t.Error("snapshot has empty Name")
		}
		if s.State.ValueString() == "" {
			t.Error("snapshot has empty State")
		}
	}
}
