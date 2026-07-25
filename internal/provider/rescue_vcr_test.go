package provider

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestRescueResource_VCRActive(t *testing.T) {
	const cassetteName = "TestRescueResource_VCRActive"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureRescueResourceVCR(t, client)

	serverID := strconv.FormatInt(int64(vcrServerIDForTest(t, cassetteName)), 10)
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, serverID),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, nil),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
		"id":        tftypes.NewValue(tftypes.String, serverID),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var result rescueResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if !result.Active.ValueBool() {
		t.Error("Active = false, want true")
	}
	if result.ID.ValueString() == "" {
		t.Error("ID is empty")
	}
}

func TestRescueResource_VCRInactive(t *testing.T) {
	const cassetteName = "TestRescueResource_VCRInactive"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureRescueResourceVCR(t, client)

	serverID := strconv.FormatInt(int64(vcrServerIDForTest(t, cassetteName)), 10)
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, serverID),
		"active":    tftypes.NewValue(tftypes.Bool, false),
		"password":  tftypes.NewValue(tftypes.String, nil),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
		"id":        tftypes.NewValue(tftypes.String, serverID),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if !resp.State.Raw.IsNull() {
		t.Error("State.Raw should be null after RemoveResource for inactive rescue")
	}
}

func TestRescueResource_VCRActiveWithPassword(t *testing.T) {
	if os.Getenv("VCR_RECORD") == "1" {
		t.Skip("Active with password test uses a hand-authored cassette; enable requires server reboot")
	}

	const cassetteName = "TestRescueResource_VCRActiveWithPassword"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureRescueResourceVCR(t, client)

	serverID := strconv.FormatInt(int64(vcrServerIDForTest(t, cassetteName)), 10)
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, serverID),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "vcr-redacted-password"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
		"id":        tftypes.NewValue(tftypes.String, serverID),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var result rescueResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if !result.Active.ValueBool() {
		t.Error("Active = false, want true")
	}
	if result.Password.ValueString() != "vcr-redacted-password" {
		t.Errorf("Password = %q, want vcr-redacted-password", result.Password.ValueString())
	}
}
