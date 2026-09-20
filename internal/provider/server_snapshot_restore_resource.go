package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// serverSnapshotRestoreTaskTimeout bounds how long Create blocks polling an
// accepted snapshot restore task via WaitForTask before giving up. A snapshot
// restore is typically faster than a native OS reinstall, so the bound is
// tighter than reinstallTaskTimeout. When the deadline fires WaitForTask
// returns context.DeadlineExceeded, which is NOT a *netcup.TaskError, so
// Create treats it as INDETERMINATE (persist + warn).
const serverSnapshotRestoreTaskTimeout = 30 * time.Minute

var _ resource.Resource = &serverSnapshotRestoreResource{}
var _ resource.ResourceWithConfigure = &serverSnapshotRestoreResource{}
var _ resource.ResourceWithImportState = &serverSnapshotRestoreResource{}

// serverSnapshotRestoreResource performs a one-shot, destructive restore of a
// netcup server snapshot via the SDK RestoreSnapshot (POST
// /v1/servers/{id}/snapshots/{name}/revert). See ADR-0002
// (docs/adr/0002-snapshot-restore-resource-lifecycle.md) for the lifecycle
// model: restore inputs and `triggers` use RequiresReplace so every restore
// shows as a resource replacement in the plan; Delete is a no-op; Read is thin.
//
// DESTRUCTIVE: applying (or replacing) this resource REVERTS the server to
// the specified snapshot.
type serverSnapshotRestoreResource struct {
	client *netcup.Client
}

// serverSnapshotRestoreResourceModel mirrors the Terraform schema for
// netcup_server_snapshot_restore. The computed fields map 1:1 onto
// netcup.TaskInfo.
type serverSnapshotRestoreResourceModel struct {
	ServerID     types.String `tfsdk:"server_id"`
	SnapshotName types.String `tfsdk:"snapshot_name"`
	Triggers     types.Map    `tfsdk:"triggers"`
	Wait         types.Bool   `tfsdk:"wait"`
	ID           types.String `tfsdk:"id"`
	TaskID       types.String `tfsdk:"task_id"`
}

// NewServerSnapshotRestoreResource returns a new netcup_server_snapshot_restore resource factory.
func NewServerSnapshotRestoreResource() resource.Resource {
	return &serverSnapshotRestoreResource{}
}

func (r *serverSnapshotRestoreResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "netcup_server_snapshot_restore"
}

func (r *serverSnapshotRestoreResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Performs a one-shot, destructive restore of a netcup server snapshot via the SCP API " +
			"(POST /v1/servers/{id}/snapshots/{name}/revert). WARNING: This is DESTRUCTIVE — the restore " +
			"REVERTS the server to the specified snapshot, discarding all changes made since the snapshot. " +
			"Changing `server_id`, `snapshot_name`, or `triggers` forces a replacement, which RE-RUNS the restore. " +
			"Destroying this resource is a no-op — it does NOT revert the server.",
		Attributes: map[string]schema.Attribute{
			"server_id": schema.StringAttribute{
				Required:    true,
				Description: "The numeric server ID to restore. Forces replacement (a restore) if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"snapshot_name": schema.StringAttribute{
				Required:    true,
				Description: "The snapshot name in SCP to restore to. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"triggers": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Arbitrary map of values that, when changed, force a replacement (re-run the restore) " +
					"even if the restore inputs are unchanged — e.g. a snapshot version hash or a rotation timestamp. " +
					"Analogous to terraform_data / null_resource `triggers`.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
			"wait": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "When true (default), apply waits for the restore task to reach a terminal state via WaitForTask before returning.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The server ID (same as server_id; used as the resource identifier for import).",
			},
			"task_id": schema.StringAttribute{
				Computed:    true,
				Description: "The UUID of the most recent restore task, or null if the restore completed synchronously or no UUID was returned.",
			},
		},
	}
}

func (r *serverSnapshotRestoreResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// isDefinitiveRestoreRejection reports whether an error from RestoreSnapshot
// PROVES the restore was not accepted by netcup. Only ErrPreDispatch
// (request never built/dispatched) and a 4xx *APIError (a definitive
// client rejection, e.g. 422 ValidationError) qualify. A 5xx *APIError
// does NOT: a reverse proxy may have returned 502/504 after the upstream
// already accepted the POST, so the destructive restore may be running and
// the outcome is indeterminate — the caller must persist state + warn.
func isDefinitiveRestoreRejection(err error) bool {
	if errors.Is(err, netcup.ErrPreDispatch) {
		return true
	}
	var apiErr *netcup.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode < 500
	}
	return false
}

