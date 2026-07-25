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

func TestServerPowerResource_VCRCreate(t *testing.T) {
	const cassetteName = "TestServerPowerResource_VCRCreate"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureServerPowerResourceVCR(t, client)

	serverID := strconv.FormatInt(int64(vcrServerIDForTest(t, cassetteName)), 10)
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, serverID),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.ServerID.ValueString() != serverID {
		t.Errorf("ServerID = %q, want %s", state.ServerID.ValueString(), serverID)
	}
	if state.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON", state.State.ValueString())
	}
	if state.ID.ValueString() != serverID {
		t.Errorf("ID = %q, want %s", state.ID.ValueString(), serverID)
	}
}

func TestServerPowerResource_VCRCreateWithPowerOff(t *testing.T) {
	if os.Getenv("VCR_RECORD") == "1" {
		t.Skip("power-off cassette is a hand-authored fixture; recording sends a live OFF command with no power-on cleanup")
	}

	const cassetteName = "TestServerPowerResource_VCRCreateWithPowerOff"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureServerPowerResourceVCR(t, client)

	serverID := strconv.FormatInt(int64(vcrServerIDForTest(t, cassetteName)), 10)
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, serverID),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF", state.State.ValueString())
	}
	if state.ServerID.ValueString() != serverID {
		t.Errorf("ServerID = %q, want %s", state.ServerID.ValueString(), serverID)
	}
}

func TestServerPowerResource_VCRRead(t *testing.T) {
	const cassetteName = "TestServerPowerResource_VCRRead"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureServerPowerResourceVCR(t, client)

	serverID := strconv.FormatInt(int64(vcrServerIDForTest(t, cassetteName)), 10)
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, serverID),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
		"id":           tftypes.NewValue(tftypes.String, serverID),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if result.State.ValueString() == "" {
		t.Error("State is empty after Read")
	}
	if result.ID.ValueString() == "" {
		t.Error("ID is empty after Read")
	}
	if result.ServerID.ValueString() != serverID {
		t.Errorf("ServerID = %q, want %s", result.ServerID.ValueString(), serverID)
	}
}

func TestServerPowerResource_VCRReadSuspended(t *testing.T) {
	const cassetteName = "TestServerPowerResource_VCRReadSuspended"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureServerPowerResourceVCR(t, client)

	serverID := strconv.FormatInt(int64(vcrServerIDForTest(t, cassetteName)), 10)
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, serverID),
		"state":        tftypes.NewValue(tftypes.String, "SUSPENDED"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
		"id":           tftypes.NewValue(tftypes.String, serverID),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if result.State.ValueString() == "" {
		t.Error("State is empty after Read")
	}
}
