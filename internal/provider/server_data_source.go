package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

var _ datasource.DataSource = &serverDataSource{}

var _ datasource.DataSourceWithConfigure = &serverDataSource{}

type serverDataSource struct {
	client *netcup.Client
}

type serverDataSourceModel struct {
	ID            types.String      `tfsdk:"id"`
	Hostname      types.String      `tfsdk:"hostname"`
	Status        types.String      `tfsdk:"status"`
	ProductName   types.String      `tfsdk:"product_name"`
	IPv4Addresses types.List        `tfsdk:"ipv4_addresses"`
	IPv6Addresses []serverIPv6Model `tfsdk:"ipv6_addresses"`
}

type serverIPv6Model struct {
	NetworkPrefix       types.String `tfsdk:"network_prefix"`
	NetworkPrefixLength types.Int64  `tfsdk:"network_prefix_length"`
}

func NewServerDataSource() datasource.DataSource {
	return &serverDataSource{}
}

func (d *serverDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "netcup_server"
}

func (d *serverDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves details for a single server.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:    true,
				Description: "The numeric server ID.",
			},
			"hostname": schema.StringAttribute{
				Computed:    true,
				Description: "The hostname (empty string when unset).",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "The server power state (empty string when unset).",
			},
			"product_name": schema.StringAttribute{
				Computed:    true,
				Description: "The product template name (empty string when unset).",
			},
			"ipv4_addresses": schema.ListAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "The IPv4 addresses assigned to the server.",
			},
			"ipv6_addresses": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"network_prefix": schema.StringAttribute{
							Computed:    true,
							Description: "The IPv6 network prefix.",
						},
						"network_prefix_length": schema.Int64Attribute{
							Computed:    true,
							Description: "The IPv6 network prefix length.",
						},
					},
				},
				Description: "The IPv6 networks assigned to the server.",
			},
		},
	}
}

func (d *serverDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *serverDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_server.",
		)
		return
	}

	var config serverDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.ParseInt(config.ID.ValueString(), 10, 32)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid server ID",
			fmt.Sprintf("Cannot parse %q as a numeric server ID.", config.ID.ValueString()),
		)
		return
	}

	server, err := d.client.GetServer(ctx, int32(id))
	if err != nil {
		diag, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(diag)
		return
	}

	state := serverDataSourceModel{
		ID: types.StringValue(strconv.FormatInt(int64(server.ID), 10)),
	}

	if server.Hostname != nil {
		state.Hostname = types.StringValue(*server.Hostname)
	} else {
		state.Hostname = types.StringValue("")
	}

	if server.ServerLiveInfo != nil {
		state.Status = types.StringValue(server.ServerLiveInfo.State)
	} else {
		state.Status = types.StringValue("")
	}

	if server.Template != nil {
		state.ProductName = types.StringValue(server.Template.Name)
	} else {
		state.ProductName = types.StringValue("")
	}

	ipv4Values := make([]attr.Value, len(server.IPv4Addresses))
	for i, addr := range server.IPv4Addresses {
		ipv4Values[i] = types.StringValue(addr.IP)
	}
	state.IPv4Addresses = types.ListValueMust(types.StringType, ipv4Values)

	state.IPv6Addresses = make([]serverIPv6Model, len(server.IPv6Addresses))
	for i, addr := range server.IPv6Addresses {
		state.IPv6Addresses[i] = serverIPv6Model{
			NetworkPrefix:       types.StringValue(addr.NetworkPrefix),
			NetworkPrefixLength: types.Int64Value(int64(addr.NetworkPrefixLength)),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
