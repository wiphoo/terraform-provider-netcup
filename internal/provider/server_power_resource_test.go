package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// TestServerPowerResource_Create_MaintenanceDefinitive verifies Thread C (P1):
// the documented 503 MAINTENANCE response from SetPowerState is a DEFINITIVE
// rejection — the request was explicitly refused and no task was created — so
// Create surfaces an ERROR and persists NO state (no desired value, no sentinel).
// This lets Terraform retry once maintenance ends, instead of the previous
// indeterminate handling that persisted the desired state + sentinel and made
// Read report false convergence forever. A genuinely ambiguous/unexpected 5xx
// (e.g. 502 with no MAINTENANCE marker) remains indeterminate — covered by
// TestServerPowerResource_Create_Indeterminate5xxSetPowerState.
func TestServerPowerResource_Create_MaintenanceDefinitive(t *testing.T) {
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

	// 503 MAINTENANCE is definitive: an ERROR, no warning, and NO persisted state.
	if !resp.Diagnostics.HasError() {
		t.Fatal("Create() expected an ERROR on a 503 MAINTENANCE (definitive rejection), got none")
	}
	if resp.Diagnostics.WarningsCount() != 0 {
		t.Errorf("Create() expected no warnings on a definitive 503 MAINTENANCE, got %d", resp.Diagnostics.WarningsCount())
	}
	if !resp.State.Raw.IsNull() {
		t.Error("State must NOT be persisted on a 503 MAINTENANCE (so Terraform retries after maintenance ends)")
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

// TestServerPowerResource_Create_TaskError_StateNotPersisted verifies that when
// the async task ends in a confirmed terminal failure (*netcup.TaskError), Create
// returns an ERROR diagnostic AND does NOT persist the desired state — so a retry
// safely re-issues the (recoverable) power command.
func TestServerPowerResource_Create_TaskError_StateNotPersisted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-term","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-term":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-term","state":"ERROR","message":"power op failed"}`))
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
	if resp.Diagnostics.WarningsCount() != 0 {
		t.Errorf("Create() expected no warnings on terminal failure, got %d", resp.Diagnostics.WarningsCount())
	}
	// State must NOT be persisted so a retry re-issues the recoverable command.
	if !resp.State.Raw.IsNull() {
		t.Error("State should NOT be persisted on a confirmed terminal task failure")
	}
}

// TestServerPowerResource_Create_IndeterminateWait verifies that when
// SetPowerState is accepted (202) but WaitForTask returns an INDETERMINATE error
// (here a permanent non-*TaskError API error on the task poll — e.g. a 400 that
// is not a confirmed task-failure terminal), Create persists the full desired
// state and emits a WARNING (not an error) — so a later apply reconciles rather
// than blindly re-issuing the (possibly destructive) power command. A ctx.Err()
// from a canceled apply is handled identically (also non-*TaskError).
func TestServerPowerResource_Create_IndeterminateWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-ind","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-ind":
			// A permanent client error on the poll (400) surfaces as an *APIError,
			// which is NOT a *netcup.TaskError → indeterminate. WaitForTask returns
			// it immediately (no retry loop), keeping the test fast.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"BAD","message":"transient poll failure"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "77"),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, "POWEROFF"),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	// Indeterminate wait must NOT error (erroring would taint → recreate → re-issue).
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() should not error on indeterminate wait, got: %v", resp.Diagnostics.Errors())
	}
	if resp.Diagnostics.WarningsCount() == 0 {
		t.Error("Create() expected a WARNING diagnostic on indeterminate wait, got none")
	}

	// Desired state must be persisted so the next apply sees no diff.
	if resp.State.Raw.IsNull() {
		t.Fatal("Desired state must be persisted on an indeterminate wait")
	}
	var state serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (desired state persisted)", state.State.ValueString())
	}
	if state.StateOption.ValueString() != "POWEROFF" {
		t.Errorf("StateOption = %q, want POWEROFF (full desired state persisted)", state.StateOption.ValueString())
	}
	if state.ID.ValueString() != "77" {
		t.Errorf("ID = %q, want 77", state.ID.ValueString())
	}
}

// TestServerPowerResource_Create_TaskTimeout verifies that when SetPowerState is
// accepted (202) but the task never reaches a terminal state, task polling is
// bounded by a finite deadline instead of hanging forever. When the deadline
// fires, WaitForTask returns context.DeadlineExceeded (NOT a *netcup.TaskError),
// which classifyTaskWaitError treats as INDETERMINATE: Create returns promptly,
// persists the full desired state, and emits a WARNING (not an error) so a later
// apply reconciles rather than re-issuing the (possibly destructive) command.
//
// The production deadline is defaultTaskTimeout (15m); here the caller ctx
// carries a short deadline that context.WithTimeout preserves (it takes the
// earlier of the two), keeping the test fast while exercising the exact same
// deadline→indeterminate→persist+warn path.
func TestServerPowerResource_Create_TaskTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-hang","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-hang":
			// Never terminal: always PENDING, forcing WaitForTask to poll until
			// the bounded deadline fires.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-hang","state":"PENDING"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(
		netcup.WithAPIEndpoint(srv.URL),
		netcup.WithAccessToken("tok"),
		// Poll rapidly so the deadline (below) is reached in a few iterations.
		netcup.WithTaskPollInterval(5*time.Millisecond),
	)
	r, schemaResp := configureServerPowerResource(t, client)

	// A short caller deadline; context.WithTimeout(ctx, defaultTaskTimeout) keeps
	// the earlier (this) deadline, so the wait is bounded and the test is fast.
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "55"),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, "POWEROFF"),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}

	// Guard against a regression that lets Create hang: run it with a hard cap and
	// fail the test (instead of blocking the suite) if it never returns.
	done := make(chan struct{})
	go func() {
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Create() did not return: task polling was not bounded by a deadline")
	}

	// A hit deadline is INDETERMINATE: must NOT error (erroring would taint →
	// recreate → re-issue the destructive power command).
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() should not error when the task-polling deadline is exceeded, got: %v", resp.Diagnostics.Errors())
	}
	if resp.Diagnostics.WarningsCount() == 0 {
		t.Error("Create() expected a WARNING diagnostic when the task-polling deadline is exceeded, got none")
	}

	// Desired state must be persisted so the next apply sees no diff.
	if resp.State.Raw.IsNull() {
		t.Fatal("Desired state must be persisted when the task-polling deadline is exceeded")
	}
	var state serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (desired state persisted on timeout)", state.State.ValueString())
	}
	if state.StateOption.ValueString() != "POWEROFF" {
		t.Errorf("StateOption = %q, want POWEROFF (full desired state persisted on timeout)", state.StateOption.ValueString())
	}
	if state.ID.ValueString() != "55" {
		t.Errorf("ID = %q, want 55", state.ID.ValueString())
	}
}

// TestServerPowerResource_Update_IndeterminateWait verifies that when an Update
// changes the power state, SetPowerState is accepted (202), but WaitForTask
// returns an INDETERMINATE error, the NEW desired state is persisted with a
// WARNING (not an error) — fixing the old behaviour that retained prior state and
// caused the next apply to re-issue the command.
func TestServerPowerResource_Update_IndeterminateWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/servers/123":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-upd-ind","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-upd-ind":
			// Permanent non-*TaskError poll error → indeterminate (returns fast).
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"BAD","message":"transient poll failure"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Prior state ON; plan flips to OFF (a real state change → SetPowerState).
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
		t.Fatalf("Update() should not error on indeterminate wait, got: %v", resp.Diagnostics.Errors())
	}
	if resp.Diagnostics.WarningsCount() == 0 {
		t.Error("Update() expected a WARNING diagnostic on indeterminate wait, got none")
	}

	var state serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	// The NEW desired state (OFF) must be persisted — not the prior state (ON).
	if state.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (new desired state persisted, not prior ON)", state.State.ValueString())
	}
}

// TestServerPowerResource_Create_IndeterminateSetPowerState verifies that when
// SetPowerState fails with an INDETERMINATE error (transport/decode/5xx) — here a
// truncated 202 body that fails to decode — Create persists the full desired
// state + the pendingTaskIDIndeterminate sentinel and emits a WARNING (not an
// error). This prevents the next apply from re-issuing a destructive power
// command (e.g. rebooting a server twice on RESET/POWERCYCLE) when the op may
// already be running remotely.
func TestServerPowerResource_Create_IndeterminateSetPowerState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 202 accepted, but a truncated/malformed body ⇒ SetPowerState returns a
		// decode error (NOT an *APIError) ⇒ indeterminate.
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"task-`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "321"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, "RESET"),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	// Indeterminate SetPowerState must NOT error (erroring would allow a re-issue).
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() should not error on indeterminate SetPowerState, got: %v", resp.Diagnostics.Errors())
	}
	if resp.Diagnostics.WarningsCount() == 0 {
		t.Error("Create() expected a WARNING on indeterminate SetPowerState, got none")
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("Desired state must be persisted on an indeterminate SetPowerState")
	}
	var state serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (desired state persisted)", state.State.ValueString())
	}
	if state.StateOption.ValueString() != "RESET" {
		t.Errorf("StateOption = %q, want RESET (full desired state persisted)", state.StateOption.ValueString())
	}
	if state.PendingTaskID.ValueString() != pendingTaskIDIndeterminate {
		t.Errorf("PendingTaskID = %q, want %q (sentinel — no UUID available)", state.PendingTaskID.ValueString(), pendingTaskIDIndeterminate)
	}
}

