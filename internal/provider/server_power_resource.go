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
		return newIndeterminateMarker()
	}
	return task.UUID
}

// indeterminateMarkerSep separates the pendingTaskIDIndeterminate stem from an
// embedded first-seen timestamp (UnixNano) in the power resource's sentinel marker,
// e.g. "indeterminate@1721731200000000000".
const indeterminateMarkerSep = "@"

// newIndeterminateMarker builds a power-resource indeterminate sentinel that embeds
// the current time as a first-seen timestamp (thread r3635966368). A same-state
// RESET/POWERCYCLE sentinel can never self-clear on live-equality (a false pre-op
// RUNNING would drop a possibly-unfinished reboot), so without a bound it would be
// retained forever — hiding a LATER external shutdown indefinitely. Embedding the
// first-seen time lets Read bound retention to the risky-transition window
// (powerPropagationWindow) and then reconcile from live so eventual drift surfaces.
// It reuses the single pending_task_id string (no new schema attribute); the shared
// bare pendingTaskIDIndeterminate stem keeps rescue's marker semantics untouched.
func newIndeterminateMarker() string {
	return pendingTaskIDIndeterminate + indeterminateMarkerSep + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// isIndeterminateMarker reports whether a pending_task_id value is an indeterminate
// sentinel — either the bare pendingTaskIDIndeterminate stem (legacy state written
// before timestamps, or a rescue-style marker) or a timestamped
// "indeterminate@<unixnano>" produced by newIndeterminateMarker.
func isIndeterminateMarker(s string) bool {
	return s == pendingTaskIDIndeterminate || strings.HasPrefix(s, pendingTaskIDIndeterminate+indeterminateMarkerSep)
}

// indeterminateMarkerTime extracts the embedded first-seen timestamp from a
// timestamped indeterminate marker. It returns ok=false for a bare (timestamp-less)
// sentinel or an unparseable suffix, so callers fall back to the original unbounded
// retain for such markers.
func indeterminateMarkerTime(s string) (time.Time, bool) {
	prefix := pendingTaskIDIndeterminate + indeterminateMarkerSep
	if !strings.HasPrefix(s, prefix) {
		return time.Time{}, false
	}
	nanos, err := strconv.ParseInt(s[len(prefix):], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, nanos), true
}

// isSameStatePowerOp reports whether the (state, state_option) pair describes a
// SAME-STATE power operation: one whose desired power state is identical BEFORE
// and AFTER the op, so the server is expected to be in the same power state at
// rest on both sides while transiting through an intermediate state mid-flight.
//
// Per pkg/netcup/power.go (SetPowerState) the SCP API documents state_option per
// target state: ON accepts POWERCYCLE and RESET, OFF accepts POWEROFF, and
// SUSPENDED accepts none. RESET and POWERCYCLE are REBOOTS applied to an already-
// ON server: the desired `state` is ON, and the server is ON both before and
// after, transiting ON → SHUTOFF → ON during the reboot. POWEROFF (state=OFF) is
// NOT same-state — it moves the server from ON to OFF, so its post-op power state
// differs from its pre-op one and live==desired equality genuinely proves it.
//
// Threads A & B (P1/P2) hinge on this: for a same-state op,
// liveStateToDesiredPower(server) == desired (RUNNING == ON) does NOT prove the
// op converged — the server may be ON merely because the reboot has not started
// yet, or has already completed. Only the TASK reaching a terminal state is
// authoritative. Callers use this to gate the convergence/clear/force-drift
// decisions so a same-state reboot is never treated as converged on live equality
// alone.
//
// Matching is case-insensitive on both fields to mirror the validator/mapping
// helpers (which upper-case before comparing), so `reset`/`Reset` are recognized.
func isSameStatePowerOp(state, stateOption string) bool {
	if !strings.EqualFold(strings.TrimSpace(state), string(netcup.PowerOn)) {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(stateOption)) {
	case "RESET", "POWERCYCLE":
		return true
	default:
		return false
	}
}

var _ resource.Resource = &serverPowerResource{}

var _ resource.ResourceWithConfigure = &serverPowerResource{}

var _ resource.ResourceWithImportState = &serverPowerResource{}

var _ resource.ResourceWithModifyPlan = &serverPowerResource{}

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
					// Thread A (P1): stock UseStateForUnknown for plan stability on
					// in-place updates; the resource-level ModifyPlan below forces this
					// unknown on any planned server_id replacement so the new server_id
					// becomes the new id (rather than the old id being copied in).
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
					// Thread A (P1): stock UseStateForUnknown for plan stability on
					// in-place updates; the resource-level ModifyPlan below forces this
					// unknown on any planned server_id replacement so pending_task_id is
					// recomputed for the new server rather than copied from prior state.
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

// ModifyPlan forces the UseStateForUnknown computed attributes (id,
// pending_task_id) to UNKNOWN ("known after apply") whenever a replacement is
// planned (the target server_id changes or is unknown), so the destroy/create
// cycle recomputes them for the (re)created power resource instead of copying
// stale prior-state values into the plan.
//
// Thread A (P1) fix — mirrors rescueResource.ModifyPlan:
//
// server_id has RequiresReplace, so changing it plans a destroy+create. But
// Terraform computes that plan while the PRIOR state is still available, and
// stock stringplanmodifier.UseStateForUnknown() copies the prior computed `id`
// (the OLD server's id) into the replacement plan. Create then returns the NEW
// server's id, so Terraform rejects the apply with "Provider produced
// inconsistent result after apply" — AFTER the disruptive power op already ran.
// (The earlier schema comment claiming a replacement re-plans with null prior
// state — making UseStateForUnknown a no-op — was wrong for the server_id-CHANGE
// case: the prior state is present when the combined plan is computed.)
//
// This resource-level ModifyPlan fixes the server_id-CHANGE case authoritatively
// (no heuristic — it compares the two server_ids and forces the computed values
// unknown when they differ or the planned server_id is unknown). A FORCED replace
// with an UNCHANGED server_id (`-replace=ADDRESS`) is already correct: Terraform
// re-plans the create half with a NULL prior state, where stock
// UseStateForUnknown does nothing and the framework marks the computed nils
// unknown, so Create fills real values and the result is consistent. We
// deliberately do NOT force-unknown on a normal in-place update (server_id
// unchanged and known) so a wait-only change keeps a stable plan.
func (r *serverPowerResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Nothing to do on create (no prior state) or destroy (null plan).
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	var stateServerID types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("server_id"), &stateServerID)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var planServerID types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("server_id"), &planServerID)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A replacement is planned when the target server changes. An unknown planned
	// server_id (derived from another resource's not-yet-known output) is also a
	// potential replacement whose target cannot be proven to match — treat it as a
	// replacement so the computed values are recomputed rather than copied. A
	// null/unknown prior-state server_id likewise cannot be proven equal.
	sameServer := !planServerID.IsNull() && !planServerID.IsUnknown() &&
		!stateServerID.IsNull() && !stateServerID.IsUnknown() &&
		stateServerID.ValueString() == planServerID.ValueString()
	if sameServer {
		// In-place update (server_id unchanged and known). id stays stable (id ==
		// server_id, which is unchanged), so stock UseStateForUnknown correctly copies
		// it forward.
		//
		// pending_task_id, however, is NOT stable when the power command itself is
		// changing. If state or state_option differs from prior state, Update submits a
		// new power command and writes a freshly-computed pending_task_id (a new task
		// UUID, the indeterminate sentinel, or null). Stock UseStateForUnknown has
		// already copied the PRIOR pending_task_id — including null — into the plan, so
		// for the normal asynchronous 202 the applied value differs from the planned one
		// and Terraform rejects the apply with "Provider produced inconsistent result
		// after apply" — AFTER the disruptive command has already run. Force
		// pending_task_id UNKNOWN whenever an in-place update will issue a new command so
		// the recomputed value is accepted. A wait-only / no-op update (state and
		// state_option unchanged) issues no command and preserves the prior marker, so
		// leaving UseStateForUnknown's stable copy in place keeps that plan clean.
		var stateState, planState types.String
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("state"), &stateState)...)
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("state"), &planState)...)
		var stateOption, planOption types.String
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("state_option"), &stateOption)...)
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("state_option"), &planOption)...)
		if resp.Diagnostics.HasError() {
			return
		}
		// Equal treats an unknown planned value (e.g. state_option derived from another
		// resource's not-yet-known output) as NOT equal, so an unprovable change forces
		// the marker unknown — the safe direction (an unknown plan value accepts any
		// applied result, whereas a wrongly-stable one risks the inconsistency above).
		if !planState.Equal(stateState) || !planOption.Equal(stateOption) {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("pending_task_id"), types.StringUnknown())...)
		}
		return
	}

	// Replacement planned: force the UseStateForUnknown computed values unknown so
	// Create recomputes them for the (re)created power resource.
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("pending_task_id"), types.StringUnknown())...)
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

	id, err := parseServerID(plan.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id", err.Error())
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

	// Submit the power command and reconcile the accepted task. Create and Update
	// share this identical submit-and-classify flow (thread r3637429687).
	r.submitPowerCommand(ctx, id, state, stateOption, plan.Wait.ValueBool(), &desired, &resp.State, &resp.Diagnostics)
}

