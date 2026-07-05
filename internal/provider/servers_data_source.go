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

var _ datasource.DataSource = &serversDataSource{}

// Ensure serversDataSource satisfies the optional Configure interface.
var _ datasource.DataSourceWithConfigure = &serversDataSource{}

type serversDataSource struct {
	client *netcup.Client
}

type serversDataSourceModel struct {
	Servers []serverMinimalModel `tfsdk:"servers"`
}

type serverMinimalModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Hostname    types.String `tfsdk:"hostname"`
	Nickname    types.String `tfsdk:"nickname"`
	Disabled    types.Bool   `tfsdk:"disabled"`
	ProductName types.String `tfsdk:"product_name"`
}

func NewServersDataSource() datasource.DataSource {
	return &serversDataSource{}
}

func (d *serversDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "netcup_servers"
}

func (d *serversDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists all servers accessible to the authenticated netcup account.",
		Attributes: map[string]schema.Attribute{
			"servers": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:    true,
							Description: "The numeric server ID.",
						},
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "The server name.",
						},
						"hostname": schema.StringAttribute{
							Computed:    true,
							Description: "The hostname (empty string when unset).",
						},
						"nickname": schema.StringAttribute{
							Computed:    true,
							Description: "The nickname (empty string when unset).",
						},
						"disabled": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether the server is disabled.",
						},
						"product_name": schema.StringAttribute{
							Computed:    true,
							Description: "The product template name (empty string when unset).",
						},
					},
				},
			},
		},
	}
}

func (d *serversDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *serversDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_servers.",
		)
		return
	}

	servers, err := d.client.ListServers(ctx)
	if err != nil {
		diag, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(diag)
		return
	}

	var state serversDataSourceModel
	for _, s := range servers {
		model := serverMinimalModel{
			ID:       types.StringValue(strconv.FormatInt(int64(s.ID), 10)),
			Name:     types.StringValue(s.Name),
			Disabled: types.BoolValue(s.Disabled),
		}
		if s.Hostname != nil {
			model.Hostname = types.StringValue(*s.Hostname)
		} else {
			model.Hostname = types.StringValue("")
		}
		if s.Nickname != nil {
			model.Nickname = types.StringValue(*s.Nickname)
		} else {
			model.Nickname = types.StringValue("")
		}
		if s.Template != nil {
			model.ProductName = types.StringValue(s.Template.Name)
		} else {
			model.ProductName = types.StringValue("")
		}
		state.Servers = append(state.Servers, model)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
