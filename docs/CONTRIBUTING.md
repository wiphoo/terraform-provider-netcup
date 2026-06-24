# Contributing

Thank you for contributing to Terraform Provider Netcup.

## Development Requirements

- Go
- Terraform CLI
- Make
- Git

## Setup

Clone the repository:

```bash
git clone https://github.com/wiphoo/terraform-provider-netcup.git
cd terraform-provider-netcup
```

Install dependencies:

```bash
go mod download
```

Run tests:

```bash
go test ./...
```

## Architecture Rules

Resources must not:

- Call HTTP APIs directly
- Use generated API clients directly
- Contain business logic that belongs in services

Resources should call service interfaces.

## Generated Code

Generated OpenAPI code must not be edited manually.

Regenerate generated clients instead.

## Testing

Every feature should include:

- Unit tests
- Documentation updates
- Example updates when applicable

Acceptance tests are required for resources that interact with real Netcup APIs.

## Pull Request Checklist

- Tests pass
- Documentation updated
- Examples updated when applicable
- No unnecessary breaking changes

## Labels

Suggested label groups:

- type/bug
- type/enhancement
- type/documentation
- type/refactor
- area/provider
- area/scp
- area/ccp
- area/dns
- area/testing
- priority/high
- priority/medium
- priority/low

## Release Process

Releases should be automated through GitHub Actions.

Versioning should follow Semantic Versioning.

Examples:

- v0.1.0
- v0.2.0
- v1.0.0

## Community Guidelines

Be respectful and constructive.

The goal is to build a useful open-source Terraform provider for Netcup.
