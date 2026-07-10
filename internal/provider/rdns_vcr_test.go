package provider

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// TestRDNSResource_VCRCreate replays a hand-authored cassette with exactly one
// POST (SetRDNS) followed by one GET (ConfirmRDNS read-back) whose response
// matches the requested hostname on the first attempt. The corresponding SDK
// package-level var rdnsConfirmDelay is not zeroed here (it is unexported), so
// if the cassette is re-authored with a non-matching first GET the provider's
// ConfirmRDNS will fail after 5 real-second-spaced retries — keep the GET
// response in the cassette aligned with the plan hostname.
func TestRDNSResource_VCRCreate(t *testing.T) {
	const cassetteName = "TestRDNSResource_VCRCreate"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureRDNSResource(t, client)

	ip := vcrRDNSIPForTest(t, cassetteName)
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"ip_address": tftypes.NewValue(tftypes.String, ip),
		"hostname":   tftypes.NewValue(tftypes.String, vcrTestRDNSHostname),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if len(resp.Diagnostics) != 0 {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics)
	}

	var state rdnsResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.IPAddress.ValueString() != ip {
		t.Errorf("IPAddress = %q, want %s", state.IPAddress.ValueString(), ip)
	}
	if state.Hostname.ValueString() == "" {
		t.Error("Hostname is empty")
	}
	if state.ID.ValueString() == "" {
		t.Error("ID is empty")
	}
}

func TestRDNSResource_VCRRead(t *testing.T) {
	const cassetteName = "TestRDNSResource_VCRRead"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureRDNSResource(t, client)

	ip := vcrRDNSIPForTest(t, cassetteName)

	if os.Getenv("VCR_RECORD") == "1" {
		// Seed the PTR with an unrecorded live client so the recorded Read
		// captures the expected hostname. Without this, running just this test
		// in record mode (or a changed test order) could capture a no-PTR or
		// stale-PTR response. Mirrors the SDK-level TestGetRDNS prep.
		live := liveRDNSClient(t)
		if _, err := live.SetRDNS(context.Background(), ip, vcrTestRDNSHostname); err != nil {
			t.Fatalf("SetRDNS (record-mode prep) error = %v", err)
		}
		if _, err := live.ConfirmRDNS(context.Background(), ip, &netcup.RdnsEntry{Hostname: vcrTestRDNSHostname}); err != nil {
			t.Fatalf("ConfirmRDNS (record-mode prep) error = %v", err)
		}
	}

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, ip),
		"ip_address": tftypes.NewValue(tftypes.String, ip),
		"hostname":   tftypes.NewValue(tftypes.String, vcrTestRDNSHostname),
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
	const cassetteName = "TestRDNSResource_VCRReadNoPTR"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureRDNSResource(t, client)

	ip := vcrRDNSIPForTest(t, cassetteName)

	if os.Getenv("VCR_RECORD") == "1" {
		// Use an unrecorded live client for prep so the DeleteRDNS and
		// ConfirmRDNS polling GETs don't leak into the cassette. rDNS
		// deletions are asynchronous, so confirm the PTR is empty before
		// issuing the recorded read — otherwise a stale hostname can be
		// captured instead of null.
		live := liveRDNSClient(t)
		if err := live.DeleteRDNS(context.Background(), ip); err != nil {
			// A 404 means the IP already has no custom PTR — the desired
			// pre-test state — so tolerate it. Any other error is fatal.
			var apiErr *netcup.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
				t.Fatalf("DeleteRDNS (record-mode prep) error = %v", err)
			}
		}
		if _, err := live.ConfirmRDNS(context.Background(), ip, &netcup.RdnsEntry{Hostname: ""}); err != nil {
			t.Fatalf("ConfirmRDNS (record-mode prep) error = %v", err)
		}
	}

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, ip),
		"ip_address": tftypes.NewValue(tftypes.String, ip),
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
	if os.Getenv("VCR_RECORD") == "1" {
		t.Skip("404 case uses a fixed RFC 5737 address the account does not own; it is a hand-authored fixture, not a live recording")
	}
	client := newVCRClient(t, "TestRDNSResource_VCRRead404")
	ctx := context.Background()
	r, schemaResp := configureRDNSResource(t, client)

	ip := "203.0.113.99"
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, ip),
		"ip_address": tftypes.NewValue(tftypes.String, ip),
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
	const cassetteName = "TestRDNSResource_VCRDelete"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureRDNSResource(t, client)

	ip := vcrRDNSIPForTest(t, cassetteName)

	if os.Getenv("VCR_RECORD") == "1" {
		// A prior test in this file (TestRDNSResource_VCRReadNoPTR) deletes
		// and confirms the PTR is empty, so without seeding here the recorded
		// Delete would run against an already-empty IP and save a no-op/404
		// cassette. Use an unrecorded live client for SetRDNS + ConfirmRDNS
		// prep so the cassette captures only the intended DELETE.
		live := liveRDNSClient(t)
		if _, err := live.SetRDNS(context.Background(), ip, vcrTestRDNSHostname); err != nil {
			t.Fatalf("SetRDNS (record-mode prep) error = %v", err)
		}
		if _, err := live.ConfirmRDNS(context.Background(), ip, &netcup.RdnsEntry{Hostname: vcrTestRDNSHostname}); err != nil {
			t.Fatalf("ConfirmRDNS (record-mode prep) error = %v", err)
		}
	}

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, ip),
		"ip_address": tftypes.NewValue(tftypes.String, ip),
		"hostname":   tftypes.NewValue(tftypes.String, vcrTestRDNSHostname),
	})

	var resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
}
