package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

var _ datasource.DataSource = &sshKeysDataSource{}

// Ensure sshKeysDataSource satisfies the optional Configure interface.
var _ datasource.DataSourceWithConfigure = &sshKeysDataSource{}

type sshKeysDataSource struct {
	client *netcup.Client
}

type sshKeyModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
	Key  types.String `tfsdk:"key"`
}

type sshKeysDataSourceModel struct {
	Keys []sshKeyModel `tfsdk:"keys"`
}

// NewSSHKeysDataSource returns a new netcup_ssh_keys data source factory.
func NewSSHKeysDataSource() datasource.DataSource {
	return &sshKeysDataSource{}
}

func (d *sshKeysDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "netcup_ssh_keys"
}

func (d *sshKeysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the SSH keys registered in the netcup SCP account.",
		Attributes: map[string]schema.Attribute{
			"keys": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The SSH keys registered in the account.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:    true,
							Description: "The numeric SCP SSH-key id.",
						},
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "The SSH key label.",
						},
						"key": schema.StringAttribute{
							Computed:    true,
							Description: "The SSH public key content.",
						},
					},
				},
			},
		},
	}
}

func (d *sshKeysDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	d.client = client
}

func (d *sshKeysDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_ssh_keys.",
		)
		return
	}

	keys, err := d.client.ListSSHKeys(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list SSH keys", err.Error())
		return
	}

	state := sshKeysDataSourceModel{
		Keys: []sshKeyModel{},
	}

	for _, k := range keys {
		state.Keys = append(state.Keys, sshKeyModel{
			ID:   types.StringValue(strconv.FormatInt(int64(k.ID), 10)),
			Name: types.StringValue(k.Name),
			Key:  types.StringValue(k.Key),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
