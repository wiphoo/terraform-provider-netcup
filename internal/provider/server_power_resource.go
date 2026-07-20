package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

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

	if plan.Wait.ValueBool() && task != nil {
		if _, err := r.client.WaitForTask(ctx, task.UUID); err != nil {
			diag, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(diag)
			return
		}
	}

	newState := serverPowerResourceModel{
		ServerID:    plan.ServerID,
		State:       plan.State,
		StateOption: plan.StateOption,
		Wait:        plan.Wait,
		ID:          types.StringValue(plan.ServerID.ValueString()),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
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
			if _, err := r.client.WaitForTask(ctx, task.UUID); err != nil {
				diag, _ := apiErrorToDiag(err, true)
				resp.Diagnostics.Append(diag)
				return
			}
		}
	}

	newState := serverPowerResourceModel{
		ServerID:    plan.ServerID,
		State:       plan.State,
		StateOption: plan.StateOption,
		Wait:        plan.Wait,
		ID:          types.StringValue(plan.ServerID.ValueString()),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
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

// liveStateToDesiredPower maps a live ServerInfo.State value (as returned by
// the SCP GET /v1/servers/{id} endpoint) to a desired PowerState suitable for
// storing in Terraform state. Returns an empty string for transitional or
// unknown states, indicating the caller should leave the desired state unchanged.
//
// Known live states from the SCP API:
//
//	RUNNING   → ON
//	SHUTOFF   → OFF
//	SUSPENDED → SUSPENDED
//	PAUSED    → SUSPENDED (paused is effectively suspended)
//	others    → "" (transitional: keep current desired state)
func liveStateToDesiredPower(liveState string) netcup.PowerState {
	switch strings.ToUpper(liveState) {
	case "RUNNING":
		return netcup.PowerOn
	case "SHUTOFF":
		return netcup.PowerOff
	case "SUSPENDED", "PAUSED":
		return netcup.PowerSuspended
	default:
		// Transitional states (SHUTDOWN, SAVE_RESTORE, PMSUSPENDED, …) or unknown:
		// return empty to signal "no change" so we don't create a spurious diff.
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
