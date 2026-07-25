package provider

import (
	"context"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestServerImagesDataSource(t *testing.T) {
	const cassetteName = "TestServerImagesDataSource"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	ds, schemaResp := configureServerImagesDataSource(t, client)

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

	var state serverImagesDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if len(state.Images) == 0 {
		t.Fatal("Images is empty, want at least 1")
	}

	sawImage := false
	for _, img := range state.Images {
		if img.ID.ValueInt64() == 0 {
			t.Errorf("image flavour has zero ID: %+v", img)
		}
		if img.Name.ValueString() == "" {
			t.Errorf("image flavour %d has empty name", img.ID.ValueInt64())
		}
		if img.Image != nil {
			sawImage = true
			if img.Image.ID.ValueInt64() == 0 {
				t.Errorf("flavour %d nested image has zero ID", img.ID.ValueInt64())
			}
		}
	}
	if !sawImage {
		t.Error("no flavour carried a decoded nested image; want at least one")
	}
}
