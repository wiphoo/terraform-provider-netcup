# Terraform Provider for Netcup

[![CI](https://github.com/wiphoo/terraform-provider-netcup/actions/workflows/ci.yml/badge.svg)](https://github.com/wiphoo/terraform-provider-netcup/actions/workflows/ci.yml)
[![License: MPL 2.0](https://img.shields.io/badge/License-MPL_2.0-brightgreen.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/wiphoo/terraform-provider-netcup.svg)](https://pkg.go.dev/github.com/wiphoo/terraform-provider-netcup)

A modern, open-source Terraform provider and CLI for [Netcup](https://www.netcup.de/)
infrastructure. It targets the SCP (Server Control Panel) REST API first, with CCP/DNS
and other Netcup APIs planned in later releases.

## Status

**v0.2.0 — Terraform provider is available.**

The `netcupctl` CLI, shared Go SDK, CI, and release automation shipped in v0.1.0.
The Terraform provider (data sources, rDNS resource, examples, and docs) ships in v0.2.0 on top of the same SDK.

See the [Roadmap](docs/ROADMAP.md) for the full release plan.

## Quick start — netcupctl

Download the latest `netcupctl` binary from the
[Releases page](https://github.com/wiphoo/terraform-provider-netcup/releases),
or build from source:

```bash
go install github.com/wiphoo/terraform-provider-netcup/cmd/netcupctl@latest
```

Log in with the OAuth 2.0 device-authorization flow (opens a browser
verification URL):

```bash
netcupctl auth login
```

List your servers (smoke-tests both authentication gates):

```bash
netcupctl server list
netcupctl server get <id>
```

Manage reverse DNS:

```bash
netcupctl rdns get <ip>
netcupctl rdns set <ip> <hostname>
```

Print the version:

```bash
netcupctl version
```

### Headless / scripted use

Skip the browser flow by supplying pre-issued tokens:

```bash
export NETCUP_ACCESS_TOKEN="..."
export NETCUP_REFRESH_TOKEN="..."
netcupctl server list
```

## Authentication model

The Netcup SCP REST API is an OAuth 2.0 / OIDC API backed by Keycloak. There is no
traditional client ID / client secret flow — clients authenticate against the public
`scp` client using the **device authorization flow** and then call the REST API with
a short-lived Bearer access token.

There are two independent authentication gates:

1. **IP allowlist** — your client IP (or CIDR) must be allowed in the SCP REST API
   settings before any token-based call succeeds.
2. **Device authorization** — browser approval grants tokens to the `scp` client
   without putting your account password in a script.

Environment variables:

```bash
export NETCUP_API_ENDPOINT="https://www.servercontrolpanel.de/scp-core/api"
export NETCUP_OIDC_ENDPOINT="https://www.servercontrolpanel.de/realms/scp/protocol/openid-connect"
export NETCUP_ACCESS_TOKEN="..."    # pre-issued; optional when using auth login
export NETCUP_REFRESH_TOKEN="..."   # pre-issued; optional when using auth login
```

Treat the refresh token like a password: it can mint new access tokens without
another browser approval. Never log or commit tokens.

## Terraform provider (v0.2.0 — available)

The Terraform provider is built on the same Go SDK as `netcupctl` and ships in
v0.2.0. See [examples/](examples/) for ready-to-use configurations:

- [Provider configuration](examples/provider.tf)
- [netcup_servers data source](examples/servers.tf)
- [netcup_server data source](examples/server.tf)
- [netcup_rdns resource](examples/rdns.tf)

```hcl
terraform {
  required_providers {
    netcup = {
      source = "wiphoo/netcup"
    }
  }
}

provider "netcup" {
  # Pre-issued tokens (minted by `netcupctl auth login`).
  access_token  = var.netcup_access_token
  refresh_token = var.netcup_refresh_token
}

data "netcup_servers" "all" {}

resource "netcup_rdns" "server" {
  ip_address = "203.0.113.10"
  hostname   = "server.example.com"
}
```

> **Refresh-token rotation caveat**
>
> Keycloak may rotate the refresh token when it is used. Reusing the same
> `NETCUP_REFRESH_TOKEN` across separate `terraform apply` runs may fail after
> a rotation has occurred. If you encounter token errors, re-run
> `netcupctl auth login` to mint fresh tokens.

### Terraform provider local development

Before the provider is published on the Terraform Registry (planned for v1.0.0),
you need a `dev_overrides` CLI configuration to point at a locally-built binary.
Add the following to `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "wiphoo/netcup" = "/path/to/your/clone/bin"
  }
  direct {}
}
```

Build the provider binary, then run `terraform plan` directly — `terraform init`
will fail with a "provider not found" error because `wiphoo/netcup` is not yet
published on the Terraform Registry, but the dev override makes init
unnecessary for plan/apply:

```bash
cd /path/to/your/clone
go build -o bin/ ./cmd/terraform-provider-netcup
cd path/to/your/examples
terraform plan
```

## Design principles

- Keep the public Terraform interface simple and stable.
- Hide SCP/CCP implementation details behind stable resource abstractions.
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

The workflow builds `netcupctl` for linux, macOS, and Windows (amd64 and arm64),
embeds the tag as the version (visible in `netcupctl version`), produces
`tar.gz` / `.zip` archives and a SHA-256 `checksums.txt`, and creates a GitHub
Release with all assets attached.

### Verifying a release

The `checksums.txt` file is signed with [cosign](https://docs.sigstore.dev/) in
**keyless** mode using the GitHub Actions OIDC identity — no private signing key
to store or rotate. Each release includes `checksums.txt.sig` and
`checksums.txt.pem`.

```bash
# 1. Verify the checksums file was signed by this repo's release workflow.
#    checksums.txt.bundle contains the signature and signing certificate.
cosign verify-blob \
  --bundle checksums.txt.bundle \
  --certificate-identity-regexp '^https://github.com/wiphoo/terraform-provider-netcup/\.github/workflows/release\.yml@refs/tags/' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  checksums.txt

# 2. Verify your downloaded archive against the checksums.
sha256sum --check --ignore-missing checksums.txt
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, conventions,
and PR process.

Please report security vulnerabilities privately — see [SECURITY.md](SECURITY.md).

## Documentation

- [Roadmap](docs/ROADMAP.md)
- [Architecture](docs/ARCHITECTURE.md)
- [MVP Specification](docs/MVP.md)
- [Contributing](CONTRIBUTING.md)
- [Security](SECURITY.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)

## License

This project is licensed under the Mozilla Public License 2.0. See [LICENSE](LICENSE).
