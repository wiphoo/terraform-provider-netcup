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
- The resource ID is the canonical IP string (e.g. `2a03:4000:6:b1d::1`, not
  `2A03:4000:0006:0B1D:0000:0000:0000:0001`).
- IPv4 is already canonical after parse.

This is the chosen default; raise it on the PR if you'd prefer store-as-written.

## Other endpoints (for later milestones)

The spec also exposes (v0.3.0+ territory): `servers/{id}/snapshots*`,
`servers/{id}/rescuesystem`, `servers/{id}/image` + `imageflavours` (OS install),
`servers/{id}/iso`/`isoimages`, `servers/{id}/interfaces*/firewall`,
`servers/{id}/metrics/*`, `tasks/{uuid}` (async operations + `:cancel`),
`users/{userId}/failoverips/*`, `ssh-keys`, `vlans`. Not in scope for v0.1.0/v0.2.0.
