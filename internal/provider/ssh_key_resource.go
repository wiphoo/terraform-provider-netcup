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
	// Always create a fresh key. This resource owns the key it creates; adopting a
	// pre-existing SCP key is done via `terraform import`, not Create.
	key, err := r.client.CreateSSHKey(ctx, plan.Name.ValueString(), plan.PublicKey.ValueString())
	if err != nil {
		if isDefinitiveSSHKeyRejection(err) {
			// Definitively not created (pre-dispatch or 4xx): plain error, no state,
			// so the next apply retries safely.
			d, _ := apiErrorToDiag(err, true)
			resp.Diagnostics.Append(d)
			return
		}
		// Ambiguous (5xx / transport / decode after dispatch): netcup MAY have
		// created the key, but the outcome could not be confirmed. Do NOT auto-adopt
		// a matching key: the provider cannot prove a matching key was created by
		// THIS request rather than pre-existing or created concurrently by another
		// resource/client, and wrongly owning it would let a later replace/destroy
		// delete a key it never created. Surface an actionable error and persist no
		// state instead.
		resp.Diagnostics.AddError(
			"netcup SSH key creation outcome could not be confirmed",
			fmt.Sprintf("The create request may have been accepted by netcup, but the outcome could not be confirmed (%s). "+
				"A key may have been created. Check the SCP control panel: if a matching key exists, adopt it with "+
				"`terraform import netcup_ssh_key.<name> <id>`; otherwise re-apply to create it. The provider does not "+
				"auto-adopt a matching key because it cannot prove the key was created by this request rather than "+
				"pre-existing or created concurrently.", err.Error()),
		)
		return
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
