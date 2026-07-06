package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestServersDataSource(t *testing.T) {
	client := newVCRClient(t, "TestServersDataSource")
	ctx := context.Background()
	ds := NewServersDataSource().(datasource.DataSourceWithConfigure)

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema
	ds.Read(ctx, datasource.ReadRequest{}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serversDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if len(state.Servers) == 0 {
		t.Fatal("Servers is empty, want at least 1")
	}
	s := state.Servers[0]
	if s.ID.ValueString() == "" {
		t.Error("Servers[0].ID is empty")
	}
	if s.Name.ValueString() == "" {
		t.Error("Servers[0].Name is empty")
	}
}
