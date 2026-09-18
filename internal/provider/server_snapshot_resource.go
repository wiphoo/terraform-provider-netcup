package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
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

// snapshotConflictSettleTimeout bounds how long Create's preflight polls for
// a same-name snapshot to disappear before treating it as a collision: a
// replacement create is planned right after a wait = false destroy whose
// async deletion is still running, so the old name stays listed until the
// deletion settles (the same race exists, briefly, after a wait = true delete
// whose completion lags the listing). A var, not a const, so tests can
// shorten it.
var snapshotConflictSettleTimeout = 5 * time.Minute

// maxSnapshotStringFieldLength is the SCP's maximum length for snapshot
// string fields (name, description).
const maxSnapshotStringFieldLength = 255

// snapshotStringFieldLengthValidator rejects a snapshot string field (name,
// description) longer than maxSnapshotStringFieldLength at plan time instead
// of after a failed create. It counts characters (not bytes) so multi-byte
// values are measured the way the API does.
type snapshotStringFieldLengthValidator struct{ field string }

func (v snapshotStringFieldLengthValidator) Description(_ context.Context) string {
	return fmt.Sprintf("the %s must be at most %d characters long", v.field, maxSnapshotStringFieldLength)
}

func (v snapshotStringFieldLengthValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v snapshotStringFieldLengthValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsUnknown() || req.ConfigValue.IsNull() {
		return
	}
	if n := utf8.RuneCountInString(req.ConfigValue.ValueString()); n > maxSnapshotStringFieldLength {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			fmt.Sprintf("Snapshot %s too long", v.field),
			fmt.Sprintf("The snapshot %s must be at most %d characters long; the given value is %d characters long.", v.field, maxSnapshotStringFieldLength, n),
		)
	}
}

// snapshotNameBlankValidator rejects an empty or whitespace-only snapshot
// name at plan time, matching the SDK's ErrPreDispatch guard that would
// otherwise reject it during apply.
type snapshotNameBlankValidator struct{}

func (v snapshotNameBlankValidator) Description(_ context.Context) string {
	return "the name must not be blank"
}

func (v snapshotNameBlankValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v snapshotNameBlankValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsUnknown() || req.ConfigValue.IsNull() {
		return
	}
	if strings.TrimSpace(req.ConfigValue.ValueString()) == "" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Snapshot name is blank",
			"The snapshot name must not be empty or whitespace-only.",
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
	CreateRequestedAt types.String `tfsdk:"create_requested_at"`
	PreCreateUUIDs    types.List   `tfsdk:"pre_create_uuids"`
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
					snapshotStringFieldLengthValidator{field: "name"},
					snapshotNameBlankValidator{},
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Optional description for the snapshot (max 255 characters). Forces replacement if changed.",
				Validators: []validator.String{
					snapshotStringFieldLengthValidator{field: "description"},
				},
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
			"create_requested_at": schema.StringAttribute{
				Computed: true,
				Description: "The RFC 3339 time the create request was dispatched. Used to identify the " +
					"snapshot created by an unconfirmed create when no task UUID is available; null for " +
					"imported resources.",
			},
			"pre_create_uuids": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "The UUIDs of the snapshots that already existed on the server when this " +
					"resource's create was dispatched. Reconciliation of an unconfirmed create never " +
					"adopts one of these, so a pre-existing same-name snapshot is excluded by identity " +
					"rather than by timestamp; null for imported resources.",
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

// sameNameCreateCandidates returns every same-name snapshot this create could
// own, applying every server-side ownership proof available:
//
//   - exclude (the snapshots listed before the create was dispatched) removes
//     pre-existing same-name snapshots by identity;
//   - startedAt (the create task's server-side start time) removes snapshots
//     created before the task started, covering the gap between the pre-create
//     listing and the dispatch — a snapshot a concurrent actor creates in
//     that gap is absent from exclude yet still not the one this create
//     produces.
//
// Both bounds come from the server, so no host clock is involved. The result
// is nil when neither proof applies (imports, state persisted before
// pre_create_uuids existed), in which case the caller falls back to
// unbounded matching.
func sameNameCreateCandidates(snapshots []netcup.SnapshotMinimal, name string, exclude map[string]bool, startedAt *time.Time) []netcup.SnapshotMinimal {
	if exclude == nil && startedAt == nil {
		return nil
	}
	out := []netcup.SnapshotMinimal{}
	for i := range snapshots {
		if snapshots[i].Name != name {
			continue
		}
		if exclude != nil && exclude[snapshots[i].UUID] {
			continue
		}
		if startedAt != nil && snapshots[i].CreationTime.Before(*startedAt) {
			continue
		}
		out = append(out, snapshots[i])
	}
	return out
}

// latestSameNameCreatedAfter returns the most recently created snapshot with
// the given name that was created no earlier than since (when since is
// non-nil), or nil. Bounding the match by the task's start time keeps a
// pre-existing same-name snapshot from being mistaken for one this task
// produced.
func latestSameNameCreatedAfter(snapshots []netcup.SnapshotMinimal, name string, since *time.Time) *netcup.SnapshotMinimal {
	var best *netcup.SnapshotMinimal
	for i := range snapshots {
		if snapshots[i].Name != name {
			continue
		}
		if since != nil && snapshots[i].CreationTime.Before(*since) {
			continue
		}
		if best == nil || snapshots[i].CreationTime.After(best.CreationTime) {
			best = &snapshots[i]
		}
	}
	return best
}

// createRequestedSince returns the recorded dispatch time (create_requested_at)
// used to bound adoption for an unconfirmed create, or nil when it is absent or
// unparseable — imported state carries no dispatch time, in which case
// latestSameNameCreatedAfter falls back to name-only matching.
func createRequestedSince(state *serverSnapshotResourceModel) *time.Time {
	v := state.CreateRequestedAt
	if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, v.ValueString())
	if err != nil {
		return nil
	}
	return &t
}

