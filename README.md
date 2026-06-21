# Terraform Provider for Netcup

A modern, open-source Terraform provider for Netcup infrastructure.

This project aims to provide a clean Terraform interface for Netcup services, starting with SCP (Server Control Panel) features and expanding later to CCP/DNS and other Netcup APIs.

## Status

Early planning / bootstrap stage.

The first target release is `v0.1.0`.

## Planned v0.1.0 Scope

- Provider configuration
- SCP authentication
- `netcup_servers` data source
- `netcup_server` data source
- `netcup_rdns` resource
- Import support for reverse DNS
- Unit tests
- Acceptance test foundation
- GitHub Actions CI
- Release automation

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

## Environment Variables

```bash
export NETCUP_CLIENT_ID="..."
export NETCUP_CLIENT_SECRET="..."
export NETCUP_API_ENDPOINT="https://www.servercontrolpanel.de/scp-core/api/v1"
```

## Design Principles

- Keep Terraform resource names clean and provider-oriented.
- Hide SCP/CCP implementation details behind provider services.
- Avoid destructive lifecycle features in early releases.
- Do not use Terraform as a cloud-init, SSH, Ansible, or Kubernetes bootstrap tool.

## Documentation

- [Roadmap](docs/ROADMAP.md)
- [Architecture](docs/ARCHITECTURE.md)
- [MVP Specification](docs/MVP.md)
- [Contributing](docs/CONTRIBUTING.md)

## License

This project is intended to be released under the Mozilla Public License 2.0.
