# SCP REST API Notes

Pinned shapes and decisions for the netcup Server Control Panel (SCP) REST API,
derived from the live OpenAPI document. Downstream issues (servers, reverse DNS,
the Terraform resources) should treat this as the source of truth and update it
when the API changes.

- **Spec:** `SCP (Server Control Panel) REST API`, version `2026.0624.115833`
- **Source:** `GET https://www.servercontrolpanel.de/scp-core/api/v1/openapi` (public; JSON)
- Regenerate/recheck with: `curl -H 'accept: application/json' "$NETCUP_API_ENDPOINT/v1/openapi"`

## Endpoint structure

The service root is `/scp-core/api`. There are two tiers:

| Path | Auth | Notes |
|------|------|-------|
| `GET /scp-core/api/ping` | none | Health check → `200 OK` (text/plain). IP-allowlist gate still applies. |
| `/scp-core/api/v1/...` | Bearer | All versioned resources require an access token. |

Verified live: `/scp-core/api/ping` → `OK`; `/scp-core/api/v1/ping` → `401 {"message":"Authorization header missing."}`.

## Authentication

- Scheme: `openIdConnect` (Keycloak), well-known at `/realms/scp/.well-known/openid-configuration`.
- The CCP customer number is the SCP username; the SCP `userId` is a separate internal id (do not assume they are equal).
- Access token is sent as `Authorization: Bearer <token>`. Device flow + refresh are covered in [ARCHITECTURE](ARCHITECTURE.md).

## Two gates

1. **IP allowlist** — configured in the SCP REST API settings; blocks calls from non-allowed IPs before auth even matters.
2. **Bearer token** — required for everything under `/v1`.

A `401`/`403` should surface a hint covering both (the SDK does this).

## Servers

### List — `GET /v1/servers` → `[]ServerListMinimal`

Minimal projection — **no IP addresses, no power state, no product fields**:

| Field | Type | Notes |
|-------|------|-------|
| `id` | int32 | |
| `name` | string | |
| `hostname` | string? | nullable |
| `nickname` | string? | nullable |
| `disabled` | bool | |
| `template` | object? | `ServerTemplateMinimal { id, name }` (`name` required) |

> ⚠️ The MVP draft assumed `netcup_servers` exposes `status`, `product_name`,
> `ipv4_addresses`, `ipv6_addresses`. The **list** endpoint does not return those.
> The `netcup_servers` data source should expose only the minimal fields above
> (plus `template.name` as the product), and require a follow-up `GET` per server
> if richer fields are needed.

### Detail — `GET /v1/servers/{serverId}` → `Server`

Relevant fields for the provider/CLI:

| Provider/CLI field | Source field | Notes |
|--------------------|--------------|-------|
| `id` | `id` | int32 |
| `name` | `name` | |
| `hostname` | `hostname` | nullable |
| `product_name` | `template.name` | `template` is `ServerTemplateMinimal` |
| `status` | `serverLiveInfo.state` | `serverLiveInfo` (`ServerInfo`) is nullable; `state` is the power state |
| `ipv4_addresses` | `ipv4Addresses[].ip` | items: `IPv4AddressMinimal { id, ip, netmask, gateway?, broadcast? }` |
| `ipv6_addresses` | `ipv6Addresses[].networkPrefix` (+ `networkPrefixLength`) | items: `IPv6AddressMinimal { id, networkPrefix, networkPrefixLength, gateway? }` |

Other `Server` fields available: `architecture`, `disabled`, `nickname`, `site`,
`maxCpuCount`, `snapshotAllowed`, `snapshotCount`, `rescueSystemActive`,
`gpuDriverAvailable`, `disksAvailableSpaceInMiB`.

### Power state — `PATCH /v1/servers/{serverId}`

Change a server's power state. The SDK exposes this as `SetPowerState` and the
CLI as `netcupctl server power on|off|suspend|reboot`.

- **Content type:** `application/merge-patch+json` (⚠️ **not** `application/json` —
  the endpoint rejects the wrong media type). Body is `ServerStatePatch`:
  `{ "state": "ON" | "OFF" | "SUSPENDED" }` (the write-side enum `ServerState1`).
- **Responses:** `202 TaskInfo` (async — poll with `GET /v1/tasks/{uuid}`), `200`
  (applied synchronously, no body), `503 ResponseError` (node in maintenance).
