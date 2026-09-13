package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// snapshotTaskTimeout bounds how long Create and Delete block polling an
// accepted snapshot task via WaitForTask before giving up. A snapshot is
// faster than a native OS reinstall, so the bound is tighter than
// reinstallTaskTimeout. When the deadline fires WaitForTask returns
// context.DeadlineExceeded, which is NOT a *netcup.TaskError, so Create treats
// it as INDETERMINATE (persist + warn) while Delete treats it as an error
// (deleting is idempotent, so the next apply retries safely).
const snapshotTaskTimeout = 30 * time.Minute

// maxSnapshotNameLength is the SCP's maximum snapshot name length.
const maxSnapshotNameLength = 255

// snapshotNameLengthValidator rejects snapshot names longer than
// maxSnapshotNameLength at plan time instead of after a failed create.
// It counts characters (not bytes) so multi-byte names are measured the way
// the API does.
type snapshotNameLengthValidator struct{}

func (v snapshotNameLengthValidator) Description(_ context.Context) string {
	return fmt.Sprintf("the name must be at most %d characters long", maxSnapshotNameLength)
}

func (v snapshotNameLengthValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v snapshotNameLengthValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsUnknown() || req.ConfigValue.IsNull() {
		return
	}
	if n := utf8.RuneCountInString(req.ConfigValue.ValueString()); n > maxSnapshotNameLength {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Snapshot name too long",
			fmt.Sprintf("The snapshot name must be at most %d characters long; the given name is %d characters long.", maxSnapshotNameLength, n),
		)
	}
}

var _ resource.Resource = &serverSnapshotResource{}
var _ resource.ResourceWithConfigure = &serverSnapshotResource{}
var _ resource.ResourceWithImportState = &serverSnapshotResource{}
var _ resource.ResourceWithValidateConfig = &serverSnapshotResource{}

// serverSnapshotResource manages a single snapshot of a netcup server via the
// SCP snapshot endpoints (POST/DELETE /v1/servers/{id}/snapshots). Every input
// attribute carries RequiresReplace: the SCP API has no snapshot update or
// rename, so any input change is planned as a destroy + create. Destroying the
// resource DELETES the snapshot.
type serverSnapshotResource struct {
	client *netcup.Client
}

