package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
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