- **Read-back:** there is no dedicated power-read endpoint; use
  `GET /v1/servers/{id}` → `serverLiveInfo.state` (`ServerState`: `RUNNING`,
  `SHUTOFF`, `PAUSED`, `PMSUSPENDED`, …). Note this **live/read** enum differs
  from the **desired/write** enum `ServerState1` above.

#### `stateOption` query parameter (open question — RESOLVED by the spec)

`stateOption` is typed as a free-form string, but the live OpenAPI parameter
description enumerates the accepted values per target state:

| Target `state` | Valid `stateOption` values | Meaning |
|----------------|----------------------------|---------|
| `ON` | `POWERCYCLE`, `RESET` | `POWERCYCLE` = graceful power cycle (used for **reboot**); `RESET` = hard reset |
| `OFF` | `POWEROFF` | hard poweroff (the default `OFF` with no option is a soft/ACPI shutdown) |
| `SUSPENDED` | *(none)* | — |

So a **reboot** is native: `PATCH {state:ON}?stateOption=POWERCYCLE` (no
OFF-then-ON orchestration needed); `RESET` is the hard variant. The CLI maps its
`--hard` flag to `POWEROFF` for `off` and `RESET` for `reboot`; omitting the flag
uses the soft form (no option for `off`, `POWERCYCLE` for `reboot`).

> Confirmed against live OpenAPI `2026.0703.095128` (the issue referenced
> `2026.0624.115833`; the `stateOption` description is unchanged). Recheck with
> `curl -H 'accept: application/json' "$NETCUP_API_ENDPOINT/v1/openapi"` and grep
> the `PATCH /v1/servers/{serverId}` `stateOption` parameter.

## Reverse DNS (rDNS)

IPv4 and IPv6 are **separate endpoints**. The SDK/CLI/provider must detect the IP
family and route accordingly.

| Operation | IPv4 | IPv6 |
|-----------|------|------|
| Set (create/update) | `POST /v1/rdns/ipv4` | `POST /v1/rdns/ipv6` |
| Read | `GET /v1/rdns/ipv4/{ip}` | `GET /v1/rdns/ipv6/{ip}` |
| Delete | `DELETE /v1/rdns/ipv4/{ip}` | `DELETE /v1/rdns/ipv6/{ip}` |

- **Set body** (`SetRdnsIpv4` / `SetRdnsIpv6`): `{ "ip": "<addr>", "rdns": "<fqdn>" }` — both required.
  - `rdns` must be an FQDN: `maxLength 255` (effective ≤253), label/TLD pattern enforced.
  - There is **no `PUT`** — create and update both use `POST` to the collection (upsert).
- **Read** (`Rdns`): `{ "rdns": string|null }` — `null` means no custom PTR is set.
- **Delete** → `204 No Content`.
- Validation errors → `422 ValidationError`.

### Decision 1 — rDNS delete semantics (RESOLVED by the spec)

`DELETE /v1/rdns/{ipv4|ipv6}/{ip}` returns `204` and removes the **custom** rDNS
entry; a subsequent `GET` returns `{"rdns": null}` (the address reverts to its
provider-default PTR). So:

- Terraform `netcup_rdns` **Delete** → `DELETE /v1/rdns/.../{ip}`.
- **Read** maps `rdns: null` to "resource gone" (so a manually-cleared PTR shows
  as drift / triggers recreation rather than an error).
- **Create and Update** both → `POST /v1/rdns/{ipv4|ipv6}` (upsert).

### Decision 2 — IPv6 address normalization (CHOSEN; override if desired)

The API accepts loosely-formatted IPv6 in the path (broad regex), so the same
address can be written many ways. To avoid spurious Terraform diffs and duplicate
resource IDs:

- **Canonicalize every IP to its RFC 5952 form** (compressed, lowercase) via Go
  `netip.ParseAddr(s).String()` before using it as the request path **and** as the
  resource ID.
- The resource ID is the canonical IP string (e.g. `2001:db8:6:b1d::1`, not
  `2001:0DB8:0006:0B1D:0000:0000:0000:0001`).
- IPv4 is already canonical after parse.

This is the chosen default; raise it on the PR if you'd prefer store-as-written.

## Rescue system

