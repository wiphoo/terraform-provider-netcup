package provider

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
	"github.com/wiphoo/terraform-provider-netcup/tests/vcr"
)

// snapshotResourceVCRPlan creates a tfsdk.Plan for the snapshot resource.
func snapshotResourceVCRPlan(schemaResp resource.SchemaResponse, values map[string]tftypes.Value) tfsdk.Plan {
	ctx := context.Background()
	objType := schemaResp.Schema.Type().TerraformType(ctx)
	objAttrs := objType.(tftypes.Object).AttributeTypes

	raw := map[string]tftypes.Value{}
	for name, attrType := range objAttrs {
		if v, ok := values[name]; ok {
			raw[name] = v
		} else {
			raw[name] = tftypes.NewValue(attrType, nil)
		}
	}
	return tfsdk.Plan{
		Raw:    tftypes.NewValue(objType, raw),
		Schema: schemaResp.Schema,
	}
}

// snapshotResourceVCRState creates a tfsdk.State for the snapshot resource.
func snapshotResourceVCRState(schemaResp resource.SchemaResponse, values map[string]tftypes.Value) tfsdk.State {
	ctx := context.Background()
	objType := schemaResp.Schema.Type().TerraformType(ctx)
	objAttrs := objType.(tftypes.Object).AttributeTypes

	raw := map[string]tftypes.Value{}
	for name, attrType := range objAttrs {
		if v, ok := values[name]; ok {
			raw[name] = v
		} else {
			raw[name] = tftypes.NewValue(attrType, nil)
		}
	}
	return tfsdk.State{
		Raw:    tftypes.NewValue(objType, raw),
		Schema: schemaResp.Schema,
	}
}

// configureServerSnapshotResourceVCR configures the server snapshot resource with a VCR client.
func configureServerSnapshotResourceVCR(t *testing.T, client *netcup.Client) (resource.ResourceWithConfigure, resource.SchemaResponse) {
	t.Helper()
	r := NewServerSnapshotResource().(resource.ResourceWithConfigure)
	ctx := context.Background()

	var configResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	return r, schemaResp
}

// TestServerSnapshotResource_VCRCreate replays the CreateSnapshot lifecycle
// using the TestServerSnapshotResource_VCRCreate.yaml cassette.
func TestServerSnapshotResource_VCRCreate(t *testing.T) {
	if os.Getenv("VCR_RECORD") == "1" {
		t.Skip("replay-only: snapshot VCR cassette")
	}
	client := vcr.NewClient(t, "TestServerSnapshotResource_VCRCreate")
	serverID := vcr.ServerIDForTest(t, "TestServerSnapshotResource_VCRCreate")

	r, schemaResp := configureServerSnapshotResourceVCR(t, client)
	ctx := context.Background()

	desc := "vcr-redacted-description"
	disk := "vda"

	// Prepare create request (Plan) — align with cassette: cassette POST omits onlineSnapshot.
	plan := snapshotResourceVCRPlan(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, strconv.FormatInt(int64(serverID), 10)),
		"name":                tftypes.NewValue(tftypes.String, "server-0a0b0c0d"),
		"description":         tftypes.NewValue(tftypes.String, desc),
		"disk_name":           tftypes.NewValue(tftypes.String, disk),
		"wait":                tftypes.NewValue(tftypes.Bool, true),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-08-20T10:00:00Z"),
	})

	// Execute create
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", createResp.Diagnostics.Errors())
	}

	// Verify the create response has the expected UUID and TaskID.
	var state serverSnapshotResourceModel
	createResp.Diagnostics.Append(createResp.State.Get(ctx, &state)...)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", createResp.Diagnostics.Errors())
	}

	if state.UUID.ValueString() != "88888888-8888-4888-8888-888888888888" {
		t.Errorf("UUID = %q, want 88888888-8888-4888-8888-888888888888", state.UUID.ValueString())
	}
	if state.TaskID.ValueString() != "77777777-7777-4777-8777-777777777777" {
		t.Errorf("TaskID = %q, want 77777777-7777-4777-8777-777777777777", state.TaskID.ValueString())
	}
	if state.Name.ValueString() != "server-0a0b0c0d" {
		t.Errorf("Name = %q, want server-0a0b0c0d", state.Name.ValueString())
	}
	if state.Description.ValueString() != desc {
		t.Errorf("Description = %q, want %q", state.Description.ValueString(), desc)
	}
	if state.DiskName.ValueString() != disk {
		t.Errorf("DiskName = %q, want %q", state.DiskName.ValueString(), disk)
	}
}

// TestServerSnapshotResource_VCRDelete replays the DeleteSnapshot lifecycle
// using the TestServerSnapshotResource_VCRDelete.yaml cassette.
func TestServerSnapshotResource_VCRDelete(t *testing.T) {
	if os.Getenv("VCR_RECORD") == "1" {
		t.Skip("replay-only: snapshot VCR cassette")
	}
	var requests vcr.RequestLog
	client := vcr.NewRequestLoggingClient(t, "TestServerSnapshotResource_VCRDelete", &requests)
	serverID := vcr.ServerIDForTest(t, "TestServerSnapshotResource_VCRDelete")

	r, schemaResp := configureServerSnapshotResourceVCR(t, client)
	ctx := context.Background()

	name := "server-0a0b0c0d"

	// Prepare delete request (State) - the Delete cassette's initial GET
	// listing returns a snapshot with this UUID, so the state must have it.
	deleteState := snapshotResourceVCRState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, strconv.FormatInt(int64(serverID), 10)),
		"name":                tftypes.NewValue(tftypes.String, name),
		"uuid":                tftypes.NewValue(tftypes.String, "77777777-7777-4777-8777-777777777777"),
		"task_id":             tftypes.NewValue(tftypes.String, "77777777-7777-4777-8777-777777777777"),
		"wait":                tftypes.NewValue(tftypes.Bool, true),
		"description":         tftypes.NewValue(tftypes.String, "vcr-redacted-description"),
		"disk_name":           tftypes.NewValue(tftypes.String, "vda"),
		"online_snapshot":     tftypes.NewValue(tftypes.Bool, true),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-08-20T10:00:00Z"),
	})

	var deleteResp resource.DeleteResponse
	deleteResp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Delete(ctx, resource.DeleteRequest{State: deleteState}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("Delete() unexpected diagnostics: %v", deleteResp.Diagnostics.Errors())
	}

	// wait=true must actually poll the delete task. go-vcr replay does NOT
	// fail when a cassette interaction goes unconsumed, so a Delete regression
	// that stopped polling after listing + DELETE would replay green. Assert the
	// terminal task GET was issued to pin the wait path this test claims to exercise.
	taskURL := "https://www.servercontrolpanel.de/scp-core/api/v1/tasks/77777777-7777-4777-8777-777777777777"
	if !requests.Contains(http.MethodGet, taskURL) {
		t.Errorf("Delete() with wait=true did not poll the delete task (want GET %s); requests:\n%s", taskURL, &requests)
	}
}
