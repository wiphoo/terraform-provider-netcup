# Roadmap

## Vision

Build a modern open-source Terraform provider for Netcup.

The provider should expose a clean Terraform interface and hide SCP, CCP, and future API implementation details behind stable resources.

The project should also provide a reusable Go SDK foundation. A small companion CLI, `netcupctl`, may be included to support login, token refresh, API debugging, and provider development.

## Authentication Direction

The SCP API should be modeled around:

- access token
- refresh token
- token refresh flow

Do not design the MVP around client ID / client secret unless Netcup later exposes an official machine-to-machine flow.

## v0.1.0 - Foundation

Initial scope:

- Shared Go SDK package
- SCP access-token authentication
- Refresh-token support
- Environment variable support
- Optional `netcupctl auth refresh`
- Optional `netcupctl server list`
- Provider configuration
- `netcup_servers` data source
- `netcup_server` data source
- `netcup_rdns` resource
- Import support for `netcup_rdns`
- Unit tests
- Acceptance test foundation
- GitHub Actions CI
- Release automation

## v0.2.0 - CLI and Operations

Planned scope:

- `netcupctl auth login` helper if the device-login flow is stable enough
- `netcupctl rdns get`
- `netcupctl rdns set`
- Image data sources
- Snapshot data sources
- Power state management
- Rescue mode support

Power and rescue features must document downtime and operational risk.

## v0.3.0 - Snapshot Management

Planned scope:

- Snapshot create
- Snapshot delete
- Snapshot restore workflows

## v0.4.0 - DNS Support

Planned scope:

- DNS zones
- DNS records
- Examples for cert-manager and ExternalDNS

## v1.0.0

Requirements:

- Stable provider API
- Stable SDK interfaces
- Terraform Registry publication
- Documentation for all resources
- Import support for all resources
- Acceptance test coverage
- Upgrade guidance

## Non-goals

The provider should not become a provisioning or configuration-management tool.

Out of scope:

- SSH provisioning
- OS bootstrap
- Kubernetes installation
- Ansible replacement
- Application deployment