// TestServerPowerResource_Create_Indeterminate5xxSetPowerState verifies that a
// 5xx from SetPowerState is treated as INDETERMINATE (the server may have
// accepted the op): desired state + sentinel persisted, WARNING not error.
func TestServerPowerResource_Create_Indeterminate5xxSetPowerState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"code":"BAD_GATEWAY","message":"upstream error"}`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "654"),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, "POWEROFF"),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() should not error on a 5xx SetPowerState, got: %v", resp.Diagnostics.Errors())
	}
	if resp.Diagnostics.WarningsCount() == 0 {
		t.Error("Create() expected a WARNING on a 5xx SetPowerState, got none")
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("Desired state must be persisted on a 5xx SetPowerState")
	}
	var state serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.PendingTaskID.ValueString() != pendingTaskIDIndeterminate {
		t.Errorf("PendingTaskID = %q, want %q", state.PendingTaskID.ValueString(), pendingTaskIDIndeterminate)
	}
}

// TestServerPowerResource_Create_DefinitiveRejection verifies that a DEFINITIVE
// 4xx rejection from SetPowerState (request understood and refused; no task
// created) surfaces an ERROR and persists NO state, so a retry is safe.
func TestServerPowerResource_Create_DefinitiveRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"INVALID","message":"unsupported state_option"}`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "111"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Create() expected an ERROR on a definitive 4xx rejection, got none")
	}
	if !resp.State.Raw.IsNull() {
		t.Error("State must NOT be persisted on a definitive 4xx rejection (safe retry)")
	}
}

