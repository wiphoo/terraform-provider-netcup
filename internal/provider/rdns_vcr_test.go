package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestRDNSResource_VCRCreate(t *testing.T) {
	client := newVCRClient(t, "TestRDNSResource_VCRCreate")
	ctx := context.Background()
	r, schemaResp := configureRDNSResource(t, client)

	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"ip_address": tftypes.NewValue(tftypes.String, "203.0.113.10"),
		"hostname":   tftypes.NewValue(tftypes.String, "host-a1b2c3d4.example.com"),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state rdnsResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.IPAddress.ValueString() != "203.0.113.10" {
		t.Errorf("IPAddress = %q, want 203.0.113.10", state.IPAddress.ValueString())
	}
	if state.Hostname.ValueString() == "" {
		t.Error("Hostname is empty")
	}
	if state.ID.ValueString() == "" {
		t.Error("ID is empty")
	}
}

func TestRDNSResource_VCRRead(t *testing.T) {
	client := newVCRClient(t, "TestRDNSResource_VCRRead")
	ctx := context.Background()
	r, schemaResp := configureRDNSResource(t, client)

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, "203.0.113.10"),
		"ip_address": tftypes.NewValue(tftypes.String, "203.0.113.10"),
		"hostname":   tftypes.NewValue(tftypes.String, "host-a1b2c3d4.example.com"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var result rdnsResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if result.Hostname.ValueString() == "" {
		t.Error("Hostname is empty")
	}
	if result.ID.ValueString() == "" {
		t.Error("ID is empty")
	}
}

func TestRDNSResource_VCRReadNoPTR(t *testing.T) {
	client := newVCRClient(t, "TestRDNSResource_VCRReadNoPTR")
	ctx := context.Background()
	r, schemaResp := configureRDNSResource(t, client)

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, "203.0.113.20"),
		"ip_address": tftypes.NewValue(tftypes.String, "203.0.113.20"),
		"hostname":   tftypes.NewValue(tftypes.String, ""),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if !resp.State.Raw.IsNull() {
		t.Error("State.Raw should be null after RemoveResource for missing PTR")
	}
}

func TestRDNSResource_VCRRead404(t *testing.T) {
	client := newVCRClient(t, "TestRDNSResource_VCRRead404")
	ctx := context.Background()
	r, schemaResp := configureRDNSResource(t, client)

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, "203.0.113.99"),
		"ip_address": tftypes.NewValue(tftypes.String, "203.0.113.99"),
		"hostname":   tftypes.NewValue(tftypes.String, "host-a1b2c3d4.example.com"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if !resp.State.Raw.IsNull() {
		t.Error("State.Raw should be null after RemoveResource for 404")
	}
}

func TestRDNSResource_VCRDelete(t *testing.T) {
	client := newVCRClient(t, "TestRDNSResource_VCRDelete")
	ctx := context.Background()
	r, schemaResp := configureRDNSResource(t, client)

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, "203.0.113.10"),
		"ip_address": tftypes.NewValue(tftypes.String, "203.0.113.10"),
		"hostname":   tftypes.NewValue(tftypes.String, "host-a1b2c3d4.example.com"),
	})

	var resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
}
