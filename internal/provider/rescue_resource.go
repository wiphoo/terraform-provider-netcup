package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// defaultTaskTimeout bounds how long Create (wait=true) and Delete block while
// polling an asynchronous SCP enable/disable task via WaitForTask.
//
// Thread C fix: previously the incoming framework context was passed straight to
// WaitForTask, so a task that never reached a terminal state — or an endless run
// of retryable poll errors (5xx/429) — would block `terraform apply`/`destroy`
// indefinitely. Wrapping the context with this deadline guarantees a stalled
// task eventually surfaces a clear deadline-exceeded diagnostic instead of
// hanging. netcup enable/disable reboots typically finish within a few minutes;
// 15m leaves generous headroom for a slow reboot while still bounding the wait.
//
// Follow-up (#83 "optional timeout knob"): a configurable per-resource `timeout`
// attribute is a nice-to-have; this bounded default is the required minimum and
// can be made configurable later without changing this default.
const defaultTaskTimeout = 15 * time.Minute

// pendingTaskIDIndeterminate is a sentinel stored in pending_task_id when Create
// submitted an enable but never received a usable TaskInfo/UUID back (an
// indeterminate POST failure — truncated 202, transport error mid-response, or a
// 5xx that the server may have accepted). There is no real task UUID to query,
// so this marker keeps the resource tracked through the activation-reconciliation
// window instead of dropping it.
//
// Thread A (round 8) fix: it MUST NOT collide with a real SCP task UUID. SCP task
// UUIDs are RFC 4122 UUIDs (36-char, hyphenated hex); the literal string
// "indeterminate" cannot be one, so the sentinel is unambiguous. Read special-
// cases this value BEFORE calling GetTask (there is no task to look up) and
// RETAINS the resource; a later Read that observes active==true clears it via the
// normal active branch.
const pendingTaskIDIndeterminate = "indeterminate"

// rescuePropagationWindow bounds how long Read keeps re-persisting
// pending_task_id after the enable task has reached terminal SUCCESS (FINISHED)
// while GetRescueSystem still reports active:false.
//
// Thread B (round 9) fix: that FINISHED+inactive branch exists to ride out the
// short SCP status-propagation lag between "enable task finished" and
// "rescuesystem endpoint reports active". Previously it retained the resource
// with no deadline, so if rescue was disabled EXTERNALLY before any refresh ever
// observed it active, Read re-persisted the same pending_task_id on every
// refresh forever — the resource stayed active=false in state indefinitely and
// Terraform never recreated the missing rescue system.
//
// TaskInfo exposes the task's own completion timestamp (FinishedAt), so we bound
// the retain window by wall-clock time since the task finished: within the
// window we treat active:false as propagation lag and retain; once it elapses we
// treat active:false as genuine absence, clear pending_task_id and
// RemoveResource so the next apply restores the declared state. 2 minutes
// comfortably covers real SCP propagation (seconds) while ensuring a truly
// disabled rescue system is reconciled promptly.
const rescuePropagationWindow = 2 * time.Minute

var _ resource.Resource = &rescueResource{}

var _ resource.ResourceWithConfigure = &rescueResource{}

var _ resource.ResourceWithImportState = &rescueResource{}

var _ resource.ResourceWithModifyPlan = &rescueResource{}

type rescueResource struct {
	client *netcup.Client
}

type rescueResourceModel struct {
	ServerID      types.String `tfsdk:"server_id"`
	Active        types.Bool   `tfsdk:"active"`
	Password      types.String `tfsdk:"password"`
	Wait          types.Bool   `tfsdk:"wait"`
	ID            types.String `tfsdk:"id"`
	PendingTaskID types.String `tfsdk:"pending_task_id"`
}

func NewRescueResource() resource.Resource {
	return &rescueResource{}
}

func (r *rescueResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "netcup_server_rescue"
}