// submitPowerCommand issues a power command (SetPowerState) and reconciles the
// accepted task, writing the FINAL resource state (or an error) into respState /
// diags. It is the shared submit-and-classify flow used by BOTH Create and Update:
// SetPowerState → handleSetPowerStateError → pendingMarkerFor → WaitForTask →
// classifyTaskWaitError. Extracting it (thread r3637429687) keeps that sequence in
// one place so a new wait outcome or a change to how the marker is assigned is
// edited once rather than in two copy-pasted branches that could silently diverge.
//
// Callers pass the fully-populated `desired` model (with pending_task_id defaulted
// to null) and MUST return immediately after calling it. It never falls back to any
// caller-specific tail (e.g. Update's prior-marker restore): every path here sets
// its own marker per the unified rule (new task UUID / sentinel / null-for-sync).
func (r *serverPowerResource) submitPowerCommand(
	ctx context.Context,
	id int32,
	state netcup.PowerState,
	stateOption string,
	wait bool,
	desired *serverPowerResourceModel,
	respState *tfsdk.State,
	diags *diag.Diagnostics,
) {
	task, err := r.client.SetPowerState(ctx, id, state, stateOption)
	if err != nil {
		handleSetPowerStateError(ctx, respState, diags, err, desired)
		return
	}

	// wait=false: the async task has not completed, so a read-back would reflect
	// the pre-op/intermediate live state (e.g. RUNNING while OFF was requested).
	// Persist the accepted task UUID alongside the desired state so a refresh that
	// observes a transient live state consults the task (GetTask) before mapping
	// it over the desired value and re-issuing the (possibly destructive) command.
	if !wait {
		if task != nil {
			// Thread A (P1): a 202 whose body omits `uuid` yields a non-nil task with
			// an empty UUID; store the sentinel (not an empty marker Read would skip).
			desired.PendingTaskID = types.StringValue(pendingMarkerFor(task))
		}
		diags.Append(respState.Set(ctx, desired)...)
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
				persistDesiredStateWithWarning(ctx, respState, diags, desired, indeterminateDiag)
				return
			} else {
				// Confirmed terminal FAILURE: the operation definitively failed and
				// the server state was not changed, so retrying is safe. Return an
				// error without persisting desired state.
				diags.Append(terminalDiag)
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
		diags.Append(respState.Set(ctx, desired)...)
		return
	}

	// Synchronous 200 (task == nil): the command completed synchronously, so there
	// is no async task to track — pending_task_id stays null (set on `desired`
	// above).
	diags.Append(respState.Set(ctx, desired)...)
}