func (r *serverSnapshotRestoreResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_server_snapshot_restore.",
		)
		return
	}

	var plan serverSnapshotRestoreResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := parseServerID(plan.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id", err.Error())
		return
	}

	snapshotName := plan.SnapshotName.ValueString()

	task, err := r.client.RestoreSnapshot(ctx, id, snapshotName)
	if err != nil {
		// Distinguish a DEFINITIVE rejection from an AMBIGUOUS dispatch outcome:
		//
		//  - errors.Is(err, netcup.ErrPreDispatch): the request was never built
		//    or dispatched (token acquisition / request construction failed).
		//    The restore definitively did not start → error, persist NO state so
		//    the next apply re-runs Create safely.
		//  - *netcup.APIError with a 4xx status (incl. 422 ValidationError): a
		//    definitive rejection by netcup → error, persist NO state.
		//  - *netcup.APIError with a 5xx status (e.g. a reverse-proxy 502/504
		//    where the upstream may have accepted the POST first): AMBIGUOUS →
		//    persist state + warn (handled below, alongside transport/decode).
		//  - Any other error (a transport error from http.Do, or a decode
		//    failure on a truncated 2xx body): AMBIGUOUS — the POST may already
		//    have been accepted by netcup and the destructive restore may be
		//    running. Persist state + warn (mirroring the indeterminate
		//    WaitForTask branch below) so the next apply does NOT revert the
		//    server a second time.
		if isDefinitiveRestoreRejection(err) {
			d, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(d)
			return
		}

		plan.ID = types.StringValue(plan.ServerID.ValueString())
		plan.TaskID = types.StringNull()
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddWarning(
			"netcup snapshot restore dispatch outcome could not be confirmed",
			fmt.Sprintf(
				"The restore request may have been accepted by netcup, but the outcome could not be confirmed (%s) "+
					"— e.g. a network failure or a truncated response after dispatch, or a 5xx from a reverse proxy "+
					"after the request was forwarded. The restore is likely already running.\n\n"+
					"The resource has been recorded in Terraform state to avoid re-issuing the (destructive) "+
					"restore on the next apply. Check the server / task status in the SCP control panel.",
				err.Error(),
			),
		)
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
	waitCtx, cancel := context.WithTimeout(ctx, serverSnapshotRestoreTaskTimeout)
	defer cancel()
	if _, err := r.client.WaitForTask(waitCtx, task.UUID); err != nil {
		var taskErr *netcup.TaskError
		if errors.As(err, &taskErr) {
			// Confirmed terminal FAILURE (ERROR/CANCELED/ROLLBACK): the restore
			// definitively failed. Surface an error and persist NO state so the next
			// apply retries the restore (recovery from a half-restored server).
			d, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(d)
			return
		}
		// INDETERMINATE (deadline exceeded, canceled apply, transport error): the
		// task exists and is likely still running. Persist state + warn rather than
		// error — erroring would drop the resource from state and the next apply
		// would REVERT THE SERVER AGAIN.
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddWarning(
			"netcup snapshot restore task completion could not be confirmed",
			fmt.Sprintf(
				"The restore was accepted by netcup (task %s), but waiting for it to reach a terminal "+
					"state was interrupted (%s) — e.g. the apply was canceled or the %s task-polling "+
					"deadline was exceeded. Canceling the wait does NOT cancel the remote restore, so it "+
					"is likely still running.\n\n"+
					"The resource has been recorded in Terraform state to avoid re-issuing the (destructive) "+
					"restore on the next apply. Check the server / task status in the SCP control panel.",
				task.UUID, err.Error(), serverSnapshotRestoreTaskTimeout,
			),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverSnapshotRestoreResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured.",
		)
		return
	}

	var state serverSnapshotRestoreResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := parseServerID(state.ServerID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid server_id in state", err.Error())
		return
	}

	// Read is deliberately thin (ADR-0002): a restore leaves no durable object to
	// GET, and the reverted server state is not recoverable from the API — treating
	// the restore inputs as drift would produce permanent spurious diffs. So Read only
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
// `wait`, since every restore input and `triggers` carry RequiresReplace. It never
// restores (that path is always a replace, per ADR-0002): a revert must never hide
// behind an in-place `~ update`. It carries the computed `id`/`task_id` forward
// from prior state so no restore is implied.
func (r *serverSnapshotRestoreResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serverSnapshotRestoreResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var prior serverSnapshotRestoreResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = types.StringValue(plan.ServerID.ValueString())
	plan.TaskID = prior.TaskID

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete is intentionally a no-op: destroying this resource must NEVER revert
// or restore the server. It only removes the resource from Terraform state.
func (r *serverSnapshotRestoreResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// No-op: state-only removal. The server is NOT reverted or restored.
}

func (r *serverSnapshotRestoreResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Parse import ID in format "server_id:snapshot_name" or just "server_id".
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
	// Set id, server_id, and snapshot_name from the import ID so Read can
	// locate the server and the first plan won't replace on a missing required attribute.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(serverID))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), types.StringValue(serverID))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("snapshot_name"), types.StringValue(name))...)
}
