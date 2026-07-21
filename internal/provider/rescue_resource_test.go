package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// configureRescueResource sets up a rescueResource with the given client and
// returns it along with its schema (mirroring the rdns test helper pattern).
func configureRescueResource(t *testing.T, client *netcup.Client) (resource.ResourceWithConfigure, resource.SchemaResponse) {
	t.Helper()
	r := NewRescueResource().(resource.ResourceWithConfigure)
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

// rescueClient returns a test client pointed at the given server URL with a
// near-zero poll interval so WaitForTask does not sleep during unit tests.
func rescueClient(serverURL string) *netcup.Client {
	return netcup.New(
		netcup.WithAPIEndpoint(serverURL),
		netcup.WithAccessToken("tok123"),
		netcup.WithTaskPollInterval(time.Millisecond),
	)
}

// TestRescueResource_Create verifies the happy path: POST enable → 202+task,
// GET task → FINISHED, GET rescuesystem → active+password.
func TestRescueResource_Create(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/12345/rescuesystem":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-uuid-1","name":"EnableRescue","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-uuid-1":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-uuid-1","name":"EnableRescue","state":"FINISHED"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/12345/rescuesystem":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"active":true,"password":"s3cr3t-rescue-pw"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state rescueResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.ServerID.ValueString() != "12345" {
		t.Errorf("ServerID = %q, want 12345", state.ServerID.ValueString())
	}
	if state.ID.ValueString() != "12345" {
		t.Errorf("ID = %q, want 12345", state.ID.ValueString())
	}
	if !state.Active.ValueBool() {
		t.Error("Active = false, want true")
	}
	if state.Password.ValueString() != "s3cr3t-rescue-pw" {
		t.Errorf("Password = %q, want s3cr3t-rescue-pw", state.Password.ValueString())
	}
}

// TestRescueResource_CreateNilPassword verifies that a nil password from the API
// is stored as null (not an error), mirroring the #62 CLI behavior.
func TestRescueResource_CreateNilPassword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/12345/rescuesystem":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-uuid-1","name":"EnableRescue","state":"FINISHED"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-uuid-1":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-uuid-1","name":"EnableRescue","state":"FINISHED"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/12345/rescuesystem":
			// API returns active but password not yet surfaced.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"active":true,"password":null}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() should not error on nil password: %v", resp.Diagnostics.Errors())
	}

	var state rescueResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !state.Password.IsNull() {
		t.Errorf("Password should be null when API returns null, got %q", state.Password.ValueString())
	}
	if !state.Active.ValueBool() {
		t.Error("Active = false, want true")
	}
}

// TestRescueResource_CreateEnableError verifies that an API error during enable
// is propagated as a diagnostic error.
func TestRescueResource_CreateEnableError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/servers/12345/rescuesystem" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"rescue system already active"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Create() should return error on API 400")
	}
	// Thread B fix: a definitive 4xx rejection means no enable task was created
	// and rescue is NOT active — no state should be persisted.
	if !resp.State.Raw.IsNull() {
		t.Error("State should be null after a definitive 4xx enable rejection")
	}
}

// TestRescueResource_Read verifies that an active rescue system is reflected in
// state correctly.
func TestRescueResource_Read(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/servers/12345/rescuesystem" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"active":true,"password":"s3cr3t-rescue-pw"}`))
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "s3cr3t-rescue-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
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
	if result.Password.ValueString() != "s3cr3t-rescue-pw" {
		t.Errorf("Password = %q, want s3cr3t-rescue-pw", result.Password.ValueString())
	}
	if result.ID.ValueString() != "12345" {
		t.Errorf("ID = %q, want 12345", result.ID.ValueString())
	}
}

// TestRescueResource_ReadInactive verifies that an inactive rescue system causes
// the resource to be removed from state (so Terraform plans a re-enable).
func TestRescueResource_ReadInactive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"active":false,"password":null}`))
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "old-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
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

// TestRescueResource_Read404 verifies that a 404 (server not found) causes
// the resource to be removed from state rather than raising an error.
func TestRescueResource_Read404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"server not found"}`))
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "old-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
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