// liveConfirmsDesired reports whether the server's live power state maps to the
// desired power state — the shared primitive of the same-state marker-clear
// invariant (thread r3637428704). A nil server/live-info or a transitional live
// state (liveStateToDesiredPower == "") does NOT confirm. Centralizing this one
// check keeps the three reconciliation branches (sentinel / task-gone / terminal)
// from each re-deriving it and drifting apart.
func liveConfirmsDesired(server *netcup.Server, desired string) bool {
	if server == nil || server.ServerLiveInfo == nil {
		return false
	}
	mapped := liveStateToDesiredPower(server.ServerLiveInfo.State)
	return mapped != "" && string(mapped) == desired
}

// reconcilePendingTask consults a possibly-pending power task before Read maps
// the live state over the desired state (Thread B). When Create/Update submitted a
// power command but could not confirm it terminally (wait=false, or a
// bounded/indeterminate WaitForTask), it stored the task UUID (or the indeterminate
// sentinel) in pending_task_id. While that async task runs the live ServerState is
// transient and does NOT yet reflect the request, so mapping it over the desired
// `state` would make the next plan re-issue the (possibly destructive) command.
//
// It returns the server snapshot Read should map from (possibly a fresh post-lookup
// refetch), whether Read should map the live state (mapLiveState), and whether the
// resource was removed (removed => Read must return immediately). It may mutate
// *state (clear/adjust the marker, blank state_option) and append diagnostics.
//
// The core same-state invariant — for a same-state RESET/POWERCYCLE op, live-state
// equality ALONE never clears a marker; only a TERMINAL task is authoritative — is
// enforced across the sentinel / task-gone / terminal branches, all of which route
// their live-equality question through the shared liveConfirmsDesired primitive.
func (r *serverPowerResource) reconcilePendingTask(
	ctx context.Context,
	id int32,
	state *serverPowerResourceModel,
	server *netcup.Server,
	sameState bool,
	resp *resource.ReadResponse,
) (updatedServer *netcup.Server, mapLiveState bool, removed bool) {
	mapLiveState = true
	pending := state.PendingTaskID
	if pending.IsNull() || pending.IsUnknown() || pending.ValueString() == "" {
		return server, mapLiveState, false
	}
	uuid := pending.ValueString()

	// The indeterminate sentinel is NOT a real UUID (Create/Update stored it because
	// the accepted response could not be decoded — no UUID was available). There is
	// nothing to GetTask, so retain the desired state until either the live state
	// confirms the desired value (non-same-state) or the risky-transition window
	// elapses (same-state).
	if isIndeterminateMarker(uuid) {
		if sameState {
			// SAME-STATE op (RESET/POWERCYCLE on an ON server): the sentinel must NOT
			// self-clear on live-equality — an accepted-but-untrackable reboot transits
			// ON → SHUTOFF → ON, and the FIRST refresh can observe the pre-op RUNNING
			// (== desired ON) before the reboot even begins; clearing then would let a
			// later refresh during the reboot's SHUTOFF phase record OFF and the next
			// apply submit a SECOND reboot.
			//
			// Thread r3635966368 (P2): retaining FOREVER (the old behavior) hides a LATER
			// external shutdown/suspension indefinitely — state stays ON, config matches,
			// so no Update ever runs. Bound the retain by the marker's embedded first-seen
			// timestamp: WITHIN powerPropagationWindow of first-seen the reboot's risky
			// transition may still be in flight, so RETAIN (map nothing); ONCE the window
			// elapses the reboot is over, so clear the sentinel and reconcile from the
			// live state below so eventual drift surfaces. A bare/legacy marker with no
			// embedded timestamp falls back to the original unbounded retain.
			if firstSeen, ok := indeterminateMarkerTime(uuid); ok && time.Since(firstSeen) > powerPropagationWindow {
				state.PendingTaskID = types.StringNull()
				// mapLiveState stays true: reconcile from live below.
			} else {
				mapLiveState = false
			}
		} else {
			// NON-same-state op: live-equality genuinely proves convergence (the pre-/
			// post-op power states differ), so clear the sentinel on a live match;
			// otherwise retain the desired state (map nothing) until it converges.
			mapLiveState = false
			if liveConfirmsDesired(server, state.State.ValueString()) {
				state.PendingTaskID = types.StringNull()
			}
		}
	} else if task, terr := r.client.GetTask(ctx, uuid); terr != nil {
		// A 404/gone means the task record no longer exists (including a brief
		// 404 right after an accepted task): reconcile from the live state and
		// clear the pending marker.
		d, gone := apiErrorToDiag(terr, false)
		if gone {
			// Thread B (P2) fix: REFETCH the live server before reconciling and
			// clearing the marker — mirror the terminal-success branch below. The
			// `server` snapshot above was taken BEFORE this GetTask; if the task
			// disappeared between the two calls (e.g. it completed and was purged,
			// or a transient 404 right after acceptance), that snapshot can still be
			// a mid-transition state (e.g. RUNNING or SHUTOFF captured mid-OFF /
			// mid-POWERCYCLE for a wait=false op). Reconciling from it would record
			// the wrong power state and make the next apply re-issue the (possibly
			// destructive) command. Take a fresh POST-lookup snapshot and reconcile
			// from THAT.
			//
			// On refetch failure: 404/gone ⇒ the server itself disappeared, remove
			// the resource; any other error ⇒ KEEP the marker + surface a diagnostic
			// and do NOT map stale state, so the next refresh retries.
			fresh, ferr := r.client.GetServer(ctx, id)
			if ferr != nil {
				fd, fgone := apiErrorToDiag(ferr, false)
				if fgone {
					resp.State.RemoveResource(ctx)
					return server, false, true
				}
				resp.Diagnostics.Append(fd)
				mapLiveState = false
			} else {
				// Thread B (P1 follow-up) fix: the fresh POST-lookup snapshot can
				// STILL show the operation's pre-op/intermediate live state — SCP
				// live-state propagation lags a purged/404 task just as it lags a
				// FINISHED one (e.g. a POWERCYCLE whose refetch is still SHUTOFF
				// before the server comes back RUNNING). Clearing the marker
				// unconditionally would record the wrong state (OFF) and make the
				// next apply reboot again. So only CLEAR + reconcile when the fresh
				// live state CONFIRMS the desired `state`; otherwise fall back to the
				// indeterminate sentinel and RETAIN the desired state (map nothing).
				//
				// Unlike the terminal-success branch there is no task.FinishedAt to
				// bound a time window here (the task record is gone), so the bound is
				// the sentinel's own convergence check above: the next refresh clears
				// the sentinel as soon as the live state matches the desired value —
				// the same deliberate residual limitation the sentinel path documents.
				server = fresh
				if sameState {
					// Thread A (P1): SAME-STATE op (RESET/POWERCYCLE on an ON server).
					// The refetched live state being RUNNING (== desired ON) does NOT
					// prove the reboot converged — the server may be ON only because the
					// reboot has not begun (or has already finished). Clearing the marker
					// on that live-equality would let a later refresh during the reboot's
					// SHUTOFF phase record OFF, and the next apply would submit a SECOND
					// reboot. So do NOT clear on live-equality here: only a TERMINAL task
					// is authoritative for a same-state op.
					//
					// A GetTask 404 gives no FinishedAt to bound a time window, so bound
					// retention by RE-QUERYING the task: keep the REAL UUID marker (not the
					// sentinel) so the NEXT refresh calls GetTask again. A 404 right after
					// acceptance is typically transient — the task record reappears and,
					// once it reaches FINISHED, the terminal-FINISHED branch clears the
					// marker (treating FINISHED itself as convergence for same-state ops).
					// Retaining the queryable UUID (rather than degrading to the sentinel,
					// whose live-equality self-clear would fire on the false RUNNING signal)
					// is the bound: retention lasts only until the task is terminal-
					// confirmed on a later refresh.
					//
					// Tradeoff: if the task record NEVER reappears (permanently purged
					// while the server sits RUNNING), the marker is retained indefinitely
					// and Terraform keeps the desired ON without re-issuing the reboot —
					// the deliberately safe choice (never silently drop a possibly-
					// unfinished reboot), matching the sentinel path's documented residual
					// limitation. An operator can force reconciliation with a re-apply.
					mapLiveState = false
					// state.PendingTaskID left unchanged (retain the real UUID).
				} else if liveConfirmsDesired(fresh, state.State.ValueString()) {
					// NON-same-state op: live-equality genuinely proves convergence
					// (the pre-/post-op power states differ), so clear + reconcile.
					state.PendingTaskID = types.StringNull()
				} else {
					state.PendingTaskID = types.StringValue(newIndeterminateMarker())
					mapLiveState = false
				}
			}
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
		fresh, ferr := r.client.GetServer(ctx, id)
		if ferr != nil {
			d, gone := apiErrorToDiag(ferr, false)
			if gone {
				// Server disappeared post-completion: remove the resource.
				resp.State.RemoveResource(ctx)
				return server, false, true
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
			// Thread B (P1) correction to 25afe91: for BOTH same-state and
			// non-same-state ops, a FINISHED task alone is NOT sufficient to clear
			// the marker — SCP live-state propagation can lag, so the post-FINISHED
			// refetch may still report the operation's intermediate state (e.g. a
			// POWERCYCLE whose refetch is still SHUTOFF before the server comes back
			// RUNNING). 25afe91 cleared the same-state marker UNCONDITIONALLY on
			// FINISHED; that records OFF on a lagged SHUTOFF refetch and the next
			// apply reboots again. So gate the clear on the SAME bounded window the
			// non-same-state path uses:
			//   converged (fresh live == desired) ⇒ clear + reconcile;
			//   mismatch within powerPropagationWindow of FinishedAt ⇒ RETAIN the
			//     marker + keep the desired state (map nothing) — propagation lag;
			//   past the window (or no FinishedAt) ⇒ clear + reconcile (genuine drift).
			//
			// This reconciles with the round-1 same-state intent: FINISHED is
			// REQUIRED to clear a same-state marker (a bare live-RUNNING without a
			// terminal task never clears it — see the non-terminal and sentinel
			// branches), AND after FINISHED the live state must confirm the desired
			// value (or the window must expire) before clearing. `sameState` no
			// longer changes THIS branch's clear/retain decision (the window logic is
			// identical for both); it still governs the FAILURE branch below.
			converged := liveConfirmsDesired(fresh, state.State.ValueString())
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

			// Thread B (P2) fix: a FAILED SAME-STATE op (RESET/POWERCYCLE on an ON
			// server) leaves the server RUNNING (== desired ON) even though the reboot
			// never happened. Reconciling from live would keep state = ON and
			// state_option = RESET (both == config), and Terraform would report NO
			// changes — the failed reboot would NEVER be retried. For a NON-same-state
			// failure the live state already diverges from desired (e.g. failed
			// power-on ⇒ SHUTOFF ⇒ OFF ≠ ON), so clear+reconcile-from-live above
			// already surfaces drift and we leave those unchanged.
			//
			// To surface drift for the same-state failure, FORCE a corrective plan by
			// BLANKING the stored operation-specific field: clear state_option to null.
			// state_option is Optional (no Default/Computed), so config `state_option =
			// "RESET"` vs stored null yields a real plan diff (null → RESET), which
			// re-runs Update (stateChanged||optionChanged ⇒ optionChanged true) and
			// re-issues the reboot. We deliberately blank state_option rather than
			// `state`: `state` must keep meaning the live power state (ON), and its
			// validator only accepts ON/OFF/SUSPENDED — corrupting it would misreport
			// the server's power state. On a SUCCESSFUL same-state op the FINISHED
			// branch above keeps state_option intact (it is never blanked there), so a
			// succeeded reboot leaves no perpetual diff.
			if sameState {
				state.StateOption = types.StringNull()
			}
		}
	}

	return server, mapLiveState, false
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

	id, err := parseServerID(state.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id in state", err.Error())
		return
	}

	server, err := r.client.GetServer(ctx, id)
	if err != nil {
		diag, gone := apiErrorToDiag(err, false)
		if gone {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(diag)
		return
	}

	// Reconcile a possibly-pending power task before mapping the live state over the
	// desired state (Thread B). While an async power task is still in flight the live
	// ServerState is transient and must not be mapped over the desired `state`;
	// reconcilePendingTask consults the task and decides map/retain/reconcile (and may
	// refetch or remove the server). See its doc for the same-state invariant.
	sameState := isSameStatePowerOp(state.State.ValueString(), state.StateOption.ValueString())
	server, mapLiveState, removed := r.reconcilePendingTask(ctx, id, &state, server, sameState, resp)
	if removed {
		return
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

	id, err := parseServerID(plan.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id", err.Error())
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

		// A new command is issued: run the shared submit-and-classify flow (thread
		// r3637429687) and RETURN. Every path inside submitPowerCommand sets its own
		// marker per the unified rule (new task UUID / sentinel / null-for-sync), so we
		// must NOT fall through to the prior-marker restore tail below — that would
		// resurrect the OBSOLETE prior task ID and suppress live-state mapping against
		// the new operation (Thread B P2).
		r.submitPowerCommand(ctx, id, powerState, stateOption, plan.Wait.ValueBool(), &desired, &resp.State, &resp.Diagnostics)
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
		priorUUID != "" && !isIndeterminateMarker(priorUUID)

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
	// Validate that the import ID is a numeric server ID. Reuse parseServerID so the
	// import path stays in sync with the parse/validation rule used everywhere else.
	if _, err := parseServerID(req.ID); err != nil {
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

	desired.PendingTaskID = types.StringValue(newIndeterminateMarker())
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
