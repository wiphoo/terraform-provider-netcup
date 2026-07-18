# Contributing to terraform-provider-netcup

Thank you for contributing! This document covers everything you need to get
started — from setting up a dev environment to opening a pull request.

## Table of contents

- [Code of conduct](#code-of-conduct)
- [What can I contribute?](#what-can-i-contribute)
- [Development setup](#development-setup)
- [Running checks](#running-checks)
- [Acceptance tests](#acceptance-tests)
- [Branch and commit conventions](#branch-and-commit-conventions)
- [Pull request process](#pull-request-process)
- [Branch protection / merge rules](#branch-protection--merge-rules)
- [Architecture overview](#architecture-overview)
- [Release process](#release-process)
- [Labels](#labels)

---

## Code of conduct

This project follows the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md).
By participating you agree to uphold it. Please report unacceptable behavior to
[wiphoo.m@toffoli.co.th](mailto:wiphoo.m@toffoli.co.th).

---

## What can I contribute?

- Bug reports and reproduction cases — open a [Bug report](https://github.com/wiphoo/terraform-provider-netcup/issues/new?template=bug_report.yml) issue.
- Feature proposals — open a [Feature request](https://github.com/wiphoo/terraform-provider-netcup/issues/new?template=feature_request.yml) issue.
- SDK, CLI, or provider code — see the workflow below.
- Documentation improvements — PRs welcome.
- Test coverage — especially acceptance tests against the live SCP API.

If you are planning a large change, open an issue first so we can align on
direction before you invest time writing code.

---

## Development setup

**Prerequisites:** Go 1.25+ · Git

```bash
git clone https://github.com/wiphoo/terraform-provider-netcup.git
cd terraform-provider-netcup
go mod download
```

Run the test suite:

```bash
go test ./...
```

Build the `netcupctl` CLI:

```bash
go build ./cmd/netcupctl
```

For Terraform provider development you will also need the
[Terraform CLI](https://developer.hashicorp.com/terraform/downloads) (v1.0+).

---

## Running checks

| Check | Command |
| --- | --- |
| Unit tests | `go test ./...` |
| Build | `go build ./...` |
| Vet | `go vet ./...` |
| Format | `gofmt -l .` |
| Lint | `golangci-lint run` (requires [golangci-lint](https://golangci-lint.run/usage/install/)) |
| Acceptance tests (release gate) | `make acc` (requires `TF_ACC=1`) |
| Regenerate go-vcr cassettes | `make acc-record` (requires `VCR_RECORD=1`, see below) |

All checks must pass before a PR can be merged.

To validate the GoReleaser config or test a local snapshot build (signing and
publishing are skipped):

```bash
goreleaser check
goreleaser release --snapshot --clean --skip=sign,publish
```

---

## Acceptance tests

The v0.2.0 provider uses a **two-tier acceptance test strategy** to work around
the SCP API's IP-allowlist gate (which makes hosted-runner testing impractical).

### Tier 1 — go-vcr replay (PR CI)

Files: `tests/vcr/*_vcr_test.go` and `internal/provider/*_vcr_test.go`

Plain Go tests that replay pre-recorded SCP responses using
[go-vcr](https://github.com/dnaeon/go-vcr). They run in every `go test ./...`
invocation with **no credentials and no network access**. Cassettes are stored
in `tests/vcr/testdata/cassettes/`; PII (headers, body fields, and IPs in
URLs) is scrubbed before committing — see [Redaction](#redaction--what-gets-scrubbed)
below.

### Tier 2 — live acceptance tests (release gate)

Files: `internal/provider/*_acc_test.go`

Standard Terraform provider acceptance tests guarded by `TF_ACC=1`:
`os.Getenv("TF_ACC") == "" { t.Skip() }`. They hit the live SCP API and
require:
- `TF_ACC=1`
- `NETCUP_ACCESS_TOKEN` (fresh token from `netcupctl auth login`)
- The calling IP must be allowlisted in the SCP REST API settings
- `NETCUP_TEST_SERVER_ID` (for `TestAccServerDataSource`)
- `NETCUP_TEST_IP` (for `TestAccRDNSResource`)

Run with:

```bash
make acc
```

### Re-recording cassettes

When the SCP API shapes change, or before a release, regenerate cassettes from
live SCP:

```bash
export NETCUP_ACCESS_TOKEN=$(netcupctl auth token --raw)
export NETCUP_TEST_SERVER_ID=<your-server-id>
export NETCUP_TEST_IP=<an-ip-on-that-server>
make acc-record
```

The `make acc-record` target runs `VCR_RECORD=1 go test ./...`, which
re-records cassettes in both `tests/vcr/testdata/cassettes/` and
`internal/provider/testdata/cassettes/` simultaneously. **SetRDNS and
DeleteRDNS mutate the test IP's PTR during recording** — the test IP is set,
read, deleted, and read back as null in a single recording pass.

Re-record when:
- SCP API response shapes change
- Before cutting a v0.2.x release
- Adding new go-vcr tests

### Redaction — what gets scrubbed

The recorder's `AddSaveFilter` hook (`tests/vcr/recorder.go`) substitutes sensitive
fields with **deterministic, synthetic-but-valid** values before a cassette is
written — never deletes them, since a missing `hostname`/`ip` would break the
SDK decode/replay round-trip. The same real value always maps to the same
fake value (hash-based, no shared state), so re-recording the same account
yields identical cassettes.

| Field | Fake value |
|-------|-----------|
| `Authorization` header (request) | deleted entirely |
| `Set-Cookie` header (response), `Cookie` header (request) | deleted entirely |
| `mac` (interface MAC address, in `serverLiveInfo.interfaces[]`) | mapped to IEEE locally-administered range (`02:xx:xx:xx:xx:xx`) |
| `access_token` / `refresh_token` (JSON or form OIDC bodies) | fixed placeholder |
| `ipv4Addresses[].ip` / `.gateway` / `.broadcast`, and any `ip` in a request URL (e.g. the rDNS endpoints) | mapped into **RFC 5737** `203.0.113.0/24` |
| `ipv6Addresses[].networkPrefix` / `.gateway` | mapped into **RFC 3849** `2001:db8::/32` |
| `hostname`, `nickname`, RDNS **PTR** (request and response — `SetRDNS` sends the PTR in the request body) | `host-<hash>.example.com` |
| `userId` | fixed synthetic value (`10001`), regardless of the real value |
| `id` (any JSON number under key `"id"`: server id, template id, address id, site id) | mapped to a deterministic synthetic integer |
| `name` | mapped to a deterministic synthetic prefix (`server-<hash>`) |
| `username` (`TaskInfo.executingUser` — the CCP customer number, on every async task response) | fixed placeholder (`vcr-redacted-username`) — a non-derived constant, since the customer number's small numeric space would make an unsalted hash reversible |
| `password` (`RescueSystemStatus`, populated while rescue is active — a live root credential) | fixed placeholder (`vcr-redacted-password`) |
| `description` (snapshot free-text — may carry arbitrary notes) | fixed placeholder (`vcr-redacted-description`) |

**Preserved as-is:** `disabled`, `state`, `architecture`,
`netmask` (structurally IPv4-shaped but not identifying — there are only 33
possible values). `template.name` and `site.name`/`site.city` are also redacted
because the `"name"` key is redacted at all nesting levels.

> **rDNS replay contract.** Unlike a redacted **IP** — which `GetRDNS`
> re-derives from the *request* (so `matchInteraction` lets a replay call use
> the real IP) — a redacted **PTR hostname** round-trips through the *response*
> body: `GetRDNS` returns the cassette's `rdns` value, and that value must stay
> redacted because a real PTR (e.g. `mail.customer.example`) is exactly the PII
> this scrubbing exists to remove. Consequently a `SetRDNS` → `ConfirmRDNS`
> **replay** test must drive the flow with the committed (fake)
> `host-<hash>.example.com` hostname, not the original real one: `SetRDNS`
> echoes the caller's input, `ConfirmRDNS` compares it against the redacted
> read-back, and the two only agree when the caller already uses the fake
> value. (Live `make acc-record` is unaffected — redaction is save-time only,
> so every live round trip sees the real response before the cassette is
> rewritten.)

`TestCassettesAreScrubbed` (`tests/vcr/scrub_test.go`) is an independent guard
that scans every committed cassette (bodies, headers, and URLs) and fails on
any IP outside the documentation ranges above, a non-scrubbed `Authorization`
header, a JWT (`Bearer eyJ…`) shape, a `userId` outside the synthetic value, or
a `username`, `password`, or snapshot `description` other than its fixed
placeholder. It runs in PR CI alongside the rest of `go test ./...`, with no
credentials or network access.

#### Recordable vs. authored cassettes (v0.3.0 async/rescue surface)

Most SDK cassettes are captured live with `VCR_RECORD=1` (`make acc-record`) and
redacted at save time. Some of the v0.3.0 cassettes are instead **authored from
the documented SCP OpenAPI schema** (`2026.0703.095128`) with the same
synthetic values, because their interaction can't be reproduced idempotently
against a live server — a task that ends in `ERROR`, an *active* rescue system
or rescue enable/disable (each reboots the server), a power change (reboots the
server), and an empty snapshot list. Those tests call `skipInRecordMode(t)` so
`make acc-record` neither reboots the maintainer's server nor overwrites the
authored fixture with a non-matching live one. The read-only status/list
cassettes — `imageflavours`, `snapshots`, and rescue **status (inactive)** — are
live-refreshable as usual. The redactor still covers every field the *live*
responses would carry (see the `password`/`username` rows above), so switching
any of these to a live recording later stays safe.

---

## Branch and commit conventions

**Branch naming:** `<issue-number>/<short-description>`

```
9/set-reverse-dns
docs/opensource-hygiene
```

**Commit message:** start with the issue number (if applicable), then a short
imperative summary:

```
9: implement SetRDNS SDK method + netcupctl rdns set
docs: update CONTRIBUTING for open-source hygiene
```

Keep commits focused — one logical change per commit. Avoid unrelated
refactors or reformats bundled with feature work.

---

## Pull request process

1. Fork the repo and create a branch from `main`.
2. Make your changes, add or update tests, update docs.
3. Ensure all checks pass locally.
4. Open a PR against `main`. Fill in the PR template. Code owners listed in
   [`.github/CODEOWNERS`](.github/CODEOWNERS) are auto-requested for review
   based on the files you change.
5. A maintainer will review. Address feedback; the reviewer resolves threads
   when satisfied.
6. Once approved and CI is green, the maintainer **squash-merges** the PR (see
   [Branch protection / merge rules](#branch-protection--merge-rules)). The PR
   branch is deleted automatically after merge.

We do not require a CLA, but by submitting a PR you agree that your
contribution is licensed under the project's [MPL-2.0 license](LICENSE).

---

## Branch protection / merge rules

Repository and branch-protection settings are stored as code in
`.github/settings.yml` (using the [probot/settings][settings-app] schema) and
reviewed like any other change. To enforce them, install the free **Settings**
GitHub App from <https://github.com/apps/settings> on this repository and grant
it **Administration: Read & Write** permission. The app reconciles
`.github/settings.yml` against the repository on every push to `main`.

The `main` branch is protected as follows:

- Pull requests are required — no direct pushes to `main`.
- Required status checks: `fmt`, `test`, `lint`, each kept up to date with
  `main` before merging (no stale-green merges).
- Squash merges only; merge-commit and rebase merges are disabled.
- Linear history is required; the PR branch is deleted automatically after
  merge.
- Force-pushes and branch deletions are disabled (enforced for admins too).

Code owners are declared in [`.github/CODEOWNERS`](.github/CODEOWNERS). GitHub
automatically requests a review from the owner of any path a PR touches. While
the project has a single maintainer, required approvals are set to `0` so the
maintainer can merge their own PRs once CI is green. Bump
`required_approving_review_count` to `1` (and set
`require_code_owner_reviews: true`) in `.github/settings.yml` once there are
additional maintainers.

### Applying the rules manually

If you prefer not to install the Settings app, apply the equivalent rules in
the GitHub UI under **Settings → Branches → Branch protection rules → main**:

- Require a pull request before merging (0 approvals while solo).
- Require status checks to pass before merging: `fmt`, `test`, `lint`;
  require branches to be up to date before merging.
- Require conversation resolution before merging.
- Require linear history.
- Do not allow force pushes; do not allow deletions.
- Include administrators.

[settings-app]: https://github.com/apps/settings

---

## Architecture overview

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design.
The short version:

```
Terraform config
  └─ Terraform Plugin Framework
       └─ Provider layer
            └─ Service layer
                 └─ SDK (pkg/netcup/)  ←── also used by netcupctl CLI
                      └─ API client layer
                           └─ Netcup SCP / CCP APIs
```

Key rules:
- The SDK (`pkg/netcup/`) must not import provider or CLI packages.
- Resources call service interfaces, not the API client directly.
- The CLI and provider share the same SDK — do not duplicate HTTP or auth logic.
- Do not edit generated code (once it exists); regenerate instead.

---

## Release process

Releases are cut by pushing a SemVer tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

GoReleaser builds binaries for linux, macOS, and Windows (amd64 + arm64),
attaches them to a GitHub Release, and signs the checksums file with
[cosign keyless](README.md#releasing). See the
[Releasing section in README.md](README.md#releasing) for the full process and
how to verify a download.

Versioning follows [Semantic Versioning](https://semver.org).

---

## Labels

Labels are defined in `.github/settings.yml` and kept in sync with this table.

| Label | Meaning |
| --- | --- |
| `type/bug` | Something is broken |
| `type/feature` | New capability |
| `type/chore` | Non-functional maintenance (CI, deps, docs) |
| `type/documentation` | Docs-only change |
| `type/refactor` | Code restructuring without behavior change |
| `area/sdk` | `pkg/netcup/` SDK |
| `area/cli` | `cmd/netcupctl/` CLI |
| `area/provider` | Terraform provider |
| `area/ci` | CI / build / release automation |
| `priority/high` | Blocks a milestone |
| `priority/medium` | Important but not blocking |
| `priority/low` | Nice to have |
| `needs-decision` | Requires a human decision before work can proceed |
| `ready` | Ready to be picked up |
| `dependencies` | Dependency updates (Dependabot) |
| `stale` | No activity for 90 days; will be closed if it stays inactive (applied by the stale workflow) |
