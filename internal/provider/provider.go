// Package provider implements the Terraform Plugin Framework provider for
// netcup, on top of the pkg/netcup SDK. It owns provider configuration and
// client construction; data sources and resources register themselves via
// Provider.DataSources and Provider.Resources.
package provider

import (
	"context"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// Ensure netcupProvider satisfies the framework's Provider interface.
var _ provider.Provider = &netcupProvider{}

// netcupProvider is the netcup Terraform provider.
type netcupProvider struct {
	// version is the provider version reported in Metadata, wired from
	// internal/version.Version by main().
	version string
}

// netcupProviderModel mirrors the provider configuration block's schema.
type netcupProviderModel struct {
	AccessToken  types.String `tfsdk:"access_token"`
	RefreshToken types.String `tfsdk:"refresh_token"`
	APIEndpoint  types.String `tfsdk:"api_endpoint"`
	OIDCEndpoint types.String `tfsdk:"oidc_endpoint"`
}

// New returns a provider factory, for use with providerserver.Serve. version
// should be internal/version.String().
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &netcupProvider{version: version}
	}
}

func (p *netcupProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "netcup"
	resp.Version = p.version
}

func (p *netcupProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Interacts with netcup Server Control Panel (SCP) infrastructure.",
		Attributes: map[string]schema.Attribute{
			"access_token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "OAuth 2.0 access token minted by `netcupctl auth login`. Defaults to the NETCUP_ACCESS_TOKEN environment variable.",
			},
			"refresh_token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "OAuth 2.0 refresh token used to transparently renew the access token across a `terraform apply`. Defaults to the NETCUP_REFRESH_TOKEN environment variable.",
			},
			"api_endpoint": schema.StringAttribute{
				Optional:    true,
				Description: "Base URL of the SCP REST API. Defaults to the NETCUP_API_ENDPOINT environment variable, then " + netcup.DefaultAPIEndpoint + ".",
			},
			"oidc_endpoint": schema.StringAttribute{
				Optional:    true,
				Description: "Base URL of the SCP OIDC (Keycloak) endpoint. Defaults to the NETCUP_OIDC_ENDPOINT environment variable, then " + netcup.DefaultOIDCEndpoint + ".",
			},
		},
	}
}

func (p *netcupProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config netcupProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiEndpoint := netcup.DefaultAPIEndpoint
	if v := os.Getenv("NETCUP_API_ENDPOINT"); v != "" {
		apiEndpoint = v
	}
	if !config.APIEndpoint.IsNull() {
		apiEndpoint = config.APIEndpoint.ValueString()
	}

	oidcEndpoint := netcup.DefaultOIDCEndpoint
	if v := os.Getenv("NETCUP_OIDC_ENDPOINT"); v != "" {
		oidcEndpoint = v
	}
	if !config.OIDCEndpoint.IsNull() {
		oidcEndpoint = config.OIDCEndpoint.ValueString()
	}

	accessToken := os.Getenv("NETCUP_ACCESS_TOKEN")
	if !config.AccessToken.IsNull() {
		accessToken = config.AccessToken.ValueString()
	}

	refreshToken := os.Getenv("NETCUP_REFRESH_TOKEN")
	if !config.RefreshToken.IsNull() {
		refreshToken = config.RefreshToken.ValueString()
	}

	endpointOpts := []netcup.Option{
		netcup.WithAPIEndpoint(apiEndpoint),
		netcup.WithOIDCEndpoint(oidcEndpoint),
	}

	// The TokenSource needs its own *Client (sharing the same endpoints) to
	// make the OIDC refresh call; it is otherwise unrelated to the *Client
	// exposed to data sources/resources below.
	refreshClient := netcup.New(endpointOpts...)

	// Seed the refresh schedule from the access token's JWT "exp" claim.
	// netcupctl auth login only hands the provider bare token strings with no
	// separately tracked expiry, so this is the only way to know when to
	// refresh. A parse failure (token is not a JWT, or has no exp claim)
	// falls back to a zero expiry, which causes the TokenSource to refresh on
	// first use when a refresh token is present, or behave as a static token
	// otherwise. See docs/ARCHITECTURE.md "Authentication".
	var expiry time.Time
	if accessToken != "" {
		if parsed, err := netcup.ParseAccessTokenExpiry(accessToken); err == nil {
			expiry = parsed
		}
	}

	tokenSource := netcup.NewTokenSource(refreshClient, accessToken, refreshToken, expiry)

	client := netcup.New(append(endpointOpts, netcup.WithTokenSource(tokenSource))...)

	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *netcupProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	// No data sources are registered yet; netcup_servers and netcup_server
	// land in #27/#28.
	return nil
}

func (p *netcupProvider) Resources(_ context.Context) []func() resource.Resource {
	// No resources are registered yet; netcup_rdns lands in #29.
	return nil
}