Endpoint: `/v1/servers/{serverId}/rescuesystem`. **Operationally disruptive** —
both activation and deactivation reboot the server (into the rescue environment,
and back into the normal OS, respectively).

| Operation | Method | Request body | Success | Notes |
|-----------|--------|--------------|---------|-------|
| Status | `GET` | none | `200 RescueSystemStatus` | `{ active bool, password string? }` |
| Enable | `POST` | **none** | **`202 TaskInfo`** | async; reboots into rescue |
| Disable | `DELETE` | **none** | **`202 TaskInfo`** | async; reboots back to normal OS |

- **`RescueSystemStatus`**: `password` is nullable and populated only while the
  rescue system is active. netcup surfaces it on a follow-up `GET` shortly after
  activation finishes — so `enable --wait` polls the task to a terminal state and
  then re-reads the status to print the password.
- **POST takes no request body** (verified against the live OpenAPI
  `2026.0703.095128`; the ticket asked to confirm this). There is **no** option
  for a desired password or SSH key on this operation — the password is
  generated by netcup and read back via `GET`. If a body is ever added, thread it
  through an options struct.
- **DELETE is asynchronous and returns `202 TaskInfo`**, not a bodyless `204`.
  The SDK therefore models `DisableRescueSystem` as `(*TaskInfo, error)`
  (symmetric with `EnableRescueSystem` and the power-state calls), and
  `server rescue disable` supports `--wait`. This deviates from the issue #62
  draft, which assumed `DisableRescueSystem(ctx, id) error`.
- **Errors**: enabling when already active → `400 ResponseError` ("Rescue system
  currently active."); disabling when already deactivated → `400 ResponseError`
  ("Rescue system currently deactivated."); unknown server → `404`. All non-2xx
  surface as `*APIError`.

## Image flavours

List the installable OS images (image flavours) for a server — the input
identifiers for the OS-reinstall flows (v0.5.0/v0.6.0). Pure read, **no downtime
risk**. The SDK exposes this as `ListImageFlavours` and the CLI as
`netcupctl server images <id>`.

### List — `GET /v1/servers/{serverId}/imageflavours` → `[]ImageFlavour`

| Field | Type | Notes |
|-------|------|-------|
| `id` | int32 | flavour id |
| `name` | string | machine name |
| `alias` | string | short human-facing alias |
| `text` | string | human-facing description |
| `image` | object? | `ImageMinimal { id int32, name string }` — the underlying base image; nullable |

- An **empty list is valid** (no error); non-2xx → `*APIError`.
- Companion `GET /v1/servers/{serverId}/isoimages` exists but is **out of scope**
  for v0.3.0. User images (`GET /v1/users/{userId}/images`) are deferred to the
  provisioning milestones.

## Snapshots

List a server's snapshots. Pure read, **no downtime risk**. Snapshot
create/delete/restore/export are **v0.7.0** (Snapshot Management) and stay out of
scope here. The SDK exposes this as `ListSnapshots` and the CLI as
`netcupctl server snapshots <id>`.

### List — `GET /v1/servers/{serverId}/snapshots` → `[]SnapshotMinimal`

| Field | Type | Notes |
|-------|------|-------|
| `uuid` | string | snapshot id |
| `name` | string | |
| `description` | string? | nullable |
| `disks` | []string | may be empty |
| `creationTime` | time | RFC 3339; parsed as `time.Time` and rendered readably in the table |
| `state` | string | `ServerState` (`RUNNING`, `SHUTOFF`, …) |
| `online` | bool | taken while the server was running |
| `exported` | bool | |
| `exportedSizeInKiB` | int64? | nullable; set once exported |

- An **empty list is valid** (no error); non-2xx → `*APIError`.

## Other endpoints (for later milestones)

The spec also exposes (later-milestone territory): `servers/{id}/image` (OS
install), `servers/{id}/iso`/`isoimages`, `servers/{id}/interfaces*/firewall`,
`servers/{id}/metrics/*`, `users/{userId}/failoverips/*`, `ssh-keys`, `vlans`.
Not in scope through v0.3.0.

> Async task polling (`GET /v1/tasks/{uuid}`, `:cancel`) shipped in v0.3.0 as the
> foundation for `--wait` on the power/rescue commands; see `TaskInfo` /
> `GetTask` / `WaitForTask` in `pkg/netcup/tasks.go`.