// failingTokenSource is a netcup.TokenSource whose Token() always fails,
// simulating a refresh-token that cannot be refreshed. Because newRequest
// consults the TokenSource BEFORE httpClient.Do, SetPowerState returns the
// resulting (pre-dispatch) error without ever sending the PATCH.
type failingTokenSource struct{ err error }

func (f failingTokenSource) Token(_ context.Context) (string, error) { return "", f.err }

// TestServerPowerResource_Create_PreDispatchAuthDefinitive verifies Thread A (P1):
// a token-refresh failure surfaces from SetPowerState BEFORE the request is
// dispatched (wrapped in netcup.ErrPreDispatch). The power PATCH was DEFINITIVELY
// never submitted, so Create must ERROR and persist NO state and NO sentinel —
// NOT record indeterminate convergence (which would strand the desired state and
// stop Terraform retrying after auth recovers).
func TestServerPowerResource_Create_PreDispatchAuthDefinitive(t *testing.T) {
	dispatched := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The power PATCH must never reach the server on a pre-dispatch failure.
		dispatched = true
		t.Errorf("unexpected dispatched request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(
		netcup.WithAPIEndpoint(srv.URL),
		netcup.WithTokenSource(failingTokenSource{err: errors.New("refresh token expired")}),
	)
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "777"),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if dispatched {
		t.Fatal("power request was dispatched despite a pre-dispatch token failure")
	}
	if !resp.Diagnostics.HasError() {
		t.Fatal("Create() expected an ERROR on a pre-dispatch token failure, got none")
	}
	if !resp.State.Raw.IsNull() {
		t.Error("State must NOT be persisted on a pre-dispatch failure (safe retry, no false convergence)")
	}
	// Also assert no warning-only path was taken (which would indicate the
	// indeterminate/sentinel branch was reached).
	if resp.Diagnostics.WarningsCount() != 0 {
		t.Errorf("expected no warnings on a definitive pre-dispatch failure, got %d", resp.Diagnostics.WarningsCount())
	}
}

