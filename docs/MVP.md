# MVP Specification v0.1.0

## Objective

Deliver the first usable release of Terraform Provider Netcup.

The MVP should provide immediate operational value while minimizing risk and maintenance burden.

## Included

SDK features:

- Shared Go SDK package
- SCP HTTP client wrapper
- Access-token authentication
- Refresh-token support
- Friendly error mapping

CLI features:

- Minimal `netcupctl` command structure
- Optional token refresh command
- Optional server list command for API debugging

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
- Full interactive login unless the SCP flow is stable and documented enough
- Power operations
- Rescue mode
- ISO mounting
- OS reinstall
- Snapshot create or restore
- CCP DNS management
- SSH provisioning
- Cloud-init execution
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

- NETCUP_ACCESS_TOKEN
- NETCUP_REFRESH_TOKEN
- NETCUP_API_ENDPOINT

Default API endpoint:

https://www.servercontrolpanel.de/scp-core/api/v1

## Authentication Requirements

The provider must not assume client ID / client secret authentication.

The provider should:

- Send access tokens as bearer tokens.
- Refresh access tokens when possible.
- Avoid logging tokens.
- Avoid storing tokens in Terraform state unless unavoidable.
- Return clear authentication diagnostics.

## CLI Requirements

The optional `netcupctl` CLI should use the same SDK as the provider.

Potential MVP commands:

```bash
netcupctl auth refresh
netcupctl server list
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

Pull requests should pass:

- go test ./...
- golangci-lint run
- terraform validate for examples

## v0.1.0 Definition of Done

v0.1.0 is complete when:

- A user can authenticate using access and refresh tokens.
- A user can refresh a token through shared SDK logic.
- A user can discover servers.
- A user can inspect a server.
- A user can manage reverse DNS.
- Terraform plan/apply cycle is stable.
- Provider can be released publicly.
