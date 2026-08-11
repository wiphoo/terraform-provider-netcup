# Terraform Provider for Netcup

[![CI](https://github.com/wiphoo/terraform-provider-netcup/actions/workflows/ci.yml/badge.svg)](https://github.com/wiphoo/terraform-provider-netcup/actions/workflows/ci.yml)
[![License: MPL 2.0](https://img.shields.io/badge/License-MPL_2.0-brightgreen.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/wiphoo/terraform-provider-netcup.svg)](https://pkg.go.dev/github.com/wiphoo/terraform-provider-netcup)

A modern, open-source Terraform provider and CLI for [Netcup](https://www.netcup.de/)
infrastructure. It targets the SCP (Server Control Panel) REST API first, with CCP/DNS
and other Netcup APIs planned in later releases.

## Status

**v0.6.0 — Server reinstallation (Terraform provider) is available.**

The `netcupctl` CLI, shared Go SDK, CI, and release automation shipped in v0.1.0.
The Terraform provider (data sources, rDNS resource, examples, and docs) shipped in v0.2.0 on top of the same SDK.
v0.3.0 adds `netcupctl` operations: power state management, rescue mode, and image/snapshot listing.
v0.4.0 brings those operations to the Terraform provider: `netcup_server_power`, `netcup_server_rescue`, `netcup_server_images`, and `netcup_server_snapshots`.
v0.5.0 adds `netcupctl server reinstall` for native OS reinstallation, including `customScript` post-install bootstrap and image selection.
v0.6.0 adds the `netcup_server_reinstall` resource with native `custom_script`
bootstrap, image discovery, and async task waiting.

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

## netcupctl operations

Beyond listing servers, `netcupctl` can control a server's power state and rescue
system and list its installable images and snapshots. All commands take a numeric
server `<id>` (from `netcupctl server list`) and support `--json` for scripting.

> ℹ️ **Availability:** these operations require `netcupctl` **v0.3.0 or newer**.
> Earlier releases (v0.2.0 and before) do **not** include these subcommands, so
> running them there fails with an unknown-subcommand error — download the latest
> release or build from source (`make build`). The `server reinstall` command is
> available in **v0.5.0 or newer**.

> ⚠️ **Some of these commands cause downtime.** Read
> [Operational risk & downtime](#operational-risk--downtime) before running the
> power or rescue commands.

### Power state

```bash
# Read the current live power state (RUNNING / SHUTOFF / PAUSED / …).
netcupctl server power status <id>

# Power on (no downtime, no confirmation).
netcupctl server power on <id>

# Soft/ACPI shutdown — prompts for confirmation (causes downtime).
netcupctl server power off <id>

# Hard poweroff, skip the prompt, and wait for the async task to finish.
netcupctl server power off <id> --hard --force --wait

# Suspend (pause) the server.
netcupctl server power suspend <id>

# Reboot: soft power-cycle by default, --hard for a hard reset.
netcupctl server power reboot <id>
netcupctl server power reboot <id> --hard
```

Flags: `--wait` polls the async task to a terminal state and prints the result;
`--hard` selects the hard variant (`POWEROFF` for `off`, `RESET` for `reboot`);
`--force` (alias `--yes`) skips the downtime confirmation prompt; `--json` emits
machine-readable output.

### Rescue system

```bash
# Show whether rescue mode is active (and the password, when active).
netcupctl server rescue status <id>

# Enable rescue mode — REBOOTS into the rescue environment (prompts to confirm).
# --wait polls to completion, then attempts to read back the rescue password
# (best-effort; re-run 'rescue status' if it is not available yet).
netcupctl server rescue enable <id> --wait

# Disable rescue mode — REBOOTS back into the normal OS (prompts to confirm).
netcupctl server rescue disable <id> --wait
```

The rescue password is only available while rescue mode is active. With
`enable --wait` netcupctl reads it back once activation finishes and prints it
when the API already has it ready. The API may only expose the password a moment
later, so this readback is best-effort: if it is not yet available netcupctl
prints a hint to re-run `netcupctl server rescue status <id>`, which returns the
password once it is set.

### Images and snapshots (read-only)

```bash
# List the OS images installable on a server (ID, name, alias, base image).
netcupctl server images <id>

# List the server's snapshots (name, created, state, online, exported).
netcupctl server snapshots <id>

# Both support --json for the full objects.
netcupctl server images <id> --json
netcupctl server snapshots <id> --json
```

Snapshot listing is read-only; snapshot create/delete/restore is planned for a
later release. See the [Roadmap](docs/ROADMAP.md).

### Reinstall

> ⚠️ **DESTRUCTIVE — data loss and downtime.** A reinstall **wipes the server
> entirely**. All data on it is permanently lost and cannot be recovered. The
> server is down for the duration of the OS install. This is far more destructive
> than a power or rescue reboot. Read [Operational risk & downtime](#operational-risk--downtime)
> before running this command.

```bash
# Discover valid image IDs first (see 'server images').
netcupctl server images <id>

# Reinstall with the required --image flag (prompts for confirmation).
netcupctl server reinstall <id> --image <flavourId>

# Reinstall in full: set hostname, install SSH keys, wait for completion.
netcupctl server reinstall <id> --image 123 --hostname server.example.com \
  --ssh-key 42 --wait

# Reinstall with a post-install bootstrap script (inline).
netcupctl server reinstall <id> --image 123 \
  --custom-script '#!/bin/sh
apt-get update && apt-get install -y docker.io'

# Reinstall with a bootstrap script from a file (or '-' for stdin).
netcupctl server reinstall <id> --image 123 \
  --custom-script-file /path/to/bootstrap.sh

# Skip the confirmation prompt for scripted use.
netcupctl server reinstall <id> --image 123 --force

# Machine-readable output.
netcupctl server reinstall <id> --image 123 --json
```

Detailed guidance for the reinstall workflow and custom-script bootstrap is in
[docs/REINSTALL.md](docs/REINSTALL.md).

Flags: `--image <flavourId>` is **required** (valid ids from `netcupctl server
images <id>`); `--hostname`, `--ssh-key` (repeatable), `--ssh-password-auth`,
`--custom-script` (inline), `--custom-script-file` (file path or `-` for stdin),
`--wait` (polls the async task to completion), `--force`/`--yes` (skip the
data-loss confirmation prompt), `--json` for machine-readable output.

### Operational risk & downtime

Several `netcupctl` commands are **operationally destructive** — they interrupt
service on a running server. Treat them with the same care as running the
equivalent action from the SCP web panel.

| Command | Effect | Downtime |
|---------|--------|----------|
| `server power off` | Shuts the server down (soft/ACPI; `--hard` = forced `POWEROFF`) | **Yes** — server goes offline until powered on again |
| `server power suspend` | Pauses (suspends) the server | **Yes** — server is unresponsive until resumed |
| `server power reboot` | Power-cycles the server (soft; `--hard` = `RESET`) | **Yes** — brief outage during the reboot |
| `server power on` | Powers the server on | No |
| `server rescue enable` | **Reboots** the server into the rescue environment | **Yes** — the normal OS is not running while in rescue mode |
| `server rescue disable` | **Reboots** the server back into the normal OS | **Yes** — brief outage during the reboot |
| `server reinstall` | **Wipes the server** and reinstalls the OS | **Yes** — the server is down for the duration of the OS install; all data is lost |
| `server power status` / `rescue status` / `images` / `snapshots` | Read-only | No |

Safeguards built into the CLI:

- **Confirmation prompts.** `power off`, `power suspend`, `power reboot`,
  `rescue enable`, `rescue disable`, and `server reinstall` prompt before acting.
  The warning and prompt are written to **stderr**, so `--json` output on stdout
  stays clean.
- **`--force` / `--yes`** skips the confirmation prompt for non-interactive or
  scripted use — only pass it when you have already accepted the downtime.
- **`--wait`** polls the underlying async task (`202 TaskInfo`) to a terminal
  state so scripts can observe success/failure instead of firing and forgetting.
- **`--hard`** opts into the forced variants (`POWEROFF` / `RESET`), which do not
  give the guest OS a chance to shut down cleanly — prefer the soft default
  unless the server is unresponsive.

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

## Terraform provider (v0.6.0 — available)

The Terraform provider is built on the same Go SDK as `netcupctl` and ships in
v0.2.0 (data sources and rDNS), with provider power/rescue/images/snapshots
support added in v0.4.0. See [examples/](examples/) for ready-to-use configurations:

### Resources

- [netcup_rdns resource](examples/rdns.tf) — manage reverse DNS (v0.2.0)
- [netcup_server_power resource](examples/server_power.tf) — manage power state (v0.4.0)
- [netcup_server_rescue resource](examples/server_rescue.tf) — manage rescue system (v0.4.0)
- [netcup_server_reinstall resource](examples/server_reinstall.tf) — reinstall a server (v0.6.0; destructive)

### Data Sources

- [netcup_servers data source](examples/servers.tf) — list servers (v0.2.0)
- [netcup_server data source](examples/server.tf) — get server details (v0.2.0)
- [netcup_server_images data source](examples/server_images.tf) — list installable images (v0.4.0)
- [netcup_server_snapshots data source](examples/server_snapshots.tf) — list snapshots (v0.4.0)

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

Build the provider binary, export the tokens minted by `netcupctl auth login`,
then run `terraform plan` directly — `terraform init` will fail with a
"provider not found" error because `wiphoo/netcup` is not yet published on the
Terraform Registry, but the dev override makes init unnecessary for plan/apply:

```bash
cd /path/to/your/clone
go build -o bin/ ./cmd/terraform-provider-netcup
go build -o bin/ ./cmd/netcupctl
eval "$(./bin/netcupctl auth export)"
cd examples
terraform plan
```

A bare `terraform plan` in `examples/` only exercises the `netcup_servers` data
source, which lists the servers on *your* authenticated account — a safe
read-only smoke test. The single-server lookup (`server.tf`) and the rDNS
resource (`rdns.tf`) are opt-in placeholders, skipped via `count` unless you
pass your own values, so the plan never reads a hard-coded server ID or proposes
a placeholder PTR record:

```bash
# Look up one of your servers by ID.
terraform plan -var 'server_id=123456'

# Manage a PTR record for an IP you own.
terraform plan -var 'rdns_ip_address=203.0.113.10' -var 'rdns_hostname=host.example.com'

# Manage power state (v0.4.0; see risk table below).
terraform plan -var 'server_id=123456' -var 'power_state=OFF'

# Manage rescue mode (v0.4.0; reboots the server).
terraform plan -var 'server_id=123456' -var 'rescue_enable=true'
```

### Provider operational risk & downtime

The Terraform provider exposes the same power and rescue operations as
`netcupctl`, with the same downtime profile. Additionally, the Terraform
lifecycle introduces specific failure modes:

| Resource | Operation | Downtime | Notes |
|----------|-----------|----------|-------|
| `netcup_server_power` | Creating / updating to `OFF` or `SUSPENDED` | **Yes** | Server goes offline until changed back to `ON` |
| `netcup_server_power` | Creating / updating to `ON` (plain, no `state_option`) | No | — |
| `netcup_server_power` | **Destroy** | **No** | **No-op.** The resource is removed from state but the server is NOT powered off. This is a deliberate safety measure — see the resource description. |
| `netcup_server_power` | `state_option = "POWEROFF"` (hard off) | **Yes** | Forced poweroff, no guest OS shutdown |
| `netcup_server_power` | `state_option = "RESET"` / `"POWERCYCLE"` (reboot) | **Yes** | Brief outage during reboot |
| `netcup_server_rescue` | Create (enable) | **Yes** | **Reboots** into rescue environment |
| `netcup_server_rescue` | Destroy (disable) | **Yes** | **Reboots** back to normal OS |
| `netcup_server_reinstall` | Create or replace | **Yes** | **Wipes the server** and reinstalls the OS; all data is lost |
| `netcup_server_reinstall` | **Destroy** | **No** | **No-op.** Removes only Terraform state; it does not reinstall or wipe the server. |
| `netcup_server_images` / `netcup_server_snapshots` | Read | No | Read-only data sources |

Key semantics:

- **`wait` attribute** — defaults to `true`. When true, the resource blocks
  `terraform apply` until the underlying async SCP task reaches a terminal state
  (FINISHED, ERROR, or canceled). Set `wait = false` to fire-and-forget the
  command and return immediately. The resource retains the task UUID, but
  `netcup_server_reinstall` is an exception: its `Read` operation only confirms
  that the server exists and does not poll or reconcile the reinstall task.
  Monitor the task separately when using `wait = false`.
- **Destroy is not power-off.** Deleting `netcup_server_power` from your
  configuration or running `terraform destroy` only removes the resource from
  state — it does not change the server's power state. To explicitly power a
  server off, set `state = "OFF"` instead.
- **Rescue enable/disable both reboot.** Creating `netcup_server_rescue`
  enables rescue mode (reboot), and destroying it disables rescue mode (another
  reboot). Consider the downtime budget before applying rescue changes.
- **Reinstall is destructive.** Creating or replacing `netcup_server_reinstall`
  permanently wipes the server and keeps it unavailable during OS installation.
  Any changed install input, including `custom_script`, runs another reinstall.
  The resource's destroy operation is deliberately a no-op.
- **Reinstall secrets are Sensitive.** Treat `custom_script` as a secret because
  bootstrap scripts may contain credentials, and never put
  `additional_user_password` in logs or source control.

See [Server Reinstall](docs/REINSTALL.md#terraform-provider) for the complete
provider workflow and attribute reference.

## Design principles

- Keep the public Terraform interface simple and stable.
- Hide SCP/CCP implementation details behind stable resource abstractions.
- Build a reusable SDK layer before provider resources become complex.
- Avoid destructive lifecycle features in early releases.
- Do not use Terraform as a cloud-init, SSH, Ansible, or Kubernetes bootstrap tool.

## Releasing

`netcupctl` CLI and `terraform-provider-netcup` binaries are built and
published automatically by [GoReleaser](https://goreleaser.com) via the
`Release` GitHub Actions workflow (`.github/workflows/release.yml`).
Configuration lives in `.goreleaser.yaml`.

Cut a release by pushing a SemVer tag:

```bash
git tag v0.2.0
git push origin v0.2.0
```

The workflow builds both `netcupctl` and `terraform-provider-netcup` for linux,
macOS, and Windows (amd64 and arm64), embeds the tag as the version (visible
in `netcupctl version` and the provider's version string), produces
`tar.gz` / `.zip` archives and a single SHA-256 `checksums.txt` covering both
artifacts, and creates a GitHub Release with all assets attached.

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
