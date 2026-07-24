package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	if !isIndeterminateMarker(state.PendingTaskID.ValueString()) {
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
	if !isIndeterminateMarker(state.PendingTaskID.ValueString()) {
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
	if !isIndeterminateMarker(state.PendingTaskID.ValueString()) {
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
	if pendingTaskUUID(state.PendingTaskID.ValueString()) != "task-nowait" {
		t.Errorf("PendingTaskID = %q, want task-nowait (accepted UUID retained on wait=false)", state.PendingTaskID.ValueString())
	}
	if state.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF", state.State.ValueString())
	}
}

// TestServerPowerResource_Create_NoWaitEmptyUUIDStoresSentinel verifies Thread A
// (P1): when SetPowerState returns a syntactically valid 202 whose body OMITS
// `uuid` (e.g. `{}`), it yields a non-nil *TaskInfo with an empty UUID. On the
// wait=false path the persisted pending_task_id must be the pendingTaskIDIndeterminate
// SENTINEL (an accepted-but-untrackable task) — NOT an empty string that Read would
// skip, letting a transient live state overwrite the desired value and the next
// apply re-issue the destructive command. A subsequent Read on that sentinel keeps
// the desired state and does NOT call GetTask.
func TestServerPowerResource_Create_NoWaitEmptyUUIDStoresSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/servers/223":
			// Valid JSON, but no `uuid` field → TaskInfo.UUID == "".
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "223"),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, "POWERCYCLE"),
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
	// The empty-UUID accepted task must be recorded as the sentinel, NOT "".
	if !isIndeterminateMarker(state.PendingTaskID.ValueString()) {
		t.Errorf("PendingTaskID = %q, want %q (sentinel — empty UUID is untrackable, not an empty marker)", state.PendingTaskID.ValueString(), pendingTaskIDIndeterminate)
	}
	if state.PendingTaskID.ValueString() == "" {
		t.Error("PendingTaskID must not be an empty marker (Read would skip it and re-issue the command)")
	}
	if state.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF", state.State.ValueString())
	}

	// A subsequent Read on that sentinel must KEEP the desired state and NOT call
	// GetTask (there is no trackable task), even though the live state has not yet
	// converged.
	getTaskCalled := false
	readSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v1/servers/223":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 223, "name": "vps",
				// Live still RUNNING while OFF was requested (op not yet applied).
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/v1/tasks/"):
			getTaskCalled = true
			t.Errorf("GetTask must NOT be called for the indeterminate sentinel")
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	}))
	defer readSrv.Close()

	readClient := netcup.New(netcup.WithAPIEndpoint(readSrv.URL), netcup.WithAccessToken("tok"))
	rr, readSchemaResp := configureServerPowerResource(t, readClient)

	readSt := resourceState(readSchemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "223"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, "POWERCYCLE"),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "223"),
		"pending_task_id": tftypes.NewValue(tftypes.String, state.PendingTaskID.ValueString()),
	})

	var readResp resource.ReadResponse
	readResp.State = tfsdk.State{Schema: readSchemaResp.Schema}
	rr.Read(ctx, resource.ReadRequest{State: readSt}, &readResp)

	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", readResp.Diagnostics.Errors())
	}
	if getTaskCalled {
		t.Error("GetTask was called for the sentinel; it must be skipped")
	}
	var readResult serverPowerResourceModel
	readResp.Diagnostics.Append(readResp.State.Get(ctx, &readResult)...)
	if readResult.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (sentinel keeps desired; live RUNNING must not overwrite)", readResult.State.ValueString())
	}
	if !isIndeterminateMarker(readResult.PendingTaskID.ValueString()) {
		t.Errorf("PendingTaskID = %q, want sentinel retained until live converges", readResult.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Read_TerminalFailureReconcilesImmediately verifies
// Thread B (P2): a tracked task that reached a FAILURE terminal (ERROR) with a
// recent FinishedAt while the refetched live state is STILL SHUTOFF (a failed
// power-on) must NOT be treated as propagation lag. The propagation window applies
// only to the SUCCESS terminal (FINISHED); for a failure terminal Read IMMEDIATELY
// clears pending_task_id and reconciles from the live state (SHUTOFF → OFF) so the
// drift surfaces right away for a corrective apply, rather than retaining the
// desired ON for up to powerPropagationWindow.
func TestServerPowerResource_Read_TerminalFailureReconcilesImmediately(t *testing.T) {
	// Recent FinishedAt: WITHIN powerPropagationWindow — proves the window is NOT
	// applied to a failure terminal (it would otherwise retain the desired state).
	finished := time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/449":
			// Failed power-on: server is still SHUTOFF.
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 449, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-fail":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-fail","state":"ERROR","finishedAt":"` + finished + `","message":"power-on failed"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Desired ON (a power-on target); task failed recently; live still SHUTOFF ⇒
	// failure terminal ⇒ NO window ⇒ clear marker + reconcile to OFF immediately.
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "449"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "449"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-fail"),
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
		t.Errorf("State = %q, want OFF (failed power-on drift surfaced immediately, NOT retained by the propagation window)", result.State.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (cleared immediately on failure terminal)", result.PendingTaskID.ValueString())
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

// TestServerPowerResource_Read_PendingNonTerminalStuckPastTimeoutReconciles
// verifies thread r3638659469 (P2) refined by discussion_r3646364033: a wait=false
// task left non-terminal (RUNNING) far longer than defaultTaskTimeout is treated as
// STUCK — but the original UUID is retained so the next refresh re-checks GetTask
// rather than degrading/clearing the marker and potentially submitting a duplicate
// command while the original task is still confirmed running.
func TestServerPowerResource_Read_PendingNonTerminalStuckPastTimeoutReconciles(t *testing.T) {
	startedAt := time.Now().Add(-defaultTaskTimeout - time.Minute).UTC().Format(time.RFC3339Nano)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/334":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 334, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-stuck":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-stuck","state":"RUNNING","startedAt":"` + startedAt + `"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "334"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "334"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-stuck"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if result.PendingTaskID.ValueString() != "task-stuck" {
		t.Errorf("PendingTaskID = %q, want task-stuck (original UUID retained per discussion_r3646364033)", result.PendingTaskID.ValueString())
	}
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (desired state retained, not reconciled from live SHUTOFF)", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_PendingNonTerminalStuckSameStateSurfacesDrift
// verifies thread r3639343085 (P2) refined by discussion_r3646364033: when a
// same-state RESET task is stuck non-terminal past defaultTaskTimeout and the
// refetched server is RUNNING (== desired ON), the original UUID is retained so
// the next refresh re-checks GetTask rather than blanking state_option or
// degrading to sentinel, which could lose track of the still-running task.
func TestServerPowerResource_Read_PendingNonTerminalStuckSameStateSurfacesDrift(t *testing.T) {
	startedAt := time.Now().Add(-defaultTaskTimeout - time.Minute).UTC().Format(time.RFC3339Nano)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/336":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 336, "name": "vps",
				// Live RUNNING (== desired ON): a same-state reboot's live state proves
				// nothing, so the stuck op must still surface as a corrective diff.
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-stuck-reset":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-stuck-reset","state":"RUNNING","startedAt":"` + startedAt + `"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "336"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, "RESET"),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "336"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-stuck-reset"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if result.PendingTaskID.ValueString() != "task-stuck-reset" {
		t.Errorf("PendingTaskID = %q, want task-stuck-reset (original UUID retained per discussion_r3646364033)", result.PendingTaskID.ValueString())
	}
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (state unchanged)", result.State.ValueString())
	}
	// state_option preserved — the original marker is retained, so the next refresh
	// re-checks GetTask rather than clearing or degrading to sentinel.
	if result.StateOption.ValueString() != "RESET" {
		t.Errorf("StateOption = %q, want RESET (state_option preserved)", result.StateOption.ValueString())
	}
}

// TestServerPowerResource_Read_PendingNonTerminalWithinTimeoutRetains verifies the
// other side of thread r3638659469: a task still running WITHIN defaultTaskTimeout of
// its StartedAt is in flight, not stuck, so Read retains the marker and keeps the
// desired state (does not map the transient live state). Also covers the not-yet-
// started (StartedAt nil) case implicitly — those keep retaining too.
func TestServerPowerResource_Read_PendingNonTerminalWithinTimeoutRetains(t *testing.T) {
	startedAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/335":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 335, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-fresh":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-fresh","state":"RUNNING","startedAt":"` + startedAt + `"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "335"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "335"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-fresh"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if result.PendingTaskID.ValueString() != "task-fresh" {
		t.Errorf("PendingTaskID = %q, want task-fresh (retained while running within the timeout)", result.PendingTaskID.ValueString())
	}
	if result.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (desired kept; live RUNNING must NOT overwrite a still-in-flight op)", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_PendingQueuedNeverStartedStuckReconciles verifies
// thread r3639673890 (P2) refined by discussion_r3646364033: a wait=false task
// stuck in PENDING with StartedAt nil (never started) is bounded by the marker's
// persisted first-seen (acceptance) time. Here the marker was accepted 16m ago
// (> 15m defaultTaskTimeout) and the task is still PENDING with no startedAt, so
// Read treats it as stuck — but the original UUID is retained so the next refresh
// re-checks GetTask rather than clearing or degrading to sentinel.
func TestServerPowerResource_Read_PendingQueuedNeverStartedStuckReconciles(t *testing.T) {
	// Real-task marker (uuid + "@" + unixnano) first-seen 16m ago.
	marker := "task-queued" + indeterminateMarkerSep + strconv.FormatInt(time.Now().Add(-defaultTaskTimeout-time.Minute).UnixNano(), 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/337":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 337, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-queued":
			// Still PENDING, and StartedAt omitted (nil) — the queue never advanced.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-queued","state":"PENDING"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "337"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "337"),
		"pending_task_id": tftypes.NewValue(tftypes.String, marker),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if pendingTaskUUID(result.PendingTaskID.ValueString()) != "task-queued" {
		t.Errorf("PendingTaskID = %q, want task-queued@... (original UUID retained per discussion_r3646364033)", result.PendingTaskID.ValueString())
	}
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (desired state retained through original marker)", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_PendingQueuedNeverStartedWithinTimeoutRetains verifies
// the other side of thread r3639673890: a freshly-accepted PENDING task (StartedAt nil,
// first-seen just now) is still within defaultTaskTimeout, so it is retained — the
// acceptance-time bound must not expire a task that was only just queued.
func TestServerPowerResource_Read_PendingQueuedNeverStartedWithinTimeoutRetains(t *testing.T) {
	marker := "task-queued" + indeterminateMarkerSep + strconv.FormatInt(time.Now().UnixNano(), 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/338":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 338, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-queued":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-queued","state":"PENDING"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "338"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "338"),
		"pending_task_id": tftypes.NewValue(tftypes.String, marker),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if pendingTaskUUID(result.PendingTaskID.ValueString()) != "task-queued" {
		t.Errorf("PendingTaskID = %q, want the task-queued marker retained within the acceptance timeout", result.PendingTaskID.ValueString())
	}
	if result.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (desired kept; freshly-queued op is not yet stuck)", result.State.ValueString())
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
// terminal-task path when FinishedAt is absent: desired ON but the task finished
// and the live state is SHUTOFF. Without a completion timestamp we cannot
// distinguish propagation lag from genuine drift, so Read degrades to a
// timestamped indeterminate sentinel (discussion_r3646310296) — the sentinel's
// bounded window will resolve the ambiguity on the next refresh.
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
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (desired retained through sentinel since FinishedAt is absent)", result.State.ValueString())
	}
	if result.PendingTaskID.IsNull() || !isIndeterminateMarker(result.PendingTaskID.ValueString()) {
		t.Errorf("PendingTaskID = %q, want indeterminate sentinel (degraded from finished marker)", result.PendingTaskID.ValueString())
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

// TestServerPowerResource_Read_PendingTaskGoneUnconvergedRetains verifies the
// Thread B (P1 follow-up) fix: when the pending task is gone (GetTask 404) and the
// fresh POST-lookup refetch does NOT yet confirm the desired state, Read must NOT
// clear the marker and reconcile from the (still-unconverged) live state — doing
// so would record the wrong power state and make the next apply re-issue the
// command. Here desired is OFF but the refetched live state is still RUNNING (the
// off op hasn't propagated / the task was purged mid-transition): Read RETAINS the
// desired OFF and falls back to the indeterminate sentinel until a later refresh
// observes convergence.
func TestServerPowerResource_Read_PendingTaskGoneUnconvergedRetains(t *testing.T) {
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
	// Live RUNNING ≠ desired OFF ⇒ not converged: retain desired OFF, do not map.
	if result.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (retained; live RUNNING not yet converged after task 404)", result.State.ValueString())
	}
	// Marker degrades to the indeterminate sentinel (real UUID is gone/untrackable).
	if !isIndeterminateMarker(result.PendingTaskID.ValueString()) {
		t.Errorf("PendingTaskID = %q, want %q (retained sentinel until convergence)", result.PendingTaskID.ValueString(), pendingTaskIDIndeterminate)
	}
}

// TestServerPowerResource_Read_PendingTaskGoneConvergedClears verifies that when
// the pending task is gone (GetTask 404) and the fresh refetch CONFIRMS the desired
// state, Read clears the marker and reconciles normally. Desired ON, refetch shows
// RUNNING ⇒ converged ⇒ marker cleared, state ON.
func TestServerPowerResource_Read_PendingTaskGoneConvergedClears(t *testing.T) {
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
		"state":           tftypes.NewValue(tftypes.String, "ON"),
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
	// Live RUNNING → ON confirms desired ON ⇒ converged: clear the marker.
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (reconciled from live RUNNING after task 404)", result.State.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (cleared on convergence after task 404)", result.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Read_PendingTaskGonePowercycleLagRetains reproduces the
// reviewer's exact example: a POWERCYCLE (desired ON) whose task briefly 404s, and
// whose fresh refetch STILL reports SHUTOFF before the server comes back RUNNING.
// The unconditional-clear bug recorded OFF and rebooted again on the next apply;
// the fix retains desired ON + the sentinel until the live state converges.
func TestServerPowerResource_Read_PendingTaskGonePowercycleLagRetains(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/555":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 555, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
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
		"state":           tftypes.NewValue(tftypes.String, "ON"),
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
	// Refetch SHUTOFF (→ OFF) ≠ desired ON ⇒ propagation lag: retain ON, do not
	// record OFF (which would reboot again next apply).
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (retained; refetch SHUTOFF is unconverged POWERCYCLE lag)", result.State.ValueString())
	}
	if !isIndeterminateMarker(result.PendingTaskID.ValueString()) {
		t.Errorf("PendingTaskID = %q, want %q (retained sentinel until convergence)", result.PendingTaskID.ValueString(), pendingTaskIDIndeterminate)
	}
}

// TestServerPowerResource_Read_PendingTaskGoneRefetchesLiveState verifies Thread B
// (P2): when the pending task disappears (GetTask 404) between the initial
// GetServer and the lookup, Read REFETCHES GetServer for a fresh POST-lookup
// snapshot before clearing the marker and reconciling — rather than reconciling
// from the stale pre-lookup snapshot. Here the first GetServer returns RUNNING (a
// transient mid-OFF live state for a wait=false OFF op), GetTask returns 404, and
// the second GetServer returns the settled SHUTOFF ⇒ Read reconciles to OFF (from
// the FRESH snapshot, not the stale RUNNING) and clears the marker. Without the
// refetch it would record ON and re-issue the power-off next apply.
func TestServerPowerResource_Read_PendingTaskGoneRefetchesLiveState(t *testing.T) {
	serverCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/556":
			serverCalls++
			// First call: pre-lookup RUNNING (transient mid-OFF). Second call
			// (post-refetch): settled SHUTOFF.
			liveState := "RUNNING"
			if serverCalls >= 2 {
				liveState = "SHUTOFF"
			}
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 556, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": liveState},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-gone-2":
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
	// Desired OFF (wait=false); task gone; settled live state SHUTOFF.
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "556"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "556"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-gone-2"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if serverCalls < 2 {
		t.Errorf("GetServer was called %d time(s); expected a post-lookup refetch (2)", serverCalls)
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if result.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (reconciled from POST-lookup SHUTOFF, not stale RUNNING)", result.State.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (cleared on task 404)", result.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Read_SameStateResetTaskGoneDegradesToBoundedSentinel
// verifies thread r3638353424 (P1): a SAME-STATE op (state=ON, state_option=RESET)
// whose GetTask 404s while the refetched live state is STILL RUNNING (== desired ON)
// must NOT clear the marker on that live-equality (for a same-state reboot,
// live==desired is not proof of convergence). Read RETAINS the desired ON and does
// NOT map the live state — but instead of retaining the RAW UUID forever (which would
// hide a later external shutdown indefinitely if the purged task never reappears), it
// DEGRADES the marker to a timestamped indeterminate sentinel so the bounded-age
// reconciliation eventually surfaces drift.
func TestServerPowerResource_Read_SameStateResetTaskGoneDegradesToBoundedSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/560":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 560, "name": "vps",
				// Live RUNNING (== desired ON) — but the RESET reboot may not have
				// started yet, so this equality does NOT prove convergence.
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-reset-gone":
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
		"server_id":       tftypes.NewValue(tftypes.String, "560"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, "RESET"),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "560"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-reset-gone"),
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
		t.Errorf("State = %q, want ON (same-state RESET: live equality is not convergence)", result.State.ValueString())
	}
	// The raw UUID is DEGRADED to a timestamped indeterminate sentinel (bounded), not
	// retained forever and not cleared on the false live-equality.
	if !isIndeterminateMarker(result.PendingTaskID.ValueString()) {
		t.Errorf("PendingTaskID = %q, want a bounded indeterminate sentinel (degraded from the purged UUID)", result.PendingTaskID.ValueString())
	}
	if _, ok := indeterminateMarkerTime(result.PendingTaskID.ValueString()); !ok {
		t.Errorf("PendingTaskID = %q, want an embedded first-seen timestamp to bound retention", result.PendingTaskID.ValueString())
	}
	if result.StateOption.ValueString() != "RESET" {
		t.Errorf("StateOption = %q, want RESET (unchanged on a still-pending same-state op)", result.StateOption.ValueString())
	}
}

// TestServerPowerResource_Read_SameStateResetTaskFinishedClears verifies Thread A
// (P1): the retention in the 404 case is bounded — on a LATER refresh the
// re-queried task is FINISHED, and for a same-state op the task terminal-SUCCESS
// IS the convergence signal. Read then CLEARS the marker (not inferring
// convergence from live-equality) and reconciles from the fresh live state.
func TestServerPowerResource_Read_SameStateResetTaskFinishedClears(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/561":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 561, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-reset-fin":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-reset-fin","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "561"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, "RESET"),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "561"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-reset-fin"),
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
		t.Errorf("State = %q, want ON (FINISHED same-state reboot; live RUNNING confirms)", result.State.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (FINISHED is the convergence signal for a same-state op)", result.PendingTaskID.ValueString())
	}
	// SUCCESS same-state op: state_option is NOT blanked — no perpetual diff.
	if result.StateOption.ValueString() != "RESET" {
		t.Errorf("StateOption = %q, want RESET (restored/kept on a successful same-state op)", result.StateOption.ValueString())
	}
}

// TestServerPowerResource_Read_SameStateResetTaskFinishedMidRebootRetains verifies
// Thread B (P1): CORRECTS the 25afe91 behavior. When a same-state POWERCYCLE task
// is FINISHED but the post-FINISHED refetch STILL shows SHUTOFF (propagation lag)
// and FinishedAt is recent (WITHIN powerPropagationWindow), FINISHED alone is NOT
// sufficient to clear — the marker is RETAINED and the desired ON is KEPT (map
// nothing). 25afe91 previously cleared unconditionally and recorded OFF, which
// made the next apply reboot again; this test asserts the corrected retain.
func TestServerPowerResource_Read_SameStateResetTaskFinishedMidRebootRetains(t *testing.T) {
	finished := time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/562":
			// Still SHUTOFF right after FINISHED (propagation lag). Within the window
			// this is treated as lag: retain the marker, keep desired ON.
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 562, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-reset-fin2":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-reset-fin2","state":"FINISHED","finishedAt":"` + finished + `"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "562"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, "POWERCYCLE"),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "562"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-reset-fin2"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	// Within window + still SHUTOFF ⇒ propagation lag: retain marker, keep ON.
	if result.PendingTaskID.ValueString() != "task-reset-fin2" {
		t.Errorf("PendingTaskID = %q, want task-reset-fin2 (retained within propagation window on lagged refetch)", result.PendingTaskID.ValueString())
	}
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (desired kept; SHUTOFF refetch is propagation lag, not converged)", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_SameStateResetTaskFinishedPastWindowReconciles
// verifies Thread B (P1): once powerPropagationWindow has elapsed since FinishedAt
// and the refetch STILL shows SHUTOFF, the mismatch is treated as GENUINE drift —
// the marker is cleared and the live state reconciled to OFF so a real
// externally-stopped server surfaces for a corrective apply.
func TestServerPowerResource_Read_SameStateResetTaskFinishedPastWindowReconciles(t *testing.T) {
	// FinishedAt well past the 2m window.
	finished := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/563":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 563, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-reset-fin3":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-reset-fin3","state":"FINISHED","finishedAt":"` + finished + `"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "563"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, "POWERCYCLE"),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "563"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-reset-fin3"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	// Past window + still SHUTOFF ⇒ genuine drift: clear + reconcile to OFF.
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (past window ⇒ genuine drift, cleared)", result.PendingTaskID.ValueString())
	}
	if result.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (past window reconciled from live SHUTOFF)", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_SameStateResetTaskFailedForcesDrift verifies Thread
// B (P2): a retained wait=false RESET task that reaches a FAILURE terminal (ERROR)
// while the server is still RUNNING (== desired ON) must FORCE DRIFT. The stored
// state stays state=ON (live) but state_option is BLANKED to null so the next plan
// shows `state_option: null → RESET` (a diff that re-runs Update and re-issues the
// reboot). Without this, state=ON + state_option=RESET both equal config and
// Terraform would report NO changes, never retrying the failed reboot.
func TestServerPowerResource_Read_SameStateResetTaskFailedForcesDrift(t *testing.T) {
	finished := time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/563":
			// Reboot never happened: server is still RUNNING (== desired ON).
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 563, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-reset-fail":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-reset-fail","state":"ERROR","finishedAt":"` + finished + `","message":"reset failed"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "563"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, "RESET"),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "563"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-reset-fail"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	// state stays ON (the live power state); state_option is blanked to force drift.
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (state keeps meaning live power state)", result.State.ValueString())
	}
	if !result.StateOption.IsNull() {
		t.Errorf("StateOption = %q, want null (blanked to force `null → RESET` drift so the failed reboot is retried)", result.StateOption.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (cleared immediately on failure terminal)", result.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Read_NonSameStateTaskFailedKeepsOption verifies Thread B
// (P2): a NON-same-state failed op (state=OFF, state_option=POWEROFF) must be left
// UNCHANGED — its live state already diverges from desired (failed power-off ⇒ the
// server is still RUNNING ⇒ ON ≠ OFF), so clear+reconcile-from-live already
// surfaces drift. state_option must NOT be blanked here.
func TestServerPowerResource_Read_NonSameStateTaskFailedKeepsOption(t *testing.T) {
	finished := time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/564":
			// Failed power-off: server is still RUNNING (≠ desired OFF) ⇒ drift already
			// surfaces via live→ON.
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 564, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-off-fail":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-off-fail","state":"ERROR","finishedAt":"` + finished + `","message":"poweroff failed"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "564"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, "POWEROFF"),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "564"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-off-fail"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	// Live RUNNING → ON ≠ desired OFF: drift already surfaces; state_option untouched.
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (drift surfaces from live for a non-same-state failure)", result.State.ValueString())
	}
	if result.StateOption.ValueString() != "POWEROFF" {
		t.Errorf("StateOption = %q, want POWEROFF (NOT blanked for a non-same-state failed op)", result.StateOption.ValueString())
	}
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (cleared on failure terminal)", result.PendingTaskID.ValueString())
	}
}

// TestIsSameStatePowerOp unit-tests the same-state detection helper: RESET and
// POWERCYCLE on an ON server are same-state (reboot-through-SHUTOFF, identical
// pre-/post power state); everything else is not.
func TestIsSameStatePowerOp(t *testing.T) {
	tests := []struct {
		state       string
		stateOption string
		want        bool
	}{
		{"ON", "RESET", true},
		{"ON", "POWERCYCLE", true},
		{"ON", "reset", true},      // case-insensitive
		{"ON", "PowerCycle", true}, // case-insensitive
		{"ON", " RESET ", true},    // trimmed
		{"ON", "", false},          // plain power-on, not same-state
		{"ON", "POWEROFF", false},  // not a documented ON option
		{"OFF", "POWEROFF", false}, // power-off changes the power state
		{"OFF", "RESET", false},    // RESET only meaningful on ON
		{"SUSPENDED", "", false},   // suspend changes the power state
		{"on", "RESET", true},      // case-insensitive state
		{"", "RESET", false},       // no state
	}
	for _, tt := range tests {
		t.Run(tt.state+"/"+tt.stateOption, func(t *testing.T) {
			if got := isSameStatePowerOp(tt.state, tt.stateOption); got != tt.want {
				t.Errorf("isSameStatePowerOp(%q, %q) = %v, want %v", tt.state, tt.stateOption, got, tt.want)
			}
		})
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
	if !isIndeterminateMarker(result.PendingTaskID.ValueString()) {
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

// TestServerPowerResource_Read_SameStateSentinelRetainsOnLiveRunning verifies
// Thread C (P1): for a SAME-STATE op (RESET/POWERCYCLE on an ON server) carrying
// the indeterminate sentinel (accepted-but-untrackable reboot, no UUID), Read must
// NOT clear the sentinel when the live state equals the desired value (RUNNING ==
// ON). That first-refresh RUNNING could be the pre-op state before the reboot even
// begins; clearing then would let a later SHUTOFF refresh record OFF and trigger a
// second reboot. The sentinel + desired ON are RETAINED (no GetTask), consistent
// with the same-state 404 branch's safe-retain tradeoff.
func TestServerPowerResource_Read_SameStateSentinelRetainsOnLiveRunning(t *testing.T) {
	getTaskCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/888":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 888, "name": "vps",
				// Live RUNNING (== desired ON): for a same-state reboot this is
				// ambiguous (pre-reboot vs post-reboot) and must NOT clear the sentinel.
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
	// Same-state op: state=ON, state_option=RESET, sentinel marker, live RUNNING.
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "888"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, "RESET"),
		"wait":            tftypes.NewValue(tftypes.Bool, true),
		"id":              tftypes.NewValue(tftypes.String, "888"),
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
	// Same-state sentinel: RETAINED even though live == desired.
	if !isIndeterminateMarker(result.PendingTaskID.ValueString()) {
		t.Errorf("PendingTaskID = %q, want sentinel retained (same-state live-equality must NOT clear)", result.PendingTaskID.ValueString())
	}
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (desired kept)", result.State.ValueString())
	}
}

// timestampedSentinelMarker builds a timestamped indeterminate marker with the given
// first-seen time, mirroring newIndeterminateMarker's encoding.
func timestampedSentinelMarker(firstSeen time.Time) string {
	return pendingTaskIDIndeterminate + indeterminateMarkerSep + strconv.FormatInt(firstSeen.UnixNano(), 10)
}

// TestServerPowerResource_Read_SameStateSentinelBoundedClearsPastWindow verifies the
// P2 fix (thread r3635966368): a same-state (RESET) indeterminate sentinel carries an
// embedded first-seen timestamp. Once powerPropagationWindow elapses since first-seen
// the reboot's risky transition is over, so Read STOPS retaining — it clears the
// sentinel and reconciles from the live state, surfacing a LATER external shutdown
// (live SHUTOFF ⇒ OFF) that the old unbounded retain hid indefinitely.
func TestServerPowerResource_Read_SameStateSentinelBoundedClearsPastWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/888":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 888, "name": "vps",
				// External shutdown AFTER the reboot completed — must surface once the
				// bounded retain window has elapsed.
				"serverLiveInfo": map[string]interface{}{"state": "SHUTOFF"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
			t.Errorf("GetTask must NOT be called for the indeterminate sentinel")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// first-seen well past powerPropagationWindow.
	marker := timestampedSentinelMarker(time.Now().Add(-powerPropagationWindow - time.Minute))
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "888"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, "RESET"),
		"wait":            tftypes.NewValue(tftypes.Bool, true),
		"id":              tftypes.NewValue(tftypes.String, "888"),
		"pending_task_id": tftypes.NewValue(tftypes.String, marker),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (sentinel cleared past the propagation window)", result.PendingTaskID.ValueString())
	}
	if result.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF (external shutdown surfaced once the window elapsed)", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_SameStateSentinelBoundedRetainsWithinWindow verifies
// the other side of the P2 fix (thread r3635966368): WITHIN powerPropagationWindow of
// the embedded first-seen timestamp the reboot may still be in flight, so a same-state
// sentinel is still RETAINED (a live RUNNING == desired ON must NOT clear it) and the
// desired ON is kept — preserving the Thread C safe-retain during the risky window.
func TestServerPowerResource_Read_SameStateSentinelBoundedRetainsWithinWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/888":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 888, "name": "vps",
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
			t.Errorf("GetTask must NOT be called for the indeterminate sentinel")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Freshly first-seen (well within the window).
	marker := timestampedSentinelMarker(time.Now())
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "888"),
		"state":           tftypes.NewValue(tftypes.String, "ON"),
		"state_option":    tftypes.NewValue(tftypes.String, "RESET"),
		"wait":            tftypes.NewValue(tftypes.Bool, true),
		"id":              tftypes.NewValue(tftypes.String, "888"),
		"pending_task_id": tftypes.NewValue(tftypes.String, marker),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if !isIndeterminateMarker(result.PendingTaskID.ValueString()) {
		t.Errorf("PendingTaskID = %q, want sentinel retained within the propagation window", result.PendingTaskID.ValueString())
	}
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (desired kept while within window)", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_NonSameStateSentinelBoundedClearsPastWindow verifies
// thread r3638353419 (P1): a NON-same-state indeterminate op (here state=OFF) that
// never took effect never reaches live==desired, so the sentinel would otherwise be
// retained forever and Terraform would never retry. The embedded first-seen timestamp
// bounds it: once powerPropagationWindow elapses, Read clears the sentinel and
// reconciles from the live state (still RUNNING ⇒ ON), surfacing the drift so the next
// plan (config OFF vs state ON) re-issues the command.
func TestServerPowerResource_Read_NonSameStateSentinelBoundedClearsPastWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/889":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 889, "name": "vps",
				// The requested OFF never took effect — server is still RUNNING, so
				// live never equals desired OFF. Past the window this must surface.
				"serverLiveInfo": map[string]interface{}{"state": "RUNNING"},
			})
			_, _ = w.Write(out)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
			t.Errorf("GetTask must NOT be called for the indeterminate sentinel")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	marker := timestampedSentinelMarker(time.Now().Add(-powerPropagationWindow - time.Minute))
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "889"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "889"),
		"pending_task_id": tftypes.NewValue(tftypes.String, marker),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (non-same-state sentinel cleared past the window)", result.PendingTaskID.ValueString())
	}
	if result.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON (never-executed OFF surfaced from live once the window elapsed)", result.State.ValueString())
	}
}

