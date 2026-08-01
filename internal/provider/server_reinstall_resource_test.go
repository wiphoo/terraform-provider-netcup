package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// configureServerReinstallResource wires up a serverReinstallResource against the
// given client and returns the configured resource and its schema response.
func configureServerReinstallResource(t *testing.T, client *netcup.Client) (resource.ResourceWithConfigure, resource.SchemaResponse) {
	t.Helper()
	r := NewServerReinstallResource().(resource.ResourceWithConfigure)
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

// sshKeyListVal builds a tftypes list-of-number value for the ssh_key_ids attribute.
func sshKeyListVal(ids ...int64) tftypes.Value {
	elems := make([]tftypes.Value, len(ids))
	for i, id := range ids {
		elems[i] = tftypes.NewValue(tftypes.Number, id)
	}
	return tftypes.NewValue(tftypes.List{ElementType: tftypes.Number}, elems)
}

// TestServerReinstallResource_Create_Async verifies the happy path: ReinstallServer
// returns 202 with a task, wait=true polls it to FINISHED, and the request body
// carries the install inputs (image flavour + custom script).
func TestServerReinstallResource_Create_Async(t *testing.T) {
	taskPolled := false
	var gotBody netcup.ServerImageSetup
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/image":
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &gotBody); err != nil {
				t.Errorf("decode request body: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-1":
			taskPolled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerReinstallResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, "123"),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 42),
		"custom_script":    tftypes.NewValue(tftypes.String, "#!/bin/sh\necho hi"),
		"ssh_key_ids":      sshKeyListVal(7, 8),
		"wait":             tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !taskPolled {
		t.Error("expected WaitForTask to poll the task with wait=true")
	}

	// Only caller-set fields should be present in the request body.
	if gotBody.ImageFlavourID == nil || *gotBody.ImageFlavourID != 42 {
		t.Errorf("ImageFlavourID = %v, want 42", gotBody.ImageFlavourID)
	}
	if gotBody.CustomScript == nil || *gotBody.CustomScript != "#!/bin/sh\necho hi" {
		t.Errorf("CustomScript = %v, want the script", gotBody.CustomScript)
	}
	if len(gotBody.SSHKeyIDs) != 2 || gotBody.SSHKeyIDs[0] != 7 || gotBody.SSHKeyIDs[1] != 8 {
		t.Errorf("SSHKeyIDs = %v, want [7 8]", gotBody.SSHKeyIDs)
	}
	if gotBody.Hostname != nil {
		t.Errorf("Hostname = %v, want nil (unset field omitted)", gotBody.Hostname)
	}

	var state serverReinstallResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.ID.ValueString() != "123" {
		t.Errorf("ID = %q, want 123", state.ID.ValueString())
	}
	if state.TaskID.ValueString() != "task-1" {
		t.Errorf("TaskID = %q, want task-1", state.TaskID.ValueString())
	}
}

// TestServerReinstallResource_Create_NoWait verifies wait=false persists the
// accepted task without polling.
func TestServerReinstallResource_Create_NoWait(t *testing.T) {
	polled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/image":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-9","state":"PENDING"}`))
		case strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
			polled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-9","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerReinstallResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, "123"),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 1),
		"wait":             tftypes.NewValue(tftypes.Bool, false),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if polled {
		t.Error("did not expect WaitForTask to be called with wait=false")
	}

	var state serverReinstallResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.TaskID.ValueString() != "task-9" {
		t.Errorf("TaskID = %q, want task-9", state.TaskID.ValueString())
	}
}

// TestServerReinstallResource_Create_TaskFailure verifies a confirmed terminal task
// failure surfaces as an error and persists NO state (so the next apply retries).
func TestServerReinstallResource_Create_TaskFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/image":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-err","state":"PENDING"}`))
		case r.URL.Path == "/v1/tasks/task-err":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-err","state":"ERROR","message":"install failed"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerReinstallResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, "123"),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 1),
		"wait":             tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic on terminal task failure")
	}
	// No state should have been persisted.
	if !resp.State.Raw.IsNull() {
		t.Errorf("expected null state after task failure, got %v", resp.State.Raw)
	}
}

// TestServerReinstallResource_Create_APIError verifies a 4xx from ReinstallServer
// (e.g. 422 ValidationError) surfaces as an error with no state.
func TestServerReinstallResource_Create_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/image" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"code":"VALIDATION","message":"bad image"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerReinstallResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, "123"),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 999),
		"wait":             tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic on 422 API error")
	}
	if !resp.State.Raw.IsNull() {
		t.Errorf("expected null state after API error, got %v", resp.State.Raw)
	}
}

