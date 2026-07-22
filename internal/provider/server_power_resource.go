package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// defaultTaskTimeout (declared in rescue_resource.go, package-wide) also bounds
// how long Create/Update here poll an accepted power task via WaitForTask before
// giving up. Without it, the unbounded resource request context would let
// `terraform apply` (or unattended CI) hang forever if netcup leaves a task
// non-terminal. When the deadline fires, WaitForTask returns
// context.DeadlineExceeded — which is NOT a *netcup.TaskError, so
// classifyTaskWaitError treats it as INDETERMINATE: the desired state is
// persisted with a warning (no error, no re-issue of the possibly destructive
// power command).

// powerPropagationWindow bounds how long Read's terminal-task-success branch
// keeps retaining pending_task_id (and the desired state) when the post-FINISHED
// GetServer refetch STILL shows the operation's intermediate live state.
//
// Thread B (P1) fix: even after the ae9ca5e post-FINISHED refetch, SCP's
// live-state propagation can lag — a single fresh snapshot may still report an
// intermediate state (e.g. a POWERCYCLE whose refetch is still SHUTOFF before the
// server comes back RUNNING). Clearing the marker unconditionally then records the
// wrong state (OFF) and the next apply reboots again. So within this window we
// treat a still-mismatched live state as propagation lag and RETAIN the marker +
// keep the desired state; once time.Since(task.FinishedAt) exceeds it we treat the
// mismatch as GENUINE drift (e.g. an externally-stopped server) and reconcile from
// the live state. This mirrors rescue's bounded rescuePropagationWindow. It is a
// SEPARATE, power-named const (not a reuse of the rescue-named symbol) so each
// resource's window can evolve independently; 2 minutes comfortably covers real
// SCP propagation (seconds) while surfacing true drift promptly.
const powerPropagationWindow = 2 * time.Minute

// pendingMarkerFor returns the value to store in pending_task_id for an accepted
// (202) power task, so every UUID-recording site treats a missing UUID identically.
//
// Thread A (P1) fix: SetPowerState returns a non-nil *TaskInfo whenever the 202
// body decoded, even if it OMITS `uuid` (e.g. body `{}`) — leaving TaskInfo.UUID
// empty. Storing that empty string as the marker is unsafe: Read explicitly SKIPS
// empty/null markers (it only consults the task when the marker is non-empty), so a
// refresh during an OFF/POWERCYCLE transition would map the transient live state
// over the desired value via liveStateToDesiredPower and the next apply would
// re-issue the (possibly destructive) command. An accepted-but-untrackable task is
// exactly what the pendingTaskIDIndeterminate sentinel represents, so map a
// nil-or-empty-UUID task to the sentinel; otherwise record the real UUID.
func pendingMarkerFor(task *netcup.TaskInfo) string {
	if task == nil || task.UUID == "" {
		return pendingTaskIDIndeterminate
	}
	return task.UUID
}

var _ resource.Resource = &serverPowerResource{}

var _ resource.ResourceWithConfigure = &serverPowerResource{}

var _ resource.ResourceWithImportState = &serverPowerResource{}

// serverPowerResource manages the power state of a netcup server.
type serverPowerResource struct {
	client *netcup.Client
}

// serverPowerResourceModel mirrors the Terraform schema for netcup_server_power.
type serverPowerResourceModel struct {
	ServerID      types.String `tfsdk:"server_id"`
	State         types.String `tfsdk:"state"`
	StateOption   types.String `tfsdk:"state_option"`
	Wait          types.Bool   `tfsdk:"wait"`
	ID            types.String `tfsdk:"id"`
	PendingTaskID types.String `tfsdk:"pending_task_id"`
}

// NewServerPowerResource returns a new netcup_server_power resource factory.
func NewServerPowerResource() resource.Resource {
	return &serverPowerResource{}
}

func (r *serverPowerResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "netcup_server_power"
}

