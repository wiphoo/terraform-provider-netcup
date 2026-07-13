package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// readRequest builds a datasource.ReadRequest with the given attribute values.
// Attributes omitted from values are set to null (unknown in the config).
func readRequest(t *testing.T, schemaResp datasource.SchemaResponse, values map[string]tftypes.Value) datasource.ReadRequest {
	t.Helper()
	ctx := context.Background()
	objType := schemaResp.Schema.Type().TerraformType(ctx)
	objAttrs := objType.(tftypes.Object).AttributeTypes

	raw := map[string]tftypes.Value{}
	for name, attrType := range objAttrs {
		if v, ok := values[name]; ok {
			raw[name] = v
		} else {
			raw[name] = tftypes.NewValue(attrType, nil)
		}
	}

	return datasource.ReadRequest{
		Config: tfsdk.Config{
			Raw:    tftypes.NewValue(objType, raw),
			Schema: schemaResp.Schema,
		},
	}
}

func TestServerDataSource_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers/123456" {
			t.Errorf("path = %q, want /v1/servers/123456", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id":123456,"name":"my-server","hostname":"my-server.example.com","nickname":"My Server","disabled":false,
			"template":{"id":10,"name":"VM 2000"},
			"serverLiveInfo":{"state":"running"},
			"ipv4Addresses":[{"id":1,"ip":"1.2.3.4","netmask":"255.255.255.0","gateway":"1.2.3.1","broadcast":"1.2.3.255"}],
			"ipv6Addresses":[{"id":1,"networkPrefix":"2a03:4000:6:b1d::","networkPrefixLength":64,"gateway":"2a03:4000:6:b1d::1"}],
			"architecture":"x86_64","site":{"id":1,"city":"Nuremberg"}
		}`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
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
		"id": tftypes.NewValue(tftypes.String, "123456"),
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

	if state.ID.ValueString() != "123456" {
		t.Errorf("ID = %q, want 123456", state.ID.ValueString())
	}
	if state.Name.ValueString() != "my-server" {
		t.Errorf("Name = %q, want my-server", state.Name.ValueString())
	}
	if state.Hostname.ValueString() != "my-server.example.com" {
		t.Errorf("Hostname = %q, want my-server.example.com", state.Hostname.ValueString())
	}
	if state.Status.ValueString() != "running" {
		t.Errorf("Status = %q, want running", state.Status.ValueString())
	}
	if state.ProductName.ValueString() != "VM 2000" {
		t.Errorf("ProductName = %q, want VM 2000", state.ProductName.ValueString())
	}

	if len(state.IPv4Addresses.Elements()) != 1 {
		t.Errorf("got %d IPv4 addresses, want 1", len(state.IPv4Addresses.Elements()))
	}

	if len(state.IPv6Addresses) != 1 {
		t.Fatalf("got %d IPv6 addresses, want 1", len(state.IPv6Addresses))
	}
	v6 := state.IPv6Addresses[0]
	if v6.NetworkPrefix.ValueString() != "2a03:4000:6:b1d::" {
		t.Errorf("NetworkPrefix = %q, want 2a03:4000:6:b1d::", v6.NetworkPrefix.ValueString())
	}
	if v6.NetworkPrefixLength.ValueInt64() != 64 {
		t.Errorf("NetworkPrefixLength = %d, want 64", v6.NetworkPrefixLength.ValueInt64())
	}
}

func TestServerDataSource_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"server not found"}`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
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
		"id": tftypes.NewValue(tftypes.String, "999999"),
	})

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema

	ds.Read(ctx, req, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Read() expected error diagnostic for 404, got none")
	}
	var foundNotFound bool
	for _, d := range resp.Diagnostics.Errors() {
		if strings.Contains(d.Detail(), "not found") {
			foundNotFound = true
		}
	}
	if !foundNotFound {
		t.Errorf("404 diagnostic detail does not mention 'not found'. Errors: %v", resp.Diagnostics.Errors())
	}
}

func TestServerDataSource_NullableFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id":789,"name":"minimal","hostname":null,"nickname":null,"disabled":false,
			"template":null,"serverLiveInfo":null,
			"ipv4Addresses":[],"ipv6Addresses":[],
			"architecture":null,"site":null
		}`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
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
	if state.Name.ValueString() != "minimal" {
		t.Errorf("Name = %q, want minimal", state.Name.ValueString())
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