// TestRescueResource_Delete verifies the happy path: DELETE disable → 202+task,
// GET task → FINISHED.
func TestRescueResource_Delete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/12345/rescuesystem":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-uuid-del","name":"DisableRescue","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-uuid-del":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-uuid-del","name":"DisableRescue","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "s3cr3t-rescue-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
}

// TestRescueResource_DeleteTaskError verifies that a failed async task surfaces
// as a diagnostic error during Delete.
func TestRescueResource_DeleteTaskError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/12345/rescuesystem":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-uuid-err","name":"DisableRescue","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-uuid-err":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-uuid-err","name":"DisableRescue","state":"ERROR","message":"internal error"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "s3cr3t-rescue-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Delete() should return error when task fails")
	}
}

// TestRescueResource_ImportState verifies that importing by server_id populates
// the id attribute correctly.
func TestRescueResource_ImportState(t *testing.T) {
	r := NewRescueResource().(interface {
		resource.ResourceWithImportState
		resource.ResourceWithConfigure
	})

	client := netcup.New(netcup.WithAccessToken("tok"))
	ctx := context.Background()

	var configResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	var resp resource.ImportStateResponse
	objType := schemaResp.Schema.Type().TerraformType(ctx)
	resp.State = tfsdk.State{
		Raw:    tftypes.NewValue(objType, nil),
		Schema: schemaResp.Schema,
	}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "99999"}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var id types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("GetAttribute() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if id.ValueString() != "99999" {
		t.Errorf("id = %q, want 99999", id.ValueString())
	}
}

// TestRescueResource_CreateWaitFalse verifies Thread A fix: when wait=false the
// Create succeeds with KNOWN placeholder values for active/password. The
// Terraform plugin protocol requires every attribute that was unknown in the
// plan to become a known value in the final state — returning Unknown() causes
// "Provider produced inconsistent result after apply". The placeholders are:
//
//	active=true  — we just submitted an enable; the intended managed state is
//	               active; the next refresh reconciles to the live value.
//	password=null — not yet available; null is a valid known value; it becomes
//	                populated on the next refresh via Read.
func TestRescueResource_CreateWaitFalse(t *testing.T) {
	// The test server only handles the POST enable; it must NOT receive a GET
	// rescuesystem call because we skip the read-back when wait=false.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/12345/rescuesystem":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-nowait","name":"EnableRescue","state":"PENDING"}`))
		default:
			t.Errorf("unexpected request with wait=false: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"wait":      tftypes.NewValue(tftypes.Bool, false),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	// Should not have errors (warnings are OK for the task-UUID notice).
	// Note: use HasError()/Errors() rather than comparing Severity() to a
	// literal — in terraform-plugin-framework diag.SeverityError == 1 (0 is
	// SeverityInvalid), so a `Severity() == 0` guard would never catch a real
	// error and let a failing Create slip through silently.
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create(wait=false) unexpected error: %v", resp.Diagnostics.Errors())
	}

	var state rescueResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.ID.ValueString() != "12345" {
		t.Errorf("ID = %q, want 12345", state.ID.ValueString())
	}
	// Thread A fix: active and password must be KNOWN placeholder values, not
	// Unknown(). Returning Unknown() from Create violates the Terraform protocol.
	if state.Active.IsUnknown() {
		t.Errorf("Active must be a known value when wait=false (got Unknown); use BoolValue(true) as placeholder")
	}
	if !state.Active.ValueBool() {
		t.Errorf("Active = false when wait=false, want true (placeholder for intended active state)")
	}
	if state.Password.IsUnknown() {
		t.Errorf("Password must be a known value when wait=false (got Unknown); use StringNull() as placeholder")
	}
	if !state.Password.IsNull() {
		t.Errorf("Password = %q when wait=false, want null (placeholder until refresh populates it)", state.Password.ValueString())
	}
}

