# Architecture

Terraform Provider Netcup exposes Netcup infrastructure through Terraform resources and data sources.

The first implementation target is SCP support. The design should allow future CCP, DNS, object storage, or other Netcup APIs without breaking existing users.

## Design Goals

- Keep the public Terraform interface simple.
- Keep API-specific code isolated.
- Make service logic testable without real API calls.
- Avoid direct dependency from resources to generated OpenAPI models.

## Layers

Terraform configuration -> Terraform Plugin Framework -> Provider layer -> Service layer -> API client layer -> Netcup APIs

## Repository Structure

- cmd/
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

## Service Layer

Responsible for business rules, validation, API model mapping, and error translation.

Terraform resources should call service interfaces, not generated clients directly.

## API Client Layer

Responsible for HTTP communication, request serialization, and response parsing.

Generated clients should live under internal client generated directories and must not be edited manually.

## Error Handling

Errors should be readable and actionable.

Example: Server with ID 123456 not found.

## Versioning

The project should follow Semantic Versioning.
