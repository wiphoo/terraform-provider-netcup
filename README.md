# Terraform Provider for Netcup

A modern, open-source Terraform provider for Netcup infrastructure.

This project aims to provide a clean Terraform interface for Netcup services, starting with SCP (Server Control Panel) features and expanding later to CCP/DNS and other Netcup APIs.

## Status

Early planning / bootstrap stage.

The first target release is `v0.1.0`.

## Build Order

The `netcupctl` CLI is the first deliverable, not an optional add-on. Building the
CLI first lets us exercise the shared SDK and the device-authorization login flow
against the real SCP API before any Terraform resource is written. The Terraform
provider is then built on the same proven SDK.

1. Shared Go SDK foundation (HTTP client, OIDC device flow, token refresh)
2. `netcupctl` CLI (device login, token refresh, server list, rDNS inspection)
3. Terraform provider (data sources and `netcup_rdns` resource) on top of the SDK

## Release Plan

Each capability area ships the `netcupctl` CLI first, then the Terraform provider
in the following minor release (see [Roadmap](docs/ROADMAP.md)).

### v0.1.0 — netcupctl foundation (CLI)

- Shared Go SDK foundation (`pkg/netcup`)
- SCP OAuth 2.0 device-authorization login
- Refresh-token support and environment variable configuration
- `netcupctl` CLI: `auth login`, `auth refresh`, `server list`, `server get`, `rdns get`, `rdns set`
- Unit tests
- GitHub Actions CI (test + lint)
- `netcupctl` release automation

### v0.2.0 — Terraform provider foundation

- Provider configuration (pre-issued tokens, environment variables)
- `netcup_servers` data source
- `netcup_server` data source
- `netcup_rdns` resource
- Import support for reverse DNS
- Examples and `terraform validate` in CI
- Acceptance test foundation
- Provider release + Terraform Registry publication

## Authentication Model

The Netcup SCP REST API is an OAuth 2.0 / OIDC API backed by Keycloak. There is no
traditional client ID / client secret flow — clients authenticate against the public
`scp` client using the **device authorization flow** and then call the REST API with
a short-lived Bearer access token.

There are two independent gates:

1. **IP allowlist** — your client IP (or CIDR) must be allowed in the SCP REST API
   settings before any token-based call succeeds.
2. **Device authorization** — browser approval grants tokens to the `scp` client
   without putting your account password in a script.

Base URLs:

```bash
export NETCUP_SCP_BASE_URL="https://www.servercontrolpanel.de"
# API root: health check at /ping, versioned resources under /v1
export NETCUP_API_ENDPOINT="${NETCUP_SCP_BASE_URL}/scp-core/api"
export NETCUP_OIDC_ENDPOINT="${NETCUP_SCP_BASE_URL}/realms/scp/protocol/openid-connect"
```

Login flow (handled by `netcupctl auth login`):

1. `POST {OIDC}/auth/device` with `client_id=scp` and `scope=offline_access openid`
   to obtain a `device_code` and a `verification_uri_complete`.
2. Approve the device in the browser.
3. `POST {OIDC}/token` with
   `grant_type=urn:ietf:params:oauth:grant-type:device_code` to exchange the
   device code for an `access_token` (short-lived, ~300s) and a `refresh_token`.
4. Renew on demand: `POST {OIDC}/token` with `grant_type=refresh_token`.

The interactive device-login flow lives only in `netcupctl`. The Terraform provider
does not initiate browser approval during `terraform apply` (apply is non-interactive);
it consumes pre-issued tokens and refreshes them non-interactively:

```bash
export NETCUP_ACCESS_TOKEN="..."
export NETCUP_REFRESH_TOKEN="..."
```

Planned provider configuration:

```hcl
provider "netcup" {
  # Pre-issued tokens (e.g. minted by `netcupctl auth login`).
  access_token  = var.netcup_access_token
  refresh_token = var.netcup_refresh_token
}
```

Treat the refresh token like a password: it can mint new access tokens without
another browser approval. The provider should avoid writing tokens to Terraform
state unless unavoidable, and should never log them.

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

## CLI: `netcupctl`

Netcup does not provide an official general-purpose CLI for SCP automation, so this
project ships one. `netcupctl` is the first thing built and the reference consumer of
the shared SDK:

- `netcupctl auth login` — OAuth 2.0 device-authorization login (prints the
  verification URL, polls for approval, stores the resulting tokens)
- `netcupctl auth refresh` — refresh the access token from a refresh token
- `netcupctl server list` — list servers (smoke test for both auth gates)
- Reverse DNS inspection
- API debugging during provider development

The CLI reuses the same internal SDK as the Terraform provider, so the device-flow
and token-refresh logic is written and tested once.

## Design Principles

- Keep Terraform resource names clean and provider-oriented.
- Hide SCP/CCP implementation details behind provider services.
- Build a reusable SDK layer before provider resources become complex.
- Avoid destructive lifecycle features in early releases.
- Do not use Terraform as a cloud-init, SSH, Ansible, or Kubernetes bootstrap tool.

## Releasing

`netcupctl` binaries are built and published automatically by
[GoReleaser](https://goreleaser.com) via the `Release` GitHub Actions workflow
(`.github/workflows/release.yml`). Configuration lives in `.goreleaser.yaml`.

Cut a release by pushing a SemVer tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The workflow then:

- builds `netcupctl` for linux, macOS, and Windows (amd64 and arm64), embedding
  the tag as the version via `-ldflags` into `internal/version` (visible in
  `netcupctl version`);
- packages `tar.gz` archives (`.zip` on Windows) plus a `checksums.txt`
  (SHA-256); and
- creates a GitHub Release with the archives, checksums, and signature attached.

### Signing

The `checksums.txt` file is signed with [cosign](https://docs.sigstore.dev/) in
**keyless** mode, using the GitHub Actions OIDC identity — there is no private
signing key to store or rotate. Each release includes `checksums.txt.sig`
(signature) and `checksums.txt.pem` (signing certificate).

Verify a downloaded release:

```bash
# 1. Verify the checksums file was signed by this repo's release workflow.
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp '^https://github.com/wiphoo/terraform-provider-netcup/\.github/workflows/release\.yml@refs/tags/' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  checksums.txt

# 2. Verify your downloaded archive against the checksums.
sha256sum --check --ignore-missing checksums.txt
```

## Documentation

- [Roadmap](docs/ROADMAP.md)
- [Architecture](docs/ARCHITECTURE.md)
- [MVP Specification](docs/MVP.md)
- [Contributing](docs/CONTRIBUTING.md)

## License

This project is licensed under the Mozilla Public License 2.0. See [LICENSE](LICENSE).
