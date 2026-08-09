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

With `--wait`, `netcupctl` polls the task until it reaches a terminal state.
Only a `FINISHED` task is printed; `ERROR`, `CANCELED`, and `ROLLBACK` states surface as command errors (check `echo $?` and stderr):

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
netcupctl server reinstall 42 --image 123 --wait --json
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

## Terraform provider

The `netcup_server_reinstall` resource performs the same native API operation
from Terraform. It is a managed representation of a destructive action, not a
durable server-install object.

> **WARNING: applying or replacing this resource wipes the server.** All data
> is permanently lost and the server is unavailable while the OS is installed.
> Changing any install input runs another reinstall. Destroying the resource is
> a no-op: it removes only Terraform state and never reinstalls or wipes the
> server.

### Workflow

1. Use `netcup_server_images` to discover image flavours for the target server.
2. Set the selected image's `id` as `image_flavour_id` on
   `netcup_server_reinstall`.
3. Apply the configuration and let the resource wait for the reinstall task, or
   set `wait = false` to return after the API accepts the task.

The complete opt-in configuration is in
[`examples/server_reinstall.tf`](../examples/server_reinstall.tf). The example
selects an image by its name and passes a native `custom_script` bootstrap.

### Example

```hcl
data "netcup_server_images" "reinstall" {
  server_id = var.server_id
}

locals {
  image = one([
    for image in data.netcup_server_images.reinstall.images : image
    if image.name == var.reinstall_image_name
  ])
}

resource "netcup_server_reinstall" "example" {
  server_id        = var.server_id
  image_flavour_id = local.image.id

  custom_script = <<-SCRIPT
    #!/bin/sh
    set -eu
    echo "bootstrap completed" >/var/log/netcup-bootstrap.log
  SCRIPT
}
```

Do not use a placeholder server ID or image name for a real apply. The
`image_flavour_id` must come from the image-flavour list for the target server.
The request body and endpoint shape are pinned in
[SCP-API-NOTES.md](SCP-API-NOTES.md#os-install--reinstall).

### Attributes

| Attribute | Required | Description |
| --- | --- | --- |
| `server_id` | Yes | Numeric server ID as a string. Changes force a replacement. |
| `image_flavour_id` | Yes | Image flavour ID from `netcup_server_images`. Changes force a replacement. |
| `disk_name` | No | Target disk name. Changes force a replacement. |
| `root_partition_full_disk_size` | No | Allocate the full disk to the root partition. Changes force a replacement. |
| `hostname` | No | Hostname for the installed system. Changes force a replacement. |
| `locale` | No | Locale for the installed system. Changes force a replacement. |
| `timezone` | No | Timezone for the installed system. Changes force a replacement. |
| `additional_user_username` | No | Additional non-root username. Changes force a replacement. |
| `additional_user_password` | No | Password for the additional user. Sensitive; changes force a replacement. |
| `ssh_key_ids` | No | List of SCP SSH key IDs to authorize. Changes force a replacement. |
| `ssh_password_authentication` | No | Enable SSH password authentication. Changes force a replacement. |
| `custom_script` | No | Native post-install bootstrap script. Sensitive because it may contain secrets; changes force a replacement. |
| `email_to_executing_user` | No | Email the executing user when installation completes. Changes force a replacement. |
| `triggers` | No | String map for deliberate reruns, such as a script hash. Changes force a replacement. |
| `wait` | No | Defaults to `true`; wait for the async task to reach a terminal state. Changing only this attribute does not reinstall. |
| `id` | Computed | Resource ID, equal to `server_id`. |
| `task_id` | Computed | UUID of the most recent accepted reinstall task, when the API returned one. |

All install inputs and `triggers` are `RequiresReplace`, so Terraform shows a
replacement (`-/+`) before it performs another wipe. With `wait = false`, the
resource records the accepted task UUID and returns without polling; it does
not cancel the remote reinstall. A confirmed terminal task failure is returned
as an error, while an interrupted or indeterminate wait is recorded with a
warning to avoid issuing the destructive operation twice.

## SSH key management

SSH keys are referenced by their numeric id from the SCP SSH-keys endpoint, not
passed as inline public keys. Use `--ssh-key` (repeatable) to authorize one or
more keys on the new install:

```bash
# Authorize two keys.
netcupctl server reinstall 42 --image 123 --ssh-key 1 --ssh-key 2
```

To list your available SSH key ids, use the SCP API directly (the CLI does not
yet surface this). First export the tokens from the `auth login` store and set
the API endpoint:

```bash
eval "$(netcupctl auth export)"
export NETCUP_API_ENDPOINT="https://www.servercontrolpanel.de/scp-core/api"
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
