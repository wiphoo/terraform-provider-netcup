# MVP Specification v0.1.0

## Objective

Deliver the first usable release of Terraform Provider Netcup.

The MVP should provide immediate operational value while minimizing risk and maintenance burden.

## Included

SDK features:

- Shared Go SDK package (built first, before the provider)
- SCP HTTP client wrapper
- OAuth 2.0 device-authorization login
- Access-token authentication
- Refresh-token support
- Friendly error mapping

CLI features (the CLI is the first consumer of the SDK):

- `netcupctl` command structure
- `netcupctl auth login` device-authorization flow
- Token refresh command
- Server list command for API debugging

Provider features:

- Provider configuration
- Access token and refresh token configuration
- Environment variable support
- Documentation

Data sources:

- netcup_servers
- netcup_server

Resources:

- netcup_rdns

## Excluded

The MVP does not include:

- Client ID / client secret auth flow
- Power operations
- Rescue mode
- ISO mounting
- OS reinstall (planned for v0.3.0 — see ROADMAP)
- Provisioning / `customScript` bootstrap (planned for v0.3.0)
- Snapshot create or restore
- CCP DNS management
- Terraform-driven SSH provisioning
- Kubernetes installation

## Provider Configuration

The provider should support direct configuration and environment variables.

Planned configuration:

```hcl
provider "netcup" {
  access_token  = var.netcup_access_token
  refresh_token = var.netcup_refresh_token
}
```

Supported environment variables:

- NETCUP_API_ENDPOINT
- NETCUP_OIDC_ENDPOINT
- NETCUP_ACCESS_TOKEN
- NETCUP_REFRESH_TOKEN

Default REST API endpoint:

https://www.servercontrolpanel.de/scp-core/api/v1

Default OIDC endpoint:

https://www.servercontrolpanel.de/realms/scp/protocol/openid-connect

## Authentication Requirements

The SCP API is an OAuth 2.0 / OIDC API (Keycloak). The provider must not assume client
ID / client secret authentication.

The provider should:

- Accept pre-issued access and refresh tokens (minted by `netcupctl auth login`).
- Not run the interactive device-authorization flow itself; that lives only in the CLI,
  since `terraform apply` is non-interactive.
- Send access tokens as Bearer tokens.
- Refresh short-lived access tokens using the refresh token when possible.
- Avoid logging tokens.
- Avoid storing tokens in Terraform state unless unavoidable.
- Return clear authentication diagnostics, including the IP-allowlist gate as a likely
  cause of authorization failures.

## CLI Requirements

The `netcupctl` CLI is the first consumer of the shared SDK and must use the same SDK
as the provider.

MVP commands:

```bash
netcupctl auth login     # OAuth 2.0 device-authorization flow
netcupctl auth refresh   # refresh the access token
netcupctl server list    # smoke test for both auth gates
```

CLI output should be useful for debugging provider development.

## Data Source: netcup_servers

Purpose: list all servers accessible to the authenticated account.

Computed fields:

- id
- hostname
- status
- product_name
- ipv4_addresses
- ipv6_addresses

Acceptance criteria:

- Valid credentials return all available servers.
- Empty server list is not an error.
- Invalid credentials return a clear authentication error.

## Data Source: netcup_server

Purpose: retrieve details for one server.

Required fields:

- id

Computed fields:

- hostname
- status
- product_name
- ipv4_addresses
- ipv6_addresses

Acceptance criteria:

- Valid server ID returns server details.
- Unknown server ID returns a clear not-found error.

## Resource: netcup_rdns

Purpose: manage reverse DNS entries.

Required fields:

- ip_address
- hostname

Computed fields:

- id

Resource ID format:

- ip_address

CRUD behavior:

- Create configures reverse DNS.
- Read reads current reverse DNS configuration.
- Update changes hostname.
- Delete removes or resets the reverse DNS entry depending on API capability.

Import example:

terraform import netcup_rdns.server 203.0.113.10

Acceptance criteria:

- Create reverse DNS entry.
- Update reverse DNS entry.
- Read reverse DNS entry.
- Import existing reverse DNS entry.
- Delete reverse DNS entry.
- Terraform plan is empty after successful apply.

## Testing Strategy

Unit tests should cover SDK and service-layer behavior.

Acceptance tests should cover:

- TestAccServersDataSource
- TestAccServerDataSource
- TestAccRDNSResource

## CI Requirements

CI is phased in as the codebase grows so it always reflects what actually runs:

- Now (bootstrap): `go test ./...`
- When the SDK/provider code lands: `golangci-lint run` (config: `.golangci.yml`)
- When `examples/` exist: `terraform validate` for each example

The target end state is that pull requests pass all three. The `lint` target is
already available via `make lint` locally.

## v0.1.0 Definition of Done

v0.1.0 is complete when:

- A user can authenticate using the device-authorization flow via `netcupctl auth login`.
- A user can authenticate using pre-issued access and refresh tokens.
- A user can refresh a token through shared SDK logic.
- A user can discover servers.
- A user can inspect a server.
- A user can manage reverse DNS.
- Terraform plan/apply cycle is stable.
- Provider can be released publicly.
