package provider

import (
	"context"
	"fmt"
	"strconv"

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

	if plan.Wait.ValueBool() {
		if _, err := r.client.WaitForTask(ctx, task.UUID); err != nil {
			resp.Diagnostics.AddError("Rescue enable task failed", err.Error())
			return
		}
	}

	// Read back active state and password.
	status, err := r.client.GetRescueSystem(ctx, serverID)
	if err != nil {
		d, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(d)
		return
	}

	idStr := plan.ServerID.ValueString()
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

// Update is not supported: all mutable attributes either require replacement
// (server_id) or are computed. The framework will never call Update for this
// resource; this method satisfies the resource.Resource interface.
func (r *rescueResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update not supported",
		"netcup_server_rescue does not support in-place updates. All changes require resource replacement.",
	)
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