// preCreateUUIDSet returns the set of snapshot UUIDs that existed before this
// resource's create was dispatched (persisted in pre_create_uuids), or nil
// when the state carries no such set (imports, and state persisted before the
// attribute existed). A valid EMPTY set — nothing existed before the dispatch
// — is returned as a non-nil empty map: it is still proof that every listed
// snapshot is new, so the sole-candidate rule must keep applying.
func preCreateUUIDSet(state *serverSnapshotResourceModel) map[string]bool {
	v := state.PreCreateUUIDs
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	var uuids []string
	if diags := v.ElementsAs(context.Background(), &uuids, false); diags.HasError() {
		return nil
	}
	set := make(map[string]bool, len(uuids))
	for _, u := range uuids {
		set[u] = true
	}
	return set
}

// adoptUnconfirmed picks the snapshot an unconfirmed create should adopt from
// the server's listing, preferring identity over clocks:
//
//  1. the persisted pre-create UUID set, combined with the create task's
//     server-side start time when reported — a same-name snapshot is adoptable
//     only if it was NOT listed before the dispatch AND was created no earlier
//     than the task started;
//  2. the task's server-clock start time alone, when no set is persisted;
//  3. the host-clock dispatch time (create_requested_at) as a best-effort
//     bound, and name-only when even that is absent (imports).
//
// When a server-side proof applies, a snapshot is adopted only if it is the
// SOLE candidate: two or more new same-name snapshots cannot be attributed
// (this create's vs a concurrent one's), so nothing is adopted and the state
// is kept for the next refresh.
func adoptUnconfirmed(snapshots []netcup.SnapshotMinimal, name string, state *serverSnapshotResourceModel, taskStartedAt *time.Time) *netcup.SnapshotMinimal {
	if cands := sameNameCreateCandidates(snapshots, name, preCreateUUIDSet(state), taskStartedAt); cands != nil {
		if len(cands) == 1 {
			return &cands[0]
		}
		return nil
	}
	return latestSameNameCreatedAfter(snapshots, name, createRequestedSince(state))
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

	// Reconcile the server's existing snapshots BEFORE dispatching, so the
	// post-create adoption below cannot mistake a pre-existing same-name
	// snapshot for the one this request creates (name is the API's identity —
	// deletion is addressed by name). A listing failure means the existing set
	// cannot be confirmed, so the create is not dispatched under that
	// uncertainty (mirroring netcup_ssh_key's pre-create guard); persisting no
	// state lets the next apply retry safely.
	preExisting, err := r.client.ListSnapshots(ctx, serverID)
	if err != nil {
		d, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(d)
		return
	}
	preExistingUUIDs := make(map[string]bool, len(preExisting))
	for i := range preExisting {
		preExistingUUIDs[preExisting[i].UUID] = true
	}

	// A same-name snapshot already on the server collides with this create:
	// the API identifies snapshots by name (deletion is addressed by name), so
	// a second same-name snapshot would make the created one undeletable —
	// Delete refuses while two matches exist. The exception is a replacement
	// create: with wait = false, Delete returns as soon as the async DELETE is
	// accepted, so Terraform can plan this create while the old snapshot's
	// deletion is still running and its name still listed. Poll for the name
	// to clear (bounded): if it does, this create replaces the deleted
	// snapshot and proceeds; if the name persists, it is a real collision —
	// fail before the POST.
	name := plan.Name.ValueString()
	sameNameIn := func(list []netcup.SnapshotMinimal) *netcup.SnapshotMinimal {
		for i := range list {
			if list[i].Name == name {
				return &list[i]
			}
		}
		return nil
	}
	deadline := time.Now().Add(snapshotConflictSettleTimeout)
	for hit := sameNameIn(preExisting); hit != nil; {
		if !time.Now().Before(deadline) {
			resp.Diagnostics.AddError(
				"Snapshot name already in use",
				fmt.Sprintf(
					"Server %d has a snapshot named %q (UUID %s) and it was still listed %s after this apply started. "+
						"If this apply replaces a previous snapshot with wait = false, its deletion may still be "+
						"running — re-run apply once it completes. Otherwise import the existing snapshot "+
						"(`terraform import netcup_server_snapshot.<alias> %d:%s`) or choose a different name.",
					serverID, name, hit.UUID, snapshotConflictSettleTimeout, serverID, name,
				),
			)
			return
		}
		sleep := 5 * time.Second
		if rem := time.Until(deadline); sleep > rem {
			sleep = rem
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Snapshot create cancelled", ctx.Err().Error())
			return
		case <-time.After(sleep):
		}
		latest, err := r.client.ListSnapshots(ctx, serverID)
		if err != nil {
			d, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(d)
			return
		}
		preExisting = latest
		hit = sameNameIn(preExisting)
	}

	opts := netcup.ServerSnapshotCreate{Name: plan.Name.ValueString()}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		v := plan.Description.ValueString()
		opts.Description = &v
	}
	if !plan.DiskName.IsNull() && !plan.DiskName.IsUnknown() && strings.TrimSpace(plan.DiskName.ValueString()) != "" {
		v := plan.DiskName.ValueString()
		opts.DiskName = &v
	}
	if !plan.OnlineSnapshot.IsNull() && !plan.OnlineSnapshot.IsUnknown() {
		opts.OnlineSnapshot = plan.OnlineSnapshot.ValueBool()
	}

	// Record the dispatch time before the request goes out, with sub-second
	// precision (RFC3339Nano) so a pre-existing same-name snapshot created
	// earlier in the same second is excluded from the window: it bounds the
	// adoption window when the create outcome is unconfirmed and no task UUID
	// is available (an ambiguous dispatch, or wait=false without a task).
	plan.CreateRequestedAt = types.StringValue(time.Now().UTC().Format(time.RFC3339Nano))

	// Persist the UUIDs that existed BEFORE the dispatch so that post-create
	// reconciliation can exclude pre-existing same-name snapshots BY IDENTITY
	// rather than by comparing the host clock (create_requested_at) against the
	// API clock (CreationTime). An empty list means nothing pre-existed.
	preUUIDs := make([]string, 0, len(preExistingUUIDs))
	for u := range preExistingUUIDs {
		preUUIDs = append(preUUIDs, u)
	}
	preCreateList, diags := types.ListValueFrom(ctx, types.StringType, preUUIDs)
	if diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	plan.PreCreateUUIDs = preCreateList

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
	finished, err := r.client.WaitForTask(waitCtx, task.UUID)
	if err != nil {
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
	// UUID. A candidate must pass both server-side ownership proofs: it must
	// not have been listed before the create (preExistingUUIDs, recorded
	// above) and, when the task reported a start time, it must have been
	// created no earlier than the task started — a snapshot a concurrent
	// actor creates between the pre-create listing and the task start is not
	// the one this create produces. Adopt only the SOLE candidate: with
	// several new same-name snapshots the created one cannot be attributed,
	// so the resource stays unconfirmed like the not-listed case.
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
					"the snapshot once it can be identified.",
				err.Error(),
			),
		)
		return
	}
	cands := sameNameCreateCandidates(snapshots, plan.Name.ValueString(), preExistingUUIDs, finished.StartedAt)
	var found *netcup.SnapshotMinimal
	if len(cands) == 1 {
		found = &cands[0]
	}
	if found == nil {
		// No adoptable candidate: either the new snapshot is not listed yet
		// (listing lag) or several new same-name snapshots are listed and the
		// created one cannot be distinguished from a concurrent create.
		resetSnapshotComputed(&plan, plan.TaskID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		if len(cands) > 1 {
			resp.Diagnostics.AddWarning(
				"Netcup snapshot ambiguous after creation",
				fmt.Sprintf(
					"The snapshot task finished, but %d new snapshots named %q are listed on server %d. The one "+
						"created by this task cannot be distinguished from snapshots created concurrently, so "+
						"none was adopted. Remove the extra same-name snapshot(s) once they are identified; the "+
						"next refresh will adopt the created snapshot once it is the sole candidate.",
					len(cands), plan.Name.ValueString(), serverID,
				),
			)
		} else {
			resp.Diagnostics.AddWarning(
				"Netcup snapshot not found after creation",
				fmt.Sprintf(
					"The snapshot task finished, but no new snapshot named %q was listed on server %d "+
						"(a pre-existing or concurrently created snapshot with that name is never adopted). "+
						"The resource was recorded in state; the next refresh will adopt the snapshot once "+
						"it can be identified.",
					plan.Name.ValueString(), serverID,
				),
			)
		}
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

	// A null wait in state marks a fresh import: ImportState carries only
	// server_id and name, while every persisted create has the schema default
	// applied to wait. Capture it before normalizing, because it changes
	// whether a missing name match is definitive (import) or not (create).
	isImport := state.Wait.IsNull() || state.Wait.IsUnknown()
	if isImport {
		state.Wait = types.BoolValue(true)
	}

	var found *netcup.SnapshotMinimal
	switch {
	case !state.UUID.IsNull() && !state.UUID.IsUnknown() && state.UUID.ValueString() != "":
		// Known UUID: match by the snapshot's real identity.
		for i := range snapshots {
			if snapshots[i].UUID == state.UUID.ValueString() {
				found = &snapshots[i]
				break
			}
		}
		if found == nil {
			// A known UUID that is no longer listed: the snapshot was removed
			// out of band. Drift.
			resp.State.RemoveResource(ctx)
			return
		}
	case state.Name.IsNull() || state.Name.IsUnknown() || state.Name.ValueString() == "":
		// Neither UUID nor name is known: the state identifies no snapshot.
		resp.State.RemoveResource(ctx)
		return
	default:
		// The UUID is not known: an unconfirmed create (wait=false, an
		// indeterminate wait, or an ambiguous dispatch) or a fresh import.
		// The snapshot may still be in flight, so dropping the state here
		// would let the next apply create a duplicate. Resolve via the
		// recorded task when there is one, otherwise fall back to the name.
		adopted, remove, diags := r.resolveUnconfirmedCreate(ctx, &state, snapshots, isImport)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		if remove {
			resp.State.RemoveResource(ctx)
			return
		}
		if adopted == nil {
			// Not adoptable yet: keep the state (so the next apply does not
			// mint a duplicate) and tell the operator what to do.
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			resp.Diagnostics.AddWarning(
				"Netcup snapshot not found on refresh",
				fmt.Sprintf(
					"No snapshot named %q was listed on server %d that this resource could claim. "+
						"The snapshot may still be in flight (an unconfirmed create keeps this resource "+
						"in state so the next apply does not mint a duplicate); it will be adopted once it "+
						"can be identified. If it does not exist, remove this resource from state with "+
						"`terraform state rm`.",
					state.Name.ValueString(), serverID,
				),
			)
			return
		}
		found = adopted
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

	if diags := applySnapshot(ctx, &state, *found); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// resolveUnconfirmedCreate resolves a state whose UUID is unknown (an
// unconfirmed create, or a fresh import) against the server's current
// snapshot listing. It returns:
//
//   - found: a snapshot to adopt (its UUID), when one can be attributed to
//     this resource;
//   - remove: true only when absence is DEFINITIVE (the recorded task ended
//     in a failure terminal, or an import found no name match);
//   - diagnostics: a hard error (state kept) when the task's state cannot be
//     read.
//
// While the recorded task is still running, nothing in the listing is adopted:
// a same-name snapshot there is then necessarily pre-existing, and adopting it
// would make Terraform own a snapshot it did not create. Otherwise adoption
// goes through adoptUnconfirmed, which requires a same-name snapshot to pass
// every server-side ownership proof available — the persisted pre_create_uuids
// set (not listed before the dispatch) and the task's server-side start time
// (created no earlier than the task started) — and adopts only the sole
// survivor; with no server-side proof it falls back to the host-clock
// create_requested_at bound. An import (isImport) never goes through
// adoptUnconfirmed: carrying no create evidence, its only identity is the
// name, so exactly one match is adopted, none is definitive absence, and
// several is a hard error (state kept).
func (r *serverSnapshotResource) resolveUnconfirmedCreate(ctx context.Context, state *serverSnapshotResourceModel, snapshots []netcup.SnapshotMinimal, isImport bool) (*netcup.SnapshotMinimal, bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	name := state.Name.ValueString()

	if state.TaskID.IsNull() || state.TaskID.IsUnknown() || state.TaskID.ValueString() == "" {
		if isImport {
			// An import carries no create evidence (no pre-create set, no
			// dispatch time), so the name is its only identity and the listing
			// is definitive: exactly one same-name match is adopted, none is
			// definitive absence, and several is ambiguous — picking the
			// newest would bind an arbitrary UUID that the delete path then
			// refuses to operate on while the name is ambiguous.
			var match *netcup.SnapshotMinimal
			count := 0
			for i := range snapshots {
				if snapshots[i].Name == name {
					count++
					match = &snapshots[i]
				}
			}
			switch count {
			case 0:
				// The requested snapshot is not listed: the import target is gone.
				return nil, true, diags
			case 1:
				return match, false, diags
			default:
				diags.AddError(
					"Ambiguous snapshot import",
					fmt.Sprintf(
						"%d snapshots on server %s share the name %q, so the import cannot tell which one to "+
							"adopt — picking one would bind an arbitrary UUID that destroy then refuses to "+
							"delete while the name is ambiguous. Remove the extra same-name snapshots and "+
							"re-import, or remove this resource from state with `terraform state rm`.",
						count, state.ServerID.ValueString(), name,
					),
				)
				return nil, false, diags
			}
		}
		// A persisted create with no task: the snapshot may still be in
		// flight, so adoptUnconfirmed applies — pre-existing same-name
		// snapshots are excluded by identity (pre_create_uuids) when the set
		// is persisted, otherwise by the create window.
		return adoptUnconfirmed(snapshots, name, state, nil), false, diags
	}

	task, err := r.client.GetTask(ctx, state.TaskID.ValueString())
	if err != nil {
		var apiErr *netcup.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			// The task cannot be read: its outcome is unknown, so nothing is
			// adopted that is not provably new — a same-name snapshot in the
			// persisted pre-create set (or, without one, created before
			// create_requested_at) is necessarily pre-existing.
			return adoptUnconfirmed(snapshots, name, state, nil), false, diags
		}
		d, _ := apiErrorToDiag(err, true)
		diags.Append(d)
		return nil, false, diags
	}
	switch {
	case !task.State.IsTerminal():
		// The snapshot is in flight: keep the state and re-check on the next
		// refresh. Nothing is adopted while the task runs.
		return nil, false, diags
	case task.State == netcup.TaskStateFinished:
		// A FINISHED task with no startedAt must not silently widen to
		// name-only: adoption then rests on the pre-create set alone, and
		// only a sole candidate is adopted.
		return adoptUnconfirmed(snapshots, name, state, task.StartedAt), false, diags
	default:
		// ERROR/CANCELED/ROLLBACK: the snapshot was never completed and
		// nothing matching is listed — absence is definitive.
		return nil, true, diags
	}
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
	plan.CreateRequestedAt = prior.CreateRequestedAt
	plan.PreCreateUUIDs = prior.PreCreateUUIDs

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

	// An unconfirmed create (UUID unknown, task recorded) must not be deleted
	// by name while its task is still running: the in-flight snapshot is not
	// listed yet, so a 404 would be treated as success and the snapshot that
	// later appears would be left unmanaged — and a pre-existing same-name
	// snapshot could be the one deleted instead. Resolve the task first.
	var createTaskStartedAt *time.Time
	if (state.UUID.IsNull() || state.UUID.IsUnknown() || state.UUID.ValueString() == "") &&
		!state.TaskID.IsNull() && !state.TaskID.IsUnknown() && state.TaskID.ValueString() != "" {
		task, err := r.client.GetTask(ctx, state.TaskID.ValueString())
		if err == nil {
			if task.State == netcup.TaskStateFinished {
				createTaskStartedAt = task.StartedAt
			}
			switch {
			case task.State == netcup.TaskStateError || task.State == netcup.TaskStateCanceled || task.State == netcup.TaskStateRollback:
				// The create failed terminally, so this resource owns no
				// snapshot; deleting by name could only hit an unrelated
				// same-name snapshot.
				return
			case !task.State.IsTerminal():
				waitCtx, cancel := context.WithTimeout(ctx, snapshotTaskTimeout)
				defer cancel()
				finished, err := r.client.WaitForTask(waitCtx, task.UUID)
				if err != nil {
					var taskErr *netcup.TaskError
					if errors.As(err, &taskErr) {
						// The task failed while we waited: nothing of ours to
						// delete, so stop before the name-based delete.
						return
					}
					// Still not terminal after the bound: deleting by name now
					// is unsafe, and the delete is idempotent, so error and let
					// the next destroy retry once the task settles.
					d, _ := apiErrorToDiag(err, true)
					resp.Diagnostics.Append(d)
					return
				}
				createTaskStartedAt = finished.StartedAt
				// FINISHED: the snapshot should now be listed. The preflight
				// below re-lists and verifies the name is unambiguous — and,
				// for this unconfirmed create, that it points at a snapshot
				// from the create window — before the delete is issued.
			}
		} else {
			var apiErr *netcup.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
				// The task cannot be read: the create's outcome is unknown, so a
				// name-based delete could remove an unrelated same-name snapshot
				// or 404 into "already gone" while the in-flight snapshot is not
				// listed yet. Keep the resource in state: a refresh adopts the
				// snapshot within the create window once it is listed, after
				// which the delete proceeds with a confirmed UUID.
				resp.Diagnostics.AddError(
					"Snapshot create task not found",
					fmt.Sprintf(
						"The create task %s could not be read (404), so the create's outcome is unknown. "+
							"Deleting by name %q could remove an unrelated same-name snapshot or forget a "+
							"snapshot that is still in flight. Check the snapshot in the SCP control panel, "+
							"then re-run destroy once the snapshot is listed, or remove this resource from "+
							"state with `terraform state rm` if it does not exist.",
						state.TaskID.ValueString(), name,
					),
				)
				return
			}
			d, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(d)
			return
		}
	}

	// The delete endpoint addresses snapshots by name only, so verify what a
	// name-based delete would hit before issuing it: an ambiguous or
	// unresolvable name could remove an unrelated same-name snapshot, or 404
	// into "already gone" while the snapshot this resource created is still
	// not listed.
	snapshots, err := r.client.ListSnapshots(ctx, serverID)
	if err != nil {
		d, gone := apiErrorToDiag(err, false)
		if gone {
			// The server itself is gone (404) — as Read sees it — so its
			// snapshots are necessarily gone too: the end state is reached.
			return
		}
		// Any other listing failure (5xx, transport) cannot confirm what a
		// name-based delete would hit: error and let the next destroy retry.
		resp.Diagnostics.Append(d)
		return
	}
	var matches []netcup.SnapshotMinimal
	for _, s := range snapshots {
		if s.Name == name {
			matches = append(matches, s)
		}
	}
	unconfirmed := state.UUID.IsNull() || state.UUID.IsUnknown() || state.UUID.ValueString() == ""
	switch {
	case len(matches) == 0:
		if !unconfirmed {
			// A confirmed snapshot that is no longer listed is already gone:
			// the desired end state is reached.
			return
		}
		// An unconfirmed create whose snapshot is not listed is not "already
		// gone": it may still be in flight or in the post-finish visibility
		// lag, and forgetting it now would orphan the snapshot once it
		// appears (a later name-based delete could then hit an unrelated
		// same-name snapshot).
		resp.Diagnostics.AddError(
			"Snapshot not listed",
			fmt.Sprintf(
				"No snapshot named %q is listed on server %d, but this resource's create was never confirmed by UUID. "+
					"The snapshot may still be in flight or not listed yet, so the name-based delete is refused. "+
					"Re-run destroy once the snapshot is listed, or remove this resource from state with "+
					"`terraform state rm` if it does not exist.",
				name, serverID,
			),
		)
		return
	case len(matches) == 1:
		if unconfirmed {
			excl := preCreateUUIDSet(&state)
			preExisting := false
			switch {
			case createTaskStartedAt != nil:
				// The create task reported a server-side start time, so the
				// server's own clock decides: the match is ours only if it
				// passes every server-side proof (not listed before the
				// dispatch, created no earlier than the task started). A
				// host clock ahead of the API must not veto a snapshot the
				// server proofs qualify — matching adoptUnconfirmed.
				preExisting = len(sameNameCreateCandidates(snapshots, name, excl, createTaskStartedAt)) != 1
			case excl != nil && excl[matches[0].UUID]:
				// Identity: the only same-name snapshot was already listed
				// before this create was dispatched, so it is not the one
				// this resource created — regardless of what the clocks say.
				preExisting = true
			default:
				// No server-clock task proof: fall back to the (host-clock)
				// dispatch bound — best effort, see adoptUnconfirmed.
				preExisting = latestSameNameCreatedAfter(snapshots, name, createRequestedSince(&state)) == nil
			}
			if preExisting {
				// The only same-name snapshot is not the one this resource
				// created (it was listed before the dispatch or created
				// before the create task started), and the created snapshot
				// is not listed yet.
				resp.Diagnostics.AddError(
					"Snapshot not listed",
					fmt.Sprintf(
						"The only snapshot named %q on server %d is not the snapshot this resource created: "+
							"it was listed before the create was dispatched or created before the create task "+
							"started. Deleting by name would remove an unrelated snapshot. Re-run destroy once "+
							"the created snapshot is listed, or remove this resource from state with "+
							"`terraform state rm` if it does not exist.",
						name, serverID,
					),
				)
				return
			}
		} else if matches[0].UUID != state.UUID.ValueString() {
			// The only same-name snapshot is not the one this resource owns:
			// deleting by name would remove it.
			resp.Diagnostics.AddError(
				"Snapshot UUID mismatch",
				fmt.Sprintf(
					"The only snapshot named %q on server %d has UUID %s, but this resource records UUID %s. "+
						"Deleting by name would remove a different snapshot. Reconcile the resource or remove it "+
						"from state with `terraform state rm`.",
					name, serverID, matches[0].UUID, state.UUID.ValueString(),
				),
			)
			return
		}
	default:
		resp.Diagnostics.AddError(
			"Ambiguous snapshot name",
			fmt.Sprintf(
				"%d snapshots on server %d share the name %q, but the delete endpoint addresses snapshots by name "+
					"only, so refusing to guess which one to delete. Remove the extra snapshots (e.g. in the SCP "+
					"control panel) and re-run destroy, or remove this resource from state with `terraform state rm` "+
					"if none of them is yours.",
				len(matches), serverID, name,
			),
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
	parsedServerID, err := parseServerID(serverID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("The server part of the import ID must be a numeric server ID; got %q.", req.ID),
		)
		return
	}

	// Only server_id and name are seeded; the subsequent Read resolves the UUID
	// by name and backfills the rest. server_id is stored in canonical base-10
	// (the parsed value) so noncanonical import spellings such as "00123" or
	// "+123" do not drift from a config's "123" into a RequiresReplace plan.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), strconv.FormatInt(int64(parsedServerID), 10))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
}
