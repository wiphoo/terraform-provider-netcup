package provider

import (
	"context"
	"fmt"
	"strconv"

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
				Required:      true,
				Description:   "Label for the SSH key in SCP. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"public_key": schema.StringAttribute{
				Required:      true,
				Description:   "SSH public key content. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "The numeric SCP SSH-key id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
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
	key, err := r.client.EnsureSSHKey(ctx, plan.Name.ValueString(), plan.PublicKey.ValueString())
	if err != nil {
		d, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(d)
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
			// Populate name/public_key from the matched API object so an
			// imported resource (ImportState seeds only id) learns both values
			// and so out-of-band changes are detected. Without this the null
			// RequiresReplace attributes would force a spurious replacement on
			// the next plan.
			state.Name = types.StringValue(k.Name)
			state.PublicKey = types.StringValue(k.Key)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *sshKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan sshKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
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
	if _, err := strconv.ParseInt(req.ID, 10, 32); err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("The import ID must be a numeric ssh-key id; got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