func (r *serverPowerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the power state of a netcup server.\n\n" +
			"WARNING: Setting state to OFF or SUSPENDED causes immediate downtime. " +
			"Destroying this resource is a no-op — it does NOT power the server off.",
		Attributes: map[string]schema.Attribute{
			"server_id": schema.StringAttribute{
				Required:    true,
				Description: "The numeric server ID. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"state": schema.StringAttribute{
				Required:    true,
				Description: "The desired power state: ON, OFF, or SUSPENDED. OFF and SUSPENDED cause downtime.",
				Validators: []validator.String{
					powerStateValidator{},
				},
			},
			"state_option": schema.StringAttribute{
				Optional:    true,
				Description: "Optional modifier passed as ?stateOption= to the SCP API (e.g. POWEROFF for a hard poweroff when state=OFF).",
			},
			"wait": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "When true (default), apply waits for the async task to reach a terminal state via WaitForTask before returning.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The server ID (same as server_id; used as the resource identifier for import).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"pending_task_id": schema.StringAttribute{
				Computed: true,
				Description: "The UUID of an in-flight asynchronous power task, used to " +
					"reconcile a pending power operation on refresh. When Create/Update " +
					"submits a power command but the task has not confirmed terminally " +
					"(wait=false, or a bounded/indeterminate wait), this holds the task " +
					"UUID so a refresh that reads a transient live state (e.g. RUNNING " +
					"while OFF is requested) consults the task before overwriting the " +
					"desired state. Set to the \"indeterminate\" sentinel when the accepted " +
					"response could not be decoded (no UUID available). Null once the task " +
					"is terminal.",
				PlanModifiers: []planmodifier.String{
					// Mirror power's `id` approach: plain UseStateForUnknown for plan
					// stability on in-place updates. Power intentionally has no
					// resource-level ModifyPlan (server_id RequiresReplace already
					// forces a full destroy/create with null prior state, where
					// UseStateForUnknown is a no-op), so nothing copies stale values
					// across a replacement.
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *serverPowerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*netcup.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *netcup.Client, got %T.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *serverPowerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_server_power.",
		)
		return
	}

	var plan serverPowerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.ParseInt(plan.ServerID.ValueString(), 10, 32)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid server_id",
			fmt.Sprintf("Cannot parse %q as a numeric server ID.", plan.ServerID.ValueString()),
		)
		return
	}

	state := netcup.PowerState(plan.State.ValueString())
	stateOption := plan.StateOption.ValueString()

	desired := serverPowerResourceModel{
		ServerID:      plan.ServerID,
		State:         plan.State,
		StateOption:   plan.StateOption,
		Wait:          plan.Wait,
		ID:            types.StringValue(plan.ServerID.ValueString()),
		PendingTaskID: types.StringNull(),
	}

	task, err := r.client.SetPowerState(ctx, int32(id), state, stateOption)
	if err != nil {
		handleSetPowerStateError(ctx, &resp.State, &resp.Diagnostics, err, &desired)
		return
	}

	// wait=false: the async task has not completed, so a read-back would reflect
	// the pre-op/intermediate live state (e.g. RUNNING while OFF was requested).
	// Persist the accepted task UUID alongside the desired state so a refresh that
	// observes a transient live state consults the task (GetTask) before mapping
	// it over the desired value and re-issuing the (possibly destructive) command.
	if !plan.Wait.ValueBool() {
		if task != nil {
			// Thread A (P1): a 202 whose body omits `uuid` yields a non-nil task with
			// an empty UUID; store the sentinel (not an empty marker Read would skip).
			desired.PendingTaskID = types.StringValue(pendingMarkerFor(task))
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &desired)...)
		return
	}

	if task != nil {
		// Bound task polling with a finite deadline so an apply/CI run can never
		// hang indefinitely if netcup leaves the task non-terminal. A hit deadline
		// surfaces as context.DeadlineExceeded (not a *TaskError) ⇒ INDETERMINATE.
		waitCtx, cancel := context.WithTimeout(ctx, defaultTaskTimeout)
		defer cancel()
		if _, err := r.client.WaitForTask(waitCtx, task.UUID); err != nil {
			if terminalDiag, indeterminateDiag, persist := classifyTaskWaitError(err, task.UUID); persist {
				// INDETERMINATE wait (context canceled/deadline, transport error):
				// SetPowerState was already accepted (202) and may have taken
				// effect. Persist the desired state + the accepted task UUID + a
				// warning so a later refresh consults the task before overwriting
				// the desired state, and a later apply does not blindly re-issue
				// the (possibly destructive) command.
				//
				// Thread A (P1): pendingMarkerFor stores the sentinel when task.UUID is
				// empty (a 202 body without `uuid`), so Read never skips an empty marker
				// and re-issues the command.
				desired.PendingTaskID = types.StringValue(pendingMarkerFor(task))
				persistDesiredStateWithWarning(ctx, &resp.State, &resp.Diagnostics, &desired, indeterminateDiag)
				return
			} else {
				// Confirmed terminal FAILURE: the operation definitively failed and
				// the server state was not changed, so retrying is safe. Return an
				// error without persisting desired state.
				resp.Diagnostics.Append(terminalDiag)
				return
			}
		}

		// wait=true SUCCESS (task FINISHED). Thread A (P1) fix: RETAIN the finished
		// task's UUID (pendingMarkerFor(task)) instead of nulling the marker. SCP can
		// report a task FINISHED before the live server state converges (e.g. a
		// POWERCYCLE FINISHED while GetServer still returns SHUTOFF); with a null
		// marker the next Read would record OFF and the following apply would reboot
		// again. Feeding the finished UUID to Read lets its terminal/FINISHED
		// propagation-window logic govern convergence: converged ⇒ clear the marker;
		// within the window ⇒ retain the desired state; past the window ⇒ reconcile
		// from live (genuine drift). The marker is cleared by Read once live state
		// catches up, so it does not linger.
		desired.PendingTaskID = types.StringValue(pendingMarkerFor(task))
		resp.Diagnostics.Append(resp.State.Set(ctx, &desired)...)
		return
	}

	// Synchronous 200 (task == nil): the command completed synchronously, so there
	// is no async task to track — pending_task_id stays null (set on `desired`
	// above).
	resp.Diagnostics.Append(resp.State.Set(ctx, &desired)...)
}

