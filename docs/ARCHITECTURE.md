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

## Repository Structure

- cmd/terraform-provider-netcup/
- cmd/netcupctl/
- internal/provider/
- internal/datasources/
- internal/resources/
- internal/services/
- internal/sdk/
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

Responsible for stable internal APIs used by both the Terraform provider and CLI.

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

The MVP authentication model should use:

- access token
- refresh token
- API endpoint

Environment variables:

- NETCUP_ACCESS_TOKEN
- NETCUP_REFRESH_TOKEN
- NETCUP_API_ENDPOINT

Do not assume client ID / client secret for the MVP.

## Error Handling

Errors should be readable and actionable.

Example: Server with ID 123456 not found.

## Versioning

The project should follow Semantic Versioning.
