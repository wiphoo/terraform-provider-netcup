package provider

import (
	"context"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
	vcr "github.com/wiphoo/terraform-provider-netcup/tests/vcr"
)

func newVCRClient(t *testing.T, cassetteName string) *netcup.Client {
	t.Helper()
	return vcr.NewClient(t, cassetteName)
}

// liveRDNSClient returns a plain (unrecorded) *netcup.Client for rDNS prep
// operations in record mode. It uses the live NETCUP_ACCESS_TOKEN so that
// DeleteRDNS/ConfirmRDNS calls do not go through go-vcr and cannot leak into
// the cassette under test. Mirrors the same-named helper in
// tests/vcr/rdns_vcr_test.go.
func liveRDNSClient(t *testing.T) *netcup.Client {
	t.Helper()
	token := os.Getenv("NETCUP_ACCESS_TOKEN")
	if token == "" {
		t.Fatal("VCR_RECORD=1 requires NETCUP_ACCESS_TOKEN")
	}
	return netcup.New(
		netcup.WithAPIEndpoint(netcup.DefaultAPIEndpoint),
		netcup.WithAccessToken(token),
	)
}

// vcrServerIDForTest returns the server ID for provider-tier VCR tests. In
// record mode it reads NETCUP_TEST_SERVER_ID; in replay mode it derives the ID
// from the named cassette, so a cassette regenerated with any real server ID
// stays replayable with no constant to keep in sync.
func vcrServerIDForTest(t *testing.T, cassetteName string) int32 {
	t.Helper()
	return vcr.ServerIDForTest(t, cassetteName)
}

// vcrRDNSIPForTest returns the live rDNS IP in record mode and the cassette's
// redacted rDNS IP in replay mode.
func vcrRDNSIPForTest(t *testing.T, cassetteName string) string {
	t.Helper()
	return vcr.RDNSIPForTest(t, cassetteName)
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
