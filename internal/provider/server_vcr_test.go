package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestServerDataSource(t *testing.T) {
	client := newVCRClient(t, "TestServerDataSource")
	ctx := context.Background()
	ds := NewServerDataSource().(datasource.DataSourceWithConfigure)

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	req := readRequest(t, schemaResp, map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, "882863"),
	})

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema
	ds.Read(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serverDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if state.ID.ValueString() != "882863" {
		t.Errorf("ID = %q, want 882863", state.ID.ValueString())
	}
	if state.Hostname.ValueString() == "" {
		t.Error("Hostname is empty")
	}
	if state.Status.ValueString() == "" {
		t.Error("Status is empty")
	}
	if state.ProductName.ValueString() == "" {
		t.Error("ProductName is empty")
	}
	if len(state.IPv4Addresses.Elements()) == 0 {
		t.Error("IPv4Addresses is empty")
	}
	if len(state.IPv6Addresses) == 0 {
		t.Error("IPv6Addresses is empty")
	}
}

func TestServerDataSource_VCRNullableFields(t *testing.T) {
	client := newVCRClient(t, "TestServerDataSource_VCRNullableFields")
	ctx := context.Background()
	ds := NewServerDataSource().(datasource.DataSourceWithConfigure)

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	req := readRequest(t, schemaResp, map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, "789"),
	})

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema
	ds.Read(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serverDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if state.Hostname.ValueString() != "" {
		t.Errorf("Hostname = %q, want empty string", state.Hostname.ValueString())
	}
	if state.Status.ValueString() != "" {
		t.Errorf("Status = %q, want empty string", state.Status.ValueString())
	}
	if state.ProductName.ValueString() != "" {
		t.Errorf("ProductName = %q, want empty string", state.ProductName.ValueString())
	}
	if len(state.IPv4Addresses.Elements()) != 0 {
		t.Errorf("got %d IPv4 addresses, want 0", len(state.IPv4Addresses.Elements()))
	}
	if len(state.IPv6Addresses) != 0 {
		t.Errorf("got %d IPv6 addresses, want 0", len(state.IPv6Addresses))
	}
}
