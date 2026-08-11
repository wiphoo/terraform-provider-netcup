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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

var _ resource.Resource = &sshKeyResource{}
var _ resource.ResourceWithConfigure = &sshKeyResource{}
var _ resource.ResourceWithImportState = &sshKeyResource{}

type sshKeyResource struct {
	client *netcup.Client
}

type sshKeyResourceModel struct {
	Name      types.String `tfsdk:"name"`
	PublicKey types.String `tfsdk:"public_key"`
	ID        types.String `tfsdk:"id"`
}

// requiresReplaceIfNotTrimEqual is a RequiresReplaceIf function that forces a
// replacement only when the value changed by more than surrounding whitespace.
//
// It compares the trimmed prior-state and planned values directly, rather than
// relying on a semantic-equality custom type: in terraform-plugin-framework
// v1.19, stringplanmodifier.RequiresReplace compares values with the exact
// Equal(), and a type's StringSemanticEquals is applied to CRUD response state
// rather than during PlanResourceChange — so a whitespace-only edit (e.g.
// file(...) -> trimspace(file(...)), or a trailing newline) would otherwise still
// force a replacement, mint a new server-assigned id, and cascade into a
// DESTRUCTIVE netcup_server_reinstall when that id feeds ssh_key_ids.
func requiresReplaceIfNotTrimEqual(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	// Create (no prior state) is never a replacement.
	if req.StateValue.IsNull() {
		return
	}
	// If either value is unknown/null unexpectedly, be conservative and replace.
	if req.StateValue.IsUnknown() || req.PlanValue.IsUnknown() || req.PlanValue.IsNull() {
		resp.RequiresReplace = true
		return
	}
	resp.RequiresReplace = strings.TrimSpace(req.StateValue.ValueString()) != strings.TrimSpace(req.PlanValue.ValueString())
}

// isDefinitiveSSHKeyRejection reports whether a CreateSSHKey error PROVES the key
// was not created: a pre-dispatch failure (request never sent) or a 4xx client
// rejection. A 5xx, transport, or decode error is ambiguous — netcup may have
// accepted the POST before the failure — so it is NOT definitive and triggers
// reconciliation instead.
func isDefinitiveSSHKeyRejection(err error) bool {
	if errors.Is(err, netcup.ErrPreDispatch) {
		return true
	}
	var apiErr *netcup.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode < 500
	}
	return false
}

// reconcileCreatedSSHKey looks for a key matching (name, trimmed public key)
// after an ambiguous create failure, returning it (or nil if none is found) so
// an accepted-but-unconfirmed key is adopted into state rather than duplicated
// on the next apply.
//
// priorIDs is the set of key IDs that existed BEFORE the create was attempted.
// Only a NEWLY-appeared matching key is adopted: a pre-existing (priorIDs) match
// is an unmanaged key the create did not produce, and adopting it would make
// Terraform wrongly own — and later delete — a key it never created.
func (r *sshKeyResource) reconcileCreatedSSHKey(ctx context.Context, name, publicKey string, priorIDs map[int32]struct{}) (*netcup.SSHKey, error) {
	keys, err := r.client.ListSSHKeys(ctx)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	publicKey = strings.TrimSpace(publicKey)
	for i := range keys {
		if _, existed := priorIDs[keys[i].ID]; existed {
			continue // pre-existing unmanaged key — not the one this create made
		}
		if strings.TrimSpace(keys[i].Name) == name && strings.TrimSpace(keys[i].Key) == publicKey {
			return &keys[i], nil
		}
	}
	return nil, nil
}

// NewSSHKeyResource returns a new netcup_ssh_key resource factory.
func NewSSHKeyResource() resource.Resource { return &sshKeyResource{} }

func (r *sshKeyResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "netcup_ssh_key"
}

func (r *sshKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Registers an SSH public key in the netcup SCP account. The computed `id` " +
			"can be passed to netcup_server_reinstall.ssh_key_ids. Changing name or public_key replaces the key.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Label for the SSH key in SCP. Surrounding whitespace is ignored; a non-whitespace change forces replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIf(
						requiresReplaceIfNotTrimEqual,
						"Replaced when the label changes (ignoring surrounding whitespace).",
						"Replaced when the label changes (ignoring surrounding whitespace).",
					),
				},
			},
			"public_key": schema.StringAttribute{
				Required:    true,
				Description: "SSH public key content. Surrounding whitespace is ignored (so file(...) vs trimspace(file(...)) is not a change); a non-whitespace change forces replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIf(
						requiresReplaceIfNotTrimEqual,
						"Replaced when the public key changes (ignoring surrounding whitespace).",
						"Replaced when the public key changes (ignoring surrounding whitespace).",
					),
				},
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The numeric SCP SSH-key id.",
				// No UseStateForUnknown here: the SCP id is server-assigned and
				// changes on every replace (both name and public_key are
				// RequiresReplace), so on a replacement plan the id must be
				// (known after apply). Copying the old id forward would make
				// Create's new id an "inconsistent result after apply" error.
				// There is no in-place-update path, so UseStateForUnknown would add
				// no stability benefit anyway. Mirrors server_reinstall's plain
				// Computed id.
			},
		},
	}
}

