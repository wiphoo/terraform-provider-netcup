package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// reinstallTaskTimeout bounds how long Create blocks polling an accepted reinstall
// task via WaitForTask before giving up. Like rescue's defaultTaskTimeout it stops
// `terraform apply`/CI from hanging forever if netcup leaves a task non-terminal;
// a native OS reinstall can take a while, so it is set more generously than a
// reboot. When the deadline fires WaitForTask returns context.DeadlineExceeded,
// which is NOT a *netcup.TaskError, so it is treated as INDETERMINATE (persist +
// warn) rather than a confirmed failure (see Create).
const reinstallTaskTimeout = 60 * time.Minute

var _ resource.Resource = &serverReinstallResource{}

var _ resource.ResourceWithConfigure = &serverReinstallResource{}

var _ resource.ResourceWithImportState = &serverReinstallResource{}

// serverReinstallResource performs a native OS (re)install on a netcup server via
// the SDK ReinstallServer (POST /v1/servers/{id}/image). See ADR-0001
// (docs/adr/0001-server-reinstall-resource-lifecycle.md) for the lifecycle model:
// install inputs and `triggers` use RequiresReplace so every reinstall shows as a
// resource replacement in the plan; Delete is a no-op; Read is thin.
//
// DESTRUCTIVE: applying (or replacing) this resource WIPES the server.
type serverReinstallResource struct {
	client *netcup.Client
}

// serverReinstallResourceModel mirrors the Terraform schema for
// netcup_server_reinstall. The install fields map 1:1 onto netcup.ServerImageSetup.
type serverReinstallResourceModel struct {
	ServerID                  types.String `tfsdk:"server_id"`
	ImageFlavourID            types.Int64  `tfsdk:"image_flavour_id"`
	DiskName                  types.String `tfsdk:"disk_name"`
	RootPartitionFullDiskSize types.Bool   `tfsdk:"root_partition_full_disk_size"`
	Hostname                  types.String `tfsdk:"hostname"`
	Locale                    types.String `tfsdk:"locale"`
	Timezone                  types.String `tfsdk:"timezone"`
	AdditionalUserUsername    types.String `tfsdk:"additional_user_username"`
	AdditionalUserPassword    types.String `tfsdk:"additional_user_password"`
	SSHKeyIDs                 types.List   `tfsdk:"ssh_key_ids"`
	SSHPasswordAuthentication types.Bool   `tfsdk:"ssh_password_authentication"`
	CustomScript              types.String `tfsdk:"custom_script"`
	EmailToExecutingUser      types.Bool   `tfsdk:"email_to_executing_user"`
	Triggers                  types.Map    `tfsdk:"triggers"`
	Wait                      types.Bool   `tfsdk:"wait"`
	ID                        types.String `tfsdk:"id"`
	TaskID                    types.String `tfsdk:"task_id"`
}

// NewServerReinstallResource returns a new netcup_server_reinstall resource factory.
func NewServerReinstallResource() resource.Resource {
	return &serverReinstallResource{}
}

func (r *serverReinstallResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "netcup_server_reinstall"
}

func (r *serverReinstallResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Performs a native OS (re)install on a netcup server via the SCP API " +
			"(POST /v1/servers/{id}/image), with optional post-install `custom_script` bootstrap.\n\n" +
			"WARNING: This is DESTRUCTIVE — the reinstall WIPES the server and ALL data on it is lost. " +
			"Changing `image_flavour_id`, `custom_script`, `ssh_key_ids`, or any other install input " +
			"forces a replacement, which RE-RUNS the reinstall (shown as `-/+` in `terraform plan`). " +
			"Use the `triggers` map to deliberately re-run the reinstall on otherwise-unchanged config. " +
			"Destroying this resource is a no-op — it does NOT reinstall or wipe the server.",
		Attributes: map[string]schema.Attribute{
			"server_id": schema.StringAttribute{
				Required:    true,
				Description: "The numeric server ID to reinstall. Forces replacement (a reinstall) if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"image_flavour_id": schema.Int64Attribute{
				Required: true,
				Description: "The image flavour ID to install. Discover valid values with the " +
					"netcup_server_images data source. Forces replacement (a reinstall) if changed.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"disk_name": schema.StringAttribute{
				Optional:    true,
				Description: "Optional target disk name for the install. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"root_partition_full_disk_size": schema.BoolAttribute{
				Optional:    true,
				Description: "When true, the root partition uses the full disk size. Forces replacement if changed.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"hostname": schema.StringAttribute{
				Optional:    true,
				Description: "Optional hostname to set on the installed system. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"locale": schema.StringAttribute{
				Optional:    true,
				Description: "Optional locale for the installed system. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"timezone": schema.StringAttribute{
				Optional:    true,
				Description: "Optional timezone for the installed system. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"additional_user_username": schema.StringAttribute{
				Optional:    true,
				Description: "Optional additional (non-root) username to create. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"additional_user_password": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Optional password for the additional user. Sensitive. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"ssh_key_ids": schema.ListAttribute{
				Optional:    true,
				ElementType: types.Int64Type,
				Description: "Optional list of SSH key IDs to authorize on the installed system. Forces replacement if changed.",
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
			},
			"ssh_password_authentication": schema.BoolAttribute{
				Optional:    true,
				Description: "When true, enables SSH password authentication on the installed system. Forces replacement if changed.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"custom_script": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "Optional post-install bootstrap script executed by netcup's native customScript " +
					"mechanism. May contain secrets, so it is treated as Sensitive. Forces replacement " +
					"(a reinstall) if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"email_to_executing_user": schema.BoolAttribute{
				Optional:    true,
				Description: "When true, netcup emails the executing user on completion. Forces replacement if changed.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"triggers": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Arbitrary map of values that, when changed, force a replacement (re-run the " +
					"reinstall) even if the install inputs are unchanged — e.g. a script content hash or a " +
					"rotation timestamp. Analogous to terraform_data / null_resource `triggers`.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
			"wait": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "When true (default), apply waits for the reinstall task to reach a terminal state via WaitForTask before returning.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The server ID (same as server_id; used as the resource identifier for import).",
			},
			"task_id": schema.StringAttribute{
				Computed:    true,
				Description: "The UUID of the most recent reinstall task, or null if the reinstall completed synchronously or no UUID was returned.",
			},
		},
	}
}

