# Roadmap

## Vision

Build a modern open-source Terraform provider for Netcup.

The provider should expose a clean Terraform interface and hide SCP, CCP, and future API implementation details behind stable resources.

The project also provides a reusable Go SDK foundation. The companion CLI, `netcupctl`, is built first and supports device login, token refresh, API debugging, and provider development. The Terraform provider is built on the same SDK afterwards.

## Build Order

1. Shared Go SDK (HTTP client, OIDC device flow, token refresh).
2. `netcupctl` CLI as the first real consumer of the SDK.
3. Terraform provider on top of the proven SDK.

This sequencing lets us validate the device-authorization flow and token handling against the live SCP API before writing any Terraform resource.

## Authentication Direction

The SCP API is an OAuth 2.0 / OIDC API backed by Keycloak. The MVP is modeled around:

- OAuth 2.0 device authorization flow against the public `scp` client
- short-lived Bearer access tokens
- refresh tokens (`offline_access` scope) for renewal
- a separate IP-allowlist gate on the REST API

Do not design the MVP around client ID / client secret. There is no machine-to-machine client-secret flow today; the device flow plus refresh tokens is the supported model.

## Release Strategy

Each capability area ships in two steps: the `netcupctl` CLI first (one minor
release), then Terraform provider support for that area (the following minor
release). The CLI validates the SDK and authentication against the live SCP API
before the provider consumes the same SDK. Minor versions therefore alternate
CLI → provider as the project grows.

## v0.1.0 - netcupctl foundation (CLI)

Scope:

- Shared Go SDK package (`pkg/netcup`): HTTP client, OIDC device flow, token refresh
- SCP OAuth 2.0 device-authorization login
- Refresh-token support and environment variable configuration
- `netcupctl auth login`
- `netcupctl auth refresh`
- `netcupctl server list`
- `netcupctl server get`
- `netcupctl rdns get`
- `netcupctl rdns set`
- Unit tests
- GitHub Actions CI (test + lint)
- `netcupctl` release automation

## v0.2.0 - Terraform provider foundation (shipped)

Scope:

- Provider configuration (pre-issued tokens, environment variables)
- `netcup_servers` data source
- `netcup_server` data source
- `netcup_rdns` resource
- Import support for `netcup_rdns`
- Examples and documentation
- Acceptance test foundation
- go-vcr replay tests for SDK and provider

## v0.3.0 - Operations (CLI)

Scope:

- `netcupctl` power state management
- `netcupctl` rescue mode
- `netcupctl` image listing
- `netcupctl` snapshot listing

Power and rescue features must document downtime and operational risk.

## v0.4.0 - Operations (Terraform provider)

Scope:

- Power state management
- Rescue mode support
- Image data sources
- Snapshot data sources

## v0.5.0 - Provisioning and Reinstallation (CLI)

Scope:

- `netcupctl` server provisioning / OS install through the native SCP API
- OS reinstall workflows
- `customScript` support for post-install bootstrap (see the SCP REST API provisioning flow)
- Image selection for installs
- Clear documentation of the destructive nature of reinstall (it wipes the server)

This uses Netcup's native install + `customScript` mechanism. It is not Terraform-driven SSH, Ansible, or configuration management — those remain non-goals.

## v0.6.0 - Provisioning and Reinstallation (Terraform provider)

Scope:

- Provisioning / reinstall resource(s) with `customScript`
- Image selection

## v0.7.0 - Snapshot Management

Scope:

- Snapshot create
- Snapshot delete
- Snapshot restore workflows

(CLI first, then provider, following the release strategy above.)

## v0.8.0 - DNS Support

Scope:

- DNS zones
- DNS records
- Examples for cert-manager and ExternalDNS

(CLI first, then provider, following the release strategy above.)

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

The provider exposes Netcup's native server lifecycle, including OS install/reinstall
and `customScript` post-install bootstrap (see v0.5.0/v0.6.0). It should not, however,
become a configuration-management or in-guest provisioning tool.

Out of scope:

- Terraform-driven SSH provisioning
- Kubernetes installation
- Ansible replacement
- In-guest application deployment

Note: native OS reinstall and post-install `customScript` are in scope (v0.5.0/v0.6.0).
What stays out of scope is using Terraform itself as an SSH/Ansible/Kubernetes engine.
