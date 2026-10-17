# DNS (CCP API)

Netcup DNS is **not** part of the SCP REST API (the live OpenAPI has only
`/v1/rdns/*`). Zones and records are managed through the CCP JSON API, so
`netcupctl dns` and the SDK's `DNSClient` use a separate login from the
`netcupctl auth login` device flow.

## Credentials

Create an API key and API password in the CCP (look for the API settings under your master data), then export:

```bash
export NETCUP_CUSTOMER_NUMBER="123456"
export NETCUP_DNS_API_KEY="..."
export NETCUP_DNS_API_PASSWORD="..."
```

They are read from the environment only and are never written to disk. Override
the endpoint with `NETCUP_DNS_ENDPOINT` (default
`https://ccp.netcup.net/run/webservice/servers/endpoint.php?JSON`).

The credentials travel in the request body, so the endpoint must be `https`
(plain `http` is accepted only for `localhost` / loopback addresses, e.g. a test
server). HTTP redirects are not followed.

## CLI

```bash
netcupctl dns zone get example.com
netcupctl dns zone update example.com --ttl 3600
netcupctl dns records list example.com
netcupctl dns records add example.com www A 203.0.113.5
netcupctl dns records add example.com @ MX mail.example.com --priority 10
netcupctl dns records update example.com 12 --destination 203.0.113.6
netcupctl dns records delete example.com 12        # prompts; --yes (or --force) to skip
```

All commands take `--json`. Record ids come from `records list`. Use `@` for the
zone apex and `*` for a wildcard. Record types: `A AAAA MX CNAME CAA SRV TXT
TLSA NS DS`.

### Flags

- `zone update`: `--ttl`, `--refresh`, `--retry`, `--expire` (seconds). At least
  one is required and each must be positive. Unset fields keep their value.
- `records add`: `[--priority N]` (must be `>= 0`; used by MX / SRV).
- `records update`: `--hostname`, `--type`, `--destination`, `--priority`. At
  least one is required; `--priority` must be `>= 0`. Unset fields keep their value.
- `records delete`: `--yes` / `--force` skips the confirmation prompt.

### Destinations that start with `-`

Put `--` before the destination (or use `--destination=-x` on update):

```bash
netcupctl dns records add example.com @ TXT -- -x
```

### Duplicates

`records add` refuses to create a record that already exists. Matching is
normalization-aware: hostname and type are compared case-insensitively, a
trailing dot is ignored for CNAME / MX / NS / SRV destinations, and TXT
destinations are compared exactly.

> **Risk:** records change live DNS for the domain. `records delete` is
> permanent and prompts for confirmation. A zone is addressed by domain name
> (CCP has no "list zones" call).

## Verifying cert-manager and ExternalDNS records

A DNS-01 solver (cert-manager) or an ExternalDNS webhook for netcup works against
the same CCP credentials and writes ordinary records into the zone. `netcupctl`
is a convenient way to check what they did:

```bash
# cert-manager: the challenge TXT record (typically named _acme-challenge...) appears while an Order is pending
netcupctl dns records list example.com --json | jq '.[] | select(.hostname | startswith("_acme-challenge"))'

# ExternalDNS: typically, when its TXT registry is enabled, records it owns carry a TXT ownership marker alongside the A/CNAME
netcupctl dns records list example.com --json | jq '.[] | select(.type == "TXT")'

# clean up a stuck challenge record by hand
netcupctl dns records delete example.com <id>
```

Configure the controller itself with the CCP credentials above (as a Kubernetes
Secret) following the documentation of the netcup webhook you choose; this
repository does not ship one.

## Known limits (v0.8.0)

- No automatic re-login if the CCP session expires mid-command (a limited session lifetime, about 15 minutes per netcup's docs - unverified here).
- Terraform resources for DNS are planned for v0.8.1.