// TestRescueResource_CreateReadBackFailRetainsID verifies Thread 2 fix: if the
// post-enable GetRescueSystem call fails, the resource ID is still persisted in
// state so the next apply can read/reconcile rather than attempting a
// duplicate enable (which the API rejects as already-active).
func TestRescueResource_CreateReadBackFailRetainsID(t *testing.T) {
	taskDone := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/12345/rescuesystem":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-ok","name":"EnableRescue","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-ok":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			taskDone = true
			_, _ = w.Write([]byte(`{"uuid":"task-ok","name":"EnableRescue","state":"FINISHED"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/12345/rescuesystem":
			// Simulate a transient failure on the read-back.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"temporary failure"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	// Create should return an error because the read-back failed.
	if !resp.Diagnostics.HasError() {
		t.Fatal("Create() should error when read-back fails")
	}
	if !taskDone {
		t.Fatal("WaitForTask should have been called")
	}

	// But the resource ID must be in state (partial state), so Terraform does
	// not attempt a duplicate enable on the next apply.
	var state rescueResourceModel
	// Clear diagnostics before reading state (errors were about the read-back,
	// not the state set).
	resp.Diagnostics = nil
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.ID.IsNull() || state.ID.IsUnknown() || state.ID.ValueString() == "" {
		t.Error("ID should be persisted in state even when read-back fails")
	}
	if state.ServerID.ValueString() != "12345" {
		t.Errorf("ServerID = %q, want 12345", state.ServerID.ValueString())
	}
	// Thread C fix: retained partial state must use KNOWN placeholders, not
	// Unknown() — Terraform rejects a stored NewState containing unknown values
	// after an errored apply.
	if state.Active.IsUnknown() {
		t.Error("Active must be a known placeholder (not Unknown) in retained partial state when read-back fails")
	}
	if state.Password.IsUnknown() {
		t.Error("Password must be a known placeholder (not Unknown) in retained partial state when read-back fails")
	}
}

// TestRescueResource_UpdateWait verifies Thread 3 fix: changing the wait
// attribute (true→false or false→true) dispatches to Update, which must copy
// the new wait value into state without erroring or making any API call.
func TestRescueResource_UpdateWait(t *testing.T) {
	// The test server must receive NO requests — Update makes no API calls.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Update should make no API call, got: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	existingState := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "s3cr3t"),
		"wait":      tftypes.NewValue(tftypes.Bool, true), // old value
	})
	newPlan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "s3cr3t"),
		"wait":      tftypes.NewValue(tftypes.Bool, false), // changed
	})

	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.(resource.Resource).Update(ctx, resource.UpdateRequest{
		State: existingState,
		Plan:  newPlan,
	}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Update() unexpected error: %v", resp.Diagnostics.Errors())
	}

	var state rescueResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	// wait must reflect the planned value.
	if state.Wait.ValueBool() != false {
		t.Errorf("Wait = %v, want false after update", state.Wait.ValueBool())
	}
	// Other attributes must be preserved from prior state.
	if !state.Active.ValueBool() {
		t.Error("Active should remain true after wait-only update")
	}
	if state.Password.ValueString() != "s3cr3t" {
		t.Errorf("Password = %q, want s3cr3t after wait-only update", state.Password.ValueString())
	}
}

// TestRescueResource_DeleteAlreadyDeactivated verifies Thread 4 fix: if the
// rescue system was deactivated outside Terraform, DisableRescueSystem returns
// HTTP 400 with "deactivated" in the body. Delete should treat this as success
// (desired state reached) rather than a hard error.
func TestRescueResource_DeleteAlreadyDeactivated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/12345/rescuesystem" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"rescue system currently deactivated"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "s3cr3t-rescue-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() should succeed when rescue is already deactivated, got: %v", resp.Diagnostics.Errors())
	}
}

// TestRescueResource_DeleteAlreadyPending verifies Thread B fix: a 400 whose
// body contains "already" but NOT "deactivat" (e.g. "operation already
// pending") must be treated as a hard error — NOT as a successful deactivation.
// Prior to Thread B fix, the "already" substring clause in isAlreadyDeactivated
// would have silently swallowed this, dropping the resource from state while
// rescue mode was still active.
func TestRescueResource_DeleteAlreadyPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/12345/rescuesystem" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"operation already pending"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "s3cr3t-rescue-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Delete() should error on 'operation already pending' 400 — must not be treated as deactivation success")
	}
}

// TestRescueResource_DeleteUnrelated400 verifies that an unrelated 400 error
// during Delete is still treated as a hard error (i.e. we only swallow the
// specific "already deactivated" case).
func TestRescueResource_DeleteUnrelated400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/12345/rescuesystem" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"invalid server id"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "s3cr3t-rescue-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Delete() should error on unrelated 400 responses")
	}
}

// TestIsAlreadyDeactivated verifies the helper correctly distinguishes
// deactivation errors from other API errors.
//
// Thread B fix: the "already" substring was dropped because it is too broad —
// "operation already pending" or "server already locked" would be incorrectly
// treated as a successful deactivation. Only the documented "deactivat"
// substring is matched now. Tests updated accordingly.
func TestIsAlreadyDeactivated(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "deactivated body — documented API message",
			err:  &netcup.APIError{StatusCode: 400, Status: "400 Bad Request", Body: `{"message":"rescue system currently deactivated"}`},
			want: true,
		},
		{
			name: "deactivated body — alternate wording",
			err:  &netcup.APIError{StatusCode: 400, Status: "400 Bad Request", Body: `{"message":"rescue system is deactivated"}`},
			want: true,
		},
		// Thread B fix: bare "already" without "deactivat" must NOT match.
		// Prior versions matched "already" as a substring, which could cause
		// "operation already pending" or "server already locked" 400s to be
		// silently swallowed as deactivation success.
		{
			name: "already body without deactivat — must NOT match (thread B fix)",
			err:  &netcup.APIError{StatusCode: 400, Status: "400 Bad Request", Body: `{"message":"already disabled"}`},
			want: false,
		},
		{
			name: "already pending — must NOT match (thread B fix)",
			err:  &netcup.APIError{StatusCode: 400, Status: "400 Bad Request", Body: `{"message":"operation already pending"}`},
			want: false,
		},
		{
			name: "already locked — must NOT match (thread B fix)",
			err:  &netcup.APIError{StatusCode: 400, Status: "400 Bad Request", Body: `{"message":"server already locked"}`},
			want: false,
		},
		{
			name: "unrelated 400",
			err:  &netcup.APIError{StatusCode: 400, Status: "400 Bad Request", Body: `{"message":"invalid server id"}`},
			want: false,
		},
		{
			name: "500 with deactivated body",
			err:  &netcup.APIError{StatusCode: 500, Status: "500 Internal Server Error", Body: `{"message":"deactivated"}`},
			want: false,
		},
		{
			name: "non-API error",
			err:  fmt.Errorf("network timeout"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAlreadyDeactivated(tt.err)
			if got != tt.want {
				t.Errorf("isAlreadyDeactivated() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRescueResource_CreateTerminalTaskFailClearsState verifies Thread 2 fix:
// when WaitForTask returns a *TaskError (confirmed terminal failure such as
// ERROR/CANCELED/ROLLBACK), Create must error AND clear the partial state so
// that Terraform can retry the create cleanly without hitting duplicate-enable.
func TestRescueResource_CreateTerminalTaskFailClearsState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/12345/rescuesystem":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-fail","name":"EnableRescue","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-fail":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// Task reaches a confirmed terminal error state.
			_, _ = w.Write([]byte(`{"uuid":"task-fail","name":"EnableRescue","state":"ERROR","message":"hardware failure"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Create() should error when the enable task reaches a terminal failure state")
	}
	// State must be empty (RemoveResource called) because a confirmed TaskError
	// means rescue is definitively NOT enabled — no partial state should be kept.
	if !resp.State.Raw.IsNull() {
		t.Error("State should be cleared (null) after a confirmed terminal enable-task failure")
	}
}

// TestRescueResource_CreateIndeterminateTaskFailRetainsState verifies Thread 2
// fix: when WaitForTask exits for an indeterminate reason (e.g. context
// deadline, permanent poll error such as 401) — NOT a confirmed *TaskError —
// Create must error but retain the partial state (id + server_id) so the next
// apply does not re-submit a duplicate enable that the API would reject.
func TestRescueResource_CreateIndeterminateTaskFailRetainsState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/12345/rescuesystem":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-auth","name":"EnableRescue","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-auth":
			// Simulate a permanent poll error (401 Unauthorized) — WaitForTask
			// returns the *APIError directly (not a *TaskError), which is
			// indeterminate: the task may still complete.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"token expired"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Create() should error when WaitForTask fails")
	}

	// Partial state must be retained — the task outcome is unknown.
	var state rescueResourceModel
	resp.Diagnostics = nil
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.ID.IsNull() || state.ID.IsUnknown() || state.ID.ValueString() == "" {
		t.Error("ID should be retained in state for an indeterminate poll failure (not a *TaskError)")
	}
	if state.ServerID.ValueString() != "12345" {
		t.Errorf("ServerID = %q, want 12345", state.ServerID.ValueString())
	}
	// Thread C fix: retained partial state must use KNOWN placeholders, not
	// Unknown() — Terraform rejects a stored NewState containing unknown values
	// after an errored apply.
	if state.Active.IsUnknown() {
		t.Error("Active must be a known placeholder (not Unknown) in retained partial state after an errored apply")
	}
	if state.Password.IsUnknown() {
		t.Error("Password must be a known placeholder (not Unknown) in retained partial state after an errored apply")
	}
}

