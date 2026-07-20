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

var _ datasource.DataSource = &serverSnapshotsDataSource{}
var _ datasource.DataSourceWithConfigure = &serverSnapshotsDataSource{}

type serverSnapshotsDataSource struct {
	client *netcup.Client
}

type serverSnapshotsDataSourceModel struct {
	ServerID  types.String           `tfsdk:"server_id"`
	Snapshots []snapshotMinimalModel `tfsdk:"snapshots"`
}

type snapshotMinimalModel struct {
	UUID              types.String `tfsdk:"uuid"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	Disks             types.List   `tfsdk:"disks"`
	CreationTime      types.String `tfsdk:"creation_time"`
	State             types.String `tfsdk:"state"`
	Online            types.Bool   `tfsdk:"online"`
	Exported          types.Bool   `tfsdk:"exported"`
	ExportedSizeInKiB types.Int64  `tfsdk:"exported_size_in_kib"`
}

func NewServerSnapshotsDataSource() datasource.DataSource {
	return &serverSnapshotsDataSource{}
}

func (d *serverSnapshotsDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "netcup_server_snapshots"
}

func (d *serverSnapshotsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists all snapshots for a given netcup server.",
		Attributes: map[string]schema.Attribute{
			"server_id": schema.StringAttribute{
				Required:    true,
				Description: "The numeric server ID whose snapshots are listed.",
			},
			"snapshots": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The list of snapshots for the server. Empty when no snapshots exist.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"uuid": schema.StringAttribute{
							Computed:    true,
							Description: "The unique identifier of the snapshot.",
						},
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "The name of the snapshot.",
						},
						"description": schema.StringAttribute{
							Computed:    true,
							Optional:    true,
							Description: "The description of the snapshot. Null when not set.",
						},
						"disks": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
							Description: "The list of disk identifiers included in the snapshot.",
						},
						"creation_time": schema.StringAttribute{
							Computed:    true,
							Description: "The RFC 3339 timestamp when the snapshot was created.",
						},
						"state": schema.StringAttribute{
							Computed:    true,
							Description: "The current state of the snapshot (e.g. available, creating).",
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
							Optional:    true,
							Description: "The size of the exported snapshot in KiB. Null when the snapshot has not been exported.",
						},
					},
				},
			},
		},
	}
}

func (d *serverSnapshotsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *serverSnapshotsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_server_snapshots.",
		)
		return
	}

	var config serverSnapshotsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rawID, err := strconv.ParseInt(config.ServerID.ValueString(), 10, 32)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid server_id",
			fmt.Sprintf("Cannot parse %q as a numeric server ID.", config.ServerID.ValueString()),
		)
		return
	}
	serverID := int32(rawID)

	snapshots, err := d.client.ListSnapshots(ctx, serverID)
	if err != nil {
		diag, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(diag)
		return
	}

	state := serverSnapshotsDataSourceModel{
		ServerID:  config.ServerID,
		Snapshots: []snapshotMinimalModel{},
	}

	for _, s := range snapshots {
		model := snapshotMinimalModel{
			UUID:         types.StringValue(s.UUID),
			Name:         types.StringValue(s.Name),
			CreationTime: types.StringValue(s.CreationTime.Format("2006-01-02T15:04:05Z07:00")),
			State:        types.StringValue(s.State),
			Online:       types.BoolValue(s.Online),
			Exported:     types.BoolValue(s.Exported),
		}

		if s.Description != nil {
			model.Description = types.StringValue(*s.Description)
		} else {
			model.Description = types.StringNull()
		}

		if s.ExportedSizeInKiB != nil {
			model.ExportedSizeInKiB = types.Int64Value(*s.ExportedSizeInKiB)
		} else {
			model.ExportedSizeInKiB = types.Int64Null()
		}

		disks := make([]types.String, len(s.Disks))
		for i, d := range s.Disks {
			disks[i] = types.StringValue(d)
		}
		diskList, diags := types.ListValueFrom(ctx, types.StringType, disks)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		model.Disks = diskList

		state.Snapshots = append(state.Snapshots, model)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