// TestServerPowerResource_Create_AfterDispatchTransportIndeterminate verifies the
// counterpart to the pre-dispatch case: an AFTER-dispatch transport failure (the
// server hangs up mid-request, so httpClient.Do returns a *url.Error) is
// AMBIGUOUS — the PATCH may already have been accepted — so Create persists the
// desired state + the pendingTaskIDIndeterminate sentinel + a WARNING, and does
// NOT error. This confirms the discriminator: only pre-dispatch errors are
// definitive; transport errors after dispatch stay indeterminate.
func TestServerPowerResource_Create_AfterDispatchTransportIndeterminate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hijack and abruptly close the connection so httpClient.Do returns a
		// transport error (*url.Error) rather than an HTTP status.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("ResponseWriter does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "778"),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() must NOT error on an ambiguous after-dispatch transport failure; got: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("State must be persisted (desired + sentinel) on an ambiguous after-dispatch failure")
	}
	var state serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.PendingTaskID.ValueString() != pendingTaskIDIndeterminate {
		t.Errorf("PendingTaskID = %q, want %q (indeterminate sentinel)", state.PendingTaskID.ValueString(), pendingTaskIDIndeterminate)
	}
	if resp.Diagnostics.WarningsCount() == 0 {
		t.Error("expected a WARNING diagnostic on an indeterminate after-dispatch failure")
	}
}

// TestServerPowerResource_Create_NoWaitStoresTaskID verifies that wait=false
// stores the accepted task UUID in pending_task_id (instead of discarding it), so
// a refresh before the task finishes can reconcile via the task rather than
// mapping a transient live state over the desired value.
func TestServerPowerResource_Create_NoWaitStoresTaskID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/servers/222":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-nowait","state":"PENDING"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "222"),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, false),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var state serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.PendingTaskID.ValueString() != "task-nowait" {
		t.Errorf("PendingTaskID = %q, want task-nowait (accepted UUID retained on wait=false)", state.PendingTaskID.ValueString())
	}
	if state.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF", state.State.ValueString())
	}
}