func (r *serverReinstallResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// buildSetup assembles a netcup.ServerImageSetup from the resource model, sending
// only caller-set (non-null) fields so the request mirrors the config. Appends a
// diagnostic and returns ok=false if a list element cannot be read.
func buildSetup(ctx context.Context, model *serverReinstallResourceModel, diags *diag.Diagnostics) (netcup.ServerImageSetup, bool) {
	var setup netcup.ServerImageSetup

	flavour := int32(model.ImageFlavourID.ValueInt64())
	setup.ImageFlavourID = &flavour

	if !model.DiskName.IsNull() && !model.DiskName.IsUnknown() {
		v := model.DiskName.ValueString()
		setup.DiskName = &v
	}
	if !model.RootPartitionFullDiskSize.IsNull() && !model.RootPartitionFullDiskSize.IsUnknown() {
		v := model.RootPartitionFullDiskSize.ValueBool()
		setup.RootPartitionFullDiskSize = &v
	}
	if !model.Hostname.IsNull() && !model.Hostname.IsUnknown() {
		v := model.Hostname.ValueString()
		setup.Hostname = &v
	}
	if !model.Locale.IsNull() && !model.Locale.IsUnknown() {
		v := model.Locale.ValueString()
		setup.Locale = &v
	}
	if !model.Timezone.IsNull() && !model.Timezone.IsUnknown() {
		v := model.Timezone.ValueString()
		setup.Timezone = &v
	}
	if !model.AdditionalUserUsername.IsNull() && !model.AdditionalUserUsername.IsUnknown() {
		v := model.AdditionalUserUsername.ValueString()
		setup.AdditionalUserUsername = &v
	}
	if !model.AdditionalUserPassword.IsNull() && !model.AdditionalUserPassword.IsUnknown() {
		v := model.AdditionalUserPassword.ValueString()
		setup.AdditionalUserPassword = &v
	}
	if !model.SSHKeyIDs.IsNull() && !model.SSHKeyIDs.IsUnknown() {
		var ids []int64
		diags.Append(model.SSHKeyIDs.ElementsAs(ctx, &ids, false)...)
		if diags.HasError() {
			return setup, false
		}
		keys := make([]int32, len(ids))
		for i, id := range ids {
			keys[i] = int32(id)
		}
		setup.SSHKeyIDs = keys
	}
	if !model.SSHPasswordAuthentication.IsNull() && !model.SSHPasswordAuthentication.IsUnknown() {
		v := model.SSHPasswordAuthentication.ValueBool()
		setup.SSHPasswordAuthentication = &v
	}
	if !model.CustomScript.IsNull() && !model.CustomScript.IsUnknown() {
		v := model.CustomScript.ValueString()
		setup.CustomScript = &v
	}
	if !model.EmailToExecutingUser.IsNull() && !model.EmailToExecutingUser.IsUnknown() {
		v := model.EmailToExecutingUser.ValueBool()
		setup.EmailToExecutingUser = &v
	}

	return setup, true
}

func (r *serverReinstallResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_server_reinstall.",
		)
		return
	}

	var plan serverReinstallResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := parseServerID(plan.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id", err.Error())
		return
	}

	setup, ok := buildSetup(ctx, &plan, &resp.Diagnostics)
	if !ok {
		return
	}

	task, err := r.client.ReinstallServer(ctx, id, setup)
	if err != nil {
		// Any error from ReinstallServer means the reinstall was NOT accepted:
		// ErrPreDispatch (token/request build), a 4xx/5xx *APIError (incl. 422
		// ValidationError), or a transport/decode error. The install did not start,
		// so surface an error and persist NO state — the next apply re-runs Create.
		d, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(d)
		return
	}

	plan.ID = types.StringValue(plan.ServerID.ValueString())
	plan.TaskID = types.StringNull()
	if task != nil && task.UUID != "" {
		plan.TaskID = types.StringValue(task.UUID)
	}

	// wait=false, or a synchronous 200 (task == nil): nothing to poll. Persist the
	// accepted state.
	if !plan.Wait.ValueBool() || task == nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	// Bound task polling so an apply/CI run can never hang if netcup leaves the task
	// non-terminal.
	waitCtx, cancel := context.WithTimeout(ctx, reinstallTaskTimeout)
	defer cancel()
	if _, err := r.client.WaitForTask(waitCtx, task.UUID); err != nil {
		var taskErr *netcup.TaskError
		if errors.As(err, &taskErr) {
			// Confirmed terminal FAILURE (ERROR/CANCELED/ROLLBACK): the reinstall
			// definitively failed. Surface an error and persist NO state so the next
			// apply re-runs the reinstall (recovery from a half-installed server).
			d, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(d)
			return
		}
		// INDETERMINATE (deadline exceeded, canceled apply, transport error): the
		// reinstall was accepted (the task exists) and is likely still running.
		// Persist state + warn rather than error — erroring would drop the resource
		// from state and the next apply would WIPE THE SERVER AGAIN.
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddWarning(
			"netcup reinstall task completion could not be confirmed",
			fmt.Sprintf(
				"The reinstall was accepted by netcup (task %s), but waiting for it to reach a terminal "+
					"state was interrupted (%s) — e.g. the apply was canceled or the %s task-polling "+
					"deadline was exceeded. Canceling the wait does NOT cancel the remote reinstall, so it "+
					"is likely still running.\n\n"+
					"The resource has been recorded in Terraform state to avoid re-issuing the (destructive) "+
					"reinstall on the next apply. Check the server / task status in the SCP control panel.",
				task.UUID, err.Error(), reinstallTaskTimeout,
			),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverReinstallResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured.",
		)
		return
	}

	var state serverReinstallResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := parseServerID(state.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id in state", err.Error())
		return
	}

	// Read is deliberately thin (ADR-0001): a reinstall leaves no durable object to
	// GET, and the installed OS state is not recoverable from the API — treating the
	// install inputs as drift would produce permanent spurious diffs. So Read only
	// confirms the server still exists; a 404 removes the resource from state.
	if _, err := r.client.GetServer(ctx, id); err != nil {
		d, gone := apiErrorToDiag(err, false)
		if gone {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(d)
		return
	}

	// Normalize a null/unknown `wait` (e.g. after import) to the schema default so
	// the first post-import plan is clean rather than showing a spurious wait-only diff.
	if state.Wait.IsNull() || state.Wait.IsUnknown() {
		state.Wait = types.BoolValue(true)
	}
	state.ID = types.StringValue(state.ServerID.ValueString())

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update handles ONLY changes that do not force replacement — in practice just
// `wait`, since every install input and `triggers` carry RequiresReplace. It never
// reinstalls (that path is always a replace, per ADR-0001): a wipe must never hide
// behind an in-place `~ update`. It carries the computed `id`/`task_id` forward
// from prior state so no reinstall is implied.
func (r *serverReinstallResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serverReinstallResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var prior serverReinstallResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = types.StringValue(plan.ServerID.ValueString())
	plan.TaskID = prior.TaskID

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete is intentionally a no-op: destroying this resource must NEVER reinstall
// or wipe the server. It only removes the resource from Terraform state.
func (r *serverReinstallResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// No-op: state-only removal. The server is NOT reinstalled or wiped.
}

func (r *serverReinstallResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Validate that the import ID is a numeric server ID, reusing parseServerID so
	// the import path stays in sync with the parse rule used everywhere else.
	if _, err := parseServerID(req.ID); err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("The import ID must be a numeric server ID; got %q.", req.ID),
		)
		return
	}

	// Set both `id` and `server_id` from the import ID so the subsequent Read can
	// parse server_id. NOTE: install inputs (image_flavour_id, etc.) are not
	// recoverable from the API, so an imported resource whose config supplies them
	// will plan a replacement (a reinstall) on the next apply — see ADR-0001.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), req.ID)...)
}