// serverSnapshotResourceModel mirrors the Terraform schema for
// netcup_server_snapshot. The computed fields after TaskID map 1:1 onto
// netcup.SnapshotMinimal.
type serverSnapshotResourceModel struct {
	ServerID          types.String `tfsdk:"server_id"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	DiskName          types.String `tfsdk:"disk_name"`
	OnlineSnapshot    types.Bool   `tfsdk:"online_snapshot"`
	Wait              types.Bool   `tfsdk:"wait"`
	ID                types.String `tfsdk:"id"`
	UUID              types.String `tfsdk:"uuid"`
	CreationTime      types.String `tfsdk:"creation_time"`
	State             types.String `tfsdk:"state"`
	Online            types.Bool   `tfsdk:"online"`
	Exported          types.Bool   `tfsdk:"exported"`
	ExportedSizeInKiB types.Int64  `tfsdk:"exported_size_in_kib"`
	Disks             types.List   `tfsdk:"disks"`
	TaskID            types.String `tfsdk:"task_id"`
}

// NewServerSnapshotResource returns a new netcup_server_snapshot resource factory.
func NewServerSnapshotResource() resource.Resource {
	return &serverSnapshotResource{}
}

func (r *serverSnapshotResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "netcup_server_snapshot"
}

func (r *serverSnapshotResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a snapshot of a netcup server via the SCP API. Every input " +
			"attribute forces replacement (the API has no snapshot update or rename): " +
			"changing any input deletes the old snapshot and takes a new one. Destroying " +
			"this resource deletes the snapshot. Import with `server_id:snapshot_name` " +
			"(e.g. `123:pre-upgrade`).",
		Attributes: map[string]schema.Attribute{
			"server_id": schema.StringAttribute{
				Required:    true,
				Description: "The numeric server ID to snapshot. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				Description: "The snapshot name in SCP (max 255 characters). The name is the API's " +
					"identity for the snapshot — deletion is addressed by name. Forces replacement if changed.",
				Validators: []validator.String{
					snapshotNameLengthValidator{},
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Optional description for the snapshot. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"disk_name": schema.StringAttribute{
				Optional: true,
				Description: "The disk to snapshot for an OFFLINE snapshot (required unless " +
					"`online_snapshot` is true). Mutually exclusive with `online_snapshot`. " +
					"Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"online_snapshot": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
				Description: "When true, take an ONLINE snapshot of the running server without naming a " +
					"disk (mutually exclusive with `disk_name`; rejected by the API on UEFI systems). " +
					"Forces replacement if changed.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"wait": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
				Description: "When true (default), apply waits for the snapshot task to reach a terminal " +
					"state via WaitForTask before returning. Forces replacement if changed.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The UUID of the snapshot (same as `uuid`; used as the resource identifier).",
			},
			"uuid": schema.StringAttribute{
				Computed:    true,
				Description: "The UUID of the snapshot. Null when the create outcome could not be confirmed; the next refresh resolves it.",
			},
			"creation_time": schema.StringAttribute{
				Computed:    true,
				Description: "The RFC 3339 timestamp when the snapshot was created.",
			},
			"state": schema.StringAttribute{
				Computed:    true,
				Description: "The state of the server when the snapshot was taken, as the SCP `ServerState` enum in UPPERCASE (e.g. RUNNING, SHUTOFF, PAUSED, PMSUSPENDED).",
			},
			"online": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the snapshot was taken while the server was online.",
			},
			"exported": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the snapshot has been exported.",
			},
			"exported_size_in_kib": schema.Int64Attribute{
				Computed:    true,
				Description: "The size of the exported snapshot in KiB. Null when the snapshot has not been exported.",
			},
			"disks": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "The list of disk identifiers included in the snapshot.",
			},
			"task_id": schema.StringAttribute{
				Computed:    true,
				Description: "The UUID of the snapshot task, or null when no task UUID was returned.",
			},
		},
	}
}

func (r *serverSnapshotResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// isDefinitiveSnapshotRejection reports whether a CreateSnapshot error PROVES
// the snapshot task was not started: a pre-dispatch failure (request never
// sent) or a 4xx client rejection (netcup refused the request, so nothing was
// created). A 5xx, transport, or decode error is AMBIGUOUS — netcup may have
// accepted the POST before the failure — so it is NOT definitive; the caller
// persists state + warn instead (see Create).
func isDefinitiveSnapshotRejection(err error) bool {
	if errors.Is(err, netcup.ErrPreDispatch) {
		return true
	}
	var apiErr *netcup.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode < 500
	}
	return false
}

// validateSnapshotMode enforces the same online/offline rules as the v0.7.0 CLI
// (`netcupctl server snapshots create`): an online snapshot captures the
// running server and must NOT name a disk, while an offline snapshot must name
// the target disk. An unknown value on either side cannot be decided at plan
// time, so validation is skipped (the API remains the final arbiter and
// rejects an invalid combination with a 4xx).
func validateSnapshotMode(online types.Bool, diskName types.String, diags *diag.Diagnostics) {
	if online.IsUnknown() || diskName.IsUnknown() {
		return
	}
	onlineSet := online.ValueBool() // null is treated as false (the schema default)
	diskSet := !diskName.IsNull() && strings.TrimSpace(diskName.ValueString()) != ""

	if onlineSet && diskSet {
		diags.AddAttributeError(
			path.Root("online_snapshot"),
			"online_snapshot and disk_name are mutually exclusive",
			"An online snapshot captures the running server without naming a disk. Remove disk_name to take an online snapshot.",
		)
		return
	}
	if !onlineSet && !diskSet {
		diags.AddAttributeError(
			path.Root("disk_name"),
			"offline snapshot requires disk_name",
			"An offline snapshot must name the target disk. Set disk_name, or set online_snapshot = true to snapshot the running server without naming a disk.",
		)
	}
}

func (r *serverSnapshotResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config serverSnapshotResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateSnapshotMode(config.OnlineSnapshot, config.DiskName, &resp.Diagnostics)
}

// resetSnapshotComputed replaces the model's computed attributes (planned
// unknown on a create) with null — or the given task ID — so the model can be
// persisted as post-apply state: a post-apply state may not contain unknown
// values, and a snapshot whose UUID could not be confirmed has a null UUID
// until the next refresh reconciles it by name.
func resetSnapshotComputed(m *serverSnapshotResourceModel, taskID types.String) {
	m.ID = types.StringNull()
	m.UUID = types.StringNull()
	m.CreationTime = types.StringNull()
	m.State = types.StringNull()
	m.Online = types.BoolNull()
	m.Exported = types.BoolNull()
	m.ExportedSizeInKiB = types.Int64Null()
	m.Disks = types.ListNull(types.StringType)
	m.TaskID = taskID
}

// applySnapshot fills the model's computed fields (id/uuid/creation_time/
// state/online/exported/exported_size_in_kib/disks) from a listed snapshot.
// TaskID and the input fields are left untouched. RFC3339Nano preserves
// fractional seconds when the API returns them, matching the
// netcup_server_snapshots data source.
func applySnapshot(ctx context.Context, m *serverSnapshotResourceModel, s netcup.SnapshotMinimal) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(s.UUID)
	m.UUID = types.StringValue(s.UUID)
	m.CreationTime = types.StringValue(s.CreationTime.Format(time.RFC3339Nano))
	m.State = types.StringValue(s.State)
	m.Online = types.BoolValue(s.Online)
	m.Exported = types.BoolValue(s.Exported)
	if s.ExportedSizeInKiB != nil {
		m.ExportedSizeInKiB = types.Int64Value(*s.ExportedSizeInKiB)
	} else {
		m.ExportedSizeInKiB = types.Int64Null()
	}
	disks := make([]types.String, len(s.Disks))
	for i, d := range s.Disks {
		disks[i] = types.StringValue(d)
	}
	diskList, d := types.ListValueFrom(ctx, types.StringType, disks)
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}
	m.Disks = diskList
	return diags
}

// latestSnapshotByName returns the most recently created snapshot with the
// given name, or nil. A server can hold several snapshots with the same name
// over time (e.g. across destroy/create cycles), and name is the identity
// DeleteSnapshot addresses, so the newest creation is the one this resource
// owns.
func latestSnapshotByName(snapshots []netcup.SnapshotMinimal, name string) *netcup.SnapshotMinimal {
	var best *netcup.SnapshotMinimal
	for i := range snapshots {
		if snapshots[i].Name != name {
			continue
		}
		if best == nil || snapshots[i].CreationTime.After(best.CreationTime) {
			best = &snapshots[i]
		}
	}
	return best
}

func (r *serverSnapshotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_server_snapshot.")
		return
	}

	var plan serverSnapshotResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID, err := parseServerID(plan.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id", err.Error())
		return
	}

	opts := netcup.ServerSnapshotCreate{Name: plan.Name.ValueString()}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		v := plan.Description.ValueString()
		opts.Description = &v
	}
	if !plan.DiskName.IsNull() && !plan.DiskName.IsUnknown() {
		v := plan.DiskName.ValueString()
		opts.DiskName = &v
	}
	if !plan.OnlineSnapshot.IsNull() && !plan.OnlineSnapshot.IsUnknown() {
		opts.OnlineSnapshot = plan.OnlineSnapshot.ValueBool()
	}

	task, err := r.client.CreateSnapshot(ctx, serverID, opts)
	if err != nil {
		if isDefinitiveSnapshotRejection(err) {
			// Definitively not created (pre-dispatch or 4xx): plain error, no
			// state, so the next apply retries safely.
			d, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(d)
			return
		}
		// Ambiguous (5xx / transport / decode after dispatch): the snapshot MAY
		// exist. Record it without a confirmed UUID and warn — the next refresh
		// adopts it by name if it exists.
		resetSnapshotComputed(&plan, types.StringNull())
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddWarning(
			"Netcup snapshot creation outcome could not be confirmed",
			fmt.Sprintf(
				"The snapshot request may have been accepted by netcup, but the outcome could not be confirmed (%s). "+
					"A snapshot named %q may exist on server %d.\n\n"+
					"The resource was recorded in state without a confirmed UUID so the next apply does not blindly "+
					"re-create it; the next refresh will adopt the snapshot by name if it exists. Check the SCP "+
					"control panel before re-applying, since re-running the create may mint a duplicate.",
				err.Error(), plan.Name.ValueString(), serverID,
			),
		)
		return
	}

	plan.TaskID = types.StringNull()
	if task != nil && task.UUID != "" {
		plan.TaskID = types.StringValue(task.UUID)
	}

	wait := true
	if !plan.Wait.IsNull() && !plan.Wait.IsUnknown() {
		wait = plan.Wait.ValueBool()
	}

	// wait=false (or a synchronous response with no task UUID): record the
	// accepted snapshot. Its UUID is unknown until the snapshot appears in the
	// listing; the next refresh reconciles by name.
	if !wait || task == nil || task.UUID == "" {
		resetSnapshotComputed(&plan, plan.TaskID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	// Bound task polling so an apply/CI run can never hang if netcup leaves the
	// task non-terminal.
	waitCtx, cancel := context.WithTimeout(ctx, snapshotTaskTimeout)
	defer cancel()
	if _, err := r.client.WaitForTask(waitCtx, task.UUID); err != nil {
		var taskErr *netcup.TaskError
		if errors.As(err, &taskErr) {
			// Confirmed terminal failure (ERROR/CANCELED/ROLLBACK): the snapshot
			// definitively did not complete. Surface an error and persist NO
			// state so the next apply retries.
			d, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(d)
			return
		}
		// INDETERMINATE (deadline exceeded, canceled apply, transport error):
		// the task exists and is likely still running. Persist + warn so the
		// next apply does not mint a duplicate.
		resetSnapshotComputed(&plan, plan.TaskID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddWarning(
			"Netcup snapshot task completion could not be confirmed",
			fmt.Sprintf(
				"The snapshot was accepted by netcup (task %s), but waiting for it to reach a terminal "+
					"state was interrupted (%s) — e.g. the apply was canceled or the %s task-polling "+
					"deadline was exceeded. Canceling the wait does NOT cancel the remote task, so the "+
					"snapshot is likely still being taken.\n\n"+
					"The resource was recorded in state so the next apply does not mint a duplicate; the "+
					"next refresh will adopt the snapshot by name once it appears. Check the task in the SCP "+
					"control panel.",
				task.UUID, err.Error(), snapshotTaskTimeout,
			),
		)
		return
	}

	// The task FINISHED: the snapshot should now be listed. Find it by name
	// (the API's own identity — deletion is addressed by name) and adopt its
	// UUID.
	snapshots, err := r.client.ListSnapshots(ctx, serverID)
	if err != nil {
		// The snapshot finished but the listing failed: the UUID cannot be
		// confirmed. Persist + warn rather than error — the snapshot exists.
		resetSnapshotComputed(&plan, plan.TaskID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddWarning(
			"Netcup snapshot could not be listed after creation",
			fmt.Sprintf(
				"The snapshot task finished, but the server's snapshots could not be listed to confirm the "+
					"snapshot's UUID (%s). The resource was recorded in state; the next refresh will adopt "+
					"the snapshot by name.",
				err.Error(),
			),
		)
		return
	}
	found := latestSnapshotByName(snapshots, plan.Name.ValueString())
	if found == nil {
		// Task finished but the snapshot is not listed yet: persist + warn; the
		// next refresh reconciles by name.
		resetSnapshotComputed(&plan, plan.TaskID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddWarning(
			"Netcup snapshot not found after creation",
			fmt.Sprintf(
				"The snapshot task finished, but no snapshot named %q was listed on server %d. "+
					"The resource was recorded in state; the next refresh will adopt it by name once it "+
					"appears.",
				plan.Name.ValueString(), serverID,
			),
		)
		return
	}

	if diags := applySnapshot(ctx, &plan, *found); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverSnapshotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_server_snapshot.")
		return
	}

	var state serverSnapshotResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID, err := parseServerID(state.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id in state", err.Error())
		return
	}

	snapshots, err := r.client.ListSnapshots(ctx, serverID)
	if err != nil {
		d, gone := apiErrorToDiag(err, false)
		if gone {
			// The server itself is gone (404), so its snapshots are gone too.
			resp.State.RemoveResource(ctx)
			return
		}
		// Any other list error (5xx, transport) does NOT prove the snapshot is
		// gone: treat it as a hard error rather than dropping the resource.
		resp.Diagnostics.Append(d)
		return
	}

	// Match by UUID (the snapshot's real identity). Fall back to name when the
	// UUID is not known yet — a fresh import, or a create whose outcome was
	// unconfirmed and was never resolved by a refresh.
	var found *netcup.SnapshotMinimal
	switch {
	case !state.UUID.IsNull() && !state.UUID.IsUnknown() && state.UUID.ValueString() != "":
		for i := range snapshots {
			if snapshots[i].UUID == state.UUID.ValueString() {
				found = &snapshots[i]
				break
			}
		}
	case !state.Name.IsNull() && !state.Name.IsUnknown() && state.Name.ValueString() != "":
		found = latestSnapshotByName(snapshots, state.Name.ValueString())
	}
	if found == nil {
		// Drift: the snapshot no longer exists on the server.
		resp.State.RemoveResource(ctx)
		return
	}

	// Backfill inputs only when null (import / unconfirmed create), so refreshes
	// never diverge from the configured values.
	if state.Name.IsNull() {
		state.Name = types.StringValue(found.Name)
	}
	if state.Description.IsNull() && found.Description != nil {
		state.Description = types.StringValue(*found.Description)
	}
	if state.OnlineSnapshot.IsNull() {
		state.OnlineSnapshot = types.BoolValue(found.Online)
	}
	if state.DiskName.IsNull() && !found.Online && len(found.Disks) == 1 {
		// An offline snapshot's requested disk name is not separately returned;
		// a single-disk snapshot is unambiguous. (Multi-disk or online snapshots
		// leave disk_name null — the practitioner must set it explicitly.)
		state.DiskName = types.StringValue(found.Disks[0])
	}
	if state.Wait.IsNull() || state.Wait.IsUnknown() {
		state.Wait = types.BoolValue(true)
	}

	if diags := applySnapshot(ctx, &state, *found); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is never called by the framework: every input attribute carries
// RequiresReplace, so any input change is planned as a replace (destroy +
// create). The implementation exists for interface completeness and only
// carries the computed attributes forward from prior state, so no snapshot
// operation is implied by an in-place update.
func (r *serverSnapshotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serverSnapshotResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var prior serverSnapshotResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = prior.ID
	plan.UUID = prior.UUID
	plan.CreationTime = prior.CreationTime
	plan.State = prior.State
	plan.Online = prior.Online
	plan.Exported = prior.Exported
	plan.ExportedSizeInKiB = prior.ExportedSizeInKiB
	plan.Disks = prior.Disks
	plan.TaskID = prior.TaskID

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverSnapshotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_server_snapshot.")
		return
	}

	var state serverSnapshotResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID, err := parseServerID(state.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id in state", err.Error())
		return
	}
	name := state.Name.ValueString()
	if strings.TrimSpace(name) == "" {
		// Deletion is addressed by name; with no name the snapshot cannot be
		// identified and must not be guessed at. Erroring keeps the resource in
		// state so the operator can fix it (terraform state rm if it is gone).
		resp.Diagnostics.AddError(
			"Invalid snapshot name in state",
			"The snapshot name in state is empty, so the snapshot cannot be identified for deletion. "+
				"If the snapshot is known to be gone, remove the resource from state with `terraform state rm`.",
		)
		return
	}

	task, err := r.client.DeleteSnapshot(ctx, serverID, name)
	if err != nil {
		d, gone := apiErrorToDiag(err, false)
		if gone {
			// Already gone: the desired end state is reached.
			return
		}
		// Any other failure (4xx/5xx/transport) errors: deleting is idempotent
		// (a retry 404s → success), so the next apply retries safely.
		resp.Diagnostics.Append(d)
		return
	}

	wait := true
	if !state.Wait.IsNull() && !state.Wait.IsUnknown() {
		wait = state.Wait.ValueBool()
	}
	if !wait || task == nil || task.UUID == "" {
		return
	}

	waitCtx, cancel := context.WithTimeout(ctx, snapshotTaskTimeout)
	defer cancel()
	if _, err := r.client.WaitForTask(waitCtx, task.UUID); err != nil {
		// A failed or unconfirmed delete task is an error (unlike Create, where
		// an indeterminate create must be persisted to avoid a duplicate):
		// deleting is idempotent, so the next apply retries safely.
		d, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(d)
		return
	}
}

func (r *serverSnapshotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// The import ID is `server_id:snapshot_name`, split on the FIRST colon
	// (server IDs are numeric and never contain a colon; the name may).
	sep := strings.Index(req.ID, ":")
	if sep <= 0 || sep == len(req.ID)-1 {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("The import ID must be of the form server_id:snapshot_name (e.g. 123:pre-upgrade); got %q.", req.ID),
		)
		return
	}
	serverID := req.ID[:sep]
	name := req.ID[sep+1:]
	if _, err := parseServerID(serverID); err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("The server part of the import ID must be a numeric server ID; got %q.", req.ID),
		)
		return
	}

	// Only server_id and name are seeded; the subsequent Read resolves the UUID
	// by name and backfills the rest.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), serverID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
}
