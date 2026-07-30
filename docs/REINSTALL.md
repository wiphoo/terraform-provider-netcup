# Server Reinstall

⚠️ **DESTRUCTIVE — data loss and downtime.** A reinstall wipes the server
entirely. All data on it is permanently lost and cannot be recovered. The server
is down for the duration of the OS install. This is far more destructive than a
power/rescue reboot. Always confirm you have the correct server ID before
proceeding.

## Workflow

The typical reinstall flow has three steps:

1. **Discover available images** — list the OS image flavours installable on
   the target server.
2. **Reinstall** — pick an image and trigger the OS install.
3. **(Optional) Wait for completion** — poll the async task to a terminal state.

### 1. Discover images

```bash
netcupctl server images <id> [--json]
```

The output shows the image flavour ID (`ID`), machine name (`NAME`), alias
(`ALIAS`), and the underlying base image (`IMAGE`). Use the `ID` column value as
the `--image` argument for the reinstall command.

See [`server images`](../README.md#images-and-snapshots-read-only) for details.

### 2. Reinstall

The server uses the SCP API's native OS install
(`POST /v1/servers/{serverId}/image`), not Terraform-driven SSH or Ansible.

```bash
netcupctl server reinstall <id> --image <flavourId> [flags]
```

**Required:**
- `--image <flavourId>` — the image flavour ID from step 1

**Optional:**
- `--hostname <string>` — FQDN hostname for the reinstalled server
- `--ssh-key <int>` — SSH key id to authorize (repeatable for multiple keys)
- `--ssh-password-auth` — enable SSH password authentication on the new install
- `--custom-script <string>` — post-install bootstrap script (inline string)
- `--custom-script-file <path>` — post-install bootstrap script from a file
  (`-` for stdin)
- `--wait` — poll the async task to a terminal state and print the result
- `--force` / `--yes` — skip the data-loss confirmation prompt (for scripted
  or non-interactive use)
- `--json` — machine-readable JSON output

### 3. Wait for the task

Without `--wait`, the command prints the accepted task UUID and returns
immediately. The OS install continues in the background on the SCP API side.

With `--wait`, `netcupctl` polls the task until it reaches a terminal state
(`FINISHED`, `ERROR`, or canceled) and prints the final state:

```bash
netcupctl server reinstall <id> --image 123 --wait

Server: 42
Action: reinstall
Image:  123
Task:   <uuid>
Task State: FINISHED
```

## Examples

### Basic reinstall

```bash
# List available images.
netcupctl server images 42

# Reinstall with the chosen image (prompts for confirmation).
netcupctl server reinstall 42 --image 123
```

### With hostname and SSH keys

```bash
netcupctl server reinstall 42 --image 123 \
  --hostname web01.example.com \
  --ssh-key 1 --ssh-key 2
```

### Post-install bootstrap with customScript

The `customScript` field is netcup's native post-install bootstrap mechanism. It
runs after the OS is installed, before the server becomes available. Use it for
first-boot provisioning that must happen before the server serves traffic.

**Inline script:**

```bash
netcupctl server reinstall 42 --image 123 \
  --custom-script '#!/bin/sh
set -e
apt-get update
apt-get install -y docker.io
systemctl enable docker'
```

**From a file:**

```bash
netcupctl server reinstall 42 --image 123 \
  --custom-script-file ./bootstrap.sh
```

**From stdin** (requires `--force` because stdin is also used for the
confirmation prompt):

```bash
cat bootstrap.sh | netcupctl server reinstall 42 --image 123 \
  --custom-script-file - --force
```

### Non-interactive (scripted) use

```bash
netcupctl server reinstall 42 --image 123 --force --wait --json
```

### Machine-readable output

```bash
netcupctl server reinstall 42 --image 123 --json
```

```json
{
  "serverId": 42,
  "imageFlavourId": 123,
  "action": "reinstall",
  "task": {
    "uuid": "...",
    "state": "FINISHED",
    ...
  }
}
```

## SSH key management

SSH keys are referenced by their numeric id from the SCP SSH-keys endpoint, not
passed as inline public keys. Use `--ssh-key` (repeatable) to authorize one or
more keys on the new install:

```bash
# Authorize two keys.
netcupctl server reinstall 42 --image 123 --ssh-key 1 --ssh-key 2
```

To list your available SSH key ids, use the SCP API directly (the CLI does not
yet surface this):

```bash
curl -H "Authorization: Bearer $NETCUP_ACCESS_TOKEN" \
  "$NETCUP_API_ENDPOINT/v1/ssh-keys"
```

## Unsupported fields

The following `ServerImageSetup` API fields are **not yet exposed** by the CLI:

- `diskName` — target disk selection
- `locale` — system locale
- `timezone` — system timezone
- `additionalUserUsername` / `additionalUserPassword` — additional OS user
- `rootPartitionFullDiskSize` — allocate full disk to root
- `emailToExecutingUser` — email notification on completion

These are planned for a future release. Track progress in the
[Roadmap](ROADMAP.md).

## See also

- [`server reinstall` usage in README](../README.md#reinstall)
- [SCP API notes — OS install/reinstall](SCP-API-NOTES.md#os-install--reinstall)
- [Roadmap](ROADMAP.md)
