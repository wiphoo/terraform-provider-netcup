package provider

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestServerDataSource(t *testing.T) {
	client := newVCRClient(t, "TestServerDataSource")
	ctx := context.Background()
	ds, schemaResp := configureServerDataSource(t, client)

	serverID := strconv.FormatInt(int64(vcrServerIDForTest(t)), 10)
	req := readRequest(t, schemaResp, map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, serverID),
	})

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema
	ds.Read(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serverDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if state.ID.ValueString() != serverID {
		t.Errorf("ID = %q, want %s", state.ID.ValueString(), serverID)
	}
	if state.Hostname.ValueString() == "" {
		t.Error("Hostname is empty")
	}
	if state.Status.ValueString() == "" {
		t.Error("Status is empty")
	}
	if state.ProductName.ValueString() == "" {
		t.Error("ProductName is empty")
	}
	if len(state.IPv4Addresses.Elements()) == 0 {
		t.Error("IPv4Addresses is empty")
	}
	if len(state.IPv6Addresses) == 0 {
		t.Error("IPv6Addresses is empty")
	}
}

func TestServerDataSource_VCRNullableFields(t *testing.T) {
	if os.Getenv("VCR_RECORD") == "1" {
		t.Skip("nullable-fields cassette is a hand-authored fixture, not a live recording")
	}
	client := newVCRClient(t, "TestServerDataSource_VCRNullableFields")
	ctx := context.Background()
	ds, schemaResp := configureServerDataSource(t, client)

	req := readRequest(t, schemaResp, map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, "789"),
	})

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema
	ds.Read(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serverDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if state.Hostname.ValueString() != "" {
		t.Errorf("Hostname = %q, want empty string", state.Hostname.ValueString())
	}
	if state.Status.ValueString() != "" {
		t.Errorf("Status = %q, want empty string", state.Status.ValueString())
	}
	if state.ProductName.ValueString() != "" {
		t.Errorf("ProductName = %q, want empty string", state.ProductName.ValueString())
	}
	if len(state.IPv4Addresses.Elements()) != 0 {
		t.Errorf("got %d IPv4 addresses, want 0", len(state.IPv4Addresses.Elements()))
	}
	if len(state.IPv6Addresses) != 0 {
		t.Errorf("got %d IPv6 addresses, want 0", len(state.IPv6Addresses))
	}
}