func (r *serverPowerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured.",
		)
		return
	}

	var state serverPowerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.ParseInt(state.ServerID.ValueString(), 10, 32)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid server_id in state",
			fmt.Sprintf("Cannot parse %q as a numeric server ID.", state.ServerID.ValueString()),
		)
		return
	}

	server, err := r.client.GetServer(ctx, int32(id))
	if err != nil {
		diag, gone := apiErrorToDiag(err, false)
		if gone {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(diag)
		return
	}

	// Thread B fix: reconcile a possibly-pending power task before mapping the live
	// state over the desired state.
	//
	// When Create/Update submitted a power command but could not confirm it
	// terminally (wait=false, or a bounded/indeterminate WaitForTask), it stored
	// the task UUID in pending_task_id. While that async task runs, the live
	// ServerState is transient and does NOT yet reflect the request (e.g. RUNNING
	// right after requesting OFF, or SHUTOFF mid-POWERCYCLE). Mapping that live
	// value over the desired `state` here would make the next plan re-issue the
	// destructive command. So: if a task is pending, consult it and KEEP the
	// desired `state` while it is still running; only reconcile from the live
	// state once the task is terminal / gone.
	mapLiveState := true
	if pending := state.PendingTaskID; !pending.IsNull() && !pending.IsUnknown() && pending.ValueString() != "" {
		uuid := pending.ValueString()

		// The pendingTaskIDIndeterminate sentinel is NOT a real UUID (Create stored
		// it because the accepted response could not be decoded — no UUID was
		// available). There is nothing to GetTask. RETAIN the desired state and the
		// sentinel until a later refresh observes the live state actually matching
		// the desired value, at which point the mapping below is a no-op and the
		// sentinel is cleared by the "live matches desired" check.
		//
		// Residual limitation (deliberate, mirrors rescue): with no UUID we cannot
		// query the task, so an indeterminate power op that genuinely never took
		// effect keeps the desired state until the live state converges or an
		// operator intervenes — safer than overwriting the desired state and
		// re-issuing a destructive command.
		if uuid == pendingTaskIDIndeterminate {
			mapLiveState = false
			// Clear the sentinel once the live state confirms the desired value.
			if server.ServerLiveInfo != nil {
				mapped := liveStateToDesiredPower(server.ServerLiveInfo.State)
				if mapped != "" && string(mapped) == state.State.ValueString() {
					state.PendingTaskID = types.StringNull()
				}
			}
		} else if task, terr := r.client.GetTask(ctx, uuid); terr != nil {
			// A 404/gone means the task record no longer exists: reconcile from the
			// live state and clear the pending marker.
			d, gone := apiErrorToDiag(terr, false)
			if gone {
				state.PendingTaskID = types.StringNull()
			} else {
				// Transient error: we cannot tell whether the op is still pending.
				// Surface a diagnostic, KEEP the desired state and the marker so the
				// next refresh retries.
				resp.Diagnostics.Append(d)
				mapLiveState = false
			}
		} else if !task.State.IsTerminal() {
			// The task is still running (PENDING/RUNNING/WAITING_FOR_CANCEL): the
			// power op is in flight and the live state is transient. KEEP the desired
			// state and retain the marker so the next refresh re-checks.
			mapLiveState = false
		} else {
			// Terminal task: the op finished (FINISHED) or failed
			// (ERROR/CANCELED/ROLLBACK).
			//
			// Thread B (P1) fix: REFETCH the live server before reconciling. The
			// `server` snapshot above was taken BEFORE GetTask; if the task
			// transitioned to terminal in the window between those two calls, that
			// snapshot is a stale PRE-completion state (e.g. SHUTOFF captured
			// mid-POWERCYCLE even though the task has since restored RUNNING).
			// Reconciling from it would record the wrong power state and make the
			// next apply re-issue the command. Take a fresh POST-completion snapshot
			// and reconcile from THAT.
			//
			// If the refetch fails we KEEP the marker and surface a diagnostic so the
			// next refresh retries, rather than reconcile from a stale snapshot.
			fresh, ferr := r.client.GetServer(ctx, int32(id))
			if ferr != nil {
				d, gone := apiErrorToDiag(ferr, false)
				if gone {
					// Server disappeared post-completion: remove the resource.
					resp.State.RemoveResource(ctx)
					return
				}
				resp.Diagnostics.Append(d)
				mapLiveState = false
			} else if task.State == netcup.TaskStateFinished {
				// SUCCESS terminal. Thread B (P1) refinement: the task is FINISHED and
				// we have a fresh snapshot, but SCP live-state propagation can still LAG
				// — the single refetch may STILL report the operation's intermediate
				// state (e.g. a POWERCYCLE whose refetch is still SHUTOFF before the
				// server comes back RUNNING). Clearing the marker unconditionally then
				// records the wrong state (OFF) and the next apply reboots again.
				//
				// So only CLEAR the marker + reconcile if the fresh live state CONFIRMS
				// the desired `state` (converged). If it does NOT yet match, this is
				// either propagation lag or genuine drift — bound the ambiguity by the
				// task's own FinishedAt: within powerPropagationWindow, treat it as lag
				// and RETAIN the marker + KEEP the desired state (map nothing); once
				// the window elapses (or FinishedAt is absent), treat the mismatch as
				// GENUINE drift and reconcile from the live state (so a real
				// externally-stopped server still surfaces), clearing the marker.
				// Mirrors rescue's bounded post-terminal propagation handling.
				server = fresh
				mapped := ""
				if fresh.ServerLiveInfo != nil {
					mapped = string(liveStateToDesiredPower(fresh.ServerLiveInfo.State))
				}
				converged := mapped != "" && mapped == state.State.ValueString()
				withinWindow := task.FinishedAt != nil && time.Since(*task.FinishedAt) <= powerPropagationWindow
				if converged || !withinWindow {
					// Converged (live matches desired) or past the propagation window
					// (treat mismatch as genuine drift): clear the marker and reconcile
					// from the fresh live state below.
					state.PendingTaskID = types.StringNull()
				} else {
					// Still mismatched but within the window: propagation is in flight.
					// Retain the marker and keep the desired state (do not map).
					mapLiveState = false
				}
			} else {
				// FAILURE terminal (ERROR/CANCELED/ROLLBACK).
				//
				// Thread B (P2) fix: EXCLUDE failed tasks from the propagation window.
				// The b50a937 window logic exists to absorb SCP live-state propagation
				// LAG after a SUCCESSFUL operation — but a task that reached a failure
				// terminal did NOT succeed, so a still-mismatched live state is not lag,
				// it is the definitive outcome (e.g. a failed power-on leaving the
				// server SHUTOFF while desired is ON). Applying the window here would
				// RETAIN the desired state for up to powerPropagationWindow (2m), so
				// `terraform refresh`/plan would report NO changes despite the known
				// failure. Instead IMMEDIATELY clear the marker and reconcile from the
				// (refetched) live state — with no window — so the drift surfaces right
				// away for a corrective apply.
				server = fresh
				state.PendingTaskID = types.StringNull()
			}
		}
	}

	// Map the live ServerState to the desired PowerState equivalent.
	// Unknown or transitional states are treated as matching the current desired
	// state to avoid spurious diffs: we preserve whatever `state` is already in
	// Terraform state rather than forcing a re-apply.
	if mapLiveState && server.ServerLiveInfo != nil {
		mapped := liveStateToDesiredPower(server.ServerLiveInfo.State)
		if mapped != "" {
			state.State = types.StringValue(string(mapped))
		}
		// If mapped == "" the server is in a transitional state; leave
		// state.State unchanged to prevent spurious plan diffs.
	}

	state.ID = types.StringValue(state.ServerID.ValueString())

	// Normalize a null/unknown `wait` to the schema default (true). ImportState
	// only sets id + server_id, leaving `wait` null; without this, the null value
	// is written straight back to state and — because the schema proposes the
	// default `wait = true` whenever config omits it — the first plan after an
	// otherwise-successful import contains a spurious wait-only update instead of
	// being empty. Coercing null/unknown to the default here makes imported state
	// match normal resource state so the post-import plan is clean.
	if state.Wait.IsNull() || state.Wait.IsUnknown() {
		state.Wait = types.BoolValue(true)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serverPowerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured.",
		)
		return
	}

	var plan serverPowerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Read prior state so we can detect wait-only changes.
	var prior serverPowerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.ParseInt(plan.ServerID.ValueString(), 10, 32)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid server_id",
			fmt.Sprintf("Cannot parse %q as a numeric server ID.", plan.ServerID.ValueString()),
		)
		return
	}

	// Only issue a power command when state or state_option actually changed.
	// Changing only `wait` (which controls task-polling behaviour) must NOT
	// reissue the power operation — doing so would cause unexpected downtime
	// for destructive state_options like RESET or POWERCYCLE.
	stateChanged := plan.State.ValueString() != prior.State.ValueString()
	optionChanged := plan.StateOption.ValueString() != prior.StateOption.ValueString()

	desired := serverPowerResourceModel{
		ServerID:      plan.ServerID,
		State:         plan.State,
		StateOption:   plan.StateOption,
		Wait:          plan.Wait,
		ID:            types.StringValue(plan.ServerID.ValueString()),
		PendingTaskID: types.StringNull(),
	}

	if stateChanged || optionChanged {
		powerState := netcup.PowerState(plan.State.ValueString())
		stateOption := plan.StateOption.ValueString()

		task, err := r.client.SetPowerState(ctx, int32(id), powerState, stateOption)
		if err != nil {
			handleSetPowerStateError(ctx, &resp.State, &resp.Diagnostics, err, &desired)
			return
		}

		// wait=false: the async task has not completed. Persist the accepted task
		// UUID alongside the NEW desired state so a refresh that observes a
		// transient live state consults the task before overwriting the desired
		// value and re-issuing the (possibly destructive) command.
		if !plan.Wait.ValueBool() {
			if task != nil {
				// Thread A (P1): a 202 whose body omits `uuid` yields a non-nil task
				// with an empty UUID; store the sentinel (not an empty marker Read skips).
				desired.PendingTaskID = types.StringValue(pendingMarkerFor(task))
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &desired)...)
			return
		}

		if task != nil {
			// Bound task polling with a finite deadline (see defaultTaskTimeout)
			// so an apply/CI run can never hang if netcup leaves the task
			// non-terminal. A hit deadline is INDETERMINATE, not a *TaskError.
			waitCtx, cancel := context.WithTimeout(ctx, defaultTaskTimeout)
			defer cancel()
			if _, err := r.client.WaitForTask(waitCtx, task.UUID); err != nil {
				if terminalDiag, indeterminateDiag, persist := classifyTaskWaitError(err, task.UUID); persist {
					// INDETERMINATE wait: the new power command was accepted (202)
					// and may have taken effect. Persist the NEW desired state + the
					// accepted task UUID + a warning so a later refresh consults the
					// task and a later apply does not re-issue the command. (The old
					// behaviour retained prior state, which caused a re-issue.)
					//
					// Thread A (P1): pendingMarkerFor stores the sentinel when task.UUID
					// is empty (202 body without `uuid`), so Read never skips an empty
					// marker and re-issues the command.
					desired.PendingTaskID = types.StringValue(pendingMarkerFor(task))
					persistDesiredStateWithWarning(ctx, &resp.State, &resp.Diagnostics, &desired, indeterminateDiag)
					return
				} else {
					// Confirmed terminal FAILURE: the operation failed and the
					// server state was not changed; return an error without
					// persisting the new desired state.
					resp.Diagnostics.Append(terminalDiag)
					return
				}
			}

			// wait=true SUCCESS (new task FINISHED). Thread A (P1): RETAIN the NEW
			// finished task's UUID so Read's propagation-window logic governs
			// convergence (see Create). Thread B (P2): this command-issued path sets
			// its own marker and RETURNS — it must NOT fall through to the prior-marker
			// restore tail below, which would resurrect the OBSOLETE prior task ID and
			// suppress live-state mapping against the new operation.
			desired.PendingTaskID = types.StringValue(pendingMarkerFor(task))
			resp.Diagnostics.Append(resp.State.Set(ctx, &desired)...)
			return
		}

		// New command completed SYNChronously (HTTP 200, task == nil): no async task
		// to track ⇒ marker stays null (set on `desired` above). Thread B (P2): this
		// too is a command-issued path and must RETURN rather than fall through to the
		// prior-marker restore tail (which would resurrect an obsolete prior UUID).
		resp.Diagnostics.Append(resp.State.Set(ctx, &desired)...)
		return
	}

	// NO new command was issued (wait-only / no-op update: state and state_option
	// both unchanged). This is the ONLY path that restores the prior marker.
	//
	// Thread B (P2): every path that issued a new command sets its own marker per the
	// unified rule (new task UUID / sentinel / null-for-sync) and returns ABOVE, so
	// we never resurrect an obsolete prior UUID after a fresh operation.
	//
	// Thread C (P2): if `wait` is now true and the retained prior marker is a REAL
	// task UUID (not the sentinel, not empty/null), the prior op was submitted with
	// wait=false and may still be running. Flipping wait→true documents that apply
	// must block until the operation is terminal, so poll the RETAINED task with the
	// same bounded (defaultTaskTimeout) + classifyTaskWaitError logic the command
	// paths use — WITHOUT issuing another power command. On success ⇒ keep the UUID
	// marker (Thread A: Read reconciles via the window); on terminal failure ⇒ error;
	// on indeterminate ⇒ retain the marker + warning. The sentinel and null markers
	// have no real task to poll, so they are simply preserved below.
	priorMarker := prior.PendingTaskID
	priorUUID := priorMarker.ValueString()
	shouldWaitRetained := plan.Wait.ValueBool() &&
		!priorMarker.IsNull() && !priorMarker.IsUnknown() &&
		priorUUID != "" && priorUUID != pendingTaskIDIndeterminate

	if shouldWaitRetained {
		// A real retained task UUID + wait flipped to true: wait on it without
		// re-issuing the command.
		waitCtx, cancel := context.WithTimeout(ctx, defaultTaskTimeout)
		defer cancel()
		if _, err := r.client.WaitForTask(waitCtx, priorUUID); err != nil {
			if terminalDiag, indeterminateDiag, persist := classifyTaskWaitError(err, priorUUID); persist {
				// INDETERMINATE wait on the retained task: retain the marker + warn,
				// preserving the NEW wait value. A later refresh/apply reconciles.
				desired.PendingTaskID = priorMarker
				persistDesiredStateWithWarning(ctx, &resp.State, &resp.Diagnostics, &desired, indeterminateDiag)
				return
			} else {
				// Confirmed terminal FAILURE of the retained op: surface the error
				// without persisting (mirrors the command-issued failure path). No new
				// command was issued, so this Update changed nothing.
				resp.Diagnostics.Append(terminalDiag)
				return
			}
		}

		// Retained task FINISHED: keep its UUID marker (Thread A) so Read reconciles
		// convergence via the propagation window, and persist the new wait value.
		desired.PendingTaskID = priorMarker
		resp.Diagnostics.Append(resp.State.Set(ctx, &desired)...)
		return
	}

	// No-command path with nothing to poll (wait unchanged/false, or the marker is
	// the sentinel/null): PRESERVE the prior marker. `desired` was initialized with
	// pending_task_id = null, but a prior op submitted with wait=false (or an
	// indeterminate wait) may still be in flight with its task UUID/sentinel in
	// prior.PendingTaskID. Nulling it would make the next refresh stop consulting
	// that task and map a pre-op transient live state over the desired value — the
	// next apply would then re-issue the destructive command.
	desired.PendingTaskID = prior.PendingTaskID
	resp.Diagnostics.Append(resp.State.Set(ctx, &desired)...)
}

