package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

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

var _ resource.Resource = &rescueResource{}

var _ resource.ResourceWithConfigure = &rescueResource{}

var _ resource.ResourceWithImportState = &rescueResource{}

type rescueResource struct {
	client *netcup.Client
}

type rescueResourceModel struct {
	ServerID types.String `tfsdk:"server_id"`
	Active   types.Bool   `tfsdk:"active"`
	Password types.String `tfsdk:"password"`
	Wait     types.Bool   `tfsdk:"wait"`
	ID       types.String `tfsdk:"id"`
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
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"password": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "The rescue system password. Populated after enable completes. May be null if the API has not yet surfaced it.",
				PlanModifiers: []planmodifier.String{
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

	task, err := r.client.EnableRescueSystem(ctx, serverID)
	if err != nil {
		d, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(d)
		return
	}

	idStr := plan.ServerID.ValueString()

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
		placeholderState := rescueResourceModel{
			ServerID: types.StringValue(idStr),
			Active:   types.BoolValue(true),
			Password: types.StringNull(),
			Wait:     plan.Wait,
			ID:       types.StringValue(idStr),
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
	partialState := rescueResourceModel{
		ServerID: types.StringValue(idStr),
		Active:   types.BoolValue(true),
		Password: types.StringNull(),
		Wait:     plan.Wait,
		ID:       types.StringValue(idStr),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &partialState)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, pollErr := r.client.WaitForTask(ctx, task.UUID); pollErr != nil {
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

	state := rescueResourceModel{
		ServerID: types.StringValue(idStr),
		Active:   types.BoolValue(status.Active),
		Wait:     plan.Wait,
		ID:       types.StringValue(idStr),
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

	// If rescue is no longer active, remove from state so Terraform plans a
	// re-enable if the resource is still declared.
	if !status.Active {
		resp.State.RemoveResource(ctx)
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

	next := rescueResourceModel{
		ServerID: types.StringValue(idStr),
		Active:   types.BoolValue(status.Active),
		Wait:     waitVal,
		ID:       types.StringValue(idStr),
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

	if state.Wait.ValueBool() {
		if _, err := r.client.WaitForTask(ctx, task.UUID); err != nil {
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
// already deactivated. The netcup SCP API returns HTTP 400 with a body
// containing "deactivat" (e.g. "rescue system currently deactivated") when
// DisableRescueSystem is called on an already-inactive server. We match on the
// status code and the documented "deactivat" substring only.
//
// Thread B fix: the previous implementation also matched "already", which is
// too broad — a 400 for "operation already pending" or "server already locked"
// would be silently treated as success, dropping the resource from state while
// rescue mode was still active. Restrict to the documented deactivated status
// message only: match "deactivat" (covers "deactivated"/"currently deactivated")
// and do NOT match generic "already".
func isAlreadyDeactivated(err error) bool {
	var apiErr *netcup.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode != 400 {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	return strings.Contains(body, "deactivat")
}