// TestRescueResource_ReadNormalizesNullWait verifies Thread 3 fix: after
// `terraform import`, the state has only id set and wait is null. Read must
// normalize null wait → true so that a subsequent Delete polls the disable task
// (the documented default) instead of silently skipping polling.
func TestRescueResource_ReadNormalizesNullWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/99999/rescuesystem" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"active":true,"password":"import-pw"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	// Simulate the state that ImportState sets: only id is populated; all other
	// attributes are null/unknown as set by ImportStatePassthroughID.
	objType := schemaResp.Schema.Type().TerraformType(ctx)
	importedState := tfsdk.State{
		Raw: tftypes.NewValue(objType, map[string]tftypes.Value{
			"id":        tftypes.NewValue(tftypes.String, "99999"),
			"server_id": tftypes.NewValue(tftypes.String, nil), // null
			"active":    tftypes.NewValue(tftypes.Bool, nil),   // null
			"password":  tftypes.NewValue(tftypes.String, nil), // null
			"wait":      tftypes.NewValue(tftypes.Bool, nil),   // null — the case under test
		}),
		Schema: schemaResp.Schema,
	}

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: importedState}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var result rescueResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	// wait must be normalized to true, not left null, so Delete polls by default.
	if result.Wait.IsNull() || result.Wait.IsUnknown() {
		t.Error("Read() should normalize null wait to true after import")
	}
	if !result.Wait.ValueBool() {
		t.Errorf("Wait = false after import normalization, want true")
	}
}