func (r *sshKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*netcup.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *netcup.Client, got %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *sshKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_ssh_key.")
		return
	}
	var plan sshKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Always create a fresh key rather than reusing an existing one. Reuse would
	// return a prior key's id; under create_before_destroy a semantic-only
	// replacement (e.g. file(...) -> trimspace(file(...))) would then have the
	// deposed instance's Delete remove the very key the replacement points to.
	// Adopting a pre-existing SCP key is done via `terraform import`, not Create.
	//
	// Snapshot the key ids that exist BEFORE the create so that, if the outcome is
	// ambiguous, reconciliation can adopt only a NEWLY-appeared key — never a
	// pre-existing unmanaged one. If this pre-list fails, priorListErr is recorded
	// and reconciliation is skipped (we cannot tell new from pre-existing).
	priorKeys, priorListErr := r.client.ListSSHKeys(ctx)
	priorIDs := make(map[int32]struct{}, len(priorKeys))
	for _, k := range priorKeys {
		priorIDs[k.ID] = struct{}{}
	}

	key, err := r.client.CreateSSHKey(ctx, plan.Name.ValueString(), plan.PublicKey.ValueString())
	if err != nil {
		// Definitive rejection (pre-dispatch or 4xx): the key was never created —
		// surface the error and persist no state so the next apply retries safely.
		if isDefinitiveSSHKeyRejection(err) {
			d, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(d)
			return
		}
		// Ambiguous (5xx / transport / decode after dispatch): netcup may have
		// created the key but the outcome could not be confirmed. Since Create
		// always creates a fresh key, a blind retry would orphan the accepted key
		// and duplicate / name-conflict. Reconcile by matching (name, trimmed
		// content) — but only if we have a reliable pre-POST snapshot, and only
		// adopting a NEWLY-appeared key (never a pre-existing unmanaged one).
		if priorListErr != nil {
			d, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(d)
			return
		}
		reconciled, rerr := r.reconcileCreatedSSHKey(ctx, plan.Name.ValueString(), plan.PublicKey.ValueString(), priorIDs)
		if rerr != nil || reconciled == nil {
			d, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(d)
			return
		}
		key = reconciled
		resp.Diagnostics.AddWarning(
			"netcup SSH key creation outcome could not be confirmed",
			fmt.Sprintf("The create request may have been accepted by netcup, but the outcome could not be confirmed (%s). "+
				"A key matching the requested name and content was found and adopted into state to avoid creating a "+
				"duplicate on the next apply.", err.Error()),
		)
	}
	plan.ID = types.StringValue(strconv.FormatInt(int64(key.ID), 10))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *sshKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_ssh_key.")
		return
	}
	var state sshKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	keys, err := r.client.ListSSHKeys(ctx)
	if err != nil {
		// A failure of the account-level list endpoint (e.g. a 404 from an
		// invalid account id or an unavailable route) does NOT prove the
		// individual key is gone. Treat every list error as a hard error
		// (notFoundIsError=true) rather than silently dropping the resource;
		// absence is determined only from a successful list below.
		d, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(d)
		return
	}
	want := state.ID.ValueString()
	for _, k := range keys {
		if strconv.FormatInt(int64(k.ID), 10) == want {
			// On import only `id` is seeded, so backfill name/public_key from the
			// matched API object to make the RequiresReplace attributes known. On a
			// normal refresh they already hold the configured values; leave them
			// untouched so server-side normalization (e.g. SCP trimming the trailing
			// newline of a file()-sourced key) does not diverge from config and
			// force a perpetual replacement.
			if state.Name.IsNull() {
				state.Name = types.StringValue(strings.TrimSpace(k.Name))
			}
			if state.PublicKey.IsNull() {
				state.PublicKey = types.StringValue(strings.TrimSpace(k.Key))
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *sshKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan sshKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state sshKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Update runs only for non-replacement changes — per requiresReplaceIfNotTrimEqual
	// that is a whitespace-only edit, which does not change the SCP key or its id.
	// The computed `id` is planned unknown because a sibling attribute changed, so
	// carry the prior id forward; otherwise the apply fails with an inconsistent
	// (unknown) result.
	plan.ID = state.ID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *sshKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_ssh_key.")
		return
	}
	var state sshKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := strconv.ParseInt(state.ID.ValueString(), 10, 32)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ssh key id in state", err.Error())
		return
	}
	if err := r.client.DeleteSSHKey(ctx, int32(id)); err != nil {
		d, gone := apiErrorToDiag(err, false)
		if gone {
			return
		}
		resp.Diagnostics.Append(d)
	}
}

func (r *sshKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := strconv.ParseInt(req.ID, 10, 32)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("The import ID must be a numeric ssh-key id; got %q.", req.ID))
		return
	}
	// Store the canonical base-10 form. Read matches against
	// strconv.FormatInt(k.ID, 10), so a noncanonical spelling like "007" or "+7"
	// would never match and the freshly-imported resource would be dropped.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strconv.FormatInt(n, 10))...)
}