// Delete is intentionally a no-op: destroying this resource should never
// power off the server. It only removes the resource from Terraform state.
//
// WARNING: If you need to power off the server, set state = "OFF" before
// running terraform destroy, or use netcup_server_power with state = "OFF"
// explicitly.
func (r *serverPowerResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// No-op: state-only removal. The server is NOT powered off.
}

func (r *serverPowerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Validate that the import ID is a numeric server ID.
	if _, err := strconv.ParseInt(req.ID, 10, 32); err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("The import ID must be a numeric server ID; got %q.", req.ID),
		)
		return
	}

	// Set both `id` (the computed Terraform resource identifier) and
	// `server_id` (the required attribute) from the import ID so that the
	// subsequent Read refresh can parse server_id without encountering an empty
	// string. Without this, ImportStatePassthroughID would only populate `id`,
	// leaving server_id null and causing ParseInt to fail in Read.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), req.ID)...)
}

// handleSetPowerStateError classifies a non-nil error returned by SetPowerState
// (the accepting PATCH) into a DEFINITIVE rejection vs an INDETERMINATE failure,
// mirroring the terminal-vs-indeterminate philosophy classifyTaskWaitError
// applies to WaitForTask.
//
//   - DEFINITIVE rejection — a *netcup.APIError with a 4xx status (e.g. 400 invalid
//     state_option, 404 unknown server, 401/403 auth), a documented 503
//     MAINTENANCE response, OR a PRE-DISPATCH failure (netcup.ErrPreDispatch: the
//     token could not be refreshed / the request could not be constructed, so
//     httpClient.Do was never reached): the request was understood and refused, or
//     never left the client at all — no task was created, and the server state was
//     NOT changed, so a retry is safe. Surface an ERROR and persist NO state (so
//     Terraform retries once the token/maintenance issue clears).
//
//   - INDETERMINATE — an AFTER-dispatch transport error (a *url.Error from
//     httpClient.Do mid-flight), a response-body decode error (a truncated 202), or
//     an unexpected/undocumented 5xx: the PATCH may have been accepted server-side
//     and an async power task may already be running even though no usable
//     TaskInfo/UUID came back. Erroring-without-state here would let the next apply
//     re-issue the command — for RESET/POWERCYCLE, rebooting the server twice.
//     Instead persist the desired state + the pendingTaskIDIndeterminate sentinel
//     (no UUID is available to track the real task) + a WARNING, so a later
//     refresh/apply reconciles via Read's pending-task path instead of re-issuing.
//
// Thread A (P1) refinement: a PRE-DISPATCH failure (netcup.ErrPreDispatch) is
// DEFINITIVE, not indeterminate. SetPowerState builds the request in newRequest —
// which consults the TokenSource and constructs the *http.Request — BEFORE it ever
// calls httpClient.Do. If the refresh token cannot be refreshed (or the request
// cannot be built), newRequest returns an error wrapping netcup.ErrPreDispatch and
// SetPowerState returns it WITHOUT dispatching: the power PATCH was definitively
// never submitted, so no task exists and the server state is unchanged. Treating
// that as indeterminate would persist the desired state + sentinel, and Read would
// then preserve the desired value indefinitely (false convergence — Terraform
// never retries even after auth recovers). isDefinitivePowerRejection matches
// ErrPreDispatch via errors.Is so these fail cleanly with an ERROR and no state.
// The discriminator is reliable because the sentinel is added ONLY on newRequest's
// pre-dispatch paths; the after-dispatch transport *url.Error and decode errors are
// returned raw (unwrapped), so they stay indeterminate.
//
// Thread C (P1) refinement: a documented 503 MAINTENANCE response is ALSO a
// DEFINITIVE rejection, not indeterminate. docs/SCP-API-NOTES.md documents the
// PATCH /v1/servers/{id} responses as exactly `202 TaskInfo` (accepted async),
// `200` (accepted sync), or `503 ResponseError` (node in maintenance). A 503
// maintenance response means the request was EXPLICITLY refused and NO task was
// created — treating it as indeterminate would persist the desired state + the
// sentinel, and Read would then preserve that desired value indefinitely (false
// convergence: Terraform never retries once maintenance ends). So power uses
// isDefinitivePowerRejection, which extends the shared 4xx test with the
// endpoint-specific 503-maintenance case. Genuinely ambiguous failures
// (transport errors, truncated/undecodable 202, other/unexpected 5xx) remain
// indeterminate and fall through to the sentinel path below.
func handleSetPowerStateError(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics, err error, desired *serverPowerResourceModel) {
	if isDefinitivePowerRejection(err) {
		d, _ := apiErrorToDiag(err, true)
		diags.Append(d)
		return
	}

	desired.PendingTaskID = types.StringValue(pendingTaskIDIndeterminate)
	diags.Append(state.Set(ctx, desired)...)
	diags.AddWarning(
		"netcup power operation acceptance could not be confirmed",
		fmt.Sprintf(
			"Submitting the power operation to netcup returned an indeterminate error (%s): a "+
				"transport error, a truncated/undecodable accepted (202) response, or a 5xx. The "+
				"operation may already have been accepted and an async task may be running, but no "+
				"task UUID was returned to track it.\n\n"+
				"The desired state has been recorded in Terraform state (with a pending-task "+
				"sentinel) to avoid re-issuing the (possibly destructive) power command on the next "+
				"apply. The next `terraform refresh` or apply reconciles the actual power state once "+
				"the live server state converges to the desired value.",
			err.Error(),
		),
	)
}