// TestRescueResource_CreateIndeterminateEnableRetainsState verifies Thread B
// fix: when EnableRescueSystem fails with an INDETERMINATE error (here a 5xx —
// the POST may have been accepted server-side even though no usable TaskInfo
// came back), Create must persist the partial identity (id + server_id, with
// known placeholders active=true/password=null) BEFORE returning the error, so
// Terraform tracks the resource and the next refresh/Delete can reconcile.
func TestRescueResource_CreateIndeterminateEnableRetainsState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/servers/12345/rescuesystem" {
			// 5xx → indeterminate: the enable may still take effect.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"upstream timeout"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Create() should error on an indeterminate (5xx) enable failure")
	}

	var state rescueResourceModel
	resp.Diagnostics = nil
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.ID.IsNull() || state.ID.IsUnknown() || state.ID.ValueString() != "12345" {
		t.Errorf("ID should be retained as 12345 after indeterminate enable, got %v", state.ID)
	}
	if state.ServerID.ValueString() != "12345" {
		t.Errorf("ServerID = %q, want 12345", state.ServerID.ValueString())
	}
	if state.Active.IsUnknown() || !state.Active.ValueBool() {
		t.Error("Active must be a known placeholder true after indeterminate enable")
	}
	if state.Password.IsUnknown() || !state.Password.IsNull() {
		t.Error("Password must be a known null placeholder after indeterminate enable")
	}
}

