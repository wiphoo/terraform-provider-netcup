package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
	vcr "github.com/wiphoo/terraform-provider-netcup/tests/vcr"
)

func newVCRClient(t *testing.T, cassetteName string) *netcup.Client {
	t.Helper()
	return vcr.NewClient(t, cassetteName)
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

// vcrTestRDNSHostname is the redacted PTR value the provider-tier rDNS VCR
// tests plan with, single-sourced from the vcr package.
const vcrTestRDNSHostname = vcr.TestRDNSHostname

// seedLivePTR / clearLivePTR wrap the shared vcr record-mode prep helpers so
// the provider tests match the SDK tier's set/clear-and-confirm semantics.
func seedLivePTR(t *testing.T, ip string)  { t.Helper(); vcr.SeedLivePTR(t, ip) }
func clearLivePTR(t *testing.T, ip string) { t.Helper(); vcr.ClearLivePTR(t, ip) }

func configureServersDataSource(t *testing.T, client *netcup.Client) (datasource.DataSourceWithConfigure, datasource.SchemaResponse) {
	t.Helper()
	return configureDataSource(t, NewServersDataSource().(datasource.DataSourceWithConfigure), client)
}

func configureServerDataSource(t *testing.T, client *netcup.Client) (datasource.DataSourceWithConfigure, datasource.SchemaResponse) {
	t.Helper()
	return configureDataSource(t, NewServerDataSource().(datasource.DataSourceWithConfigure), client)
}

func configureServerImagesDataSource(t *testing.T, client *netcup.Client) (datasource.DataSourceWithConfigure, datasource.SchemaResponse) {
	t.Helper()
	return configureDataSource(t, NewServerImagesDataSource().(datasource.DataSourceWithConfigure), client)
}

func configureServerSnapshotsDataSource(t *testing.T, client *netcup.Client) (datasource.DataSourceWithConfigure, datasource.SchemaResponse) {
	t.Helper()
	return configureDataSource(t, NewServerSnapshotsDataSource().(datasource.DataSourceWithConfigure), client)
}

func configureServerPowerResourceVCR(t *testing.T, client *netcup.Client) (resource.ResourceWithConfigure, resource.SchemaResponse) {
	t.Helper()
	ctx := context.Background()
	r := NewServerPowerResource().(resource.ResourceWithConfigure)
	var configResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	return r, schemaResp
}

func configureRescueResourceVCR(t *testing.T, client *netcup.Client) (resource.ResourceWithConfigure, resource.SchemaResponse) {
	t.Helper()
	ctx := context.Background()
	r := NewRescueResource().(resource.ResourceWithConfigure)
	var configResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	return r, schemaResp
}

// configureDataSource runs the Configure→check-diagnostics→Schema boilerplate
// shared by the servers and server data-source VCR tests.
func configureDataSource(t *testing.T, ds datasource.DataSourceWithConfigure, client *netcup.Client) (datasource.DataSourceWithConfigure, datasource.SchemaResponse) {
	t.Helper()
	ctx := context.Background()

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	return ds, schemaResp
}
