package provider

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
	vcr "github.com/wiphoo/terraform-provider-netcup/tests/vcr"
)

func newVCRClient(t *testing.T, cassetteName string) *netcup.Client {
	t.Helper()
	return vcr.NewClient(t, cassetteName)
}

func vcrServerIDForTest(t *testing.T) int32 {
	t.Helper()
	if os.Getenv("VCR_RECORD") == "1" {
		v := os.Getenv("NETCUP_TEST_SERVER_ID")
		if v == "" {
			t.Fatal("VCR_RECORD=1 requires NETCUP_TEST_SERVER_ID")
		}
		id, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			t.Fatalf("NETCUP_TEST_SERVER_ID: %v", err)
		}
		return int32(id)
	}
	return 882863
}

func vcrRDNSIPForTest(t *testing.T) string {
	t.Helper()
	if os.Getenv("VCR_RECORD") == "1" {
		ip := os.Getenv("NETCUP_TEST_IP")
		if ip == "" {
			t.Fatal("VCR_RECORD=1 requires NETCUP_TEST_IP")
		}
		return ip
	}
	return "203.0.113.10"
}

const vcrTestRDNSHostname = "host-a1b2c3d4.example.com"

func configureServersDataSource(t *testing.T, client *netcup.Client) (datasource.DataSourceWithConfigure, datasource.SchemaResponse) {
	t.Helper()
	ctx := context.Background()
	ds := NewServersDataSource().(datasource.DataSourceWithConfigure)

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	return ds, schemaResp
}

func configureServerDataSource(t *testing.T, client *netcup.Client) (datasource.DataSourceWithConfigure, datasource.SchemaResponse) {
	t.Helper()
	ctx := context.Background()
	ds := NewServerDataSource().(datasource.DataSourceWithConfigure)

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	return ds, schemaResp
}