// TestRescueResource_CreateIndeterminateTransportRetainsState verifies Thread B
// fix for the transport-error case: if the enable connection is dropped (no HTTP
// response at all), EnableRescueSystem returns a non-*APIError transport error,
// which is indeterminate — the request may have reached the server. Create must
// retain identity.
func TestRescueResource_CreateIndeterminateTransportRetainsState(t *testing.T) {
	// Start a server, capture its URL, then close it so the POST fails at the
	// transport layer (connection refused) — a non-*APIError error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(url))

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Create() should error on a transport failure during enable")
	}

	var state rescueResourceModel
	resp.Diagnostics = nil
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.ID.IsNull() || state.ID.ValueString() != "12345" {
		t.Errorf("ID should be retained as 12345 after a transport-error enable, got %v", state.ID)
	}
	if state.ServerID.ValueString() != "12345" {
		t.Errorf("ServerID = %q, want 12345", state.ServerID.ValueString())
	}
}

// TestIsDefinitiveEnableRejection verifies the Thread B classifier: only a *APIError
// with a 4xx status is a definitive rejection; 5xx, non-API (transport) errors,
// and decode errors are indeterminate.
func TestIsDefinitiveEnableRejection(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"400 already active", &netcup.APIError{StatusCode: 400}, true},
		{"401 auth", &netcup.APIError{StatusCode: 401}, true},
		{"404 unknown server", &netcup.APIError{StatusCode: 404}, true},
		{"429 rate limited (still definitive 4xx)", &netcup.APIError{StatusCode: 429}, true},
		{"500 server error is indeterminate", &netcup.APIError{StatusCode: 500}, false},
		{"502 gateway is indeterminate", &netcup.APIError{StatusCode: 502}, false},
		{"transport error is indeterminate", fmt.Errorf("connection reset"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDefinitiveEnableRejection(tt.err); got != tt.want {
				t.Errorf("isDefinitiveEnableRejection() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRescueResource_CreateTaskTimeoutRetainsState verifies Thread C fix: when
// WaitForTask cannot reach a terminal state within defaultTaskTimeout, the poll
// is bounded and returns a deadline-exceeded error (NOT a *TaskError). Create
// must treat this as indeterminate: surface the error AND retain partial state.
//
// The test forces the deadline by overriding defaultTaskTimeout is not possible
// (const), so instead it cancels the incoming context — WaitForTask observes the
// shorter of the two deadlines. A canceled parent context produces the same
// indeterminate (non-*TaskError) outcome the timeout wrapper guards against.
func TestRescueResource_CreateTaskTimeoutRetainsState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/12345/rescuesystem":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-stall","name":"EnableRescue","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-stall":
			// Never terminal — always PENDING, forcing the poll to run until the
			// context deadline/cancel bounds it.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-stall","name":"EnableRescue","state":"PENDING"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	// Bound the incoming context so the test does not wait defaultTaskTimeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Create() should error when the enable task never reaches terminal within the deadline")
	}

	// Deadline-exceeded is indeterminate (not a *TaskError): partial state must
	// be retained so the next apply does not duplicate-enable.
	var state rescueResourceModel
	resp.Diagnostics = nil
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.ID.IsNull() || state.ID.ValueString() != "12345" {
		t.Errorf("ID should be retained after a bounded-poll timeout, got %v", state.ID)
	}
	if state.ServerID.ValueString() != "12345" {
		t.Errorf("ServerID = %q, want 12345", state.ServerID.ValueString())
	}
}

// TestRescueResource_DeleteTaskTimeout verifies Thread C fix: a disable task that
// never reaches terminal is bounded by the poll deadline and surfaces a
// diagnostic instead of hanging. The incoming context is bounded here to force
// the deadline without waiting defaultTaskTimeout.
func TestRescueResource_DeleteTaskTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/12345/rescuesystem":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del-stall","name":"DisableRescue","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-del-stall":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-del-stall","name":"DisableRescue","state":"PENDING"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	r, schemaResp := configureRescueResource(t, rescueClient(srv.URL))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "s3cr3t-rescue-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Delete() should error when the disable task never reaches terminal within the deadline")
	}
}

