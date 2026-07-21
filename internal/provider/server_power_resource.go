package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

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

var _ resource.Resource = &serverPowerResource{}

var _ resource.ResourceWithConfigure = &serverPowerResource{}

var _ resource.ResourceWithImportState = &serverPowerResource{}

// serverPowerResource manages the power state of a netcup server.
type serverPowerResource struct {
	client *netcup.Client
}

// serverPowerResourceModel mirrors the Terraform schema for netcup_server_power.
type serverPowerResourceModel struct {
	ServerID    types.String `tfsdk:"server_id"`
	State       types.String `tfsdk:"state"`
	StateOption types.String `tfsdk:"state_option"`
	Wait        types.Bool   `tfsdk:"wait"`
	ID          types.String `tfsdk:"id"`
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

	task, err := r.client.SetPowerState(ctx, int32(id), state, stateOption)
	if err != nil {
		diag, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(diag)
		return
	}

	desired := serverPowerResourceModel{
		ServerID:    plan.ServerID,
		State:       plan.State,
		StateOption: plan.StateOption,
		Wait:        plan.Wait,
		ID:          types.StringValue(plan.ServerID.ValueString()),
	}

	if plan.Wait.ValueBool() && task != nil {
		// Bound task polling with a finite deadline so an apply/CI run can never
		// hang indefinitely if netcup leaves the task non-terminal. A hit deadline
		// surfaces as context.DeadlineExceeded (not a *TaskError) ⇒ INDETERMINATE.
		waitCtx, cancel := context.WithTimeout(ctx, defaultTaskTimeout)
		defer cancel()
		if _, err := r.client.WaitForTask(waitCtx, task.UUID); err != nil {
			if terminalDiag, indeterminateDiag, persist := classifyTaskWaitError(err, task.UUID); persist {
				// INDETERMINATE wait (context canceled/deadline, transport error):
				// SetPowerState was already accepted (202) and may have taken
				// effect. Persist the desired state + a warning so a later apply
				// does not blindly re-issue the (possibly destructive) command.
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
	}

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

	// Map the live ServerState to the desired PowerState equivalent.
	// Unknown or transitional states are treated as matching the current desired
	// state to avoid spurious diffs: we preserve whatever `state` is already in
	// Terraform state rather than forcing a re-apply.
	if server.ServerLiveInfo != nil {
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
		ServerID:    plan.ServerID,
		State:       plan.State,
		StateOption: plan.StateOption,
		Wait:        plan.Wait,
		ID:          types.StringValue(plan.ServerID.ValueString()),
	}

	if stateChanged || optionChanged {
		powerState := netcup.PowerState(plan.State.ValueString())
		stateOption := plan.StateOption.ValueString()

		task, err := r.client.SetPowerState(ctx, int32(id), powerState, stateOption)
		if err != nil {
			diag, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(diag)
			return
		}

		if plan.Wait.ValueBool() && task != nil {
			// Bound task polling with a finite deadline (see defaultTaskTimeout)
			// so an apply/CI run can never hang if netcup leaves the task
			// non-terminal. A hit deadline is INDETERMINATE, not a *TaskError.
			waitCtx, cancel := context.WithTimeout(ctx, defaultTaskTimeout)
			defer cancel()
			if _, err := r.client.WaitForTask(waitCtx, task.UUID); err != nil {
				if terminalDiag, indeterminateDiag, persist := classifyTaskWaitError(err, task.UUID); persist {
					// INDETERMINATE wait: the new power command was accepted (202)
					// and may have taken effect. Persist the NEW desired state + a
					// warning so a later apply does not re-issue the command. (The
					// old behaviour retained prior state, which caused a re-issue.)
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
		}
	}

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