func (r *rescueResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the rescue system for a netcup server.\n\n" +
			"WARNING: Enabling rescue mode REBOOTS the server into the rescue environment (causes downtime). " +
			"Disabling rescue mode REBOOTS the server back into its normal operating system (also causes downtime). " +
			"Both enable and disable are asynchronous operations; the resource waits for each to complete by default.",
		Attributes: map[string]schema.Attribute{
			"server_id": schema.StringAttribute{
				Required:    true,
				Description: "The numeric server ID. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"active": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the rescue system is currently active.",
				PlanModifiers: []planmodifier.Bool{
					// Thread A (round 9): stock UseStateForUnknown keeps the prior
					// value stable across in-place updates (e.g. a wait-only change)
					// so the plan does not churn to "(known after apply)". The
					// resource-level ModifyPlan (see below) overrides this to unknown
					// whenever a replacement is planned, so a destroy/create cycle
					// recomputes active for the (possibly regenerated) rescue system.
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"password": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "The rescue system password. Populated after enable completes. May be null if the API has not yet surfaced it.",
				PlanModifiers: []planmodifier.String{
					// Thread A (round 9): stock UseStateForUnknown for plan
					// stability on in-place updates; ModifyPlan forces this unknown
					// on any planned replacement so the freshly generated rescue
					// password is recomputed rather than copied from prior state.
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"wait": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether to wait for async tasks (enable/disable) to complete. Defaults to true. Set to false only if you want to poll the task yourself.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The server ID (resource identifier, same as server_id).",
				PlanModifiers: []planmodifier.String{
					// Thread A (round 9): stock UseStateForUnknown for plan
					// stability; ModifyPlan forces this unknown on any planned
					// replacement so the new server_id becomes the new id.
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"pending_task_id": schema.StringAttribute{
				Computed: true,
				Description: "The UUID of an in-flight asynchronous enable task, used to " +
					"reconcile a pending rescue activation on refresh. When Create submits " +
					"an enable but the task has not confirmed terminally (wait=false, or a " +
					"bounded/indeterminate wait), this holds the task UUID so a refresh that " +
					"reads active:false consults the task before treating the resource as " +
					"absent. Null once the task is terminal or rescue is active.",
				PlanModifiers: []planmodifier.String{
					// Thread A (round 9): stock UseStateForUnknown for plan
					// stability; ModifyPlan forces this unknown on any planned
					// replacement so pending_task_id is recomputed for the new server.
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *rescueResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ModifyPlan forces the computed attributes (id, password, active,
// pending_task_id) to UNKNOWN ("known after apply") whenever a replacement is
// planned, so the destroy/create cycle recomputes them for the (re)created
// rescue system instead of copying stale prior-state values into the plan.
//
// Thread A (round 9) fix — WHY this replaces the custom
// useStateForUnknownUnlessReplacing* attribute modifiers:
//
// The stock stringplanmodifier.UseStateForUnknown() (now used on these four
// attributes) copies the prior state value into any unknown planned value while
// prior state exists. That is exactly what we want for an in-place UPDATE (e.g.
// a wait-only change) — the plan stays stable, no spurious "(known after
// apply)". But on a REPLACEMENT it would copy the OLD rescue system's
// id/password/active into the plan; Create then destroys+recreates and returns
// a freshly generated password, so Terraform rejects the apply with "Provider
// produced inconsistent result after apply" — AFTER the two disruptive reboots
// already ran.
//
// The previous custom modifiers guessed "replacement planned" by comparing
// plan vs prior-state server_id. That misses a replacement FORCED WITHOUT a
// server_id change (`terraform apply -replace=ADDRESS` or a replace_triggered_by
// reference): the equality test sees server_id unchanged, treats it as an
// in-place update, and copies the stale computed values.
//
// This resource-level ModifyPlan fixes the server_id-CHANGE case authoritatively
// (no heuristic — it compares the two server_ids and clears the computed values
// to unknown when they differ). For a FORCED replace with an unchanged
// server_id the provider has no in-plan signal (Terraform core applies the
// -replace decision AFTER PlanResourceChange returns), but that path is already
// correct: Terraform re-plans the create half of the replace with a NULL prior
// state, where stock UseStateForUnknown does nothing (its own null-state guard)
// and the framework marks the computed nils unknown — so Create fills the real
// values and the result is consistent.
func (r *rescueResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
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
	// potential replacement whose target cannot be proven to match — treat it as
	// a replacement so the computed values are recomputed rather than copied. A
	// null/unknown prior-state server_id likewise cannot be proven equal.
	sameServer := !planServerID.IsNull() && !planServerID.IsUnknown() &&
		!stateServerID.IsNull() && !stateServerID.IsUnknown() &&
		stateServerID.ValueString() == planServerID.ValueString()
	if sameServer {
		// In-place update (server_id unchanged and known): leave the plan as-is so
		// stock UseStateForUnknown keeps the computed values stable.
		return
	}

	// Replacement planned: force the computed values unknown so Create recomputes
	// them for the (re)created rescue system.
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("password"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("active"), types.BoolUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("pending_task_id"), types.StringUnknown())...)
}

func (r *rescueResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_server_rescue.",
		)
		return
	}

	var plan rescueResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID, err := parseServerID(plan.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id", err.Error())
		return
	}

	idStr := plan.ServerID.ValueString()

	task, err := r.client.EnableRescueSystem(ctx, serverID)
	if err != nil {
		// Thread B fix: distinguish a DEFINITIVE rejection from an INDETERMINATE
		// failure, mirroring the terminal-vs-indeterminate philosophy already
		// applied to WaitForTask below.
		//
		//   - Definitive (a *netcup.APIError with a 4xx status): the SCP API
		//     rejected the request outright — no task was created and rescue was
		//     NOT enabled. Write no state and surface the error; the resource
		//     genuinely does not exist.
		//   - Indeterminate (transport error, response-body decode error, or a
		//     5xx): the POST may have been accepted server-side and an async
		//     enable task may already be activating rescue, even though we never
		//     got a usable TaskInfo back. Persist the partial identity (known
		//     placeholders: active=true, password=null) BEFORE returning the
		//     error so Terraform tracks the resource. The next refresh/Delete can
		//     then reconcile — otherwise a retried Create would hit the
		//     already-active 400 and leave the resource untracked.
		if isDefinitiveEnableRejection(err) {
			d, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(d)
			return
		}

		// Thread A (round 8) fix: this indeterminate POST failure returned no usable
		// TaskInfo, so there is no enable-task UUID to persist. Previously we kept
		// pending_task_id null and a refresh that read active:false while the enable
		// may still be running fell into the genuine-absence path and removed the
		// resource — the next apply could then submit a DUPLICATE enable while the
		// original task was still activating (extra reboot / already-pending 400).
		//
		// Instead, persist the pendingTaskIDIndeterminate sentinel. Read's
		// inactive+pending branch special-cases it (no GetTask — there is no task to
		// query) and RETAINS the resource through the reconciliation window; a later
		// Read that observes active==true clears the marker via the active branch.
		//
		// Residual tradeoff (deliberate): a genuinely-failed indeterminate enable
		// stays tracked under the sentinel until an operator intervenes (e.g.
		// terraform state rm, or a manual enable that a later refresh observes
		// active). That is safer than dropping a resource whose rescue may in fact
		// be activating and then double-enabling it.
		indeterminateState := rescueResourceModel{
			ServerID:      types.StringValue(idStr),
			Active:        types.BoolValue(true),
			Password:      types.StringNull(),
			Wait:          plan.Wait,
			ID:            types.StringValue(idStr),
			PendingTaskID: types.StringValue(pendingTaskIDIndeterminate),
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &indeterminateState)...)
		d, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(d)
		return
	}

	// when wait=false, the enable task has not yet completed so a read-back of
	// rescue status would reflect pre-task state (active:false, no password).
	// Committing that would cause the next plan/refresh to remove the resource
	// from state and attempt a re-enable, which the API rejects because a task
	// is already pending/active.
	//
	// Thread A fix: the Terraform plugin protocol requires every attribute that
	// was unknown in the plan to become a KNOWN value in the final state after
	// apply. Returning types.BoolUnknown()/types.StringUnknown() here causes
	// Terraform to reject the apply with "Provider produced inconsistent result
	// after apply". Instead, return known placeholder values:
	//   active=true  — we just submitted an enable; the intended managed state
	//                  is active. The next refresh reconciles to live value.
	//   password=null — not yet available; null is a known value. It becomes
	//                   populated on the next refresh via Read.
	if !plan.Wait.ValueBool() {
		// Thread P1 fix: persist the enable task UUID so a refresh that reads
		// active:false while this async enable is still running can consult the
		// task (via GetTask) instead of treating the resource as absent and
		// re-enabling it. Cleared to null once the task is terminal / rescue is
		// active (see Read).
		placeholderState := rescueResourceModel{
			ServerID:      types.StringValue(idStr),
			Active:        types.BoolValue(true),
			Password:      types.StringNull(),
			Wait:          plan.Wait,
			ID:            types.StringValue(idStr),
			PendingTaskID: types.StringValue(task.UUID),
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &placeholderState)...)
		// Store the task UUID as a warning so the operator can track it.
		resp.Diagnostics.AddWarning(
			"Rescue enable task not awaited",
			fmt.Sprintf("Task %s was submitted but not awaited (wait=false). "+
				"Run terraform refresh once the task completes to update active/password.", task.UUID),
		)
		return
	}

	// Thread 2 fix: persist the resource identity BEFORE calling WaitForTask.
	// If WaitForTask exits with an indeterminate error (context deadline, transient
	// poll auth/404 — i.e. any error that is NOT a confirmed *TaskError from a
	// terminal FAILED/CANCELED/ROLLBACK state), the task may still complete after
	// the apply returns. Keeping identity in state prevents the next apply from
	// attempting a duplicate enable that the API would reject as already-active.
	//
	// Conversely, if WaitForTask returns a *TaskError we have confirmed the task
	// reached a terminal failure state — rescue is definitely NOT enabled, so we
	// clear state and let Terraform re-try the create.
	//
	// Thread C fix: use KNOWN placeholder values (active=true, password=null),
	// not types.BoolUnknown()/types.StringUnknown(). When WaitForTask or the
	// read-back below fails, this partial state becomes the final NewState that
	// Terraform stores for the errored apply. The provider protocol does not
	// permit unknown values in stored state — Terraform rejects a NewState that
	// still contains unknowns after apply ("Provider returned invalid result
	// object after apply"), which would defeat the whole point of persisting
	// identity here. Mirror the wait=false path: active=true is the intended
	// managed state (we just submitted an enable), password=null is a valid
	// known value; the next refresh reconciles both to live values.
	//
	// Thread P1 fix: persist the enable task UUID in this pre-poll partial state.
	// If WaitForTask exits INDETERMINATE (deadline-exceeded or a transient/
	// permanent poll error that is NOT a *TaskError), this state is retained and
	// pending_task_id lets a subsequent refresh consult the still-running task
	// before treating active:false as absence. On WaitForTask SUCCESS it is
	// cleared to null in the read-back below; on a terminal *TaskError the whole
	// resource is removed.
	partialState := rescueResourceModel{
		ServerID:      types.StringValue(idStr),
		Active:        types.BoolValue(true),
		Password:      types.StringNull(),
		Wait:          plan.Wait,
		ID:            types.StringValue(idStr),
		PendingTaskID: types.StringValue(task.UUID),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &partialState)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Thread C fix: bound the poll with defaultTaskTimeout so a stalled task
	// eventually returns a deadline-exceeded diagnostic instead of hanging the
	// apply. A deadline-exceeded error is NOT a *netcup.TaskError, so it falls
	// into the indeterminate branch below and the partial identity persisted
	// above is retained (the enable may still complete).
	waitCtx, cancel := context.WithTimeout(ctx, defaultTaskTimeout)
	defer cancel()

	if _, pollErr := r.client.WaitForTask(waitCtx, task.UUID); pollErr != nil {
		var taskErr *netcup.TaskError
		if errors.As(pollErr, &taskErr) {
			// Confirmed terminal failure: the task is in ERROR/CANCELED/ROLLBACK.
			// Rescue was NOT enabled. Clear the partial state so Terraform knows
			// there is no resource and can retry the create cleanly.
			resp.State.RemoveResource(ctx)
		}
		// For all failures (terminal or indeterminate): surface the error.
		// When indeterminate, partial state is retained above so the next apply
		// does not duplicate-enable.
		resp.Diagnostics.AddError("Rescue enable task failed", pollErr.Error())
		return
	}

	// Read back active state and password.
	status, err := r.client.GetRescueSystem(ctx, serverID)
	if err != nil {
		d, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(d)
		// Identity is already persisted above; return without overwriting state
		// so the next refresh can fill in active/password.
		return
	}

	// Thread A (round 7) fix: the enable task reached FINISHED, but SCP status
	// propagation can lag — the immediately-following GetRescueSystem may still
	// report active:false for a short window. Clear pending_task_id ONLY when the
	// read-back actually observes active==true. If it still reads active==false
	// (propagation delay), RETAIN pending_task_id = task.UUID and commit
	// active=false + pending, so a later Read can reconcile via GetTask instead of
	// treating the resource as absent and re-enabling an already-active server.
	pendingTaskID := types.StringNull()
	if !status.Active {
		pendingTaskID = types.StringValue(task.UUID)
	}
	state := rescueResourceModel{
		ServerID:      types.StringValue(idStr),
		Active:        types.BoolValue(status.Active),
		Wait:          plan.Wait,
		ID:            types.StringValue(idStr),
		PendingTaskID: pendingTaskID,
	}

	// Password may be nil immediately after enable (the API surfaces it shortly
	// after activation). Treat nil as null rather than erroring, mirroring the
	// CLI behavior documented in #62.
	if status.Password != nil {
		state.Password = types.StringValue(*status.Password)
	} else {
		state.Password = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *rescueResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured.",
		)
		return
	}

	var state rescueResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if state.ID.ValueString() == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	serverID, err := parseServerID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id in state", err.Error())
		return
	}

	status, err := r.client.GetRescueSystem(ctx, serverID)
	if err != nil {
		d, gone := apiErrorToDiag(err, false)
		if gone {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(d)
		return
	}

	idStr := state.ID.ValueString()

	// Thread 3 fix: after `terraform import` only `id` is populated; `wait` is
	// null. If the resource is then destroyed before any update, Delete evaluates
	// state.Wait.ValueBool() as false (null→false in Go) and skips polling,
	// silently ignoring disable-task failures. Normalize null→true here (the
	// documented default) so Delete's wait behavior matches user expectations.
	waitVal := state.Wait
	if waitVal.IsNull() || waitVal.IsUnknown() {
		waitVal = types.BoolValue(true)
	}

	// Thread P1 fix: reconcile a possibly-pending enable task before treating an
	// inactive read as absence.
	//
	// When Create submitted an enable but could not confirm it terminally
	// (wait=false, or a bounded/indeterminate WaitForTask), it stored the enable
	// task UUID in pending_task_id. The async enable reboots the server into the
	// rescue environment; during that transitional window a refresh legitimately
	// reads active:false even though rescue activation is still in progress. The
	// pre-P1 behavior removed the resource here, so the next apply re-submitted an
	// enable — causing an extra reboot / already-pending / already-active failure.
	//
	// So: if rescue is inactive AND we are tracking a pending task, consult that
	// task (GetTask) and only remove the resource once the task is terminal (the
	// enable truly finished/failed and rescue is genuinely off) or gone (404). A
	// non-terminal task means activation is still in flight — keep the resource.
	//
	// GetTask is called ONLY on this inactive+pending path (a short transitional
	// window), NOT on every refresh: an active read, or an inactive read with no
	// pending task, skips it entirely.
	if !status.Active {
		if state.PendingTaskID.IsNull() || state.PendingTaskID.IsUnknown() || state.PendingTaskID.ValueString() == "" {
			// No pending enable task to reconcile: genuine absence. Remove so
			// Terraform plans a re-enable if the resource is still declared.
			resp.State.RemoveResource(ctx)
			return
		}

		uuid := state.PendingTaskID.ValueString()

		// Thread A (round 8) fix: the pendingTaskIDIndeterminate sentinel is NOT a
		// real task UUID — Create stored it because the indeterminate enable POST
		// returned no TaskInfo to query. Do NOT call GetTask (there is nothing to
		// look up). RETAIN the resource with the marker and a known active=false;
		// a later Read that observes active==true clears the marker via the active
		// branch below. This keeps the resource tracked through the activation
		// window so the next apply does not submit a duplicate enable.
		//
		// Residual tradeoff (deliberate): if that indeterminate enable genuinely
		// failed, the resource stays tracked under the sentinel until an operator
		// intervenes — safer than dropping a possibly-active resource and
		// double-enabling it.
		if uuid == pendingTaskIDIndeterminate {
			kept := rescueResourceModel{
				ServerID:      types.StringValue(idStr),
				Active:        types.BoolValue(false),
				Password:      types.StringNull(),
				Wait:          waitVal,
				ID:            types.StringValue(idStr),
				PendingTaskID: types.StringValue(pendingTaskIDIndeterminate),
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &kept)...)
			return
		}

		task, err := r.client.GetTask(ctx, uuid)
		if err != nil {
			// A 404 (or otherwise "gone") means the task record no longer exists —
			// treat as genuine absence and remove, mirroring apiErrorToDiag's gone
			// handling for a missing object.
			d, gone := apiErrorToDiag(err, false)
			if gone {
				resp.State.RemoveResource(ctx)
				return
			}
			// Any other (transient) error: do NOT remove the resource — we cannot
			// tell whether the enable is still pending. Surface a diagnostic and
			// re-persist the prior state (including pending_task_id) so it stays
			// intact for the next refresh to retry.
			resp.Diagnostics.Append(d)
			kept := rescueResourceModel{
				ServerID:      types.StringValue(idStr),
				Active:        types.BoolValue(false),
				Password:      types.StringNull(),
				Wait:          waitVal,
				ID:            types.StringValue(idStr),
				PendingTaskID: types.StringValue(uuid),
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &kept)...)
			return
		}

		if !task.State.IsTerminal() {
			// The enable task is still running (PENDING/RUNNING/WAITING_FOR_CANCEL):
			// activation is in flight. Keep the resource and retain pending_task_id
			// so the next refresh re-checks. active stays a known false.
			pending := rescueResourceModel{
				ServerID:      types.StringValue(idStr),
				Active:        types.BoolValue(false),
				Password:      types.StringNull(),
				Wait:          waitVal,
				ID:            types.StringValue(idStr),
				PendingTaskID: types.StringValue(uuid),
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &pending)...)
			return
		}

		// The task is terminal.
		//
		// Thread A (round 7) fix: split terminal SUCCESS from terminal FAILURE.
		//
		// Terminal SUCCESS (FINISHED) while active:false is the status-propagation
		// window: the enable task finished, so rescue should become active shortly,
		// but the rescuesystem endpoint has not caught up yet. RETAIN the resource
		// and keep pending_task_id so a later Read observes active==true (the active
		// branch below then clears pending). Removing here would let Terraform
		// re-enable an already-active server (400) and leave rescue untracked.
		//
		// This retain-on-terminal-success only affects the pre-active-observation
		// (propagation) window: once any Read ever sees active==true, pending_task_id
		// is cleared, so a subsequent external disable correctly falls into
		// inactive+no-pending ⇒ RemoveResource below.
		//
		// Terminal FAILURE (ERROR/CANCELED/ROLLBACK): the enable definitively did
		// not activate rescue — remove so Terraform plans a re-enable.
		//
		// Thread B (round 9) fix: BOUND the FINISHED+inactive retain window. The
		// enable task FINISHED, but rescue reads inactive. This is legitimately the
		// status-propagation window ONLY for a short time after the task finished;
		// if rescue was disabled externally before any refresh observed it active,
		// retaining forever would strand the resource at active=false. Use the
		// task's own FinishedAt timestamp: within rescuePropagationWindow, treat
		// inactive as propagation lag and RETAIN (keep pending_task_id); once the
		// window elapses (or the API did not supply FinishedAt and we fall through
		// to the elapsed branch), treat inactive as genuine absence — clear
		// pending_task_id and RemoveResource so the next apply restores the
		// declared state.
		if task.State == netcup.TaskStateFinished {
			withinWindow := task.FinishedAt != nil && time.Since(*task.FinishedAt) <= rescuePropagationWindow
			if withinWindow {
				kept := rescueResourceModel{
					ServerID:      types.StringValue(idStr),
					Active:        types.BoolValue(false),
					Password:      types.StringNull(),
					Wait:          waitVal,
					ID:            types.StringValue(idStr),
					PendingTaskID: types.StringValue(uuid),
				}
				resp.Diagnostics.Append(resp.State.Set(ctx, &kept)...)
				return
			}
			// Past the propagation window (or no usable FinishedAt): the enable
			// finished long ago yet rescue is still off — genuine absence.
			resp.State.RemoveResource(ctx)
			return
		}

		resp.State.RemoveResource(ctx)
		return
	}

	// Rescue is active: normal path. Clear any pending_task_id — the enable has
	// taken effect, so there is no in-flight task to reconcile.
	next := rescueResourceModel{
		ServerID:      types.StringValue(idStr),
		Active:        types.BoolValue(status.Active),
		Wait:          waitVal,
		ID:            types.StringValue(idStr),
		PendingTaskID: types.StringNull(),
	}
	if status.Password != nil {
		next.Password = types.StringValue(*status.Password)
	} else {
		next.Password = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

// Update handles in-place changes to the rescue resource. The only mutable
// non-computed attribute is `wait` (server_id requires replacement). Changing
// `wait` does not require any API call — it only controls whether future
// Create/Delete operations block until the async task completes.
//
// Thread 3 fix: previously Update always returned an error, which meant
// changing wait=true to wait=false (or any plan that dispatched to Update)
// permanently failed. Now we copy the planned `wait` value into state so the
// change is accepted without an unnecessary reboot/replacement.
func (r *rescueResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var state rescueResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var plan rescueResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Copy the planned wait value into state; all other attributes are either
	// computed (active, password, id) or require replacement (server_id).
	state.Wait = plan.Wait

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *rescueResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured.",
		)
		return
	}

	var state rescueResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID, err := parseServerID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id in state", err.Error())
		return
	}

	// Thread B (round 8) fix: for state created by ImportState, `wait` stays null
	// until a Read normalizes it. An immediate `terraform destroy -refresh=false`
	// reaches Delete with wait still null, so state.Wait.ValueBool() returns false
	// (null→false in Go) and Delete would skip WaitForTask despite the documented
	// default wait=true — silently removing the resource and hiding a disable-task
	// failure. Normalize null/unknown → true here, exactly as Read does, and use
	// that normalized value to decide whether to await the disable task.
	waitVal := state.Wait
	if waitVal.IsNull() || waitVal.IsUnknown() {
		waitVal = types.BoolValue(true)
	}

	task, err := r.client.DisableRescueSystem(ctx, serverID)
	if err != nil {
		// Thread 4 fix: if DisableRescueSystem returns a 400 whose body indicates
		// the rescue system is already deactivated, treat it as success — the
		// desired state (rescue off) is already reached. This handles the case
		// where the rescue system was deactivated outside Terraform between the
		// last refresh and the destroy. Only match the specific "already
		// deactivated" 400; other 400s (and all other status codes) are still
		// treated as hard errors.
		if isAlreadyDeactivated(err) {
			return
		}
		d, gone := apiErrorToDiag(err, false)
		if gone {
			return
		}
		resp.Diagnostics.Append(d)
		return
	}

	if waitVal.ValueBool() {
		// Thread C fix: bound the disable poll with defaultTaskTimeout so a
		// stalled disable task surfaces a deadline-exceeded diagnostic instead
		// of blocking `terraform destroy` indefinitely.
		waitCtx, cancel := context.WithTimeout(ctx, defaultTaskTimeout)
		defer cancel()
		if _, err := r.client.WaitForTask(waitCtx, task.UUID); err != nil {
			resp.Diagnostics.AddError("Rescue disable task failed", err.Error())
			return
		}
	}
}

