package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// isolateNetcupEnv clears the ambient NETCUP_* environment variables for the
// duration of a test so the provider unit tests are hermetic. Without it, a
// maintainer running `make acc` (which exports NETCUP_ACCESS_TOKEN and
// NETCUP_REFRESH_TOKEN) would have those real credentials leak into Configure:
// a non-JWT test token seeds a zero expiry, the refreshing TokenSource then
// refreshes against the live OIDC endpoint, and a real bearer token overrides
// the value the test asserts. Tests set only the vars they intend to exercise
// after calling this. Empty is equivalent to unset for resolveConfigString and
// netcup.New (both treat os.Getenv() == "" as "not set").
func isolateNetcupEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"NETCUP_ACCESS_TOKEN",
		"NETCUP_REFRESH_TOKEN",
		"NETCUP_API_ENDPOINT",
		"NETCUP_OIDC_ENDPOINT",
	} {
		t.Setenv(k, "")
	}
}

// configureRequest builds a provider.ConfigureRequest from the given schema
// attribute values, driving Configure the same way Terraform would after
// parsing an HCL provider block. A nil value means the attribute was omitted
// from the config (null); tftypes.UnknownValue means the value won't be known
// until apply (e.g. it's derived from another resource in the same run).
func configureRequest(t *testing.T, schemaResp provider.SchemaResponse, values map[string]any) provider.ConfigureRequest {
	t.Helper()
	ctx := context.Background()
	objType := schemaResp.Schema.Type().TerraformType(ctx)

	raw := map[string]tftypes.Value{}
	for name := range schemaResp.Schema.Attributes {
		v, ok := values[name]
		switch {
		case !ok || v == nil:
			raw[name] = tftypes.NewValue(tftypes.String, nil)
		case v == tftypes.UnknownValue:
			raw[name] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
		default:
			raw[name] = tftypes.NewValue(tftypes.String, v)
		}
	}

	return provider.ConfigureRequest{
		Config: tfsdk.Config{
			Raw:    tftypes.NewValue(objType, raw),
			Schema: schemaResp.Schema,
		},
	}
}

func newTestSchema(t *testing.T) provider.SchemaResponse {
	t.Helper()
	p := &netcupProvider{version: "test"}
	var resp provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", resp.Diagnostics)
	}
	return resp
}

func TestConfigure_UsesConfigAccessToken(t *testing.T) {
	isolateNetcupEnv(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	schemaResp := newTestSchema(t)
	req := configureRequest(t, schemaResp, map[string]any{
		"access_token": "test-token",
		"api_endpoint": srv.URL,
	})

	p := &netcupProvider{version: "test"}
	var resp provider.ConfigureResponse
	p.Configure(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure() diagnostics = %v", resp.Diagnostics)
	}

	client, ok := resp.ResourceData.(*netcup.Client)
	if !ok {
		t.Fatalf("ResourceData type = %T, want *netcup.Client", resp.ResourceData)
	}
	if resp.DataSourceData != resp.ResourceData {
		t.Error("DataSourceData and ResourceData should be the same client")
	}

	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if want := "Bearer test-token"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestConfigure_FallsBackToEnv(t *testing.T) {
	isolateNetcupEnv(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("NETCUP_ACCESS_TOKEN", "env-token")
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)

	schemaResp := newTestSchema(t)
	// Empty provider block: every attribute is null, so env vars must apply.
	req := configureRequest(t, schemaResp, nil)

	p := &netcupProvider{version: "test"}
	var resp provider.ConfigureResponse
	p.Configure(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure() diagnostics = %v", resp.Diagnostics)
	}

	client, ok := resp.ResourceData.(*netcup.Client)
	if !ok {
		t.Fatalf("ResourceData type = %T, want *netcup.Client", resp.ResourceData)
	}
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if want := "Bearer env-token"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q (env var fallback)", gotAuth, want)
	}
}

func TestConfigure_ConfigWinsOverEnv(t *testing.T) {
	isolateNetcupEnv(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("NETCUP_ACCESS_TOKEN", "env-token")

	schemaResp := newTestSchema(t)
	req := configureRequest(t, schemaResp, map[string]any{
		"access_token": "config-token",
		"api_endpoint": srv.URL,
	})

	p := &netcupProvider{version: "test"}
	var resp provider.ConfigureResponse
	p.Configure(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure() diagnostics = %v", resp.Diagnostics)
	}

	client := resp.ResourceData.(*netcup.Client)
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if want := "Bearer config-token"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q (explicit config should win over env)", gotAuth, want)
	}
}

func TestConfigure_MalformedAccessTokenFallsBackToZeroExpiry(t *testing.T) {
	isolateNetcupEnv(t)

	// A non-JWT access token can't be parsed for an "exp" claim.
	// ParseAccessTokenExpiry returning an error must not fail Configure or
	// panic; the provider falls back to a zero expiry.
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	schemaResp := newTestSchema(t)
	req := configureRequest(t, schemaResp, map[string]any{
		"access_token": "not-a-jwt",
		"api_endpoint": srv.URL,
	})

	p := &netcupProvider{version: "test"}
	var resp provider.ConfigureResponse
	p.Configure(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure() diagnostics = %v, want no error for a malformed (non-JWT) access token", resp.Diagnostics)
	}

	client := resp.ResourceData.(*netcup.Client)
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if want := "Bearer not-a-jwt"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

// TestConfigure_UnknownAccessTokenErrorsInsteadOfClobberingEnv proves that an
// unknown config value (e.g. access_token derived from a resource not yet
// applied) surfaces a clear diagnostic instead of silently resolving to ""
// and overriding the env var fallback with an empty token.
func TestConfigure_UnknownAccessTokenErrorsInsteadOfClobberingEnv(t *testing.T) {
	isolateNetcupEnv(t)
	t.Setenv("NETCUP_ACCESS_TOKEN", "env-token")

	schemaResp := newTestSchema(t)
	req := configureRequest(t, schemaResp, map[string]any{
		"access_token": tftypes.UnknownValue,
	})

	p := &netcupProvider{version: "test"}
	var resp provider.ConfigureResponse
	p.Configure(context.Background(), req, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Configure() diagnostics has no error, want an error for an unknown access_token")
	}
	if resp.ResourceData != nil {
		t.Errorf("ResourceData = %v, want nil when Configure errors", resp.ResourceData)
	}

	found := false
	for _, d := range resp.Diagnostics.Errors() {
		if strings.Contains(d.Summary(), "access_token") {
			found = true
		}
	}
	if !found {
		t.Errorf("diagnostics = %v, want an error mentioning access_token", resp.Diagnostics)
	}
}

// TestConfigure_UnknownEndpointErrors covers the same unknown-value guard for
// a non-token attribute, proving all four schema attributes are checked.
func TestConfigure_UnknownEndpointErrors(t *testing.T) {
	isolateNetcupEnv(t)

	schemaResp := newTestSchema(t)
	req := configureRequest(t, schemaResp, map[string]any{
		"api_endpoint": tftypes.UnknownValue,
	})

	p := &netcupProvider{version: "test"}
	var resp provider.ConfigureResponse
	p.Configure(context.Background(), req, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Configure() diagnostics has no error, want an error for an unknown api_endpoint")
	}
}
