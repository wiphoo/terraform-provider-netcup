// Package provider implements the Terraform Plugin Framework provider for
// netcup, on top of the pkg/netcup SDK. It owns provider configuration and
// client construction; data sources and resources register themselves via
// Provider.DataSources and Provider.Resources.
package provider

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
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

	// An unknown value (e.g. `access_token = aws_secretsmanager_secret_version.x.secret_string`
	// during plan, before that resource has been applied) is not null, so
	// ValueString() would silently return "" for it below rather than the
	// intended value, clobbering the env var/default fallback. Reject unknown
	// values up front, checking every attribute before returning so a
	// practitioner sees all of them at once rather than one apply at a time.
	requireKnown(&resp.Diagnostics, "access_token", "NETCUP_ACCESS_TOKEN", config.AccessToken)
	requireKnown(&resp.Diagnostics, "refresh_token", "NETCUP_REFRESH_TOKEN", config.RefreshToken)
	requireKnown(&resp.Diagnostics, "api_endpoint", "NETCUP_API_ENDPOINT", config.APIEndpoint)
	requireKnown(&resp.Diagnostics, "oidc_endpoint", "NETCUP_OIDC_ENDPOINT", config.OIDCEndpoint)
	if resp.Diagnostics.HasError() {
		return
	}

	apiEndpoint := resolveConfigString(config.APIEndpoint, "NETCUP_API_ENDPOINT", netcup.DefaultAPIEndpoint)
	oidcEndpoint := resolveConfigString(config.OIDCEndpoint, "NETCUP_OIDC_ENDPOINT", netcup.DefaultOIDCEndpoint)
	accessToken := resolveConfigString(config.AccessToken, "NETCUP_ACCESS_TOKEN", "")
	refreshToken := resolveConfigString(config.RefreshToken, "NETCUP_REFRESH_TOKEN", "")

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

// requireKnown appends an attribute error to diags if config is unknown,
// since Configure has no way to resolve config > env precedence for a value
// that Terraform hasn't determined yet (see the Configure comment above).
func requireKnown(diags *diag.Diagnostics, attr, envVar string, config types.String) {
	if !config.IsUnknown() {
		return
	}
	diags.AddAttributeError(
		path.Root(attr),
		fmt.Sprintf("Unknown netcup provider %s", attr),
		fmt.Sprintf(
			"The provider cannot be configured because the %q value is not known until after apply "+
				"(for example, it comes from a resource created in this run). "+
				"Set it directly in the provider configuration, or via the %s environment variable, "+
				"instead of deriving it from a value that isn't known during plan.",
			attr, envVar,
		),
	)
}

// resolveConfigString applies the standard config > env var > fallback
// precedence for a single provider schema attribute. Callers must reject
// unknown values first (via requireKnown): ValueString() returns "" for an
// unknown value, which would otherwise silently override envVar/fallback.
func resolveConfigString(config types.String, envVar, fallback string) string {
	value := fallback
	if v := os.Getenv(envVar); v != "" {
		value = v
	}
	if !config.IsNull() {
		value = config.ValueString()
	}
	return value
}

func (p *netcupProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewServersDataSource,
		NewServerDataSource,
		NewServerImagesDataSource,
	}
}

func (p *netcupProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewRDNSResource,
		NewRescueResource,
	}
}