// TestUseStateForUnknownUnlessReplacing_String_NoReplacement verifies Thread A
// fix: when server_id is unchanged between state and plan (an in-place update
// such as toggling wait), the modifier copies the prior state value into an
// unknown plan value — preserving plan stability (no spurious "known after
// apply").
func TestUseStateForUnknownUnlessReplacing_String_NoReplacement(t *testing.T) {
	_, schemaResp := configureRescueResource(t, rescueClient("http://example.invalid"))
	ctx := context.Background()

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "old-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"server_id": tftypes.NewValue(tftypes.String, "12345"), // unchanged
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"wait":      tftypes.NewValue(tftypes.Bool, false),
	})

	req := planmodifier.StringRequest{
		State:      state,
		Plan:       plan,
		StateValue: types.StringValue("12345"),
		PlanValue:  types.StringUnknown(),
	}
	var resp planmodifier.StringResponse
	resp.PlanValue = req.PlanValue
	useStateForUnknownUnlessReplacingString{}.PlanModifyString(ctx, req, &resp)

	if resp.PlanValue.IsUnknown() {
		t.Error("without a replacement, the unknown id/password should reuse the prior state value (stable plan)")
	}
	if resp.PlanValue.ValueString() != "12345" {
		t.Errorf("PlanValue = %q, want prior state 12345", resp.PlanValue.ValueString())
	}
}

// TestUseStateForUnknownUnlessReplacing_String_Replacement verifies Thread A fix:
// when server_id changes (a replacement), the modifier leaves the value UNKNOWN
// so Create recomputes it for the new server, avoiding "Provider produced
// inconsistent result after apply".
func TestUseStateForUnknownUnlessReplacing_String_Replacement(t *testing.T) {
	_, schemaResp := configureRescueResource(t, rescueClient("http://example.invalid"))
	ctx := context.Background()

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "old-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"server_id": tftypes.NewValue(tftypes.String, "67890"), // CHANGED → replacement
		"active":    tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue),
		"password":  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	req := planmodifier.StringRequest{
		State:      state,
		Plan:       plan,
		StateValue: types.StringValue("12345"),
		PlanValue:  types.StringUnknown(),
	}
	var resp planmodifier.StringResponse
	resp.PlanValue = req.PlanValue
	useStateForUnknownUnlessReplacingString{}.PlanModifyString(ctx, req, &resp)

	if !resp.PlanValue.IsUnknown() {
		t.Errorf("on a server_id replacement, id/password must stay unknown (known after apply), got %q", resp.PlanValue.ValueString())
	}
}

// TestUseStateForUnknownUnlessReplacing_Bool_Replacement verifies the bool
// variant of the Thread A fix for the active attribute on a server_id change.
func TestUseStateForUnknownUnlessReplacing_Bool_Replacement(t *testing.T) {
	_, schemaResp := configureRescueResource(t, rescueClient("http://example.invalid"))
	ctx := context.Background()

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "old-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"server_id": tftypes.NewValue(tftypes.String, "67890"), // CHANGED
		"active":    tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue),
		"password":  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	req := planmodifier.BoolRequest{
		State:      state,
		Plan:       plan,
		StateValue: types.BoolValue(true),
		PlanValue:  types.BoolUnknown(),
	}
	var resp planmodifier.BoolResponse
	resp.PlanValue = req.PlanValue
	useStateForUnknownUnlessReplacingBool{}.PlanModifyBool(ctx, req, &resp)

	if !resp.PlanValue.IsUnknown() {
		t.Errorf("on a server_id replacement, active must stay unknown, got %v", resp.PlanValue.ValueBool())
	}
}