// isDefinitivePowerRejection reports whether a SetPowerState error is a
// DEFINITIVE rejection by the SCP API — one where the request was understood and
// refused, no async task was created, and the server state was NOT changed (so a
// retry is safe and the caller must persist NO state).
//
// It treats three cases as definitive:
//
//   - a *netcup.APIError with a 4xx status (shared with rescue's generic
//     isDefinitiveEnableRejection: 400 invalid state_option, 404 unknown server,
//     401/403 auth),
//
//   - Thread A (P1): a PRE-DISPATCH failure (netcup.ErrPreDispatch). SetPowerState
//     builds the request — consulting the TokenSource and constructing the
//     *http.Request in newRequest — BEFORE calling httpClient.Do. A token-refresh
//     failure or a request-construction failure surfaces wrapped in ErrPreDispatch
//     and the power PATCH is never dispatched, so no task was created and the
//     server state is unchanged: a retry is safe and NO state must be persisted.
//     This is a typed/sentinel signal (errors.Is), reliable because newRequest adds
//     it ONLY on its pre-dispatch paths — an after-dispatch transport *url.Error or
//     decode error is returned unwrapped and therefore stays indeterminate, AND
//
//   - Thread C (P1): a documented 503 MAINTENANCE response. For this endpoint the
//     spec documents 503 ONLY as `ResponseError` with the node in maintenance
//     (docs/SCP-API-NOTES.md, "Power state — PATCH /v1/servers/{id}"): the request
//     was explicitly rejected and no task was created. We match status 503 AND a
//     MAINTENANCE marker in the response body/code so that a hypothetical
//     UNDOCUMENTED/ambiguous 503 (with a different body) still falls through to the
//     indeterminate path; per the docs, a real maintenance rejection always carries
//     the MAINTENANCE signal.
//
// Everything else stays INDETERMINATE (transport error, truncated/undecodable
// 202 decode error, other/unexpected 5xx): the op may have been accepted
// server-side, so the caller persists the desired state + sentinel + warning.
func isDefinitivePowerRejection(err error) bool {
	// Thread A (P1): a pre-dispatch failure (token refresh / request construction)
	// means the power PATCH was never sent — definitive, safe to retry, no state.
	if errors.Is(err, netcup.ErrPreDispatch) {
		return true
	}
	if isDefinitiveEnableRejection(err) {
		return true
	}
	var apiErr *netcup.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	// Documented maintenance rejection: 503 whose ResponseError code/message
	// indicates maintenance. Match case-insensitively on the body (which
	// APIError captures verbatim, including the {"code":"MAINTENANCE",...} JSON).
	return strings.Contains(strings.ToUpper(apiErr.Body), "MAINTENANCE")
}

