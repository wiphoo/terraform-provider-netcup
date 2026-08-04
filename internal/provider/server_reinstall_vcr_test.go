package provider

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dnaeon/go-vcr/cassette"
	"gopkg.in/yaml.v2"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestServerReinstallResource_VCRCreate replays a destructive OS reinstall
// against a hand-authored cassette: POST /v1/servers/{id}/image returning a
// 202 TaskInfo, then a single GET /v1/tasks/{uuid} returning FINISHED (terminal
// on the first poll, so WaitForTask returns immediately with no between-poll
// sleep). wait=true is exercised, and the committed request body is asserted to
// carry the redacted custom-script marker rather than the real script value
// (customScript contents may contain secrets). Replay-only: recording would
// actually wipe the maintainer's server (see the VCR_RECORD skip).
func TestServerReinstallResource_VCRCreate(t *testing.T) {
	if os.Getenv("VCR_RECORD") == "1" {
		t.Skip("reinstall cassette is a hand-authored fixture; recording would perform a destructive OS reinstall")
	}

	const cassetteName = "TestServerReinstallResource_VCRCreate"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureServerReinstallResource(t, client)

	serverID := strconv.FormatInt(int64(vcrServerIDForTest(t, cassetteName)), 10)
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, serverID),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 42),
		"custom_script":    tftypes.NewValue(tftypes.String, "#!/bin/sh\necho do-not-commit-me > /etc/motd\n"),
		"wait":             tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serverReinstallResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.ID.ValueString() != serverID {
		t.Errorf("ID = %q, want %s", state.ID.ValueString(), serverID)
	}
	if state.ServerID.ValueString() != serverID {
		t.Errorf("ServerID = %q, want %s", state.ServerID.ValueString(), serverID)
	}
	if state.TaskID.ValueString() != "66666666-6666-4666-8666-666666666666" {
		t.Errorf("TaskID = %q, want the cassette task UUID", state.TaskID.ValueString())
	}

	// The reinstall request body is secret-bearing (customScript), so the
	// committed cassette must carry the redacted marker — never the real script
	// value. matchInteraction keys on method+URL only, so a body regression
	// would otherwise replay green; assert the on-disk body directly.
	requestBody := firstRequestBodyFromProviderCassette(t, cassetteName)
	if !strings.Contains(requestBody, `"customScript":"vcr-redacted-custom-script"`) {
		t.Errorf("cassette request body = %s, want customScript == %q (redacted marker)", requestBody, "vcr-redacted-custom-script")
	}
	if strings.Contains(requestBody, "do-not-commit-me") {
		t.Error("cassette request body leaks the real custom_script value")
	}
}

// TestServerReinstallResource_VCRCreateNoWait replays the wait=false path: the
// reinstall POST returns a 202 TaskInfo and Create persists the accepted task
// WITHOUT polling it (no GET /v1/tasks/{uuid} interaction in the cassette).
// Replay-only (destructive).
func TestServerReinstallResource_VCRCreateNoWait(t *testing.T) {
	if os.Getenv("VCR_RECORD") == "1" {
		t.Skip("reinstall cassette is a hand-authored fixture; recording would perform a destructive OS reinstall")
	}

	const cassetteName = "TestServerReinstallResource_VCRCreateNoWait"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureServerReinstallResource(t, client)

	serverID := strconv.FormatInt(int64(vcrServerIDForTest(t, cassetteName)), 10)
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, serverID),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 42),
		"wait":             tftypes.NewValue(tftypes.Bool, false),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	// If Create regressed and polled the task despite wait=false, the absent GET
	// interaction produces a go-vcr error that serverReinstallResource.Create
	// classifies as INDETERMINATE and surfaces as a warning (state + task ID are
	// still persisted), so neither the HasError check nor the task-ID assertion
	// below would catch it. Assert zero warnings to pin the no-poll behavior.
	if len(resp.Diagnostics.Warnings()) != 0 {
		t.Fatalf("Create() emitted warnings (wait=false must not poll the task): %v", resp.Diagnostics.Warnings())
	}

	var state serverReinstallResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.TaskID.ValueString() != "77777777-7777-4777-8777-777777777777" {
		t.Errorf("TaskID = %q, want the accepted task UUID", state.TaskID.ValueString())
	}
}

// TestServerReinstallResource_VCRCreateAPIError replays a 422 ValidationError
// rejection (an image flavour the server won't accept) and asserts Create
// surfaces an error diagnostic and persists NO state. Replay-only.
func TestServerReinstallResource_VCRCreateAPIError(t *testing.T) {
	if os.Getenv("VCR_RECORD") == "1" {
		t.Skip("reinstall cassette is a hand-authored fixture; recording would perform a destructive OS reinstall")
	}

	const cassetteName = "TestServerReinstallResource_VCRCreateAPIError"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureServerReinstallResource(t, client)

	serverID := strconv.FormatInt(int64(vcrServerIDForTest(t, cassetteName)), 10)
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, serverID),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 999999),
		"wait":             tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic on a 422 API error")
	}
	if !resp.State.Raw.IsNull() {
		t.Errorf("expected null state after a 422 API error, got %v", resp.State.Raw)
	}
}

// TestServerReinstallResource_VCRDeleteNoOp verifies Delete is a no-op over the
// VCR client: it must issue NO request (the cassette has zero interactions), so
// a reinstall/wipe request emitted on destroy would fail the replay instead of
// passing silently. The unit test asserts the same via httptest; this pins the
// behavior against the recorder transport.
func TestServerReinstallResource_VCRDeleteNoOp(t *testing.T) {
	const cassetteName = "TestServerReinstallResource_VCRDeleteNoOp"
	client := newVCRClient(t, cassetteName)
	ctx := context.Background()
	r, schemaResp := configureServerReinstallResource(t, client)

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, "1000123"),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 42),
		"wait":             tftypes.NewValue(tftypes.Bool, true),
		"id":               tftypes.NewValue(tftypes.String, "1000123"),
	})

	var resp resource.DeleteResponse
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
}

// firstRequestBodyFromProviderCassette returns the request body of the first
// interaction in the named provider-tier cassette that has one (the reinstall
// POST), so a body assertion compares against the committed fixture.
func firstRequestBodyFromProviderCassette(t *testing.T, cassetteName string) string {
	t.Helper()
	path := filepath.Join("testdata", "cassettes", cassetteName+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cassette %q: %v", path, err)
	}
	var c cassette.Cassette
	if err := yaml.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse cassette %q: %v", path, err)
	}
	for _, ia := range c.Interactions {
		if ia != nil && ia.Request.Body != "" {
			return ia.Request.Body
		}
	}
	t.Fatalf("cassette %q has no request with a body", cassetteName)
	return ""
}