func (r *rescueResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// parseServerID parses the string server_id (as stored in the schema/state)
// into int32 for SDK calls. Returns a descriptive error if the string is not a
// valid numeric server ID.
func parseServerID(s string) (int32, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("server_id %q is not a valid numeric server ID: %w", s, err)
	}
	return int32(n), nil
}

// isAlreadyDeactivated reports whether err indicates that the rescue system is
// ALREADY deactivated — the one case Delete may treat as success (the desired
// end state, rescue off, is already reached). The netcup SCP API returns HTTP
// 400 with the documented body "Rescue system currently deactivated." (see
// docs/SCP-API-NOTES.md).
//
// Thread C (round 9) fix: the previous implementation matched the bare stem
// "deactivat", which ALSO matches a DIFFERENT rejection such as "rescue cannot
// be deactivated while another operation is pending" — a case where rescue is
// still active. Treating that as success would make Delete silently drop the
// resource while rescue mode was still on. Match only a phrase that asserts the
// CURRENT deactivated STATE ("currently deactivated" / "already deactivated" /
// "is deactivated", case-insensitive), and explicitly reject negations
// ("cannot be deactivated") and pending-operation rejections ("pending").
// isDefinitiveEnableRejection reports whether an EnableRescueSystem error is a
// definitive rejection by the SCP API — a *netcup.APIError carrying a 4xx status
// code. A 4xx means the request was understood and refused (e.g. 400 already
// active, 404 unknown server, 401/403 auth): no enable task was created, so
// rescue is definitively NOT enabled and no state should be written.
//
// Everything else is treated as INDETERMINATE:
//   - a transport error (connection reset, timeout mid-request),
//   - a response-body decode error (truncated 202), or
//   - a 5xx (the server may have accepted the POST before failing).
//
// In those cases the enable may still take effect asynchronously, so the caller
// persists partial identity state before returning the error.
func isDefinitiveEnableRejection(err error) bool {
	var apiErr *netcup.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode >= 400 && apiErr.StatusCode < 500
}

func isAlreadyDeactivated(err error) bool {
	var apiErr *netcup.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode != 400 {
		return false
	}
	body := strings.ToLower(apiErr.Body)

	// Guard against negations and unrelated pending-operation rejections that
	// happen to contain "deactivated" (e.g. "rescue cannot be deactivated while
	// another operation is pending"). These mean rescue is still active, NOT that
	// it is already off.
	if strings.Contains(body, "cannot be deactivated") || strings.Contains(body, "pending") {
		return false
	}

	// Match only phrases asserting the CURRENT deactivated state. The documented
	// SCP message is "Rescue system currently deactivated."; accept close
	// variants for resilience to minor wording changes.
	return strings.Contains(body, "currently deactivated") ||
		strings.Contains(body, "already deactivated") ||
		strings.Contains(body, "is deactivated")
}