// classifyTaskWaitError classifies a non-nil error returned by WaitForTask
// (after SetPowerState has already returned 202, i.e. the task was accepted)
// into one of two categories, so Create and Update stay consistent:
//
//   - Confirmed terminal FAILURE (persist == false): WaitForTask returned a
//     *netcup.TaskError, meaning the task reached a failure terminal state
//     (ERROR/CANCELED/ROLLBACK). The power operation definitively failed and the
//     server state was not changed, so retrying is safe. terminalDiag is an
//     ERROR diagnostic the caller should append (without persisting state).
//
//   - INDETERMINATE (persist == true): any non-*TaskError (ctx.Err() from a
//     canceled apply or exceeded deadline, a transport error, etc.). The task
//     was ACCEPTED and may have taken effect, but its completion could not be
//     confirmed. indeterminateDiag is a WARNING diagnostic; the caller must
//     PERSIST the desired state (so a later apply sees no diff and does not
//     re-issue a possibly destructive command) and append the warning instead
//     of erroring — because netcup_server_power's Delete is a no-op, an errored
//     Create that persists partial state would taint the resource and trigger
//     destroy+recreate, re-issuing SetPowerState, which is exactly what we must
//     avoid.
func classifyTaskWaitError(err error, taskUUID string) (terminalDiag diag.Diagnostic, indeterminateDiag diag.Diagnostic, persist bool) {
	var taskErr *netcup.TaskError
	if errors.As(err, &taskErr) {
		// Confirmed terminal failure — safe to error and let a retry re-issue.
		d, _ := apiErrorToDiag(err, true)
		return d, nil, false
	}

	// Indeterminate — the task was accepted and may have completed. Do not error
	// (that would taint → recreate → re-issue); persist desired state + warn.
	warning := diag.NewWarningDiagnostic(
		"netcup power task acceptance could not be confirmed",
		fmt.Sprintf(
			"The power operation was accepted by netcup (task %s), but waiting for it to reach a "+
				"terminal state was interrupted (%s) — e.g. the apply was canceled or the bounded "+
				"task-polling deadline was exceeded. Canceling the wait does NOT cancel the remote "+
				"task, so the operation may still have taken effect or may still be running.\n\n"+
				"The desired state has been recorded in Terraform state to avoid re-issuing the "+
				"(possibly destructive) power command on the next apply. The next `terraform refresh` "+
				"or apply will reconcile the actual power state (Read maps the live server state back "+
				"to the desired state).",
			taskUUID, err.Error(),
		),
	)
	return nil, warning, true
}

