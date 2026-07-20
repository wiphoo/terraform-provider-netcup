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

var _ datasource.DataSource = &serverImagesDataSource{}

// Ensure serverImagesDataSource satisfies the optional Configure interface.
var _ datasource.DataSourceWithConfigure = &serverImagesDataSource{}

type serverImagesDataSource struct {
	client *netcup.Client
}

type serverImagesDataSourceModel struct {
	ServerID types.String        `tfsdk:"server_id"`
	Images   []imageFlavourModel `tfsdk:"images"`
}

type imageFlavourModel struct {
	ID    types.Int64        `tfsdk:"id"`
	Name  types.String       `tfsdk:"name"`
	Alias types.String       `tfsdk:"alias"`
	Text  types.String       `tfsdk:"text"`
	Image *imageMinimalModel `tfsdk:"image"`
}

type imageMinimalModel struct {
	ID   types.Int64  `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func NewServerImagesDataSource() datasource.DataSource {
	return &serverImagesDataSource{}
}

func (d *serverImagesDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "netcup_server_images"
}

func (d *serverImagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists image flavours installable on a netcup server.",
		Attributes: map[string]schema.Attribute{
			"server_id": schema.StringAttribute{
				Required:    true,
				Description: "The numeric server ID.",
			},
			"images": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The image flavours available for installation on this server.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.Int64Attribute{
							Computed:    true,
							Description: "The image flavour ID.",
						},
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "The image flavour name.",
						},
						"alias": schema.StringAttribute{
							Computed:    true,
							Description: "A human-facing alias for the image flavour.",
						},
						"text": schema.StringAttribute{
							Computed:    true,
							Description: "A human-facing description of the image flavour.",
						},
						"image": schema.SingleNestedAttribute{
							Computed:    true,
							Optional:    true,
							Description: "The underlying base image (nil when not set by the API).",
							Attributes: map[string]schema.Attribute{
								"id": schema.Int64Attribute{
									Computed:    true,
									Description: "The base image ID.",
								},
								"name": schema.StringAttribute{
									Computed:    true,
									Description: "The base image name.",
								},
							},
						},
					},
				},
			},
		},
	}
}

func (d *serverImagesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *serverImagesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_server_images.",
		)
		return
	}

	var config serverImagesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.ParseInt(config.ServerID.ValueString(), 10, 32)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid server ID",
			fmt.Sprintf("Cannot parse %q as a numeric server ID.", config.ServerID.ValueString()),
		)
		return
	}

	flavours, err := d.client.ListImageFlavours(ctx, int32(id))
	if err != nil {
		diag, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(diag)
		return
	}

	state := serverImagesDataSourceModel{
		ServerID: config.ServerID,
		Images:   []imageFlavourModel{},
	}

	for _, f := range flavours {
		m := imageFlavourModel{
			ID:    types.Int64Value(int64(f.ID)),
			Name:  types.StringValue(f.Name),
			Alias: types.StringValue(f.Alias),
			Text:  types.StringValue(f.Text),
		}
		if f.Image != nil {
			m.Image = &imageMinimalModel{
				ID:   types.Int64Value(int64(f.Image.ID)),
				Name: types.StringValue(f.Image.Name),
			}
		}
		state.Images = append(state.Images, m)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
