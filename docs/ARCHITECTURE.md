# Architecture

Terraform Provider Netcup exposes Netcup infrastructure through Terraform resources and data sources.

The first implementation target is SCP support. The design should allow future CCP, DNS, object storage, or other Netcup APIs without breaking existing users.

The implementation should be built around a reusable Go SDK. Terraform resources and the optional `netcupctl` CLI should share this SDK.

## Design Goals

- Keep the public Terraform interface simple.
- Keep API-specific code isolated.
- Make service logic testable without real API calls.
- Avoid direct dependency from resources to generated OpenAPI models.
- Reuse API and authentication logic between provider and CLI.

## Layers

Terraform configuration -> Terraform Plugin Framework -> Provider layer -> Service layer -> SDK layer -> API client layer -> Netcup APIs

CLI command -> CLI layer -> SDK layer -> API client layer -> Netcup APIs

## Repository Strategy

Decision: the SDK, CLI, and Terraform provider live in a single repository (monolith).
This keeps the SDK, its first consumer (`netcupctl`), and the provider in lockstep while
the APIs are still changing, and avoids premature cross-repo version coordination. The
repo name `terraform-provider-netcup` is also what the Terraform Registry expects, so a
single repo does not block publishing.

To keep the SDK reusable and a future split cheap, the SDK is a public, self-contained
boundary:

- The SDK lives under `pkg/netcup/`, not `internal/`, so it is importable by external
  consumers and by the CLI and provider.
- The SDK must not import provider or CLI packages.
- The SDK exposes its own types; resources and CLI commands do not depend on generated
  OpenAPI models directly.
- The CLI and provider depend on the SDK only through its public package surface.

Splitting is deferred until there is a concrete reason — an external consumer that should
not pull the provider dependency tree, or independent release cadences. The first step
then is a second `go.mod` inside this repo (a multi-module monorepo such as
`github.com/wiphoo/terraform-provider-netcup/sdk`), not a separate repository. A separate
repo is a last resort.

## Repository Structure

- cmd/terraform-provider-netcup/
- cmd/netcupctl/
- pkg/netcup/ (public, reusable SDK)
- internal/provider/
- internal/datasources/
- internal/resources/
- internal/services/
- internal/client/
- internal/models/
- docs/
- examples/
- tests/

## Provider Layer

Responsible for provider configuration, client initialization, dependency injection, and schema registration.

The provider layer should not contain business logic.

## CLI Layer

The optional `netcupctl` CLI should help with local development and user workflows that are awkward inside Terraform.

Examples:

- token refresh
- login helper if supported
- server list
- reverse DNS inspection
- reverse DNS update debugging

The CLI must use the same SDK and client packages as the provider.

## SDK Layer

The public, reusable layer under `pkg/netcup/`, used by both the Terraform provider and
the CLI, and importable by external consumers. It owns the HTTP client, the OIDC device
flow, and token refresh, and exposes its own types rather than generated OpenAPI models.

The SDK should expose provider-friendly operations such as:

- ListServers
- GetServer
- GetRDNS
- SetRDNS
- RefreshToken

## Service Layer

Responsible for business rules, validation, API model mapping, and error translation.

Terraform resources should call service interfaces, not generated clients directly.

## API Client Layer

Responsible for HTTP communication, request serialization, and response parsing.

Generated clients should live under internal client generated directories and must not be edited manually.

## Authentication

The SCP API is an OAuth 2.0 / OIDC API backed by Keycloak. Authentication logic lives
in the SDK and is shared by the CLI and the provider.

The MVP authentication model uses:

- OAuth 2.0 device authorization flow (public client `client_id=scp`)
- short-lived Bearer access token (~300s)
- refresh token (`offline_access` scope) for renewal
- a separate IP-allowlist gate on the REST API endpoint

Endpoints:

- OIDC base: `{base}/realms/scp/protocol/openid-connect`
  - device code: `POST {oidc}/auth/device` (`client_id=scp`, `scope=offline_access openid`)
  - token: `POST {oidc}/token`
    (`grant_type=urn:ietf:params:oauth:grant-type:device_code` and `grant_type=refresh_token`)
- REST API base: `{base}/scp-core/api` (unauthenticated health check at `/ping`; versioned resources under `/v1`)

The SDK should expose:

- `DeviceLogin` — runs the device flow and returns access + refresh tokens
- `RefreshToken` — exchanges a refresh token for a new access token
- a token source that transparently refreshes the access token before REST calls

Only the CLI calls `DeviceLogin` interactively. The provider never runs the interactive
device flow (Terraform apply is non-interactive); it takes pre-issued tokens and uses
`RefreshToken` / the token source for non-interactive renewal.

Environment variables:

- NETCUP_API_ENDPOINT (default `https://www.servercontrolpanel.de/scp-core/api`)
- NETCUP_OIDC_ENDPOINT (default `https://www.servercontrolpanel.de/realms/scp/protocol/openid-connect`)
- NETCUP_ACCESS_TOKEN (pre-issued token for headless use)
- NETCUP_REFRESH_TOKEN (pre-issued token for headless use)

Do not assume client ID / client secret for the MVP. Never log tokens. Treat the
refresh token as a secret credential.

## Error Handling

Errors should be readable and actionable.

Example: Server with ID 123456 not found.

## Versioning

The project should follow Semantic Versioning.
