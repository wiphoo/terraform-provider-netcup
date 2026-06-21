# Terraform Provider for Netcup

A modern, open-source Terraform provider for Netcup infrastructure.

This project aims to provide a clean Terraform interface for Netcup services, starting with SCP (Server Control Panel) features and expanding later to CCP/DNS and other Netcup APIs.

## Status

Early planning / bootstrap stage.

The first target release is `v0.1.0`.

## Planned v0.1.0 Scope

- Shared Go SDK foundation
- SCP access-token authentication
- Refresh-token support
- Optional `netcupctl` CLI for login, token refresh, and API debugging
- Provider configuration
- `netcup_servers` data source
- `netcup_server` data source
- `netcup_rdns` resource
- Import support for reverse DNS
- Unit tests
- Acceptance test foundation
- GitHub Actions CI
- Release automation

## Authentication Model

The Netcup SCP REST API should be treated as an access-token and refresh-token API.

Do not assume a traditional client ID / client secret flow for the provider MVP.

Supported environment variables:

```bash
export NETCUP_ACCESS_TOKEN="..."
export NETCUP_REFRESH_TOKEN="..."
export NETCUP_API_ENDPOINT="https://www.servercontrolpanel.de/scp-core/api/v1"
```

Planned provider configuration:

```hcl
provider "netcup" {
  access_token  = var.netcup_access_token
  refresh_token = var.netcup_refresh_token
}
```

The provider should avoid writing tokens to Terraform state unless unavoidable.

## Example

```hcl
terraform {
  required_providers {
    netcup = {
      source = "wiphoo/netcup"
    }
  }
}

provider "netcup" {}

data "netcup_servers" "all" {}

resource "netcup_rdns" "server" {
  ip_address = "2a03:xxxx::1"
  hostname   = "server.example.com"
}
```

## CLI Plan

Netcup does not appear to provide an official general-purpose CLI for SCP automation.

This project may include a small companion CLI named `netcupctl` to help with:

- Device/login flow helper
- Access token refresh
- Server listing
- Reverse DNS inspection
- API debugging during provider development

The CLI should reuse the same internal SDK as the Terraform provider.

## Design Principles

- Keep Terraform resource names clean and provider-oriented.
- Hide SCP/CCP implementation details behind provider services.
- Build a reusable SDK layer before provider resources become complex.
- Avoid destructive lifecycle features in early releases.
- Do not use Terraform as a cloud-init, SSH, Ansible, or Kubernetes bootstrap tool.

## Documentation

- [Roadmap](docs/ROADMAP.md)
- [Architecture](docs/ARCHITECTURE.md)
- [MVP Specification](docs/MVP.md)
- [Contributing](docs/CONTRIBUTING.md)

## License

This project is intended to be released under the Mozilla Public License 2.0.