// TestServerReinstallResource_Create_DispatchDecodeError verifies that a failure
// AFTER the request was dispatched — here netcup accepted the POST (202) but the
// response body cannot be decoded — is treated as AMBIGUOUS: the reinstall may
// already be running, so state is persisted with a warning rather than dropped
// (which would let the next apply wipe the server a second time).
func TestServerReinstallResource_Create_DispatchDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/image" {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":`)) // truncated body → decode error
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerReinstallResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, "123"),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 1),
		"wait":             tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("ambiguous post-dispatch failure must not error; got: %v", resp.Diagnostics.Errors())
	}
	if len(resp.Diagnostics.Warnings()) == 0 {
		t.Error("expected a warning diagnostic for an ambiguous dispatch outcome")
	}
	if resp.State.Raw.IsNull() {
		t.Error("expected state to be persisted after an ambiguous dispatch failure")
	}
	var state serverReinstallResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.ID.ValueString() != "123" {
		t.Errorf("ID = %q, want 123", state.ID.ValueString())
	}
}

// TestServerReinstallResource_Create_OutOfRangeImageFlavourID verifies that an
// image_flavour_id outside the signed 32-bit range is rejected before dispatch
// rather than silently wrapping (4294967338 → 42), which would install the wrong
// image while Terraform records the original value.
func TestServerReinstallResource_Create_OutOfRangeImageFlavourID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerReinstallResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, "123"),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 4294967338),
		"wait":             tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic for an out-of-range image_flavour_id")
	}
	if !resp.State.Raw.IsNull() {
		t.Errorf("expected null state after validation error, got %v", resp.State.Raw)
	}
}

// TestServerReinstallResource_Create_OutOfRangeSSHKeyID verifies that an
// ssh_key_ids element outside the signed 32-bit range is rejected before dispatch
// rather than silently wrapping.
func TestServerReinstallResource_Create_OutOfRangeSSHKeyID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerReinstallResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, "123"),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 1),
		"ssh_key_ids":      sshKeyListVal(7, 8589934594), // > math.MaxInt32
		"wait":             tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic for an out-of-range ssh_key_id")
	}
	if !resp.State.Raw.IsNull() {
		t.Errorf("expected null state after validation error, got %v", resp.State.Raw)
	}
}

// TestServerReinstallResource_Delete_NoOp verifies Delete never calls the API.
func TestServerReinstallResource_Delete_NoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Delete must be a no-op; got request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerReinstallResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, "123"),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 1),
		"id":               tftypes.NewValue(tftypes.String, "123"),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
}

// TestServerReinstallResource_Read_Gone verifies a 404 from GetServer removes the
// resource from state.
func TestServerReinstallResource_Read_Gone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"no such server"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerReinstallResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, "123"),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 1),
		"id":               tftypes.NewValue(tftypes.String, "123"),
	})

	var resp resource.ReadResponse
	resp.State = state
	r.(resource.Resource).Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !resp.State.Raw.IsNull() {
		t.Error("expected resource to be removed from state on 404")
	}
}

// TestServerReinstallResource_Read_NormalizesWait verifies Read confirms the server
// exists and coerces a null wait (post-import) to the default.
func TestServerReinstallResource_Read_NormalizesWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":123,"name":"srv"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerReinstallResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":        tftypes.NewValue(tftypes.String, "123"),
		"image_flavour_id": tftypes.NewValue(tftypes.Number, 1),
		"id":               tftypes.NewValue(tftypes.String, "123"),
		// wait intentionally left null (as after import)
	})

	var resp resource.ReadResponse
	resp.State = state
	r.(resource.Resource).Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var got serverReinstallResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if got.Wait.IsNull() || !got.Wait.ValueBool() {
		t.Errorf("Wait = %v, want true (normalized default)", got.Wait)
	}
}

// TestServerReinstallResource_ImportState verifies a numeric import ID populates
// id + server_id and a non-numeric ID errors.
func TestServerReinstallResource_ImportState(t *testing.T) {
	r := NewServerReinstallResource().(resource.ResourceWithImportState)
	ctx := context.Background()
	_, schemaResp := configureServerReinstallResource(t, netcup.New())

	var resp resource.ImportStateResponse
	resp.State = resourceState(schemaResp, nil)
	r.ImportState(ctx, resource.ImportStateRequest{ID: "123"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var serverID, id types.String
	resp.State.GetAttribute(ctx, path.Root("server_id"), &serverID)
	resp.State.GetAttribute(ctx, path.Root("id"), &id)
	if serverID.ValueString() != "123" || id.ValueString() != "123" {
		t.Errorf("import set server_id=%q id=%q, want both 123", serverID.ValueString(), id.ValueString())
	}

	var bad resource.ImportStateResponse
	bad.State = resourceState(schemaResp, nil)
	r.ImportState(ctx, resource.ImportStateRequest{ID: "not-a-number"}, &bad)
	if !bad.Diagnostics.HasError() {
		t.Error("expected an error for a non-numeric import ID")
	}
}