// TestUseStateForUnknownUnlessReplacing_String_UnknownServerID verifies the
// round-5 fix: when the planned server_id is UNKNOWN (e.g. derived from another
// resource's not-yet-known output), RequiresReplace treats it as a replacement
// but the modifier cannot prove the target is the same server. It must leave
// id/password UNKNOWN rather than copying the prior state, so Create can return
// the new server's values without "inconsistent result after apply".
func TestUseStateForUnknownUnlessReplacing_String_UnknownServerID(t *testing.T) {
	_, schemaResp := configureRescueResource(t, rescueClient("http://example.invalid"))
	ctx := context.Background()

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "old-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"server_id": tftypes.NewValue(tftypes.String, tftypes.UnknownValue), // UNKNOWN → maybe replacement
		"active":    tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue),
		"password":  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	req := planmodifier.StringRequest{
		State:      state,
		Plan:       plan,
		StateValue: types.StringValue("12345"),
		PlanValue:  types.StringUnknown(),
	}
	var resp planmodifier.StringResponse
	resp.PlanValue = req.PlanValue
	useStateForUnknownUnlessReplacingString{}.PlanModifyString(ctx, req, &resp)

	if !resp.PlanValue.IsUnknown() {
		t.Errorf("when planned server_id is unknown, id/password must stay unknown (known after apply), got %q", resp.PlanValue.ValueString())
	}
}

// TestUseStateForUnknownUnlessReplacing_Bool_UnknownServerID verifies the bool
// variant of the round-5 fix: an unknown planned server_id must leave active
// unknown rather than carrying the prior server's value.
func TestUseStateForUnknownUnlessReplacing_Bool_UnknownServerID(t *testing.T) {
	_, schemaResp := configureRescueResource(t, rescueClient("http://example.invalid"))
	ctx := context.Background()

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "old-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"server_id": tftypes.NewValue(tftypes.String, tftypes.UnknownValue), // UNKNOWN
		"active":    tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue),
		"password":  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	req := planmodifier.BoolRequest{
		State:      state,
		Plan:       plan,
		StateValue: types.BoolValue(true),
		PlanValue:  types.BoolUnknown(),
	}
	var resp planmodifier.BoolResponse
	resp.PlanValue = req.PlanValue
	useStateForUnknownUnlessReplacingBool{}.PlanModifyBool(ctx, req, &resp)

	if !resp.PlanValue.IsUnknown() {
		t.Errorf("when planned server_id is unknown, active must stay unknown, got %v", resp.PlanValue.ValueBool())
	}
}

// TestUseStateForUnknownUnlessReplacing_Bool_NoReplacement verifies the bool
// variant reuses the prior active value on a stable, unchanged known server_id
// (plan stability for in-place updates such as toggling wait).
func TestUseStateForUnknownUnlessReplacing_Bool_NoReplacement(t *testing.T) {
	_, schemaResp := configureRescueResource(t, rescueClient("http://example.invalid"))
	ctx := context.Background()

	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, "12345"),
		"server_id": tftypes.NewValue(tftypes.String, "12345"),
		"active":    tftypes.NewValue(tftypes.Bool, true),
		"password":  tftypes.NewValue(tftypes.String, "old-pw"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"server_id": tftypes.NewValue(tftypes.String, "12345"), // unchanged
		"active":    tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue),
		"password":  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"wait":      tftypes.NewValue(tftypes.Bool, false),
	})

	req := planmodifier.BoolRequest{
		State:      state,
		Plan:       plan,
		StateValue: types.BoolValue(true),
		PlanValue:  types.BoolUnknown(),
	}
	var resp planmodifier.BoolResponse
	resp.PlanValue = req.PlanValue
	useStateForUnknownUnlessReplacingBool{}.PlanModifyBool(ctx, req, &resp)

	if resp.PlanValue.IsUnknown() {
		t.Error("without a replacement, the unknown active should reuse the prior state value (stable plan)")
	}
	if !resp.PlanValue.ValueBool() {
		t.Errorf("PlanValue = %v, want prior state true", resp.PlanValue.ValueBool())
	}
}

// TestParseServerID verifies the parseServerID helper handles valid and invalid
// inputs correctly.
func TestParseServerID(t *testing.T) {
	tests := []struct {
		input   string
		want    int32
		wantErr bool
	}{
		{"12345", 12345, false},
		{"0", 0, false},
		{"-1", -1, false},
		{"not-a-number", 0, true},
		{"", 0, true},
		{"99999999999", 0, true}, // overflow int32
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseServerID(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseServerID(%q) error = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("parseServerID(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("parseServerID(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
