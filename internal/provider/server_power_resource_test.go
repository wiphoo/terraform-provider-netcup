package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// configureServerPowerResource wires up a serverPowerResource against the
// given client and returns the configured resource and its schema response.
func configureServerPowerResource(t *testing.T, client *netcup.Client) (resource.ResourceWithConfigure, resource.SchemaResponse) {
	t.Helper()
	r := NewServerPowerResource().(resource.ResourceWithConfigure)
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

// TestServerPowerResource_Create_Sync verifies Create when SetPowerState
// returns 200 (synchronous, no task).
func TestServerPowerResource_Create_Sync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/servers/123" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK) // sync — no task
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
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
	if state.ServerID.ValueString() != "123" {
		t.Errorf("ServerID = %q, want 123", state.ServerID.ValueString())
	}
	if state.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON", state.State.ValueString())
	}
	if state.ID.ValueString() != "123" {
		t.Errorf("ID = %q, want 123", state.ID.ValueString())
	}
}

// TestServerPowerResource_Create_Async verifies Create when SetPowerState
// returns 202 and wait=true causes WaitForTask to be called.
func TestServerPowerResource_Create_Async(t *testing.T) {
	taskPolled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/servers/456":
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
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "456"),
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
	if !taskPolled {
		t.Error("expected WaitForTask to poll the task, but GET /v1/tasks/task-1 was never called")
	}

	var state serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF", state.State.ValueString())
	}
}

// TestServerPowerResource_Create_NoWait verifies that when wait=false the task
// endpoint is NOT polled even though an async task is returned.
func TestServerPowerResource_Create_NoWait(t *testing.T) {
	taskPolled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/servers/789":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-2","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-2":
			taskPolled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-2","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "789"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, false),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if taskPolled {
		t.Error("expected WaitForTask NOT to be called when wait=false, but it was")
	}
}

// TestServerPowerResource_Create_WithStateOption verifies that state_option is
// forwarded in the PATCH query string.
func TestServerPowerResource_Create_WithStateOption(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "100"),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, "POWEROFF"),
		"wait":         tftypes.NewValue(tftypes.Bool, false),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if gotQuery != "stateOption=POWEROFF" {
		t.Errorf("query = %q, want stateOption=POWEROFF", gotQuery)
	}
}