// TestServerPowerResource_Read_NonSameStateSentinelClearsOnConverge verifies Thread
// C (P1): the NON-same-state sentinel path clears EARLY (within the window) when the
// live state converges to the desired value (pre-/post-op states differ, so equality
// proves convergence). Complements the same-state retain test above; the OFF-op
// sentinel tests already cover this, this one asserts it explicitly alongside the
// same-state contrast.
func TestServerPowerResource_Read_NonSameStateSentinelClearsOnConverge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/889":
			w.WriteHeader(http.StatusOK)
			out, _ := json.Marshal(map[string]interface{}{
				"id": 889, "name": "vps",
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
	// Non-same-state op: state=OFF (no state_option), sentinel, live SHUTOFF ⇒ OFF.
	st := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "889"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, true),
		"id":              tftypes.NewValue(tftypes.String, "889"),
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
	if !result.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (non-same-state sentinel clears on live-equality)", result.PendingTaskID.ValueString())
	}
	if result.State.ValueString() != "OFF" {
		t.Errorf("State = %q, want OFF", result.State.ValueString())
	}
}

// TestServerPowerResource_ModifyPlan_ServerIDChangeForcesUnknown verifies Thread A
// (P1): when server_id changes (a replacement), ModifyPlan forces id +
// pending_task_id to unknown so Create recomputes them for the NEW server —
// avoiding "Provider produced inconsistent result after apply" after the disruptive
// power op. Mirrors rescueResource.ModifyPlan.
func TestServerPowerResource_ModifyPlan_ServerIDChangeForcesUnknown(t *testing.T) {
	r, schemaResp := configureServerPowerResource(t, netcup.New(netcup.WithAPIEndpoint("http://example.invalid"), netcup.WithAccessToken("tok")))
	ctx := context.Background()

	req := resource.ModifyPlanRequest{
		State: resourceState(schemaResp, map[string]tftypes.Value{
			"server_id":       tftypes.NewValue(tftypes.String, "12345"),
			"state":           tftypes.NewValue(tftypes.String, "ON"),
			"state_option":    tftypes.NewValue(tftypes.String, nil),
			"wait":            tftypes.NewValue(tftypes.Bool, true),
			"id":              tftypes.NewValue(tftypes.String, "12345"),
			"pending_task_id": tftypes.NewValue(tftypes.String, nil),
		}),
		// What stock UseStateForUnknown would produce for a server_id change: the
		// prior computed id copied into the plan (the bug ModifyPlan corrects).
		Plan: resourcePlan(schemaResp, map[string]tftypes.Value{
			"server_id":       tftypes.NewValue(tftypes.String, "67890"),
			"state":           tftypes.NewValue(tftypes.String, "ON"),
			"state_option":    tftypes.NewValue(tftypes.String, nil),
			"wait":            tftypes.NewValue(tftypes.Bool, true),
			"id":              tftypes.NewValue(tftypes.String, "12345"),
			"pending_task_id": tftypes.NewValue(tftypes.String, nil),
		}),
	}
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	r.(resource.ResourceWithModifyPlan).ModifyPlan(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var planned serverPowerResourceModel
	resp.Diagnostics.Append(resp.Plan.Get(ctx, &planned)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Plan.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !planned.ID.IsUnknown() {
		t.Errorf("id must be unknown on a server_id replacement, got %q", planned.ID.ValueString())
	}
	if !planned.PendingTaskID.IsUnknown() {
		t.Errorf("pending_task_id must be unknown on a server_id replacement, got %q", planned.PendingTaskID.ValueString())
	}
	// server_id itself is the trigger and must remain the new known value.
	if planned.ServerID.ValueString() != "67890" {
		t.Errorf("server_id = %q, want 67890", planned.ServerID.ValueString())
	}
}

// TestServerPowerResource_ModifyPlan_InPlaceUpdatePreserves verifies Thread A
// (P1): on an in-place update (server_id unchanged and known — e.g. a wait-only
// change) ModifyPlan must NOT force the computed values unknown, so stock
// UseStateForUnknown keeps them stable (no spurious "(known after apply)").
func TestServerPowerResource_ModifyPlan_InPlaceUpdatePreserves(t *testing.T) {
	r, schemaResp := configureServerPowerResource(t, netcup.New(netcup.WithAPIEndpoint("http://example.invalid"), netcup.WithAccessToken("tok")))
	ctx := context.Background()

	req := resource.ModifyPlanRequest{
		State: resourceState(schemaResp, map[string]tftypes.Value{
			"server_id":       tftypes.NewValue(tftypes.String, "12345"),
			"state":           tftypes.NewValue(tftypes.String, "ON"),
			"state_option":    tftypes.NewValue(tftypes.String, nil),
			"wait":            tftypes.NewValue(tftypes.Bool, true),
			"id":              tftypes.NewValue(tftypes.String, "12345"),
			"pending_task_id": tftypes.NewValue(tftypes.String, "task-abc"),
		}),
		// Wait-only change: server_id unchanged; computed values still known (stock
		// UseStateForUnknown already reused prior state).
		Plan: resourcePlan(schemaResp, map[string]tftypes.Value{
			"server_id":       tftypes.NewValue(tftypes.String, "12345"),
			"state":           tftypes.NewValue(tftypes.String, "ON"),
			"state_option":    tftypes.NewValue(tftypes.String, nil),
			"wait":            tftypes.NewValue(tftypes.Bool, false),
			"id":              tftypes.NewValue(tftypes.String, "12345"),
			"pending_task_id": tftypes.NewValue(tftypes.String, "task-abc"),
		}),
	}
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	r.(resource.ResourceWithModifyPlan).ModifyPlan(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var planned serverPowerResourceModel
	resp.Diagnostics.Append(resp.Plan.Get(ctx, &planned)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Plan.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if planned.ID.IsUnknown() || planned.PendingTaskID.IsUnknown() {
		t.Error("in-place update must keep computed values known (no forced unknown / no plan churn)")
	}
	if planned.ID.ValueString() != "12345" || planned.PendingTaskID.ValueString() != "task-abc" {
		t.Errorf("in-place update must preserve prior computed values, got id=%q pending=%q", planned.ID.ValueString(), planned.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_ModifyPlan_InPlaceCommandChangeForcesMarkerUnknown
// verifies the P1 fix (thread r3635966365): on an in-place update (server_id
// unchanged) where the power command itself changes — here state_option flips from
// null to RESET — Update will submit a new command and write a freshly-computed
// pending_task_id. Stock UseStateForUnknown would copy the PRIOR marker into the
// plan, so a normal async 202 (new UUID) would differ from it and Terraform would
// reject the apply with "inconsistent result after apply" AFTER the disruptive
// command ran. ModifyPlan must force pending_task_id UNKNOWN, while keeping id
// stable (id == server_id, unchanged).
func TestServerPowerResource_ModifyPlan_InPlaceCommandChangeForcesMarkerUnknown(t *testing.T) {
	r, schemaResp := configureServerPowerResource(t, netcup.New(netcup.WithAPIEndpoint("http://example.invalid"), netcup.WithAccessToken("tok")))
	ctx := context.Background()

	req := resource.ModifyPlanRequest{
		State: resourceState(schemaResp, map[string]tftypes.Value{
			"server_id":       tftypes.NewValue(tftypes.String, "12345"),
			"state":           tftypes.NewValue(tftypes.String, "ON"),
			"state_option":    tftypes.NewValue(tftypes.String, nil),
			"wait":            tftypes.NewValue(tftypes.Bool, true),
			"id":              tftypes.NewValue(tftypes.String, "12345"),
			"pending_task_id": tftypes.NewValue(tftypes.String, "task-prior"),
		}),
		// In-place command change: server_id unchanged, state_option null → RESET.
		// Stock UseStateForUnknown copies the prior marker forward (the bug this fix
		// corrects).
		Plan: resourcePlan(schemaResp, map[string]tftypes.Value{
			"server_id":       tftypes.NewValue(tftypes.String, "12345"),
			"state":           tftypes.NewValue(tftypes.String, "ON"),
			"state_option":    tftypes.NewValue(tftypes.String, "RESET"),
			"wait":            tftypes.NewValue(tftypes.Bool, true),
			"id":              tftypes.NewValue(tftypes.String, "12345"),
			"pending_task_id": tftypes.NewValue(tftypes.String, "task-prior"),
		}),
	}
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	r.(resource.ResourceWithModifyPlan).ModifyPlan(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var planned serverPowerResourceModel
	resp.Diagnostics.Append(resp.Plan.Get(ctx, &planned)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Plan.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !planned.PendingTaskID.IsUnknown() {
		t.Errorf("pending_task_id must be unknown when an in-place update issues a new command, got %q", planned.PendingTaskID.ValueString())
	}
	// id must stay stable (server_id unchanged) — only the marker is forced unknown.
	if planned.ID.IsUnknown() || planned.ID.ValueString() != "12345" {
		t.Errorf("id must stay stable at 12345 on an in-place command change, got unknown=%v value=%q", planned.ID.IsUnknown(), planned.ID.ValueString())
	}
}

// TestServerPowerResource_ModifyPlan_UnknownServerIDForcesUnknown verifies Thread A
// (P1): an UNKNOWN planned server_id (derived from another resource's not-yet-known
// output) cannot be proven equal, so ModifyPlan treats it as a potential
// replacement and forces the computed values unknown.
func TestServerPowerResource_ModifyPlan_UnknownServerIDForcesUnknown(t *testing.T) {
	r, schemaResp := configureServerPowerResource(t, netcup.New(netcup.WithAPIEndpoint("http://example.invalid"), netcup.WithAccessToken("tok")))
	ctx := context.Background()

	req := resource.ModifyPlanRequest{
		State: resourceState(schemaResp, map[string]tftypes.Value{
			"server_id":       tftypes.NewValue(tftypes.String, "12345"),
			"state":           tftypes.NewValue(tftypes.String, "ON"),
			"state_option":    tftypes.NewValue(tftypes.String, nil),
			"wait":            tftypes.NewValue(tftypes.Bool, true),
			"id":              tftypes.NewValue(tftypes.String, "12345"),
			"pending_task_id": tftypes.NewValue(tftypes.String, nil),
		}),
		Plan: resourcePlan(schemaResp, map[string]tftypes.Value{
			"server_id":       tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
			"state":           tftypes.NewValue(tftypes.String, "ON"),
			"state_option":    tftypes.NewValue(tftypes.String, nil),
			"wait":            tftypes.NewValue(tftypes.Bool, true),
			"id":              tftypes.NewValue(tftypes.String, "12345"),
			"pending_task_id": tftypes.NewValue(tftypes.String, nil),
		}),
	}
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	r.(resource.ResourceWithModifyPlan).ModifyPlan(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var planned serverPowerResourceModel
	resp.Diagnostics.Append(resp.Plan.Get(ctx, &planned)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Plan.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !planned.ID.IsUnknown() || !planned.PendingTaskID.IsUnknown() {
		t.Error("an unknown planned server_id must force id + pending_task_id unknown (potential replacement)")
	}
}

// TestServerPowerResource_ModifyPlan_CreateAndDestroyNoop verifies Thread A (P1):
// ModifyPlan does nothing on create (null prior state) or destroy (null plan).
func TestServerPowerResource_ModifyPlan_CreateAndDestroyNoop(t *testing.T) {
	r, schemaResp := configureServerPowerResource(t, netcup.New(netcup.WithAPIEndpoint("http://example.invalid"), netcup.WithAccessToken("tok")))
	ctx := context.Background()
	objType := schemaResp.Schema.Type().TerraformType(ctx)

	createReq := resource.ModifyPlanRequest{
		State: tfsdk.State{Raw: tftypes.NewValue(objType, nil), Schema: schemaResp.Schema},
		Plan: resourcePlan(schemaResp, map[string]tftypes.Value{
			"server_id":       tftypes.NewValue(tftypes.String, "12345"),
			"state":           tftypes.NewValue(tftypes.String, "ON"),
			"state_option":    tftypes.NewValue(tftypes.String, nil),
			"wait":            tftypes.NewValue(tftypes.Bool, true),
			"id":              tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
			"pending_task_id": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		}),
	}
	createResp := resource.ModifyPlanResponse{Plan: createReq.Plan}
	r.(resource.ResourceWithModifyPlan).ModifyPlan(ctx, createReq, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan(create) unexpected diagnostics: %v", createResp.Diagnostics.Errors())
	}

	destroyReq := resource.ModifyPlanRequest{
		State: resourceState(schemaResp, map[string]tftypes.Value{
			"server_id":       tftypes.NewValue(tftypes.String, "12345"),
			"state":           tftypes.NewValue(tftypes.String, "ON"),
			"state_option":    tftypes.NewValue(tftypes.String, nil),
			"wait":            tftypes.NewValue(tftypes.Bool, true),
			"id":              tftypes.NewValue(tftypes.String, "12345"),
			"pending_task_id": tftypes.NewValue(tftypes.String, nil),
		}),
		Plan: tfsdk.Plan{Raw: tftypes.NewValue(objType, nil), Schema: schemaResp.Schema},
	}
	destroyResp := resource.ModifyPlanResponse{Plan: destroyReq.Plan}
	r.(resource.ResourceWithModifyPlan).ModifyPlan(ctx, destroyReq, &destroyResp)
	if destroyResp.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan(destroy) unexpected diagnostics: %v", destroyResp.Diagnostics.Errors())
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

// TestServerPowerResource_Update_WaitOnlyPreservesPendingTaskID verifies the
// no-command marker rule for a wait-only update that has NOTHING to poll: when the
// retained marker is the indeterminate SENTINEL (no real task) and `wait` flips
// false→true, the no-command path must PRESERVE that marker (not null it) and make
// NO SetPowerState/PATCH call and NO WaitForTask call (there is no real task to
// poll). If the marker were nulled, the next refresh would stop consulting the
// pending op and could map a pre-op transient live state over the desired value,
// causing the next apply to re-issue the destructive command. (The REAL-UUID
// wait-flip case is Thread C, covered by
// TestServerPowerResource_Update_WaitFlipWaitsRetainedTask.)
func TestServerPowerResource_Update_WaitOnlyPreservesPendingTaskID(t *testing.T) {
	powerCalled := false
	getTaskCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			powerCalled = true
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
			getTaskCalled = true
			t.Errorf("GetTask/WaitForTask must NOT be called for the sentinel marker")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Prior state: OFF, wait=false, with the indeterminate sentinel recorded (no
	// real task to poll).
	priorState := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "123"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "123"),
		"pending_task_id": tftypes.NewValue(tftypes.String, pendingTaskIDIndeterminate),
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
	if getTaskCalled {
		t.Error("WaitForTask should NOT be called for the sentinel marker, but it was")
	}

	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if !isIndeterminateMarker(result.PendingTaskID.ValueString()) {
		t.Errorf("PendingTaskID = %q, want sentinel (prior marker preserved on wait-only update)", result.PendingTaskID.ValueString())
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

// TestServerPowerResource_Create_WaitTrueFinishedStoresTaskUUID verifies Thread A
// (P1): on the default wait=true, when WaitForTask returns success (FINISHED),
// Create persists the FINISHED task's UUID in pending_task_id (NOT null). This
// hands the UUID to Read's terminal/FINISHED propagation-window logic so that if
// SCP reports the task FINISHED before the live state converges (e.g. POWERCYCLE
// FINISHED while GetServer still returns SHUTOFF), the next refresh does not record
// the wrong state and reboot again. A follow-up Read within the window keeps the
// desired state; a converged Read clears the marker.
func TestServerPowerResource_Create_WaitTrueFinishedStoresTaskUUID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/servers/990":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-fin-created","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-fin-created":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-fin-created","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "990"),
		"state":        tftypes.NewValue(tftypes.String, "ON"),
		"state_option": tftypes.NewValue(tftypes.String, "POWERCYCLE"),
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
	// Thread A: the FINISHED task's UUID is retained (not null) so Read governs
	// convergence via the propagation window.
	if pendingTaskUUID(state.PendingTaskID.ValueString()) != "task-fin-created" {
		t.Errorf("PendingTaskID = %q, want task-fin-created (finished UUID retained for Read convergence)", state.PendingTaskID.ValueString())
	}
	if state.State.ValueString() != "ON" {
		t.Errorf("State = %q, want ON", state.State.ValueString())
	}
}

// TestServerPowerResource_Create_SyncStoresNullMarker verifies the marker rule for
// a NEW command that completes SYNChronously (HTTP 200, task == nil): pending_task_id
// stays null, because there is no async task to track.
func TestServerPowerResource_Create_SyncStoresNullMarker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/servers/991" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK) // sync — no task
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "991"),
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
	if !state.PendingTaskID.IsNull() {
		t.Errorf("PendingTaskID = %q, want null (synchronous 200 has no async task)", state.PendingTaskID.ValueString())
	}
}

// TestServerPowerResource_Update_StateChangeStoresNewMarkerNotPrior verifies
// Thread B (P2): an Update that CHANGES state (issues a new command) and succeeds
// must persist the NEW task's marker (or null for a synchronous 200) — NEVER the
// obsolete prior pending_task_id. Restoring the old UUID would make future refreshes
// reconcile against the OLD operation and hide drift once the new op completed.
//
// Sub-case async: the new op returns a 202 FINISHED task ⇒ marker = the NEW UUID.
// Sub-case sync:  the new op returns a 200 ⇒ marker = null.
func TestServerPowerResource_Update_StateChangeStoresNewMarkerNotPrior(t *testing.T) {
	t.Run("async_new_uuid", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodPatch && r.URL.Path == "/v1/servers/992":
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"uuid":"task-new","state":"PENDING"}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-new":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"uuid":"task-new","state":"FINISHED"}`))
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer srv.Close()

		client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
		r, schemaResp := configureServerPowerResource(t, client)

		ctx := context.Background()
		// Prior: ON with an OBSOLETE prior task UUID; plan flips to OFF (real change).
		priorState := resourceState(schemaResp, map[string]tftypes.Value{
			"server_id":       tftypes.NewValue(tftypes.String, "992"),
			"state":           tftypes.NewValue(tftypes.String, "ON"),
			"state_option":    tftypes.NewValue(tftypes.String, nil),
			"wait":            tftypes.NewValue(tftypes.Bool, true),
			"id":              tftypes.NewValue(tftypes.String, "992"),
			"pending_task_id": tftypes.NewValue(tftypes.String, "task-OBSOLETE"),
		})
		plan := resourcePlan(schemaResp, map[string]tftypes.Value{
			"server_id":    tftypes.NewValue(tftypes.String, "992"),
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
		var state serverPowerResourceModel
		resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
		if pendingTaskUUID(state.PendingTaskID.ValueString()) != "task-new" {
			t.Errorf("PendingTaskID = %q, want task-new (NEW task UUID, not obsolete prior)", state.PendingTaskID.ValueString())
		}
		if pendingTaskUUID(state.PendingTaskID.ValueString()) == "task-OBSOLETE" {
			t.Error("PendingTaskID must NOT be the obsolete prior UUID after a new command")
		}
	})

	t.Run("sync_null", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch || r.URL.Path != "/v1/servers/993" {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			w.WriteHeader(http.StatusOK) // sync — no task
		}))
		defer srv.Close()

		client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
		r, schemaResp := configureServerPowerResource(t, client)

		ctx := context.Background()
		priorState := resourceState(schemaResp, map[string]tftypes.Value{
			"server_id":       tftypes.NewValue(tftypes.String, "993"),
			"state":           tftypes.NewValue(tftypes.String, "ON"),
			"state_option":    tftypes.NewValue(tftypes.String, nil),
			"wait":            tftypes.NewValue(tftypes.Bool, true),
			"id":              tftypes.NewValue(tftypes.String, "993"),
			"pending_task_id": tftypes.NewValue(tftypes.String, "task-OBSOLETE"),
		})
		plan := resourcePlan(schemaResp, map[string]tftypes.Value{
			"server_id":    tftypes.NewValue(tftypes.String, "993"),
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
		var state serverPowerResourceModel
		resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
		if !state.PendingTaskID.IsNull() {
			t.Errorf("PendingTaskID = %q, want null (synchronous 200 new command; not obsolete prior)", state.PendingTaskID.ValueString())
		}
	})
}

// TestServerPowerResource_Update_WaitFlipWaitsRetainedTask verifies Thread C (P2):
// when a prior op was submitted with wait=false and still has a REAL pending_task_id
// (a UUID, not the sentinel/null), changing ONLY `wait` to true must poll that
// retained task with WaitForTask (bounded) WITHOUT submitting another power command,
// so apply blocks until the tracked op is terminal — honoring wait=true's contract.
// On terminal success the UUID marker is retained (Thread A: Read reconciles via the
// window).
func TestServerPowerResource_Update_WaitFlipWaitsRetainedTask(t *testing.T) {
	powerCalled := false
	taskPolled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch:
			powerCalled = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-retained":
			taskPolled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-retained","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	// Prior: OFF, wait=false, with a REAL in-flight task UUID recorded.
	priorState := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "994"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "994"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-retained"),
	})
	// Plan: same state + state_option, only wait flipped to true.
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "994"),
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
		t.Error("SetPowerState (PATCH) must NOT be re-issued when only wait flips; the retained task is polled instead")
	}
	if !taskPolled {
		t.Error("WaitForTask SHOULD be called on the retained task UUID when wait flips false→true, but it was not")
	}

	var result serverPowerResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if result.PendingTaskID.ValueString() != "task-retained" {
		t.Errorf("PendingTaskID = %q, want task-retained (finished UUID retained for Read convergence)", result.PendingTaskID.ValueString())
	}
	if result.Wait.ValueBool() != true {
		t.Errorf("wait = %v, want true", result.Wait.ValueBool())
	}
}

// TestServerPowerResource_Update_WaitFlipRetainedTaskFailure verifies Thread C
// (P2): if the retained task polled on a wait false→true flip reaches a FAILURE
// terminal (*netcup.TaskError), Update surfaces an ERROR (like the command-issued
// failure path) and re-issues NO power command.
func TestServerPowerResource_Update_WaitFlipRetainedTaskFailure(t *testing.T) {
	powerCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch:
			powerCalled = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-retained-fail":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-retained-fail","state":"ERROR","message":"op failed"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerPowerResource(t, client)

	ctx := context.Background()
	priorState := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "995"),
		"state":           tftypes.NewValue(tftypes.String, "OFF"),
		"state_option":    tftypes.NewValue(tftypes.String, nil),
		"wait":            tftypes.NewValue(tftypes.Bool, false),
		"id":              tftypes.NewValue(tftypes.String, "995"),
		"pending_task_id": tftypes.NewValue(tftypes.String, "task-retained-fail"),
	})
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":    tftypes.NewValue(tftypes.String, "995"),
		"state":        tftypes.NewValue(tftypes.String, "OFF"),
		"state_option": tftypes.NewValue(tftypes.String, nil),
		"wait":         tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: priorState}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Update() expected an ERROR when the retained task fails terminally, got none")
	}
	if powerCalled {
		t.Error("SetPowerState (PATCH) must NOT be re-issued on a wait-only flip")
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
