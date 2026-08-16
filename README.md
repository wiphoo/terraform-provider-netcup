# Terraform Provider for Netcup

[![CI](https://github.com/wiphoo/terraform-provider-netcup/actions/workflows/ci.yml/badge.svg)](https://github.com/wiphoo/terraform-provider-netcup/actions/workflows/ci.yml)
[![License: MPL 2.0](https://img.shields.io/badge/License-MPL_2.0-brightgreen.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/wiphoo/terraform-provider-netcup.svg)](https://pkg.go.dev/github.com/wiphoo/terraform-provider-netcup)

A modern, open-source Terraform provider and CLI for [Netcup](https://www.netcup.de/)
infrastructure. It targets the SCP (Server Control Panel) REST API first, with
CCP/DNS support planned in later releases.

## Status

**v0.6.2 — SSH key management (Terraform provider) is available, with a Create-time duplicate-key guard.**

The `netcupctl` CLI, Go SDK, and release automation shipped in v0.1.0; the
Terraform provider (data sources, rDNS resource) in v0.2.0. Later releases added
power, rescue, and image/snapshot operations to the CLI (v0.3.0) and provider
(v0.4.0), native OS reinstall to the CLI (v0.5.0), the
`netcup_server_reinstall` resource to the provider (v0.6.0), and the
`netcup_ssh_key` resource / `netcup_ssh_keys` data source (v0.6.1), hardened in
v0.6.2 so `Create` refuses to register a duplicate of a key the account already
holds. See the [Roadmap](docs/ROADMAP.md) for the full plan.

## Quick start — netcupctl

Install a released binary from the
[Releases page](https://github.com/wiphoo/terraform-provider-netcup/releases),
or build from source:

```bash
go install github.com/wiphoo/terraform-provider-netcup/cmd/netcupctl@latest
```

Authenticate with the OAuth 2.0 device flow (opens a browser verification URL),
then use the CLI:

```bash
netcupctl auth login
netcupctl server list
netcupctl server get <id>
netcupctl rdns get <ip>
netcupctl rdns set <ip> <hostname>
netcupctl version
```

For headless/scripted use, skip the browser flow with pre-issued tokens:

```bash
export NETCUP_ACCESS_TOKEN="..."
export NETCUP_REFRESH_TOKEN="..."
netcupctl server list
```

## netcupctl operations

Beyond listing servers, `netcupctl` controls a server's power state and rescue
system and lists its installable images and snapshots. All commands take a
numeric server `<id>` (from `netcupctl server list`) and support `--json`.

> ℹ️ **Availability:** power/rescue/image/snapshot commands require **v0.3.0+**;
> `server reinstall` requires **v0.5.0+**. Older releases fail with an
> unknown-subcommand error — download the latest release or `make build`.

> ⚠️ **Several commands cause downtime — some are destructive.** Read
> [Operational risk & downtime](#operational-risk--downtime) before running the
> power, rescue, or reinstall commands.

### Power state

```bash
netcupctl server power status <id>              # read live state (RUNNING/SHUTOFF/…)
netcupctl server power on <id>                  # power on (no downtime, no prompt)
netcupctl server power off <id>                 # soft/ACPI shutdown (prompts)
netcupctl server power off <id> --hard --force --wait
netcupctl server power suspend <id>             # pause
netcupctl server power reboot <id> [--hard]     # power-cycle
```

Flags: `--wait` polls the async task to a terminal state; `--hard` selects the
forced variant (`POWEROFF` for `off`, `RESET` for `reboot`); `--force`/`--yes`
skips the downtime confirmation; `--json` emits machine-readable output.

### Rescue system

```bash
netcupctl server rescue status <id>             # active? (shows password when active)
netcupctl server rescue enable <id> --wait      # REBOOTS into rescue (prompts)
netcupctl server rescue disable <id> --wait     # REBOOTS back to normal OS (prompts)
```

The rescue password is only available while rescue mode is active. `enable
--wait` reads it back once activation finishes; the API may expose it a moment
later, so if it is not ready yet, re-run `rescue status` to retrieve it.

### Images and snapshots (read-only)

```bash
netcupctl server images <id> [--json]           # installable OS images
netcupctl server snapshots <id> [--json]        # snapshots
```

Snapshot create/delete/restore is planned for a later release — see the
[Roadmap](docs/ROADMAP.md).

### Reinstall

> ⚠️ **DESTRUCTIVE — data loss and downtime.** A reinstall **wipes the server
> entirely**; all data is permanently lost and unrecoverable, and the server is
> down for the duration of the OS install. Read
> [Operational risk & downtime](#operational-risk--downtime) first.

```bash
netcupctl server images <id>                    # discover valid image ids first

# Reinstall with the required --image (prompts to confirm):
netcupctl server reinstall <id> --image <flavourId>

# Full example: hostname, SSH key, wait for completion.
netcupctl server reinstall <id> --image 123 --hostname server.example.com \
  --ssh-key 42 --wait

# Post-install bootstrap script — inline, from a file, or '-' for stdin:
netcupctl server reinstall <id> --image 123 --custom-script '#!/bin/sh
apt-get update && apt-get install -y docker.io'
netcupctl server reinstall <id> --image 123 --custom-script-file /path/to/bootstrap.sh

netcupctl server reinstall <id> --image 123 --force   # skip prompt (scripted)
```

Flags: `--image <flavourId>` is **required** (ids from `server images <id>`);
`--hostname`, `--ssh-key` (repeatable), `--ssh-password-auth`, `--custom-script`
/ `--custom-script-file`, `--wait`, `--force`/`--yes`, `--json`. Full workflow
and custom-script guidance: [docs/REINSTALL.md](docs/REINSTALL.md).

### Operational risk & downtime

Several `netcupctl` commands are **operationally destructive** — treat them with
the same care as the equivalent action from the SCP web panel.

| Command | Effect | Downtime |
|---------|--------|----------|
| `server power off` | Shuts the server down (soft/ACPI; `--hard` = forced `POWEROFF`) | **Yes** — offline until powered on |
| `server power suspend` | Pauses (suspends) the server | **Yes** — unresponsive until resumed |
| `server power reboot` | Power-cycles the server (soft; `--hard` = `RESET`) | **Yes** — brief outage |
| `server power on` | Powers the server on | No |
| `server rescue enable` | **Reboots** into the rescue environment | **Yes** — normal OS not running |
| `server rescue disable` | **Reboots** back into the normal OS | **Yes** — brief outage |
| `server reinstall` | **Wipes the server** and reinstalls the OS | **Yes** — down during install; all data lost |
| `power status` / `rescue status` / `images` / `snapshots` | Read-only | No |

CLI safeguards:

- **Confirmation prompts** on `power off/suspend/reboot`, `rescue
  enable/disable`, and `server reinstall` — written to **stderr** so `--json`
  output on stdout stays clean.
- **`--force`/`--yes`** skips the prompt for non-interactive use — pass it only
  when you have accepted the downtime.
- **`--wait`** polls the async task (`202 TaskInfo`) to a terminal state so
  scripts can observe success/failure.
- **`--hard`** opts into the forced variants (`POWEROFF`/`RESET`), which skip a
  clean guest shutdown — prefer the soft default unless the server is stuck.

## Authentication model

The Netcup SCP REST API is an OAuth 2.0 / OIDC API backed by Keycloak. There is
no client-secret flow — clients authenticate against the public `scp` client
using the **device authorization flow**, then call the API with a short-lived
Bearer access token. Two independent gates apply:

1. **IP allowlist** — your client IP (or CIDR) must be allowed in the SCP REST
   API settings before any token-based call succeeds.
2. **Device authorization** — browser approval grants tokens without putting your
   account password in a script.

```bash
export NETCUP_API_ENDPOINT="https://www.servercontrolpanel.de/scp-core/api"
export NETCUP_OIDC_ENDPOINT="https://www.servercontrolpanel.de/realms/scp/protocol/openid-connect"
export NETCUP_ACCESS_TOKEN="..."    # pre-issued; optional when using auth login
export NETCUP_REFRESH_TOKEN="..."   # pre-issued; optional when using auth login
```

Treat the refresh token like a password — it can mint new access tokens without
another browser approval. Never log or commit tokens.

## Terraform provider (v0.6.2 — available)

The provider is built on the same Go SDK as `netcupctl`. See [examples/](examples/)
for ready-to-use configurations.

**Resources:** [`netcup_rdns`](examples/rdns.tf) (v0.2.0),
[`netcup_server_power`](examples/server_power.tf) (v0.4.0),
[`netcup_server_rescue`](examples/server_rescue.tf) (v0.4.0),
[`netcup_server_reinstall`](examples/server_reinstall.tf) (v0.6.0; destructive),
[`netcup_ssh_key`](examples/ssh_key.tf) (v0.6.1).

**Data sources:** [`netcup_servers`](examples/servers.tf),
[`netcup_server`](examples/server.tf) (v0.2.0),
[`netcup_server_images`](examples/server_images.tf),
[`netcup_server_snapshots`](examples/server_snapshots.tf) (v0.4.0),
[`netcup_ssh_keys`](examples/ssh_key.tf) (v0.6.1).

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

> **Refresh-token rotation caveat.** Keycloak may rotate the refresh token when
> it is used, so reusing the same `NETCUP_REFRESH_TOKEN` across separate
> `terraform apply` runs can fail. If you hit token errors, re-run
> `netcupctl auth login` to mint fresh tokens.

### Local development

Until the provider is published on the Terraform Registry (planned for v1.0.0),
use a `dev_overrides` block in `~/.terraformrc` pointing at a locally-built
binary:

```hcl
provider_installation {
  dev_overrides {
    "wiphoo/netcup" = "/path/to/your/clone/bin"
  }
  direct {}
}
```

Build the binaries, export tokens, and run `terraform plan` directly (the dev
override makes `terraform init` unnecessary — and init would otherwise fail
because `wiphoo/netcup` is not yet on the Registry):

```bash
cd /path/to/your/clone
go build -o bin/ ./cmd/terraform-provider-netcup
go build -o bin/ ./cmd/netcupctl
eval "$(./bin/netcupctl auth export)"
cd examples
terraform plan
```

A bare `terraform plan` in `examples/` only exercises the `netcup_servers` data
source — a safe read-only smoke test on your account. The single-server lookup
and rDNS/power/rescue examples are opt-in via `count`, skipped unless you pass
values:

```bash
terraform plan -var 'server_id=123456'
terraform plan -var 'rdns_ip_address=203.0.113.10' -var 'rdns_hostname=host.example.com'
terraform plan -var 'server_id=123456' -var 'power_state=OFF'      # v0.4.0
terraform plan -var 'server_id=123456' -var 'rescue_enable=true'   # v0.4.0; reboots
```

### Provider operational risk & downtime

The provider exposes the same power/rescue/reinstall operations with the same
downtime profile; the Terraform lifecycle adds specific failure modes:

| Resource | Operation | Downtime | Notes |
|----------|-----------|----------|-------|
| `netcup_server_power` | Create/update to `OFF` or `SUSPENDED` | **Yes** | Offline until set back to `ON` |
| `netcup_server_power` | Create/update to `ON` (no `state_option`) | No | — |
| `netcup_server_power` | **Destroy** | **No** | **No-op** — removed from state; server NOT powered off (deliberate safety measure) |
| `netcup_server_power` | `state_option = "POWEROFF"` | **Yes** | Forced poweroff, no guest shutdown |
| `netcup_server_power` | `state_option = "RESET"`/`"POWERCYCLE"` | **Yes** | Brief outage during reboot |
| `netcup_server_rescue` | Create (enable) / Destroy (disable) | **Yes** | Both **reboot** the server |
| `netcup_server_reinstall` | Create or replace | **Yes** | **Wipes the server** and reinstalls the OS; all data lost |
| `netcup_server_reinstall` | **Destroy** | **No** | **No-op** — removes only Terraform state; does not reinstall/wipe |
| `netcup_server_images` / `netcup_server_snapshots` | Read | No | Read-only data sources |

Key semantics:

- **`wait`** (default `true`) blocks `terraform apply` until the async SCP task
  reaches a terminal state (FINISHED, ERROR, or canceled). Set `wait = false` to
  fire-and-forget and monitor the task separately. `netcup_server_reinstall`'s
  `Read` only confirms the server exists — it does not poll or reconcile the
  reinstall task.
- **Destroy is not power-off.** Removing `netcup_server_power` only drops it from
  state; to power a server off, set `state = "OFF"`.
- **Reinstall is destructive.** Creating or replacing `netcup_server_reinstall`
  wipes the server; any changed install input (including `custom_script`) runs
  another reinstall. Destroy is deliberately a no-op.
- **Reinstall secrets are Sensitive.** Treat `custom_script` as a secret (it may
  contain credentials) and never put `additional_user_password` in logs or
  source control.

Full provider workflow and attribute reference:
[Server Reinstall](docs/REINSTALL.md#terraform-provider).

## Design principles

- Keep the public Terraform interface simple and stable.
- Hide SCP/CCP implementation details behind stable resource abstractions.
- Build a reusable SDK layer before provider resources become complex.
- Avoid destructive lifecycle features in early releases.
- Do not use Terraform as a cloud-init, SSH, Ansible, or Kubernetes bootstrap tool.

## Releasing

`netcupctl` and `terraform-provider-netcup` binaries are built and published
automatically by [GoReleaser](https://goreleaser.com) via the `Release` GitHub
Actions workflow (`.github/workflows/release.yml`); configuration is in
`.goreleaser.yaml`. Cut a release by pushing a SemVer tag:

```bash
git tag v0.6.2
git push origin v0.6.2
```

The workflow builds both binaries for linux, macOS, and Windows (amd64 and
arm64), embeds the tag as the version, produces `tar.gz`/`.zip` archives plus a
single SHA-256 `checksums.txt`, and creates a GitHub Release with all assets.

### Verifying a release

`checksums.txt` is signed with [cosign](https://docs.sigstore.dev/) in
**keyless** mode using the GitHub Actions OIDC identity — no signing key to store
or rotate. Each release includes `checksums.txt.bundle` (signature + signing
certificate in one file):

```bash
# 1. Verify the checksums file was signed by this repo's release workflow.
cosign verify-blob \
  --bundle checksums.txt.bundle \
  --certificate-identity-regexp '^https://github.com/wiphoo/terraform-provider-netcup/\.github/workflows/release\.yml@refs/tags/' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  checksums.txt

# 2. Verify your downloaded archive against the checksums.
sha256sum --check --ignore-missing checksums.txt
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, conventions, and
the PR process. Please report security vulnerabilities privately — see
[SECURITY.md](SECURITY.md).

## Documentation

- [Roadmap](docs/ROADMAP.md)
- [Architecture](docs/ARCHITECTURE.md)
- [MVP Specification](docs/MVP.md)
- [Contributing](CONTRIBUTING.md)
- [Security](SECURITY.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)

## License

Licensed under the Mozilla Public License 2.0. See [LICENSE](LICENSE).