// TestServerPowerResource_Create_APIError verifies that a non-2xx from
// SetPowerState surfaces as a Terraform error diagnostic.
func TestServerPowerResource_Create_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"MAINTENANCE","message":"Node is in maintenance."}`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "1"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, false),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Create() expected error diagnostics, got none")
	}
}

// TestServerPowerResource_Create_TaskError verifies that a failed async task
// (ERROR state) surfaces as a Terraform error diagnostic.
func TestServerPowerResource_Create_TaskError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-err","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-err":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-err","state":"ERROR","message":"power op failed"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "2"),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Create() expected error from failed task, got none")
	}
}

// TestServerPowerResource_Read_Running verifies Read maps RUNNING → ON.
func TestServerPowerResource_Read_Running(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/servers/123" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		out, _ := json.Marshal(map[string]interface{}{
			"id":   123,
			"name": "vps01",
			"serverLiveInfo": map[string]interface{}{
				"state": "RUNNING",
			},
		})
		_, _ = w.Write(out)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
		"id":           tftypes.NewValue(tftypes.String, "123"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (RUNNING → ON)", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_Shutoff verifies Read maps SHUTOFF → OFF.
func TestServerPowerResource_Read_Shutoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		out, _ := json.Marshal(map[string]interface{}{
			"id":             123,
			"name":           "vps01",
			"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
		})
		_, _ = w.Write(out)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
		"id":           tftypes.NewValue(tftypes.String, "123"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if result.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (SHUTOFF → OFF)", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_Suspended verifies Read maps SUSPENDED → SUSPENDED.
func TestServerPowerResource_Read_Suspended(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		out, _ := json.Marshal(map[string]interface{}{
			"id":             123,
			"name":           "vps01",
			"serverLiveInfo": map[string]interface{}{"state": "SUSPENDED"},
		})
		_, _ = w.Write(out)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, "SUSPENDED"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
		"id":           tftypes.NewValue(tftypes.String, "123"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if result.State.ValueString() != "SUSPENDED" {
		t.Errorf("State = %q, want SUSPENDED", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_Transitional verifies that a transitional live
// state (e.g. "SHUTDOWN") does not produce a spurious diff: the desired state
// in Terraform state is preserved.
func TestServerPowerResource_Read_Transitional(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		out, _ := json.Marshal(map[string]interface{}{
			"id":             123,
			"name":           "vps01",
			"serverLiveInfo": map[string]interface{}{"state": "SHUTDOWN"},
		})
		_, _ = w.Write(out)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Desired state is ON; server is transitionally SHUTDOWN.
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
		"id":           tftypes.NewValue(tftypes.String, "123"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	// Desired state should remain ON (no spurious diff).
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (transitional state should not change desired)", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_NoLiveInfo verifies Read handles a server with no
// serverLiveInfo (state remains unchanged).
func TestServerPowerResource_Read_NoLiveInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		out, _ := json.Marshal(map[string]interface{}{
			"id":             123,
			"name":           "vps01",
			"serverLiveInfo": nil,
		})
		_, _ = w.Write(out)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
		"id":           tftypes.NewValue(tftypes.String, "123"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
}

// TestServerPowerResource_Read_404 verifies Read on a missing server removes
// the resource from state (no error).
func TestServerPowerResource_Read_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "999"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
		"id":           tftypes.NewValue(tftypes.String, "999"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics for 404: %v", resp.Diagnostics.Errors())
	}
	if !resp.State.Raw.IsNull() {
		t.Error("State.Raw should be null after RemoveResource for 404")
	}
}

// TestServerPowerResource_Update_Async verifies Update calls SetPowerState and
// WaitForTask when wait=true.
func TestServerPowerResource_Update_Async(t *testing.T) {
	taskPolled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/servers/123":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-upd","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-upd":
			taskPolled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-upd","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Prior state has state=ON so that plan's state=OFF is a real change.
	priorState := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
		"id":           tftypes.NewValue(tftypes.String, "123"),
	})
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: priorState}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Update() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !taskPolled {
		t.Error("expected WaitForTask to be called, but GET /v1/tasks was never hit")
	}

	var state serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF", state.State.ValueString())
	}
}

// TestServerPowerResource_Delete_IsNoop verifies Delete makes no HTTP calls.
func TestServerPowerResource_Delete_IsNoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Delete should not make any HTTP calls, got: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
		"id":           tftypes.NewValue(tftypes.String, "123"),
	})

	var resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
}

// TestServerPowerResource_ImportState verifies ImportState sets both id and
// server_id from the import ID string so that the subsequent Read refresh can
// parse server_id without encountering an empty string.
func TestServerPowerResource_ImportState(t *testing.T) {
	r := NewServerPowerResource().(interface {
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
	r.ImportState(ctx, resource.ImportStateRequest{ID: "42"}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var id types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("GetAttribute(id) unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if id.ValueString() != "42" {
		t.Errorf("id = %q, want 42", id.ValueString())
	}

	// server_id must also be populated so Read can ParseInt it without error.
	var serverID types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("server_id"), &serverID)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("GetAttribute(server_id) unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if serverID.ValueString() != "42" {
		t.Errorf("server_id = %q, want 42 (must be set so Read can parse it)", serverID.ValueString())
	}
}

// TestServerPowerResource_ImportState_InvalidID verifies that a non-numeric
// import ID is rejected with an error diagnostic.
func TestServerPowerResource_ImportState_InvalidID(t *testing.T) {
	r := NewServerPowerResource().(interface {
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
	r.ImportState(ctx, resource.ImportStateRequest{ID: "not-a-number"}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("ImportState() expected error diagnostic for non-numeric ID, got none")
	}
}

// TestServerPowerResource_Update_WaitOnlyChange verifies that changing only
// `wait` does NOT reissue the SetPowerState command. This is critical for
// destructive state_options like RESET or POWERCYCLE where reissuing the
// command would cause unexpected downtime.
func TestServerPowerResource_Update_WaitOnlyChange(t *testing.T) {
	powerCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			powerCalled = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Prior state: ON with RESET state_option.
	priorState := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, "RESET"),
		"wait":         tftypes.NewValue(tftypes.Bool, false),
		"id":           tftypes.NewValue(tftypes.String, "123"),
	})
	// Plan: same state + state_option, only wait changed to true.
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, "RESET"),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: priorState}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Update() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if powerCalled {
		t.Error("SetPowerState (PATCH) should NOT be called when only wait changed, but it was")
	}

	// The new wait value must be persisted in state.
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if result.Wait.ValueBool() != true {
		t.Errorf("wait = %v, want true after wait-only update", result.Wait.ValueBool())
	}
}

// TestServerPowerResource_Update_StateChange verifies that changing `state`
// DOES issue a SetPowerState call even when wait is unchanged.
func TestServerPowerResource_Update_StateChange(t *testing.T) {
	powerCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			powerCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	priorState := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
		"id":           tftypes.NewValue(tftypes.String, "123"),
	})
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: priorState}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Update() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !powerCalled {
		t.Error("SetPowerState (PATCH) SHOULD be called when state changes, but it was not")
	}
}

// TestServerPowerResource_PowerStateValidator verifies the state validator accepts
// valid values and rejects invalid ones.
func TestServerPowerResource_PowerStateValidator(t *testing.T) {
	v := powerStateValidator{}

	tests := []struct {
		input   string
		hasDiag bool
	}{
		{"ON", false},
		{"OFF", false},
		{"SUSPENDED", false},
		{"on", true},
		{"off", true},
		{"RUNNING", true},
		{"SHUTOFF", true},
		{"", true},
		{"POWERCYCLE", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(context.Background(), validator.StringRequest{
				ConfigValue: types.StringValue(tt.input),
			}, &resp)
			if tt.hasDiag && !resp.Diagnostics.HasError() {
				t.Errorf("expected diagnostic for %q, got none", tt.input)
			}
			if !tt.hasDiag && resp.Diagnostics.HasError() {
				t.Errorf("unexpected diagnostic for %q: %v", tt.input, resp.Diagnostics.Errors())
			}
		})
	}
}

// TestLiveStateToDesiredPower verifies the state mapping function covers all
// known cases.
func TestLiveStateToDesiredPower(t *testing.T) {
	tests := []struct {
		liveState string
		want      netcup.PowerState
	}{
		{"RUNNING", netcup.PowerOn},
		{"running", netcup.PowerOn}, // case-insensitive
		{"SHUTOFF", netcup.PowerOff},
		{"SUSPENDED", netcup.PowerSuspended},
		{"PAUSED", netcup.PowerSuspended},
		{"paused", netcup.PowerSuspended},
		{"SHUTDOWN", ""},       // transitional
		{"PMSUSPENDED", ""},    // transitional
		{"SAVE_RESTORE", ""},   // transitional
		{"", ""},               // unknown
		{"UNKNOWN_FUTURE", ""}, // unknown future state
	}

	for _, tt := range tests {
		t.Run(tt.liveState, func(t *testing.T) {
			got := liveStateToDesiredPower(tt.liveState)
			if got != tt.want {
				t.Errorf("liveStateToDesiredPower(%q) = %q, want %q", tt.liveState, got, tt.want)
			}
		})
	}
}