// TestServerPowerResource_Read_PendingNonTerminalKeepsDesired verifies that when
// pending_task_id references a still-running task, Read KEEPS the desired state
// and does NOT overwrite it with the transient live state (RUNNING while OFF was
// requested), and retains the pending marker.
func TestServerPowerResource_Read_PendingNonTerminalKeepsDesired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/333":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 333, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-run":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-run","state":"RUNNING"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Desired OFF; task still running; live state is transiently RUNNING.
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "333"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "333"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-run"),
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
		t.Errorf("State = %q, want OFF (desired kept while task running; live RUNNING must NOT overwrite)", result.State.ValueString())
	}
	if result.PendingTaskID.ValueString() != "task-run" {
		t.Errorf("PendingTaskID = %q, want task-run (retained while running)", result.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Read_PendingTerminalReconciles verifies that when
// pending_task_id references a TERMINAL task, Read clears the marker and
// reconciles `state` from the live ServerState via liveStateToDesiredPower.
func TestServerPowerResource_Read_PendingTerminalReconciles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/444":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 444, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-fin":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-fin","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Desired OFF; task finished; live state SHUTOFF ⇒ maps to OFF; marker cleared.
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "444"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "444"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-fin"),
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
		t.Errorf("State = %q, want OFF (reconciled from live SHUTOFF)", result.State.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (cleared on terminal task)", result.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Read_PendingTerminalReconcilesDrift verifies the
// terminal-task path also surfaces DRIFT: desired ON but the task finished and
// the live state is SHUTOFF ⇒ Read maps to OFF so Terraform proposes a fix.
func TestServerPowerResource_Read_PendingTerminalReconcilesDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/445":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 445, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-fin2":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-fin2","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "445"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "445"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-fin2"),
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
		t.Errorf("State = %q, want OFF (drift surfaced from live SHUTOFF after terminal task)", result.State.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null", result.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Read_TerminalPropagationLagWithinWindow verifies
// Thread B (P1): after a FINISHED task, the post-completion refetch can STILL
// report the operation's intermediate live state (e.g. a POWERCYCLE whose refetch
// is still SHUTOFF before the server comes back RUNNING). WITHIN
// powerPropagationWindow (measured from task.FinishedAt), Read treats the
// mismatch as propagation lag: it RETAINS pending_task_id and KEEPS the desired
// state rather than recording OFF (which would reboot again on the next apply).
func TestServerPowerResource_Read_TerminalPropagationLagWithinWindow(t *testing.T) {
	finished := time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/446":
			// Both the pre-GetTask snapshot and the post-completion refetch still
			// report the intermediate SHUTOFF (propagation has not caught up).
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 446, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-lag":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-lag","state":"FINISHED","finishedAt":"` + finished + `"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Desired ON (a POWERCYCLE target); task finished recently; refetch still
	// SHUTOFF ⇒ within window ⇒ retain marker + keep desired ON.
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "446"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "446"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-lag"),
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
		t.Errorf("State = %q, want ON (desired kept during propagation lag, not reconciled to OFF)", result.State.ValueString())
	}
	if result.PendingTaskID.ValueString() != "task-lag" {
		t.Errorf("PendingTaskID = %q, want task-lag (marker retained within propagation window)", result.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Read_TerminalPropagationPastWindow verifies Thread B
// (P1): once time.Since(task.FinishedAt) exceeds powerPropagationWindow, a still-
// mismatched live state is treated as GENUINE drift (e.g. the server was stopped
// externally after the task finished) — Read CLEARS the marker and reconciles
// from the live state so real drift surfaces for a corrective apply.
func TestServerPowerResource_Read_TerminalPropagationPastWindow(t *testing.T) {
	// FinishedAt older than powerPropagationWindow (2m).
	finished := time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/447":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 447, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-old":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-old","state":"FINISHED","finishedAt":"` + finished + `"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "447"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "447"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-old"),
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
		t.Errorf("State = %q, want OFF (genuine drift surfaced past propagation window)", result.State.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (cleared past window)", result.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Read_TerminalConverged verifies Thread B (P1): when the
// post-completion refetch CONFIRMS the desired state (live RUNNING for desired
// ON), Read clears the marker and reconciles regardless of the window. This is
// the converged happy path, analogous to the ae9ca5e reconcile test.
func TestServerPowerResource_Read_TerminalConverged(t *testing.T) {
	finished := time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/448":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 448, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-conv":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-conv","state":"FINISHED","finishedAt":"` + finished + `"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "448"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "448"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-conv"),
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
		t.Errorf("State = %q, want ON (converged: live RUNNING confirms desired)", result.State.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (cleared on convergence)", result.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Read_PendingTaskGone verifies that a 404 on the pending
// task (record gone) clears the marker and reconciles from the live state.
func TestServerPowerResource_Read_PendingTaskGone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/555":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 555, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-gone":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"task not found"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "555"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "555"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-gone"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	// Live RUNNING → ON; marker cleared because the task is gone.
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (reconciled from live RUNNING after task 404)", result.State.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (cleared on task 404)", result.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Read_SentinelKeepsDesiredNoGetTask verifies that the
// pendingTaskIDIndeterminate sentinel makes Read KEEP the desired state WITHOUT
// calling GetTask (there is no real task to query), while the live state has not
// yet converged to the desired value.
func TestServerPowerResource_Read_SentinelKeepsDesiredNoGetTask(t *testing.T) {
	getTaskCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/666":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 666, "name": "vps",
				// Live still RUNNING while OFF was requested (op not yet applied).
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
			getTaskCalled = true
			t.Errorf("GetTask must NOT be called for the indeterminate sentinel")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "666"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, true),
		"id":              tftypes.NewValue(tftypes.String, "666"),
		"pending_task_id": tftypes.NewValue(tftypes.String, pendingTaskIDIndeterminate),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if getTaskCalled {
		t.Error("GetTask was called for the sentinel; it must be skipped")
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	// Desired OFF must be kept (live RUNNING must NOT overwrite it) and the
	// sentinel retained because the live state has not converged to OFF yet.
	if result.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (sentinel keeps desired; live RUNNING must not overwrite)", result.State.ValueString())
	}
	if result.PendingTaskID.ValueString() != pendingTaskIDIndeterminate {
		t.Errorf("PendingTaskID = %q, want sentinel retained until live converges", result.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Read_SentinelClearedOnConverge verifies that once the
// live state converges to the desired value, Read clears the indeterminate
// sentinel (still without calling GetTask).
func TestServerPowerResource_Read_SentinelClearedOnConverge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/777":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 777, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
			})
			_, _ = w.Write(out)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Desired OFF; live now SHUTOFF (→ OFF) confirms the op ⇒ clear sentinel.
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "777"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, true),
		"id":              tftypes.NewValue(tftypes.String, "777"),
		"pending_task_id": tftypes.NewValue(tftypes.String, pendingTaskIDIndeterminate),
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
		t.Errorf("State = %q, want OFF", result.State.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (cleared once live converged to desired)", result.PendingTaskID.ValueString())
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

// TestServerPowerResource_Read_PMSuspended verifies that PMSUSPENDED (guest PM
// suspend) is treated as a persistent non-running state and mapped to SUSPENDED,
// so that Terraform detects drift when the desired state is ON.
func TestServerPowerResource_Read_PMSuspended(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		out, _ := json.Marshal(map[string]interface{}{
			"id":             123,
			"name":           "vps01",
			"serverLiveInfo": map[string]interface{}{"state": "PMSUSPENDED"},
		})
		_, _ = w.Write(out)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Desired state is ON; server is in PMSUSPENDED (guest PM suspend).
	// PMSUSPENDED is persistent, not transitional: state must flip to SUSPENDED
	// so Terraform detects drift and proposes a corrective apply.
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
	// Must map to SUSPENDED so drift is detected (not preserve ON).
	if result.State.ValueString() != "SUSPENDED" {
		t.Errorf("State = %q, want SUSPENDED (PMSUSPENDED is persistent, must surface drift)", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_Crashed verifies that CRASHED (guest domain
// crashed) is treated as a persistent non-running state and mapped to OFF,
// so that Terraform detects drift when the desired state is ON.
func TestServerPowerResource_Read_Crashed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		out, _ := json.Marshal(map[string]interface{}{
			"id":             123,
			"name":           "vps01",
			"serverLiveInfo": map[string]interface{}{"state": "CRASHED"},
		})
		_, _ = w.Write(out)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Desired state is ON; server has crashed. CRASHED is persistent:
	// state must flip to OFF so Terraform detects drift and repairs.
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
	// Must map to OFF so drift is detected (not preserve ON).
	if result.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (CRASHED is persistent, must surface drift)", result.State.ValueString())
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

// TestServerPowerResource_ReadNormalizesNullWait verifies that a post-import
// Read normalizes a null `wait` to the schema default (true). After ImportState
// sets only id + server_id, `wait` is null; without normalization Read would
// write that null back, and because the schema proposes the default wait=true
// when config omits it, the first post-import plan would contain a spurious
// wait-only update. Read must coerce null → true so the post-import plan is clean.
func TestServerPowerResource_ReadNormalizesNullWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/servers/123" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		out, _ := json.Marshal(map[string]interface{}{
			"id":             123,
			"name":           "vps01",
			"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
		})
		_, _ = w.Write(out)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Simulate post-import state: ImportState sets only id + server_id; state and
	// wait are null. Read must normalize the null wait to the schema default true.
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "123"),
		"state":        tftypes.NewValue(tftypes.String, nil),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, nil),
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
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if result.Wait.IsNull() || result.Wait.IsUnknown() {
		t.Fatalf("wait = %v, want a known value (default true) after post-import Read", result.Wait)
	}
	if result.Wait.ValueBool() != true {
		t.Errorf("wait = %v, want true (null must normalize to schema default)", result.Wait.ValueBool())
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

// TestServerPowerResource_Update_WaitOnlyPreservesPendingTaskID verifies Thread A
// (P1): when a prior op was submitted with wait=false, its task UUID is recorded
// in pending_task_id. Changing ONLY `wait` reaches the no-command path, which must
// PRESERVE that marker (not null it). If it were nulled, the next refresh would
// stop consulting the still-running task and could map a pre-op transient live
// state over the desired value, causing the next apply to re-issue the destructive
// command. The wait-only Update must also make NO SetPowerState/PATCH call.
func TestServerPowerResource_Update_WaitOnlyPreservesPendingTaskID(t *testing.T) {
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
	// Prior state: OFF, wait=false, with an in-flight task recorded.
	priorState := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "123"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "123"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "some-uuid"),
	})
	// Plan: same state + state_option, only wait flipped to true.
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
	if powerCalled {
		t.Error("SetPowerState (PATCH) should NOT be called on a wait-only update, but it was")
	}

	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if result.PendingTaskID.ValueString() != "some-uuid" {
		t.Errorf("PendingTaskID = %q, want some-uuid (prior marker preserved on wait-only update)", result.PendingTaskID.ValueString())
	}
	if result.Wait.ValueBool() != true {
		t.Errorf("wait = %v, want true after wait-only update", result.Wait.ValueBool())
	}
}

// TestServerPowerResource_Read_PendingTerminalRefetchesLiveState verifies Thread B
// (P1): when Read finds pending_task_id set and GetTask reports the task TERMINAL,
// it REFETCHES GetServer to reconcile from a POST-completion live snapshot rather
// than the stale pre-completion snapshot taken before GetTask. Here the first
// GetServer returns SHUTOFF (mid-POWERCYCLE), GetTask returns FINISHED, and the
// second GetServer returns RUNNING (post-completion) ⇒ Read reconciles to ON and
// clears the marker. Without the refetch it would record OFF and reboot again.
func TestServerPowerResource_Read_PendingTerminalRefetchesLiveState(t *testing.T) {
	serverCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/888":
			serverCalls++
			// First call: pre-completion SHUTOFF (transient mid-POWERCYCLE).
			// Second call (post-refetch): post-completion RUNNING.
			liveState := "SHUTOFF"
			if serverCalls >= 2 {
				liveState = "RUNNING"
			}
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 888, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": liveState},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-cycle":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-cycle","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Desired ON (a POWERCYCLE reboot); task finished; post-completion live RUNNING.
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "888"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, "POWERCYCLE"),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "888"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-cycle"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if serverCalls < 2 {
		t.Errorf("GetServer was called %d time(s); expected a post-completion refetch (2)", serverCalls)
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (reconciled from POST-completion RUNNING, not stale SHUTOFF)", result.State.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (cleared after terminal task + refetch)", result.PendingTaskID.ValueString())
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
// known cases, including persistent non-running states that must produce drift.
func TestLiveStateToDesiredPower(t *testing.T) {
	tests := []struct {
		liveState string
		want      netcup.PowerState
		comment   string
	}{
		// Persistent running state.
		{"RUNNING", netcup.PowerOn, "running server → ON"},
		{"running", netcup.PowerOn, "case-insensitive"},

		// Persistent non-running states: must produce concrete values so Terraform
		// detects drift when the desired state diverges from the live state.
		{"SHUTOFF", netcup.PowerOff, "normal off state"},
		{"CRASHED", netcup.PowerOff, "guest crashed; persistent non-running → OFF surfaces drift"},

		// Persistent suspended states.
		{"SUSPENDED", netcup.PowerSuspended, "hypervisor suspend"},
		{"PAUSED", netcup.PowerSuspended, "hypervisor-level pause → SUSPENDED"},
		{"paused", netcup.PowerSuspended, "case-insensitive"},
		{"PMSUSPENDED", netcup.PowerSuspended, "guest PM suspend; persistent → SUSPENDED (not transitional)"},

		// Genuinely short-lived transitions: preserve prior desired state.
		{"SHUTDOWN", "", "ACPI shutdown in progress; resolves to SHUTOFF within seconds"},
		{"SAVE_RESTORE", "", "live-migration/snapshot in flight"},

		// Unknown states: preserve to avoid incorrect mapping.
		{"", "", "empty/unknown"},
		{"UNKNOWN_FUTURE", "", "unknown future state"},
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
