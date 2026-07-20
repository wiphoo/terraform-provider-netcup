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

	// Thread 1 fix: when wait=false, the enable task has not yet completed so a
	// read-back of rescue status would reflect pre-task state (active:false,
	// no password). Committing that would cause the next plan/refresh to remove
	// the resource from state and attempt a re-enable, which the API rejects
	// because a task is already pending/active. Instead, persist only the known
	// identity and leave active/password as unknown (computed) to be resolved on
	// the next refresh.
	if !plan.Wait.ValueBool() {
		idStr := plan.ServerID.ValueString()
		partialState := rescueResourceModel{
			ServerID: types.StringValue(idStr),
			Active:   types.BoolUnknown(),
			Password: types.StringUnknown(),
			Wait:     plan.Wait,
			ID:       types.StringValue(idStr),
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &partialState)...)
		// Store the task UUID as a warning so the operator can track it.
		resp.Diagnostics.AddWarning(
			"Rescue enable task not awaited",
			fmt.Sprintf("Task %s was submitted but not awaited (wait=false). "+
				"Run terraform refresh once the task completes to update active/password.", task.UUID),
		)
		return
	}

	if _, err := r.client.WaitForTask(ctx, task.UUID); err != nil {
		resp.Diagnostics.AddError("Rescue enable task failed", err.Error())
		return
	}

	// Thread 2 fix: persist the resource identity into state BEFORE the
	// fallible GetRescueSystem read-back. If GetRescueSystem returns a transient
	// error, the resource ID is already in state so Terraform knows the server
	// is in rescue mode; the next refresh/apply will re-read and fill
	// active/password rather than attempting a duplicate enable.
	idStr := plan.ServerID.ValueString()
	partialState := rescueResourceModel{
		ServerID: types.StringValue(idStr),
		Active:   types.BoolUnknown(),
		Password: types.StringUnknown(),
		Wait:     plan.Wait,
		ID:       types.StringValue(idStr),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &partialState)...)
	if resp.Diagnostics.HasError() {
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
	next := rescueResourceModel{
		ServerID: types.StringValue(idStr),
		Active:   types.BoolValue(status.Active),
		Wait:     state.Wait,
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
// status code and body substring to avoid swallowing unrelated 400s.
func isAlreadyDeactivated(err error) bool {
	var apiErr *netcup.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode != 400 {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	return strings.Contains(body, "deactivat") || strings.Contains(body, "already")
}