// persistDesiredStateWithWarning writes the full desired model to Terraform
// state and appends the given (warning) diagnostic. It is shared by Create and
// Update so the indeterminate-wait handling stays identical in both.
func persistDesiredStateWithWarning(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics, desired *serverPowerResourceModel, warning diag.Diagnostic) {
	diags.Append(state.Set(ctx, desired)...)
	diags.Append(warning)
}

// liveStateToDesiredPower maps a live ServerInfo.State value (as returned by
// the SCP GET /v1/servers/{id} endpoint) to a desired PowerState suitable for
// storing in Terraform state. Returns an empty string only for genuinely
// short-lived transitional states, indicating the caller should leave the
// desired state unchanged to avoid spurious plan diffs during transitions.
//
// Persistent non-running states are always mapped to a concrete PowerState so
// that Terraform detects drift and proposes a corrective apply:
//
//   - RUNNING     → ON
//   - SHUTOFF     → OFF
//   - CRASHED     → OFF  (guest crashed; persistent non-running; surfaces drift)
//   - SUSPENDED   → SUSPENDED
//   - PAUSED      → SUSPENDED (hypervisor-paused; effectively suspended)
//   - PMSUSPENDED → SUSPENDED (guest PM suspend; persistent; surfaces drift for
//     configs targeting ON — mapping to SUSPENDED rather than OFF
//     because the guest is still in memory, analogous to SUSPENDED)
//
// Genuinely short-lived transitions preserve the prior desired state (return ""):
//
//   - SHUTDOWN     → "" (ACPI shutdown in progress; resolves to SHUTOFF within seconds)
//   - SAVE_RESTORE → "" (live-migration / snapshot save-restore in flight)
//   - unknown      → "" (future state names; avoid incorrect mapping)
func liveStateToDesiredPower(liveState string) netcup.PowerState {
	switch strings.ToUpper(liveState) {
	case "RUNNING":
		return netcup.PowerOn
	case "SHUTOFF", "CRASHED":
		// SHUTOFF is the normal off state.
		// CRASHED is a persistent failure state (guest domain crashed); surface
		// as OFF so Terraform detects drift for any config targeting ON or SUSPENDED.
		return netcup.PowerOff
	case "SUSPENDED", "PAUSED", "PMSUSPENDED":
		// SUSPENDED: normal hypervisor suspend.
		// PAUSED: hypervisor-level pause (effectively suspended).
		// PMSUSPENDED: guest triggered PM suspend (suspend-to-RAM); persistent,
		// mapped to SUSPENDED (not OFF) because the guest is still in memory.
		return netcup.PowerSuspended
	case "SHUTDOWN", "SAVE_RESTORE":
		// Genuinely short-lived transitions: ACPI shutdown in progress or
		// live-migration/save-restore in flight. Return "" to preserve the
		// prior desired state and avoid plan thrash during these brief windows.
		return ""
	default:
		// Unknown future state: preserve current desired state to avoid
		// mapping to an incorrect PowerState.
		return ""
	}
}

// powerStateValidator restricts the state attribute to ON, OFF, or SUSPENDED.
type powerStateValidator struct{}

func (v powerStateValidator) Description(_ context.Context) string {
	return "Requires state to be one of: ON, OFF, SUSPENDED."
}

func (v powerStateValidator) MarkdownDescription(_ context.Context) string {
	return "Requires `state` to be one of: `ON`, `OFF`, `SUSPENDED`."
}

func (v powerStateValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	val := req.ConfigValue.ValueString()
	switch netcup.PowerState(val) {
	case netcup.PowerOn, netcup.PowerOff, netcup.PowerSuspended:
		// valid
	default:
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid power state",
			fmt.Sprintf("state must be one of ON, OFF, or SUSPENDED; got %q.", val),
		)
	}
}
